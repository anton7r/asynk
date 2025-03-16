package watcher

import (
	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

type Watcher struct {
	watcher *fsnotify.Watcher
	log     *zap.Logger
}

/*
Because of the limitations posed by the fsnotify package in Go,
we cannot watch for changes in the file system using regex with
a very performance oriented way.

basically this would have to be implemented in a way that adds
listeners for directories that have matching file change patterns.

We could also implement a custom file change detection mechanism,
that would utilize sha1 checksums or other hashing techniques.
*/
func NewWatcher(log *zap.Logger, watchedDirectories ...string) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	for _, dir := range watchedDirectories {
		err = watcher.Add(dir)
		if err != nil {
			return nil, err
		}
	}
	return &Watcher{watcher: watcher, log: log}, nil
}

func (w *Watcher) Close() {
	w.watcher.Close()
}

func (w *Watcher) Start(eventHandler func(event fsnotify.Event)) {
	go func() {
		for {
			select {
			case event, ok := <-w.watcher.Events:
				if !ok {
					return
				}
				eventHandler(event)
			case err, ok := <-w.watcher.Errors:
				if !ok {
					return
				}
				w.log.Error("Error watching file system", zap.Error(err))
			}
		}
	}()
}
