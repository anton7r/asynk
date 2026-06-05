package watcher

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anton7r/asynk/config"
	configutil "github.com/anton7r/asynk/config/util"
	"github.com/anton7r/asynk/util"
	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// ---------- mock FsWatcher ----------

// testFsWatcher implements FsWatcher with controllable channels.
type testFsWatcher struct {
	mu        sync.Mutex
	events    chan fsnotify.Event
	errors    chan error
	addedDirs []string
	addErr    error
	closed    bool
}

func newTestFsWatcher() *testFsWatcher {
	return &testFsWatcher{
		events: make(chan fsnotify.Event, 16),
		errors: make(chan error, 16),
	}
}

func (t *testFsWatcher) Add(name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.addErr != nil {
		return t.addErr
	}
	t.addedDirs = append(t.addedDirs, name)
	return nil
}

func (t *testFsWatcher) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		close(t.events)
		close(t.errors)
	}
	return nil
}

func (t *testFsWatcher) Events() <-chan fsnotify.Event {
	return t.events
}

func (t *testFsWatcher) Errors() <-chan error {
	return t.errors
}

func (t *testFsWatcher) sendEvent(e fsnotify.Event) {
	t.events <- e
}

// ---------- mock FileSystem ----------

// mockFS implements util.FileSystem with in-memory data.
type mockFS struct {
	files map[string]mockFileEntry // path -> entry
}

type mockFileEntry struct {
	isDir   bool
	modTime time.Time
	content []byte
}

func newMockFS() *mockFS {
	return &mockFS{files: make(map[string]mockFileEntry)}
}

func (m *mockFS) addFile(name string, modTime time.Time) {
	m.files[name] = mockFileEntry{isDir: false, modTime: modTime, content: nil}
}

func (m *mockFS) addDir(name string) {
	m.files[name] = mockFileEntry{isDir: true, modTime: time.Now()}
}

func (m *mockFS) Lstat(name string) (os.FileInfo, error) {
	entry, ok := m.files[name]
	if !ok {
		return nil, &os.PathError{Op: "lstat", Path: name, Err: os.ErrNotExist}
	}
	return &mockStatInfo{name: filepath.Base(name), entry: entry}, nil
}

func (m *mockFS) ReadFile(name string) ([]byte, error) {
	entry, ok := m.files[name]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", name)
	}
	return entry.content, nil
}

func (m *mockFS) Walk(root string, fn filepath.WalkFunc) error {
	// Sort paths to simulate proper tree-order walk, required for SkipDir to work.
	sorted := make([]string, 0, len(m.files))
	for name := range m.files {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	skipPrefix := ""
	for _, name := range sorted {
		// If we are skipping a directory subtree, skip all paths under it.
		if skipPrefix != "" && strings.HasPrefix(name, skipPrefix) {
			continue
		}
		skipPrefix = ""

		entry := m.files[name]
		info := &mockStatInfo{name: filepath.Base(name), entry: entry}
		if err := fn(name, info, nil); err != nil {
			if err == filepath.SkipDir {
				// Skip everything under this directory.
				if entry.isDir {
					skipPrefix = name + "/"
				}
				continue
			}
			return err
		}
	}
	return nil
}

func (m *mockFS) Remove(name string) error {
	if _, ok := m.files[name]; !ok {
		return fmt.Errorf("file not found: %s", name)
	}
	delete(m.files, name)
	return nil
}

func (m *mockFS) ReadDir(name string) ([]fs.DirEntry, error) {
	var entries []fs.DirEntry
	for fName, entry := range m.files {
		dir := filepath.Dir(fName)
		if dir == name || fName == name {
			entries = append(entries, &mockDirEntry{name: filepath.Base(fName), entry: entry})
		}
	}
	return entries, nil
}

// mockStatInfo implements os.FileInfo.
type mockStatInfo struct {
	name  string
	entry mockFileEntry
}

func (m *mockStatInfo) Name() string      { return m.name }
func (m *mockStatInfo) Size() int64       { return int64(len(m.entry.content)) }
func (m *mockStatInfo) Mode() os.FileMode { return 0644 }
func (m *mockStatInfo) ModTime() time.Time {
	return m.entry.modTime
}
func (m *mockStatInfo) IsDir() bool      { return m.entry.isDir }
func (m *mockStatInfo) Sys() interface{} { return nil }

// mockDirEntry implements fs.DirEntry.
type mockDirEntry struct {
	name  string
	entry mockFileEntry
}

func (d *mockDirEntry) Name() string      { return d.name }
func (d *mockDirEntry) IsDir() bool       { return d.entry.isDir }
func (d *mockDirEntry) Type() fs.FileMode { return 0 }
func (d *mockDirEntry) Info() (fs.FileInfo, error) {
	return &mockStatInfo{name: d.name, entry: d.entry}, nil
}

// =====================================================================
// Tests: handleFsEvent with mock FsWatcher and mock FileSystem
// =====================================================================

func TestHandleFsEvent_WriteEvent(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	modTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	mfs.addFile("src/main.go", modTime)

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	propagate := func(events map[string]AggregatedEvent) {
		eventsChan <- events
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/main.go",
		Op:   fsnotify.Write,
	})

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src")
		agg := events["src"]
		assert.Equal(t, "src", agg.Dir)
		assert.Contains(t, agg.Files, "src/main.go")
		assert.Equal(t, modTime, agg.Files["src/main.go"].ModifiedTime)
		assert.True(t, agg.Tasks["build"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for propagated event")
	}

	w.Close()
}

// =====================================================================
// Tests: transient (temp) file handling in handleFsEvent
// These tests reproduce the Go 1.26 compiler temp file issue where
// temporary files are created and deleted quickly, causing Lstat to
// fail with "no such file or directory".
// =====================================================================

func TestHandleFsEvent_TransientFile_LstatNotExist_ShouldNotError(t *testing.T) {
	// When a file is created and quickly deleted (e.g., Go compiler temp files
	// like "tmp/bin/adasdasderii4o4jro-go-tmp-umask"), fsnotify fires a Create
	// event, but by the time handleFsEvent calls Lstat, the file is gone.
	// This should NOT be logged as an error — it should be treated as a
	// transient/removed file event and handled gracefully.
	logger, obs := setupObservedLogger()
	mfs := newMockFS()
	// Do NOT add the temp file to mockFS — Lstat will return os.ErrNotExist

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"tmp/bin": {
				MatchedDirectory: "tmp/bin",
				RelativePath:     "./tmp/bin",
				TaskIds:          map[string]bool{"server": true},
			},
		},
	}

	propagateCalled := false
	propagate := func(events map[string]AggregatedEvent) {
		propagateCalled = true
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	// Simulate a Create event for a Go compiler temp file that no longer exists
	w.handleFsEvent(fsnotify.Event{
		Name: "tmp/bin/adasdasderii4o4jro-go-tmp-umask",
		Op:   fsnotify.Create,
	})

	// Give a small window for any async propagation
	time.Sleep(300 * time.Millisecond)

	// The event should NOT have been propagated (file doesn't exist, treat as removed)
	assert.False(t, propagateCalled,
		"propagate should not be called for a transient file that disappeared")

	// Verify no ERROR level logs were emitted — the "no such file" case
	// for a Create event should be handled gracefully (debug/info at most)
	errorLogs := obs.FilterLevelExact(zap.ErrorLevel).All()
	for _, entry := range errorLogs {
		assert.NotContains(t, entry.Message, "Error getting file info",
			"Transient file disappearance should not produce ERROR level logs")
	}

	w.Close()
}

