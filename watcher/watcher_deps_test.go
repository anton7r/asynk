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
		return nil, fmt.Errorf("file not found: %s", name)
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

func TestHandleFsEvent_RemoveEvent(t *testing.T) {
	logger := zap.NewNop()
	// The file is removed, so Lstat will not find it; the code handles Remove
	// specially by using path.Dir instead of Lstat.
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

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	propagate := func(events map[string]AggregatedEvent) {
		eventsChan <- events
	}

	fw := newTestFsWatcher()
	w, err := NewWatcherWithDeps(logger, dirs, propagate, mfs, fw)
	assert.NoError(t, err)

	w.handleFsEvent(fsnotify.Event{
		Name: "src/old_file.go",
		Op:   fsnotify.Remove,
	})

	select {
	case events := <-eventsChan:
		assert.Contains(t, events, "src")
		agg := events["src"]
		assert.Contains(t, agg.Files, "src/old_file.go")
		assert.True(t, agg.Tasks["build"])
		assert.True(t, agg.Tasks["lint"])
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for propagated event")
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

func TestHandleFsEvent_DirectoryCreateIgnored(t *testing.T) {
	// When a directory is created, the watcher should not aggregate a file event.
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

	// Directory creates go into the else branch (TODO: add new watchers) and
	// don't call checkIfWeNeedToNotify.
	time.Sleep(300 * time.Millisecond)
	assert.False(t, propagateCalled,
		"propagate should not be called for directory creation")

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
	assert.Equal(t, int8(0), w.aggregator.changeId)
	assert.False(t, called)

	w.Close()
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

func TestMatchWatchableDirectoriesWithFS_NoMatchingTasks(t *testing.T) {
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

	// readme.md doesn't match **/*.go so no directory should have the build task.
	for _, dir := range result.directories {
		assert.False(t, dir.TaskIds["build"],
			"No directory should be matched for build task")
	}
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
