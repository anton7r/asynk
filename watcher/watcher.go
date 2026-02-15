package watcher

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anton7r/asynk/util"
	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

type Watcher struct {
	directories          *WatchableDirectories
	watcher              FsWatcher
	log                  *zap.Logger
	aggregator           *FileEventAggregator
	propagateChangeEvent func(events map[string]AggregatedEvent)
	fs                   util.FileSystem
}

type UpdatedFile struct {
	ModifiedTime time.Time
}

type AggregatedEvent struct {
	// Relative path to the where the asynk command was run.
	Dir string
	// Relative path to the where the asynk command was run.
	Files map[string]*UpdatedFile
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
	return NewWatcherWithDeps(log, watchedDirectories, propagateChangeEvent, util.NewOSFileSystem(), nil)
}

// NewWatcherWithDeps creates a Watcher with injected dependencies for testing.
// If fsWatcher is nil, a real fsnotify watcher will be created.
func NewWatcherWithDeps(
	log *zap.Logger,
	watchedDirectories *WatchableDirectories,
	propagateChangeEvent func(events map[string]AggregatedEvent),
	fs util.FileSystem,
	fsWatcher FsWatcher,
) (*Watcher, error) {
	if fsWatcher == nil {
		var err error
		fsWatcher, err = NewRealFsWatcher()
		if err != nil {
			return nil, err
		}
	}

	if fs == nil {
		fs = util.NewOSFileSystem()
	}

	w := &Watcher{
		watcher:              fsWatcher,
		log:                  log,
		directories:          watchedDirectories,
		propagateChangeEvent: propagateChangeEvent,
		fs:                   fs,
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
	go w.initFsEventWatcher()
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

func (w *Watcher) initFsEventWatcher() {
	for {
		select {
		case event, ok := <-w.watcher.Events():
			if !ok {
				return
			}

			w.handleFsEvent(event)

		case err, ok := <-w.watcher.Errors():
			if !ok {
				return
			}
			w.log.Error("Error watching file system", zap.Error(err))
		}
	}
}

func (w *Watcher) handleFsEvent(event fsnotify.Event) {
	changePath := filepath.ToSlash(event.Name)
	// Also handle backslashes that filepath.ToSlash won't convert on Linux
	// (filepath.ToSlash is a no-op on non-Windows platforms, but we may
	// receive Windows-style paths in cross-platform scenarios)
	changePath = strings.ReplaceAll(changePath, "\\", "/")

	w.log.Info("File changes",
		zap.String("eventType", event.Op.String()),
		zap.String("filePath", changePath),
	)

	var dirPath string
	var isDir bool
	var modifiedTime time.Time

	if event.Op&fsnotify.Remove == fsnotify.Remove {
		// For removed files, we can't stat them. Use path.Dir as best approximation.
		dirPath = path.Dir(changePath)
		isDir = false
		modifiedTime = time.Now()
	} else {
		stat, err := w.fs.Lstat(changePath)
		if err != nil {
			// If the file no longer exists, it was a transient file (e.g.,
			// temporary files created and quickly deleted by build tools like
			// the Go compiler). Treat this the same as a Remove event — skip
			// it silently rather than logging an error.
			if errors.Is(err, os.ErrNotExist) {
				w.log.Debug("File no longer exists, skipping transient file event",
					zap.String("filePath", changePath),
				)
				return
			}
			w.log.Error("Error getting file info", zap.Error(err))
			return
		}

		modifiedTime = stat.ModTime()
		isDir = stat.IsDir()
		if isDir {
			dirPath = changePath
		} else {
			dirPath = path.Dir(changePath)
		}
	}

	// Check if it is actually a file
	if !isDir {
		w.checkIfWeNeedToNotify(changePath, dirPath, modifiedTime)
	} else {
		// TODO: Add new watchers for new directories

	}

}

func (w *Watcher) checkIfWeNeedToNotify(
	changePath string,
	dirPath string,
	modifiedTime time.Time,
) {
	directories, ok := w.directories.directories[dirPath]

	if !ok {
		w.log.Info("Directory not found in watchable directories",
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
				Files: make(map[string]*UpdatedFile),
				Tasks: make(map[string]bool),
			}
		}

		event.Files[changePath] = &UpdatedFile{
			ModifiedTime: modifiedTime,
		}
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