func TestHandleFsEvent_TransientFile_WriteEvent_LstatNotExist(t *testing.T) {
	// Similar to above, but with a Write event for a transient file.
	// The Go compiler may write to a temp file that gets deleted before
	// we can Lstat it.
	logger, obs := setupObservedLogger()
	mfs := newMockFS()

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"tmp/bin": {
				MatchedDirectory: "tmp/bin",
				RelativePath:     "./tmp/bin",
				TaskIds:          map[string]bool{"server": true},
			},
		},
	}

	propagateCalled := false
	propagate := func(events map[string]AggregatedEvent) {
		propagateCalled = true
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "tmp/bin/some-temp-file-go-tmp-umask",
		Op:   fsnotify.Write,
	})

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"propagate should not be called for a transient file write event")

	errorLogs := obs.FilterLevelExact(zap.ErrorLevel).All()
	for _, entry := range errorLogs {
		assert.NotContains(t, entry.Message, "Error getting file info",
			"Transient file disappearance should not produce ERROR level logs")
	}

	w.Close()
}

func TestHandleFsEvent_TransientFile_RealLstatError_StillLogsError(t *testing.T) {
	// For actual Lstat errors (not os.ErrNotExist), such as permission denied,
	// the error should still be logged at ERROR level.
	logger, obs := setupObservedLogger()
	mfs := &lstatErrorMockFS{
		mockFS: newMockFS(),
		errForPath: map[string]error{
			"src/protected.go": fmt.Errorf("permission denied"),
		},
	}

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	propagateCalled := false
	propagate := func(events map[string]AggregatedEvent) {
		propagateCalled = true
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/protected.go",
		Op:   fsnotify.Write,
	})

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled)

	// For real errors (not ErrNotExist), ERROR level log IS expected
	errorLogs := obs.FilterLevelExact(zap.ErrorLevel).All()
	foundErrorLog := false
	for _, entry := range errorLogs {
		if entry.Message == "Error getting file info" {
			foundErrorLog = true
			break
		}
	}
	assert.True(t, foundErrorLog,
		"Real Lstat errors (not ErrNotExist) should still produce ERROR level logs")

	w.Close()
}

// lstatErrorMockFS wraps mockFS but returns custom errors for specific paths on Lstat.
type lstatErrorMockFS struct {
	*mockFS
	errForPath map[string]error
}

func (m *lstatErrorMockFS) Lstat(name string) (os.FileInfo, error) {
	if err, ok := m.errForPath[name]; ok {
		return nil, err
	}
	return m.mockFS.Lstat(name)
}

// setupObservedLogger creates a zap logger with an observer for inspecting log output in tests.
func setupObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, obs := observer.New(zap.DebugLevel)
	return zap.New(core), obs
}

func TestHandleFsEvent_RemoveEvent(t *testing.T) {
	// Remove events should be silently skipped — a file deletion is not an
	// actionable change and should not trigger task restarts. This also
	// prevents a timing issue where the time.Now() timestamp on Remove
	// events could defeat the filterRunningTasks deduplication check.
	logger, obs := setupObservedLogger()
	mfs := newMockFS()

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true, "lint": true},
			},
		},
	}

	propagateCalled := false
	propagate := func(events map[string]AggregatedEvent) {
		propagateCalled = true
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/old_file.go",
		Op:   fsnotify.Remove,
	})

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"Remove events should be skipped and not trigger propagation")

	// Verify a Debug log was emitted for the skip
	debugLogs := obs.FilterLevelExact(zap.DebugLevel).All()
	foundSkipLog := false
	for _, entry := range debugLogs {
		if entry.Message == "Skipping Remove event" {
			foundSkipLog = true
			break
		}
	}
	assert.True(t, foundSkipLog, "Expected a Debug-level 'Skipping Remove event' log")

	w.Close()
}

