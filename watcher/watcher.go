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

const DefaultFSDebounce = 200 * time.Millisecond

type Watcher struct {
	directories          *WatchableDirectories
	watcher              FsWatcher
	log                  *zap.Logger
	aggregator           *FileEventAggregator
	propagateChangeEvent func(events map[string]AggregatedEvent)
	fs                   util.FileSystem
	defaultFSDebounce    time.Duration
	taskFSDebounces      map[string]time.Duration
}

type WatcherOptions struct {
	DefaultFSDebounce    time.Duration
	DefaultFSDebounceSet bool
	TaskFSDebounces      map[string]time.Duration
}

type UpdatedFile struct {
	ModifiedTime time.Time
}

type AggregatedEvent struct {
	// Relative path to the where the asynk command was run.
	Dir string
	// Relative path to the where the asynk command was run.
	Files map[string]*UpdatedFile
	// Task ids affected by the files in this event.
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

func NewWatcherWithOptions(
	log *zap.Logger,
	watchedDirectories *WatchableDirectories,
	propagateChangeEvent func(events map[string]AggregatedEvent),
	options WatcherOptions,
) (*Watcher, error) {
	return NewWatcherWithDepsAndOptions(log, watchedDirectories, propagateChangeEvent, util.NewOSFileSystem(), nil, options)
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
	return NewWatcherWithDepsAndOptions(log, watchedDirectories, propagateChangeEvent, fs, fsWatcher, WatcherOptions{})
}

func NewWatcherWithDepsAndOptions(
	log *zap.Logger,
	watchedDirectories *WatchableDirectories,
	propagateChangeEvent func(events map[string]AggregatedEvent),
	fs util.FileSystem,
	fsWatcher FsWatcher,
	options WatcherOptions,
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

	defaultFSDebounce := DefaultFSDebounce
	if options.DefaultFSDebounceSet {
		defaultFSDebounce = options.DefaultFSDebounce
	}

	w := &Watcher{
		watcher:              fsWatcher,
		log:                  log,
		directories:          watchedDirectories,
		propagateChangeEvent: propagateChangeEvent,
		fs:                   fs,
		defaultFSDebounce:    defaultFSDebounce,
		taskFSDebounces:      copyTaskFSDebounces(options.TaskFSDebounces),
		aggregator: &FileEventAggregator{
			aggregated:     make(map[string]AggregatedEvent),
			aggregatorLock: sync.Mutex{},
			changeId:       0,
		},
	}

	return w, nil
}

func copyTaskFSDebounces(source map[string]time.Duration) map[string]time.Duration {
	debounces := make(map[string]time.Duration, len(source))
	for taskId, debounce := range source {
		debounces[taskId] = debounce
	}
	return debounces
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

	if event.Op&fsnotify.Remove == fsnotify.Remove {
		// Removed files should not trigger task restarts. A file deletion is
		// not an actionable change — the interesting events are CREATE and
		// WRITE for new/updated files. Skipping Remove events also prevents
		// a timing issue where the time.Now() timestamp assigned to Remove
		// events could defeat the filterRunningTasks deduplication check,
		// causing unnecessary restarts of continuous tasks.
		w.log.Debug("Skipping Remove event",
			zap.String("filePath", changePath),
		)
		return
	}

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

	modifiedTime := stat.ModTime()
	isDir := stat.IsDir()

	if isDir {
		// TODO: Add new watchers for new directories
		return
	}

	dirPath := path.Dir(changePath)
	w.checkIfWeNeedToNotify(changePath, dirPath, modifiedTime)

}

func (w *Watcher) checkIfWeNeedToNotify(
	changePath string,
	dirPath string,
	modifiedTime time.Time,
) {
	eventDirPath, eventFilePath, directories, ok := w.findWatchableDirectory(dirPath, changePath)

	if !ok {
		w.log.Info("Directory not found in watchable directories",
			zap.String("directory", dirPath))
		return
	}

	tasks := w.matchingTasksForFile(eventFilePath, directories.TaskIds)

	if len(tasks) > 0 {
		w.aggregator.aggregatorLock.Lock()
		event, exists := w.aggregator.aggregated[eventDirPath]
		if !exists {
			event = AggregatedEvent{
				Dir:   eventDirPath,
				Files: make(map[string]*UpdatedFile),
				Tasks: make(map[string]bool),
			}
		}

		event.Files[eventFilePath] = &UpdatedFile{
			ModifiedTime: modifiedTime,
		}
		for taskId := range tasks {
			event.Tasks[taskId] = true
		}
		w.aggregator.aggregated[eventDirPath] = event
		delay := w.fsDebounceForTasks(w.pendingTasks())
		w.aggregator.changeId++
		changeId := w.aggregator.changeId
		w.aggregator.aggregatorLock.Unlock()

		go w.propagateEvents(delay, changeId)
	}
}

func (w *Watcher) matchingTasksForFile(filePath string, directoryTasks map[string]bool) map[string]bool {
	if len(w.directories.taskConfigs) == 0 {
		return copyTaskIds(directoryTasks)
	}

	return getMatchingTasks(normalizePathForLookup(filePath), w.directories.taskConfigs)
}

func copyTaskIds(source map[string]bool) map[string]bool {
	target := make(map[string]bool, len(source))
	for taskId, value := range source {
		target[taskId] = value
	}
	return target
}

func (w *Watcher) pendingTasks() map[string]bool {
	tasks := make(map[string]bool)
	for _, event := range w.aggregator.aggregated {
		for taskId := range event.Tasks {
			tasks[taskId] = true
		}
	}
	return tasks
}

func (w *Watcher) findWatchableDirectory(dirPath string, changePath string) (string, string, WatchableDirectory, bool) {
	directories, ok := w.directories.directories[dirPath]
	if ok {
		return dirPath, changePath, directories, true
	}

	normalizedDirPath := normalizePathForLookup(dirPath)
	for candidateDirPath, directories := range w.directories.directories {
		if normalizePathForLookup(candidateDirPath) != normalizedDirPath {
			continue
		}

		return candidateDirPath, pathWithDirectoryStyle(candidateDirPath, dirPath, changePath), directories, true
	}

	return "", "", WatchableDirectory{}, false
}

func normalizePathForLookup(value string) string {
	value = filepath.ToSlash(value)
	return strings.ReplaceAll(value, "\\", "/")
}

func pathWithDirectoryStyle(targetDirPath string, sourceDirPath string, sourceFilePath string) string {
	suffix := strings.TrimPrefix(sourceFilePath, sourceDirPath)
	if strings.Contains(targetDirPath, "\\") {
		suffix = strings.ReplaceAll(suffix, "/", "\\")
	}
	return targetDirPath + suffix
}

func (w *Watcher) fsDebounceForTasks(tasks map[string]bool) time.Duration {
	var delay time.Duration
	first := true

	for taskId := range tasks {
		taskDelay, exists := w.taskFSDebounces[taskId]
		if !exists {
			taskDelay = w.defaultFSDebounce
		}

		if first || taskDelay > delay {
			delay = taskDelay
			first = false
		}
	}

	if first {
		return w.defaultFSDebounce
	}

	return delay
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
