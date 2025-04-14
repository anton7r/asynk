package watcher

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

type Watcher struct {
	directories          *WatchableDirectories
	watcher              *fsnotify.Watcher
	log                  *zap.Logger
	aggregator           *FileEventAggregator
	propagateChangeEvent func(events map[string]AggregatedEvent)
}

type AggregatedEvent struct {
	// Relative path to the where the asynk command was run.
	Dir string
	// Relative path to the where the asynk command was run.
	Files map[string]bool
	// Tasks ids that could be wait for the completion.
	// We use the wording "could be" because we cannot be
	// certain that the file which triggered the event actually
	// matches the tasks glob pattern.
	Tasks map[string]bool
}

type FileEventAggregator struct {
	aggregated     map[string]AggregatedEvent
	aggregatorLock sync.Mutex
	changeId       int8
}

func NewWatcher(
	log *zap.Logger,
	watchedDirectories *WatchableDirectories,
	propagateChangeEvent func(events map[string]AggregatedEvent),
) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		watcher:              watcher,
		log:                  log,
		directories:          watchedDirectories,
		propagateChangeEvent: propagateChangeEvent,
		aggregator: &FileEventAggregator{
			aggregated:     make(map[string]AggregatedEvent),
			aggregatorLock: sync.Mutex{},
			changeId:       0,
		},
	}

	return w, nil
}

func (w *Watcher) Close() {
	w.log.Info("Closing watcher...")

	w.watcher.Close()
}

func (w *Watcher) Start() {
	w.watchDirs()
	go w.handleFsEvents()
}

func (w *Watcher) watchDirs() {
	for _, dir := range w.directories.directories {
		w.log.Info("Watching directory",
			zap.String("directory", dir.MatchedDirectory),
			zap.String("relativePath", dir.RelativePath),
		)

		err := w.watcher.Add(dir.RelativePath)
		if err != nil {
			w.log.Error("Error adding directory to watcher", zap.Error(err))
			return
		}
	}
}

func (w *Watcher) handleFsEvents() {
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}

			w.log.Info("File changes",
				zap.String("eventType", event.Op.String()),
				zap.String("filePath", event.Name),
			)

			changePath := event.Name

			var dirPath string
			var isDir bool
			//var isRemove bool

			if event.Op&fsnotify.Remove == fsnotify.Remove {
				//isRemove = true
				// We can only do best approximation here, since the file is now deleted
				// or we could perhaps cache it in memory.
				isDir = !strings.Contains(path.Base(changePath), ".")
				if isDir {
					dirPath = changePath
				} else {
					dirPath = filepath.Dir(changePath)
				}

			} else {
				stat, err := os.Lstat(changePath)
				if err != nil {
					w.log.Error("Error getting file info", zap.Error(err))
					continue
				}

				isDir = stat.IsDir()
				if isDir {
					dirPath = changePath
				} else {
					dirPath = filepath.Dir(changePath)
				}
			}

			// Check if it is actually a file
			if !isDir {
				w.checkIfWeNeedToNotify(changePath, dirPath)
			} else {
				// TODO: Add new watchers for new directories

			}

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.log.Error("Error watching file system", zap.Error(err))
		}
	}
}

func (w *Watcher) checkIfWeNeedToNotify(changePath string, dirPath string) {
	directories, ok := w.directories.directories[dirPath]

	if !ok {
		w.log.Error("Directory not found in watchable directories",
			zap.String("directory", dirPath))
		return
	}

	tasks := directories.TaskIds

	if len(tasks) > 0 {
		w.aggregator.aggregatorLock.Lock()
		event, exists := w.aggregator.aggregated[dirPath]
		if !exists {
			event = AggregatedEvent{
				Dir:   dirPath,
				Files: make(map[string]bool),
				Tasks: make(map[string]bool),
			}
		}

		event.Files[changePath] = true
		for taskId := range tasks {
			event.Tasks[taskId] = true
		}
		w.aggregator.aggregated[dirPath] = event
		delay := time.Millisecond * 200
		w.aggregator.changeId++
		w.aggregator.aggregatorLock.Unlock()

		go w.propagateEvents(delay, w.aggregator.changeId)
	}
}

// If the changeId matches the current changeId, we propagate the events.
// TODO: This is a bit of a hack. The propagation should be delayed to avoid spamming the system.
func (w *Watcher) propagateEvents(delay time.Duration, changeId int8) {
	time.Sleep(delay)
	w.aggregator.aggregatorLock.Lock()
	defer w.aggregator.aggregatorLock.Unlock()

	if w.aggregator.changeId != changeId {
		w.log.Debug("Cancelling event propagation, because additional changes were detected.")
		return
	}

	w.propagateChangeEvent(w.aggregator.aggregated)
	w.aggregator.aggregated = make(map[string]AggregatedEvent)
}