func TestHandleFsEvent_RemoveEvent_PropagatesForSuppressedTask(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
		"lint": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true, "lint": true},
			},
		},
		taskConfigs: taskConfigs,
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{
			DefaultFSDebounce:    0,
			DefaultFSDebounceSet: true,
			RebuildSuppressionTasks: map[string]bool{
				"build": true,
			},
		},
	)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/main.go",
		Op:   fsnotify.Remove,
	})

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src")
		assert.Contains(t, events["src"].Files, "src/main.go")
		assert.True(t, events["src"].Tasks["build"])
		assert.False(t, events["src"].Tasks["lint"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for suppressed remove event")
	}

	w.Close()
}

func TestHandleFsEvent_RenameAwayEvent_PropagatesForSuppressedTask(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
		"lint": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true, "lint": true},
			},
		},
		taskConfigs: taskConfigs,
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{
			DefaultFSDebounce:    0,
			DefaultFSDebounceSet: true,
			RebuildSuppressionTasks: map[string]bool{
				"build": true,
			},
		},
	)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/main.go",
		Op:   fsnotify.Rename,
	})

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src")
		assert.Contains(t, events["src"].Files, "src/main.go")
		assert.True(t, events["src"].Tasks["build"])
		assert.False(t, events["src"].Tasks["lint"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for suppressed rename-away event")
	}

	w.Close()
}

func TestHandleFsEvent_CreateEvent(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	modTime := time.Date(2026, 2, 10, 8, 30, 0, 0, time.UTC)
	mfs.addFile("pkg/handler.go", modTime)

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"pkg": {
				MatchedDirectory: "pkg",
				RelativePath:     "./pkg",
				TaskIds:          map[string]bool{"test": true},
			},
		},
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	propagate := func(events map[string]AggregatedEvent) {
		eventsChan <- events
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "pkg/handler.go",
		Op:   fsnotify.Create,
	})

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "pkg")
		agg := events["pkg"]
		assert.Equal(t, "pkg", agg.Dir)
		assert.Contains(t, agg.Files, "pkg/handler.go")
		assert.Equal(t, modTime, agg.Files["pkg/handler.go"].ModifiedTime)
		assert.True(t, agg.Tasks["test"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for propagated event")
	}

	w.Close()
}

func TestHandleFsEvent_LstatError(t *testing.T) {
	// When Lstat fails on a non-Remove event, handleFsEvent should log an error
	// and return without crashing or aggregating.
	logger := zap.NewNop()
	mfs := newMockFS() // file not registered => Lstat returns error

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	propagateCalled := false
	propagate := func(events map[string]AggregatedEvent) {
		propagateCalled = true
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	// Write event for a file that doesn't exist in mockFS => Lstat error
	w.handleFsEvent(fsnotify.Event{
		Name: "src/nonexistent.go",
		Op:   fsnotify.Write,
	})

	// Give a small window for any async propagation
	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled, "propagate should not be called when Lstat fails")

	w.Close()
}

func TestHandleFsEvent_DirectoryCreateWatchesEmptyDirectory(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	mfs.addDir("src/newpkg")

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	propagateCalled := false
	propagate := func(events map[string]AggregatedEvent) {
		propagateCalled = true
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg",
		Op:   fsnotify.Create,
	})

	fw.mu.Lock()
	assert.Contains(t, fw.addedDirs, "./src/newpkg")
	fw.mu.Unlock()

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"empty directory creation should not propagate a file event")

	w.Close()
}

func TestHandleFsEvent_DirectoryWriteDoesNotScanDirectory(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	mfs.addDir("src/newpkg")
	mfs.addFile("src/newpkg/main.go", time.Now())

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	propagateCalled := false
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) { propagateCalled = true }, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg",
		Op:   fsnotify.Write,
	})

	fw.mu.Lock()
	assert.NotContains(t, fw.addedDirs, "./src/newpkg")
	fw.mu.Unlock()

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"directory write events should not rescan or propagate existing files")

	w.Close()
}

func TestHandleFsEvent_DirectoryCreateRefreshesExistingWatch(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	mfs.addDir("src/newpkg")

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
			"src/newpkg": {
				MatchedDirectory: "src/newpkg",
				RelativePath:     "./src/newpkg",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	propagateCalled := false
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) { propagateCalled = true }, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg",
		Op:   fsnotify.Create,
	})

	fw.mu.Lock()
	assert.Contains(t, fw.addedDirs, "./src/newpkg")
	fw.mu.Unlock()

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"recreated empty directory should refresh the watch without propagating a file event")

	w.Close()
}

func TestHandleFsEvent_DirectoryCreateCanonicalizesDotSlashPath(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	mfs.addDir("./src/newpkg")

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{DefaultFSDebounce: 0, DefaultFSDebounceSet: true},
	)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "./src/newpkg",
		Op:   fsnotify.Create,
	})

	fw.mu.Lock()
	assert.Contains(t, fw.addedDirs, "./src/newpkg")
	fw.mu.Unlock()
	assert.Contains(t, dirs.directories, "src/newpkg")
	assert.NotContains(t, dirs.directories, "./src/newpkg")

	modTime := time.Date(2026, 4, 2, 11, 30, 0, 0, time.UTC)
	mfs.addFile("src/newpkg/main.go", modTime)
	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg/main.go",
		Op:   fsnotify.Create,
	})

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src/newpkg")
		assert.Contains(t, events["src/newpkg"].Files, "src/newpkg/main.go")
		assert.True(t, events["src/newpkg"].Tasks["build"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for future file event in dot-slash-created directory")
	}

	w.Close()
}

