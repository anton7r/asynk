package watcher

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

// MockFsWatcher is a mock implementation of fsnotify.Watcher
type MockFsWatcher struct {
	mock.Mock
	events chan fsnotify.Event
	errors chan error
	mu     sync.Mutex
}

func NewMockFsWatcher() *MockFsWatcher {
	return &MockFsWatcher{
		events: make(chan fsnotify.Event),
		errors: make(chan error),
	}
}

func (m *MockFsWatcher) Add(name string) error {
	args := m.Called(name)
	return args.Error(0)
}

func (m *MockFsWatcher) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	close(m.events)
	close(m.errors)
	return nil
}

// SendEvent sends a mock event
func (m *MockFsWatcher) SendEvent(event fsnotify.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events <- event
}

// SendError sends a mock error
func (m *MockFsWatcher) SendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors <- err
}

// TestNewWatcher tests the creation of a new Watcher
func TestNewWatcher(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	dirs := &WatchableDirectories{
		directories: make(map[string]WatchableDirectory),
	}

	propagateFunc := func(events map[string]AggregatedEvent) {}

	watcher, err := NewWatcher(logger, dirs, propagateFunc)
	assert.NoError(t, err)
	assert.NotNil(t, watcher)

	// Clean up
	watcher.Close()
}

// TestCheckIfWeNeedToNotify tests the checkIfWeNeedToNotify method
func TestCheckIfWeNeedToNotify(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// Create temporary test directory structure
	tempDir, err := os.MkdirTemp("", "watcher_test_*")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Setup test directory and file paths
	testDirPath := filepath.Join(tempDir, "testdir")
	testFilePath := filepath.Join(testDirPath, "testfile.txt")

	err = os.MkdirAll(testDirPath, 0755)
	assert.NoError(t, err)

	f, err := os.Create(testFilePath)
	assert.NoError(t, err)
	f.Close()

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			testDirPath: {
				MatchedDirectory: "testdir",
				RelativePath:     testDirPath,
				TaskIds:          map[string]bool{"task1": true, "task2": true},
			},
		},
	}

	// Create a channel to receive propagated events
	eventsChan := make(chan map[string]AggregatedEvent, 1)

	propagateFunc := func(events map[string]AggregatedEvent) {
		eventsChan <- events
	}

	watcher, err := NewWatcher(logger, dirs, propagateFunc)
	assert.NoError(t, err)

	// Test the method
	watcher.checkIfWeNeedToNotify(testFilePath, testDirPath, time.Now())

	// Wait for propagation with timeout
	select {
	case events := <-eventsChan:
		// Verify the event was properly aggregated
		assert.Contains(t, events, testDirPath)
		assert.Contains(t, events[testDirPath].Files, testFilePath)
		assert.Contains(t, events[testDirPath].Tasks, "task1")
		assert.Contains(t, events[testDirPath].Tasks, "task2")
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for event propagation")
	}

	// Clean up
	watcher.Close()
}

