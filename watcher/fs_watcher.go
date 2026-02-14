package watcher

import "github.com/fsnotify/fsnotify"

// FsWatcher abstracts the fsnotify.Watcher for testing purposes.
type FsWatcher interface {
	Add(name string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}

// RealFsWatcher wraps a *fsnotify.Watcher to implement FsWatcher.
type RealFsWatcher struct {
	w *fsnotify.Watcher
}

func NewRealFsWatcher() (*RealFsWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &RealFsWatcher{w: w}, nil
}

func (r *RealFsWatcher) Add(name string) error {
	return r.w.Add(name)
}

func (r *RealFsWatcher) Close() error {
	return r.w.Close()
}

func (r *RealFsWatcher) Events() <-chan fsnotify.Event {
	return r.w.Events
}

func (r *RealFsWatcher) Errors() <-chan error {
	return r.w.Errors
}