func TestHandleFsEvent_DirectoryCreateScansExistingMatchingFiles(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	modTime := time.Date(2026, 4, 2, 9, 15, 0, 0, time.UTC)
	mfs.addDir("src/newpkg")
	mfs.addFile("src/newpkg/main.go", modTime)

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}
	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
		taskConfigs: taskConfigs,
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{DefaultFSDebounce: 0, DefaultFSDebounceSet: true},
	)
	assert.NoError(t, err)

	discoveryStarted := time.Now()
	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg",
		Op:   fsnotify.Create,
	})

	fw.mu.Lock()
	assert.Contains(t, fw.addedDirs, "./src/newpkg")
	fw.mu.Unlock()

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src/newpkg")
		assert.Contains(t, events["src/newpkg"].Files, "src/newpkg/main.go")
		assert.False(t, events["src/newpkg"].Files["src/newpkg/main.go"].ModifiedTime.Before(discoveryStarted))
		assert.True(t, events["src/newpkg"].Tasks["build"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for scanned directory file event")
	}

	w.Close()
}

func TestHandleFsEvent_DirectoryCreateUsesDiscoveryTimeForScannedFiles(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	oldModTime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	mfs.addDir("src/newpkg")
	mfs.addFile("src/newpkg/main.go", oldModTime)

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}
	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
		taskConfigs: taskConfigs,
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{DefaultFSDebounce: 0, DefaultFSDebounceSet: true},
	)
	assert.NoError(t, err)

	discoveryStarted := time.Now()
	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg",
		Op:   fsnotify.Create,
	})

	select {
	case events := <-eventsChan:
		scannedFile := events["src/newpkg"].Files["src/newpkg/main.go"]
		assert.NotEqual(t, oldModTime, scannedFile.ModifiedTime)
		assert.False(t, scannedFile.ModifiedTime.Before(discoveryStarted))
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for scanned directory file event")
	}

	w.Close()
}

func TestHandleFsEvent_DirectoryCreateKeepsDiscoveryTimeAfterFileEvent(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	oldModTime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	mfs.addDir("src/newpkg")
	mfs.addFile("src/newpkg/main.go", oldModTime)

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}
	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
		taskConfigs: taskConfigs,
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{DefaultFSDebounce: 50 * time.Millisecond, DefaultFSDebounceSet: true},
	)
	assert.NoError(t, err)

	discoveryStarted := time.Now()
	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg",
		Op:   fsnotify.Create,
	})
	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg/main.go",
		Op:   fsnotify.Create,
	})

	select {
	case events := <-eventsChan:
		scannedFile := events["src/newpkg"].Files["src/newpkg/main.go"]
		assert.NotEqual(t, oldModTime, scannedFile.ModifiedTime)
		assert.False(t, scannedFile.ModifiedTime.Before(discoveryStarted))
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for debounced scanned directory file event")
	}

	w.Close()
}

func TestHandleFsEvent_FileEventMatchesDotSlashInclude(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	modTime := time.Date(2026, 4, 2, 12, 30, 0, 0, time.UTC)
	mfs.addFile("./src/pkg/main.go", modTime)

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("./src/**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}
	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src/pkg": {
				MatchedDirectory: "src/pkg",
				RelativePath:     "./src/pkg",
				TaskIds:          map[string]bool{"build": true},
			},
		},
		taskConfigs: taskConfigs,
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{DefaultFSDebounce: 0, DefaultFSDebounceSet: true},
	)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "./src/pkg/main.go",
		Op:   fsnotify.Write,
	})

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src/pkg")
		assert.Contains(t, events["src/pkg"].Files, "./src/pkg/main.go")
		assert.True(t, events["src/pkg"].Tasks["build"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for dot-slash include file event")
	}

	w.Close()
}

func TestHandleFsEvent_FileEventHonorsDotSlashExclude(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	mfs.addFile("src/generated/main.go", time.Now())

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("src/**/*.go"),
			Exclude: configutil.NewGlobArray("./src/generated/**"),
		},
	}
	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src/generated": {
				MatchedDirectory: "src/generated",
				RelativePath:     "./src/generated",
				TaskIds:          map[string]bool{"build": true},
			},
		},
		taskConfigs: taskConfigs,
	}

	propagateCalled := false
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { propagateCalled = true },
		mfs,
		fw,
		WatcherOptions{DefaultFSDebounce: 0, DefaultFSDebounceSet: true},
	)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/generated/main.go",
		Op:   fsnotify.Write,
	})

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"dot-slash exclude should still apply to normalized file events")

	w.Close()
}

func TestHandleFsEvent_DirectoryCreateSkipsUnrelatedIncludeRoot(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	mfs.addDir("vendor")
	mfs.addDir("vendor/lib")
	mfs.addFile("vendor/lib/main.go", time.Now())

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("src/**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}
	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			".": {
				MatchedDirectory: ".",
				RelativePath:     ".",
				TaskIds:          map[string]bool{"build": true},
			},
		},
		taskConfigs: taskConfigs,
	}

	propagateCalled := false
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { propagateCalled = true },
		mfs,
		fw,
		WatcherOptions{DefaultFSDebounce: 0, DefaultFSDebounceSet: true},
	)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "vendor",
		Op:   fsnotify.Create,
	})

	fw.mu.Lock()
	assert.NotContains(t, fw.addedDirs, "./vendor")
	assert.NotContains(t, fw.addedDirs, "./vendor/lib")
	fw.mu.Unlock()
	assert.NotContains(t, dirs.directories, "vendor")
	assert.NotContains(t, dirs.directories, "vendor/lib")

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"unrelated created trees should not be watched or propagated")

	w.Close()
}