// TestHandleFsEvent tests the handleFsEvent method
func TestHandleFsEvent(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// Create temporary test directory structure
	tempDir, err := os.MkdirTemp("", "watcher_test_*")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testDirPath := filepath.Join(tempDir, "testdir")
	testFilePath := filepath.Join(testDirPath, "testfile.txt")

	err = os.MkdirAll(testDirPath, 0755)
	assert.NoError(t, err)

	f, err := os.Create(testFilePath)
	assert.NoError(t, err)
	f.Close()

	dirs := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			testDirPath: {
				MatchedDirectory: "testdir",
				RelativePath:     testDirPath,
				TaskIds:          map[string]bool{"task1": true},
			},
		},
	}

	// Channel to track propagated events
	eventsChan := make(chan map[string]AggregatedEvent, 1)

	propagateFunc := func(events map[string]AggregatedEvent) {
		eventsChan <- events
	}

	watcher, err := NewWatcher(logger, dirs, propagateFunc)
	assert.NoError(t, err)

	// Test file modification event
	modifyEvent := fsnotify.Event{
		Name: testFilePath,
		Op:   fsnotify.Write,
	}

	watcher.handleFsEvent(modifyEvent)

	// Wait for propagation with timeout
	select {
	case events := <-eventsChan:
		// Verify the event
		assert.Contains(t, events, testDirPath)
		assert.Contains(t, events[testDirPath].Files, testFilePath)
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for event propagation")
	}

	// Test removal event (file) — Remove events should now be skipped
	// and NOT trigger propagation, since deleted files are not actionable.
	removeEvent := fsnotify.Event{
		Name: testFilePath,
		Op:   fsnotify.Remove,
	}

	watcher.handleFsEvent(removeEvent)

	// Should NOT propagate
	select {
	case <-eventsChan:
		t.Fatal("Remove event should not trigger propagation")
	case <-time.After(300 * time.Millisecond):
		// Expected — Remove events are skipped
	}

	// Clean up
	watcher.Close()
}

// TestFileEventAggregator tests the FileEventAggregator
func TestFileEventAggregator(t *testing.T) {
	aggregator := &FileEventAggregator{
		aggregated:     make(map[string]AggregatedEvent),
		aggregatorLock: sync.Mutex{},
		changeId:       0,
	}

	// Test adding events
	aggregator.aggregatorLock.Lock()

	dir1 := "/path/to/dir1"
	file1 := "/path/to/dir1/file1.txt"
	taskId1 := "task1"

	event := AggregatedEvent{
		Dir: dir1,
		Files: map[string]*UpdatedFile{file1: &UpdatedFile{
			ModifiedTime: time.Now(),
		}},
		Tasks: map[string]bool{taskId1: true},
	}

	aggregator.aggregated[dir1] = event
	aggregator.changeId++
	aggregator.aggregatorLock.Unlock()

	// Verify the events were aggregated correctly
	aggregator.aggregatorLock.Lock()
	assert.Contains(t, aggregator.aggregated, dir1)
	assert.Contains(t, aggregator.aggregated[dir1].Files, file1)
	assert.Contains(t, aggregator.aggregated[dir1].Tasks, taskId1)
	aggregator.aggregatorLock.Unlock()
}

// TestPropagateEvents tests the propagateEvents method
func TestPropagateEvents(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	dirs := &WatchableDirectories{
		directories: make(map[string]WatchableDirectory),
	}

	eventsChan := make(chan map[string]AggregatedEvent, 1)
	propagateFunc := func(events map[string]AggregatedEvent) {
		eventsChan <- events
	}

	watcher, err := NewWatcher(logger, dirs, propagateFunc)
	assert.NoError(t, err)

	// Setup test data
	watcher.aggregator.aggregatorLock.Lock()
	dir1 := "/path/to/dir1"
	file1 := "/path/to/dir1/file1.txt"
	taskId1 := "task1"

	event := AggregatedEvent{
		Dir:   dir1,
		Files: map[string]*UpdatedFile{file1: &UpdatedFile{ModifiedTime: time.Now()}},
		Tasks: map[string]bool{taskId1: true},
	}

	watcher.aggregator.aggregated[dir1] = event
	currentChangeId := watcher.aggregator.changeId
	watcher.aggregator.aggregatorLock.Unlock()

	// Test propagation
	watcher.propagateEvents(0, currentChangeId)

	// Verify events were propagated
	select {
	case events := <-eventsChan:
		assert.Contains(t, events, dir1)
		assert.Contains(t, events[dir1].Files, file1)
		assert.Contains(t, events[dir1].Tasks, taskId1)

		// Verify the map was cleared
		watcher.aggregator.aggregatorLock.Lock()
		assert.Empty(t, watcher.aggregator.aggregated)
		watcher.aggregator.aggregatorLock.Unlock()
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for event propagation")
	}

	// Clean up
	watcher.Close()
}