func TestHandleFsEvent_DirectoryCreateScansCopiedTreeRecursively(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	modTime := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	mfs.addDir("src/newpkg")
	mfs.addDir("src/newpkg/sub")
	mfs.addFile("src/newpkg/sub/handler.go", modTime)

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}
	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
		taskConfigs: taskConfigs,
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{DefaultFSDebounce: 0, DefaultFSDebounceSet: true},
	)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg",
		Op:   fsnotify.Create,
	})

	fw.mu.Lock()
	assert.Contains(t, fw.addedDirs, "./src/newpkg")
	assert.Contains(t, fw.addedDirs, "./src/newpkg/sub")
	fw.mu.Unlock()

	select {
	case events := <-eventsChan:
		assert.Len(t, events, 1)
		assert.Contains(t, events, "src/newpkg/sub")
		assert.Contains(t, events["src/newpkg/sub"].Files, "src/newpkg/sub/handler.go")
		assert.True(t, events["src/newpkg/sub"].Tasks["build"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for recursive scanned directory file event")
	}

	w.Close()
}

func TestHandleFsEvent_DirectoryCreateSkipsGlobalExclude(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	mfs.addDir("src/node_modules")
	mfs.addFile("src/node_modules/pkg.js", time.Now())

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
		globallyExcluded: configutil.NewGlobArray("**/node_modules"),
	}

	propagateCalled := false
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) { propagateCalled = true }, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/node_modules",
		Op:   fsnotify.Create,
	})

	fw.mu.Lock()
	assert.NotContains(t, fw.addedDirs, "./src/node_modules")
	fw.mu.Unlock()

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"globally excluded directory creation should not propagate a file event")

	w.Close()
}

func TestHandleFsEvent_DirectoryCreateWatchesFutureFiles(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	mfs.addDir("src/newpkg")
	modTime := time.Date(2026, 4, 2, 11, 0, 0, 0, time.UTC)

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{DefaultFSDebounce: 0, DefaultFSDebounceSet: true},
	)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg",
		Op:   fsnotify.Create,
	})
	mfs.addFile("src/newpkg/main.go", modTime)
	w.handleFsEvent(fsnotify.Event{
		Name: "src/newpkg/main.go",
		Op:   fsnotify.Create,
	})

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src/newpkg")
		assert.Contains(t, events["src/newpkg"].Files, "src/newpkg/main.go")
		assert.Equal(t, modTime, events["src/newpkg"].Files["src/newpkg/main.go"].ModifiedTime)
		assert.True(t, events["src/newpkg"].Tasks["build"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for future file event in newly watched directory")
	}

	w.Close()
}

func TestHandleFsEvent_UnwatchedDirectory(t *testing.T) {
	// Events in directories not in WatchableDirectories should not trigger propagation.
	logger := zap.NewNop()
	mfs := newMockFS()
	mfs.addFile("other/file.txt", time.Now())

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	propagateCalled := false
	propagate := func(events map[string]AggregatedEvent) {
		propagateCalled = true
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "other/file.txt",
		Op:   fsnotify.Write,
	})

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"propagate should not be called for an unwatched directory")

	w.Close()
}

// =====================================================================
// Tests: Watcher.Close() properly shuts down
// =====================================================================

func TestWatcherClose(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	dirs := &WatchableDirectories{
		directories: make(map[string]WatchableDirectory),
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) {}, mfs, fw)
	assert.NoError(t, err)

	// Start the event loop so Close actually exercises channel closure.
	w.Start()

	// Close should not panic.
	w.Close()

	fw.mu.Lock()
	assert.True(t, fw.closed, "FsWatcher should be closed after Watcher.Close()")
	fw.mu.Unlock()
}

func TestWatcherClose_IdempotentOnFsWatcher(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	fw := newTestFsWatcher()

	dirs := &WatchableDirectories{
		directories: make(map[string]WatchableDirectory),
	}

	w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) {}, mfs, fw)
	assert.NoError(t, err)

	// First close should succeed.
	w.Close()

	fw.mu.Lock()
	assert.True(t, fw.closed)
	fw.mu.Unlock()
}

// =====================================================================
// Tests: NewWatcherWithDeps
// =====================================================================

func TestNewWatcherWithDeps_InitializesCorrectly(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	fw := newTestFsWatcher()

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	called := false
	propagate := func(events map[string]AggregatedEvent) { called = true }

	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)
	assert.NotNil(t, w)
	assert.NotNil(t, w.aggregator)
	assert.Empty(t, w.aggregator.aggregated)
	assert.Equal(t, int64(0), w.aggregator.changeId)
	assert.False(t, called)

	w.Close()
}

func TestWatcherFSDebounceForTasks_Default(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	fw := newTestFsWatcher()
	dirs := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}

	w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) {}, mfs, fw)
	assert.NoError(t, err)

	delay := w.fsDebounceForTasks(map[string]bool{"build": true})
	assert.Equal(t, 200*time.Millisecond, delay)

	w.Close()
}

func TestWatcherFSDebounceForTasks_UsesMaximumTaskDebounce(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	fw := newTestFsWatcher()
	dirs := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}

	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) {},
		mfs,
		fw,
		WatcherOptions{
			DefaultFSDebounce:    100 * time.Millisecond,
			DefaultFSDebounceSet: true,
			TaskFSDebounces: map[string]time.Duration{
				"fast": 50 * time.Millisecond,
				"slow": 750 * time.Millisecond,
			},
		},
	)
	assert.NoError(t, err)

	delay := w.fsDebounceForTasks(map[string]bool{"fast": true, "slow": true})
	assert.Equal(t, 750*time.Millisecond, delay)

	w.Close()
}

func TestWatcherFSDebounceForTasks_AllowsZeroTaskDebounce(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	fw := newTestFsWatcher()
	dirs := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}

	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) {},
		mfs,
		fw,
		WatcherOptions{
			DefaultFSDebounce:    100 * time.Millisecond,
			DefaultFSDebounceSet: true,
			TaskFSDebounces: map[string]time.Duration{
				"instant": 0,
			},
		},
	)
	assert.NoError(t, err)

	delay := w.fsDebounceForTasks(map[string]bool{"instant": true})
	assert.Equal(t, time.Duration(0), delay)

	w.Close()
}

func TestWatcherFSDebounce_PendingSlowEventIsNotFlushedByFastEvent(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	fw := newTestFsWatcher()
	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"slow": true},
			},
			"assets": {
				MatchedDirectory: "assets",
				RelativePath:     "./assets",
				TaskIds:          map[string]bool{"fast": true},
			},
		},
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{
			TaskFSDebounces: map[string]time.Duration{
				"slow": 120 * time.Millisecond,
				"fast": 0,
			},
		},
	)
	assert.NoError(t, err)
	defer w.Close()

	now := time.Now()
	w.checkIfWeNeedToNotify("src/main.go", "src", now)
	time.Sleep(10 * time.Millisecond)
	w.checkIfWeNeedToNotify("assets/app.css", "assets", now)

	select {
	case <-eventsChan:
		t.Fatal("fast event flushed pending slow event before the slow debounce elapsed")
	case <-time.After(60 * time.Millisecond):
	}

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src")
		assert.Contains(t, events, "assets")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for debounced events")
	}
}

func TestWatcherFSDebounce_DoesNotWrapGenerationForLargeBatch(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	fw := newTestFsWatcher()
	dirs := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}

	eventsChan := make(chan map[string]AggregatedEvent, 4)
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{DefaultFSDebounce: 30 * time.Millisecond, DefaultFSDebounceSet: true},
	)
	assert.NoError(t, err)
	defer w.Close()

	now := time.Now()
	for i := 0; i < 257; i++ {
		w.addAggregatedEvent(
			"src",
			fmt.Sprintf("src/file%03d.go", i),
			now,
			map[string]bool{"build": true},
		)
	}

	time.Sleep(120 * time.Millisecond)

	var batches []map[string]AggregatedEvent
	for {
		select {
		case events := <-eventsChan:
			batches = append(batches, events)
		default:
			assert.Len(t, batches, 1)
			assert.Len(t, batches[0]["src"].Files, 257)
			return
		}
	}
}

func TestWatcherFSDebounce_UsesChangedFileMatchingTasks(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	mfs.addDir("src")
	mfs.addFile("src/main.go", time.Now())
	mfs.addFile("src/style.css", time.Now())

	taskConfigs := TaskConfigMap{
		"go": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
		"css": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.css"),
			Exclude: configutil.NewGlobArray(),
		},
	}
	dirs := MatchWatchableDirectoriesWithFS(logger, configutil.GlobArray{}, taskConfigs, mfs)

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	fw := newTestFsWatcher()
	w, err := NewWatcherWithDepsAndOptions(
		logger,
		dirs,
		func(events map[string]AggregatedEvent) { eventsChan <- events },
		mfs,
		fw,
		WatcherOptions{
			TaskFSDebounces: map[string]time.Duration{
				"go":  120 * time.Millisecond,
				"css": 0,
			},
		},
	)
	assert.NoError(t, err)
	defer w.Close()

	w.checkIfWeNeedToNotify("src/style.css", "src", time.Now())

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src")
		assert.Contains(t, events["src"].Files, "src/style.css")
	case <-time.After(40 * time.Millisecond):
		t.Fatal("css-only change waited for unrelated go task debounce")
	}
}

func TestNewWatcherWithDeps_NilFsWatcherCreatesReal(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	dirs := &WatchableDirectories{
		directories: make(map[string]WatchableDirectory),
	}

	// Passing nil for fsWatcher should fall back to creating a real one.
	w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) {}, mfs, nil)
	assert.NoError(t, err)
	assert.NotNil(t, w)

	w.Close()
}

func TestNewWatcherWithDeps_WatchDirsAddsDirs(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	fw := newTestFsWatcher()

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
			"pkg": {
				MatchedDirectory: "pkg",
				RelativePath:     "./pkg",
				TaskIds:          map[string]bool{"test": true},
			},
			"internal": {
				MatchedDirectory: "internal",
				RelativePath:     "./internal",
				TaskIds:          map[string]bool{"lint": true},
			},
		},
	}

	w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) {}, mfs, fw)
	assert.NoError(t, err)

	// watchDirs is called by Start; call it directly to test Add.
	w.watchDirs()

	fw.mu.Lock()
	assert.Len(t, fw.addedDirs, 3)
	assert.Contains(t, fw.addedDirs, "./src")
	assert.Contains(t, fw.addedDirs, "./pkg")
	assert.Contains(t, fw.addedDirs, "./internal")
	fw.mu.Unlock()

	w.Close()
}

func TestNewWatcherWithDeps_WatchDirsAddError(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	fw := newTestFsWatcher()
	fw.addErr = fmt.Errorf("permission denied")

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
			"pkg": {
				MatchedDirectory: "pkg",
				RelativePath:     "./pkg",
				TaskIds:          map[string]bool{"test": true},
			},
		},
	}

	w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) {}, mfs, fw)
	assert.NoError(t, err)

	// watchDirs should return early after first Add error.
	w.watchDirs()

	fw.mu.Lock()
	// No dirs should be added because Add returns error.
	assert.Empty(t, fw.addedDirs)
	fw.mu.Unlock()

	w.Close()
}

// =====================================================================
// Tests: initFsEventWatcher integration with mock FsWatcher
// =====================================================================

func TestInitFsEventWatcher_ProcessesEvents(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	modTime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	mfs.addFile("src/app.go", modTime)

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"run": true},
			},
		},
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	propagate := func(events map[string]AggregatedEvent) {
		eventsChan <- events
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	// Start the event loop (runs initFsEventWatcher in a goroutine).
	w.Start()

	// Send event through the mock watcher channel.
	fw.sendEvent(fsnotify.Event{Name: "src/app.go", Op: fsnotify.Write})

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src")
		assert.Contains(t, events["src"].Files, "src/app.go")
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for event from initFsEventWatcher")
	}

	w.Close()
}

func TestInitFsEventWatcher_StopsOnClose(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()
	fw := newTestFsWatcher()

	dirs := &WatchableDirectories{
		directories: make(map[string]WatchableDirectory),
	}

	w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) {}, mfs, fw)
	assert.NoError(t, err)

	done := make(chan struct{})
	go func() {
		w.initFsEventWatcher()
		close(done)
	}()

	// Closing the watcher should cause initFsEventWatcher to return.
	w.Close()

	select {
	case <-done:
		// Success — goroutine exited.
	case <-time.After(2 * time.Second):
		t.Fatal("initFsEventWatcher did not exit after Close")
	}
}

// =====================================================================
// Tests: MatchWatchableDirectoriesWithFS
// =====================================================================

func TestMatchWatchableDirectoriesWithFS_BasicMatching(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()

	// Populate mock FS with files that should be walked.
	mfs.addDir("./")
	mfs.addDir("./src")
	mfs.addFile("./src/main.go", time.Now())
	mfs.addFile("./src/util.go", time.Now())
	mfs.addDir("./docs")
	mfs.addFile("./docs/readme.md", time.Now())

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	result := MatchWatchableDirectoriesWithFS(logger, configutil.GlobArray{}, taskConfigs, mfs)

	assert.NotNil(t, result)
	// The "src" directory should be watchable because it contains .go files.
	// We check that at least one directory containing .go files was matched.
	foundGoDir := false
	for _, dir := range result.directories {
		if dir.TaskIds["build"] {
			foundGoDir = true
			break
		}
	}
	assert.True(t, foundGoDir, "Expected at least one directory with 'build' task")
}

func TestMatchWatchableDirectoriesWithFS_WatchesAncestorsOfMatchedDirectories(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()

	mfs.addDir("./")
	mfs.addDir("./src")
	mfs.addDir("./src/pkg")
	mfs.addFile("./src/pkg/a.go", time.Now())

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	result := MatchWatchableDirectoriesWithFS(logger, configutil.GlobArray{}, taskConfigs, mfs)

	assert.NotNil(t, result)
	assert.Contains(t, result.directories, "src/pkg")
	assert.Contains(t, result.directories, "src")
	assert.True(t, result.directories["src"].TaskIds["build"])
}

func TestMatchWatchableDirectoriesWithFS_WatchesExistingIncludeRootWithoutMatches(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()

	mfs.addDir("./")
	mfs.addDir("./src")

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("src/**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	result := MatchWatchableDirectoriesWithFS(logger, configutil.GlobArray{}, taskConfigs, mfs)

	assert.NotNil(t, result)
	assert.Contains(t, result.directories, "src")
	assert.True(t, result.directories["src"].TaskIds["build"])
}

func TestMatchWatchableDirectoriesWithFS_WatchesNearestExistingParentForMissingIncludeRoot(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()

	mfs.addDir("./")

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("src/**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	result := MatchWatchableDirectoriesWithFS(logger, configutil.GlobArray{}, taskConfigs, mfs)

	assert.NotNil(t, result)
	assert.Contains(t, result.directories, ".")
	assert.True(t, result.directories["."].TaskIds["build"])
}

func TestMatchWatchableDirectoriesWithFS_ExcludesGlobalDirs(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()

	mfs.addDir("./")
	mfs.addDir("./src")
	mfs.addFile("./src/app.go", time.Now())
	mfs.addDir("./node_modules")
	mfs.addFile("./node_modules/pkg.js", time.Now())

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go", "**/*.js"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	globalExclude := configutil.NewGlobArray("**/node_modules")
	result := MatchWatchableDirectoriesWithFS(logger, globalExclude, taskConfigs, mfs)

	assert.NotNil(t, result)
	// node_modules should not appear in watchable directories.
	for dirPath := range result.directories {
		assert.NotContains(t, dirPath, "node_modules",
			"node_modules should be excluded")
	}
}

func TestMatchWatchableDirectoriesWithFS_MultipleTasksMatchSameDir(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()

	mfs.addDir("./")
	mfs.addDir("./src")
	mfs.addFile("./src/app.go", time.Now())

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
		"lint": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	result := MatchWatchableDirectoriesWithFS(logger, configutil.GlobArray{}, taskConfigs, mfs)
	assert.NotNil(t, result)

	// Find a directory that was matched (should have both tasks).
	for _, dir := range result.directories {
		if dir.TaskIds["build"] || dir.TaskIds["lint"] {
			// If one Go-file directory is found it should have both tasks.
			assert.True(t, dir.TaskIds["build"], "Expected 'build' task in matched dir")
			assert.True(t, dir.TaskIds["lint"], "Expected 'lint' task in matched dir")
		}
	}
}

func TestMatchWatchableDirectoriesWithFS_EmptyFS(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	result := MatchWatchableDirectoriesWithFS(logger, configutil.GlobArray{}, taskConfigs, mfs)
	assert.NotNil(t, result)
	assert.Empty(t, result.directories)
}

func TestMatchWatchableDirectoriesWithFS_SeedsRootIncludeWithoutMatches(t *testing.T) {
	logger := zap.NewNop()
	mfs := newMockFS()

	mfs.addDir("./")
	mfs.addFile("./readme.md", time.Now())

	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	result := MatchWatchableDirectoriesWithFS(logger, configutil.GlobArray{}, taskConfigs, mfs)
	assert.NotNil(t, result)

	assert.Contains(t, result.directories, ".")
	assert.True(t, result.directories["."].TaskIds["build"])
}

// =====================================================================
// Tests: verify FsWatcher interface compliance
// =====================================================================

func TestTestFsWatcher_ImplementsFsWatcher(t *testing.T) {
	// Compile-time check that testFsWatcher satisfies the FsWatcher interface.
	var _ FsWatcher = (*testFsWatcher)(nil)
}

func TestMockFS_ImplementsFileSystem(t *testing.T) {
	// Compile-time check that mockFS satisfies the util.FileSystem interface.
	var _ util.FileSystem = (*mockFS)(nil)
}

// =====================================================================
// Tests: nil-guard behavior (review comment fixes)
// =====================================================================

func TestNewWatcherWithDeps_NilFS(t *testing.T) {
	// Passing nil fs should not panic; it should default to NewOSFileSystem().
	logger := zap.NewNop()
	fw := newTestFsWatcher()

	dirs := &WatchableDirectories{
		directories: make(map[string]WatchableDirectory),
	}

	assert.NotPanics(t, func() {
		w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) {}, nil, fw)
		assert.NoError(t, err)
		assert.NotNil(t, w)
		assert.NotNil(t, w.fs, "fs should be defaulted to a non-nil FileSystem")
		w.Close()
	})
}

func TestNewWatcherWithDeps_BothNilFsAndFsWatcher(t *testing.T) {
	// Both nil should default without panic.
	logger := zap.NewNop()

	dirs := &WatchableDirectories{
		directories: make(map[string]WatchableDirectory),
	}

	assert.NotPanics(t, func() {
		w, err := NewWatcherWithDeps(logger, dirs, func(events map[string]AggregatedEvent) {}, nil, nil)
		assert.NoError(t, err)
		assert.NotNil(t, w)
		assert.NotNil(t, w.fs)
		w.Close()
	})
}

// =====================================================================
// Tests: cross-platform path normalization in handleFsEvent
// =====================================================================

func TestMatchWatchableDirectoriesWithFS_NilFS(t *testing.T) {
	// Passing nil fs should not panic; it should default to NewOSFileSystem().
	logger := zap.NewNop()
	taskConfigs := TaskConfigMap{
		"build": &config.TaskConfig{
			Include: configutil.NewGlobArray("**/*.go"),
			Exclude: configutil.NewGlobArray(),
		},
	}

	assert.NotPanics(t, func() {
		result := MatchWatchableDirectoriesWithFS(logger, configutil.GlobArray{}, taskConfigs, nil)
		assert.NotNil(t, result, "result should be non-nil even with nil fs")
	})
}

func TestHandleFsEvent_RemoveEvent_GoCompilerTempFile_NoRestart(t *testing.T) {
	// The Go compiler creates a temp file (e.g., "xxx-go-tmp-umask") in tmp/bin/,
	// then removes it. The Remove event for this temp file should not propagate
	// to the runner and should not trigger a restart of continuous tasks.
	// This was previously causing unnecessary restarts because Remove events
	// used time.Now() as their modification time, defeating the
	// filterRunningTasks deduplication check.
	logger, obs := setupObservedLogger()
	mfs := newMockFS()

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"tmp/bin": {
				MatchedDirectory: "tmp/bin",
				RelativePath:     "./tmp/bin",
				TaskIds:          map[string]bool{"backend": true},
			},
		},
	}

	propagateCalled := false
	propagate := func(events map[string]AggregatedEvent) {
		propagateCalled = true
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	// Simulate the Remove event for the Go compiler temp file
	w.handleFsEvent(fsnotify.Event{
		Name: "tmp/bin/qRQZhbuU7fD54xuc7HUqzw-go-tmp-umask",
		Op:   fsnotify.Remove,
	})

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"Remove event for Go compiler temp file should not trigger propagation")

	// Also test Remove of the old intermediate binary
	w.handleFsEvent(fsnotify.Event{
		Name: "tmp/bin/Z4gUCoCCKAdhdeXNTfwq2w",
		Op:   fsnotify.Remove,
	})

	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"Remove event for old intermediate binary should not trigger propagation")

	// Verify debug logs for both
	debugLogs := obs.FilterLevelExact(zap.DebugLevel).All()
	skipCount := 0
	for _, entry := range debugLogs {
		if entry.Message == "Skipping Remove event" {
			skipCount++
		}
	}
	assert.Equal(t, 2, skipCount, "Expected 2 'Skipping Remove event' debug logs")

	w.Close()
}

func TestHandleFsEvent_BackslashPathNormalization(t *testing.T) {
	// Simulates a Windows-style backslash path on a Linux host.
	// The watcher should normalize it to forward slashes.
	logger := zap.NewNop()
	mfs := newMockFS()
	modTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	mfs.addFile("src/main.go", modTime)

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"src": {
				MatchedDirectory: "src",
				RelativePath:     "./src",
				TaskIds:          map[string]bool{"build": true},
			},
		},
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	propagate := func(events map[string]AggregatedEvent) {
		eventsChan <- events
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	// Send event with Windows-style backslash path
	w.handleFsEvent(fsnotify.Event{
		Name: "src\\main.go",
		Op:   fsnotify.Write,
	})

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src")
		agg := events["src"]
		// The path should be normalized to forward slashes
		assert.Contains(t, agg.Files, "src/main.go")
		assert.True(t, agg.Tasks["build"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for propagated event with backslash path")
	}

	w.Close()
}
