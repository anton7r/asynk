package watcher

import (
	"asynk/config"
	"asynk/config/util"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewWatchableDirectoryMatcher_ReturnsErrorOnInputError(t *testing.T) {
	watchable := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}
	globallyExcluded := util.GlobArray{}
	taskConfigs := TaskConfigMap{}

	matcher := newWatchableDirectoryMatcher(watchable, globallyExcluded, taskConfigs)

	expectedErr := fmt.Errorf("input error")
	err := matcher("some/path", nil, expectedErr)

	assert.Equal(t, expectedErr, err, "Expected error does not match")
}

func TestNewWatchableDirectoryMatcher_SkipsGloballyExcludedDirectory(t *testing.T) {
	watchable := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}
	globallyExcluded := util.NewGlobArray("**/excluded/**")
	taskConfigs := TaskConfigMap{
		"task1": &config.TaskConfig{
			Include: util.NewGlobArray("**/*.go"),
			Exclude: util.NewGlobArray(),
		},
	}

	matcher := newWatchableDirectoryMatcher(watchable, globallyExcluded, taskConfigs)

	info := &mockFileInfo{isDir: true}
	err := matcher("some/excluded/path", info, nil)

	assert.Equal(t, filepath.SkipDir, err, "Expected SkipDir error")
}

func TestNewWatchableDirectoryMatcher_DoesNotSkipNonExcludedDirectory(t *testing.T) {
	watchable := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}
	globallyExcluded := util.NewGlobArray("**/excluded/**")
	taskConfigs := TaskConfigMap{
		"task1": &config.TaskConfig{
			Include: util.NewGlobArray("**/*.go"),
			Exclude: util.NewGlobArray(),
		},
	}

	matcher := newWatchableDirectoryMatcher(watchable, globallyExcluded, taskConfigs)

	info := &mockFileInfo{isDir: false}
	err := matcher("some/included/path.go", info, nil)

	assert.NoError(t, err, "Expected no error")

	// This would fail on windows if we used the filepath package
	// as it calls filepath.FromSlash internally.
	// It makes forward slahes in the path to be converted to backslashes on
	// windows systems.
	assert.Contains(t, watchable.directories, "some/included",
		"Expected directory 'some/included' to be added to watchable directories")
}

func TestNewWatchableDirectoryMatcher_AddsDirectoryWithMatchingTask(t *testing.T) {
	watchable := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}
	globallyExcluded := util.GlobArray{}
	taskConfigs := TaskConfigMap{
		"task1": &config.TaskConfig{
			Include: util.NewGlobArray("**/*.go"),
			Exclude: util.NewGlobArray("**/excluded/**"),
		},
	}

	matcher := newWatchableDirectoryMatcher(watchable, globallyExcluded, taskConfigs)

	info := &mockFileInfo{isDir: false}
	err := matcher("some/included/path.go", info, nil)

	assert.NoError(t, err, "Expected no error")
	assert.Contains(t, watchable.directories, "some/included",
		"Expected directory 'some/included' to be added to watchable directories")
	assert.Contains(t, watchable.directories["some/included"].TaskIds, "task1",
		"Expected task 'task1' to be associated with 'some/included' directory")
}
func TestNewWatchableDirectoryMatcher_ExcludeOnlyTasks(t *testing.T) {
	watchable := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}
	globallyExcluded := util.GlobArray{}
	taskConfigs := TaskConfigMap{
		"task1": &config.TaskConfig{
			Include: util.NewGlobArray("**/*.go"),
			Exclude: util.NewGlobArray("**/excluded/**"),
		},
	}

	matcher := newWatchableDirectoryMatcher(watchable, globallyExcluded, taskConfigs)

	info := &mockFileInfo{isDir: false}
	err := matcher("some/excluded/path.go", info, nil)

	assert.NoError(t, err, "Expected no error")
	assert.NotContains(t, watchable.directories, "some/excluded",
		"Expected directory 'some/excluded' not to be added to watchable directories")
}

func TestNewWatchableDirectoryMatcher_UpdatesExistingDirectoryTaskIds(t *testing.T) {
	watchable := &WatchableDirectories{
		directories: map[string]WatchableDirectory{
			"some/included": {
				MatchedDirectory: "some/included",
				TaskIds:          map[string]bool{"task1": true},
			},
		},
	}
	globallyExcluded := util.GlobArray{}
	taskConfigs := TaskConfigMap{
		"task1": &config.TaskConfig{
			Include: util.NewGlobArray("**/*.go"),
			Exclude: util.NewGlobArray(),
		},
		"task2": &config.TaskConfig{
			Include: util.NewGlobArray("**/*.go"),
			Exclude: util.NewGlobArray(),
		},
	}

	matcher := newWatchableDirectoryMatcher(watchable, globallyExcluded, taskConfigs)

	info := &mockFileInfo{isDir: false}
	err := matcher("some/included/path.go", info, nil)

	assert.NoError(t, err, "Expected no error")
	assert.Contains(t, watchable.directories, "some/included",
		"Expected directory 'some/included' to be in watchable directories")
	assert.Contains(t, watchable.directories["some/included"].TaskIds, "task1",
		"Expected task 'task1' to remain associated with 'some/included' directory")
	assert.Contains(t, watchable.directories["some/included"].TaskIds, "task2",
		"Expected task 'task2' to be added to 'some/included' directory")
}

func TestNewWatchableDirectoryMatcher_NoMatchingIncludePatterns(t *testing.T) {
	watchable := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}
	globallyExcluded := util.GlobArray{}
	taskConfigs := TaskConfigMap{
		"task1": &config.TaskConfig{
			Include: util.NewGlobArray("**/*.js"),
			Exclude: util.NewGlobArray(),
		},
		"task2": &config.TaskConfig{
			Include: util.NewGlobArray("**/*.css"),
			Exclude: util.NewGlobArray(),
		},
	}

	matcher := newWatchableDirectoryMatcher(watchable, globallyExcluded, taskConfigs)

	info := &mockFileInfo{isDir: false}
	err := matcher("some/path/file.txt", info, nil)

	assert.NoError(t, err, "Expected no error")
	assert.NotContains(t, watchable.directories, "some/path",
		"Expected directory 'some/path' not to be added to watchable directories")
}

func BenchmarkNewWatchableDirectoryMatcher(b *testing.B) {
	watchable := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}
	globallyExcluded := util.GlobArray{}
	taskConfigs := TaskConfigMap{
		"task1": &config.TaskConfig{
			Include: util.NewGlobArray("**/*.go"),
			Exclude: util.GlobArray{},
		},
	}

	matcher := newWatchableDirectoryMatcher(watchable, globallyExcluded, taskConfigs)

	b.ResetTimer()

	info := &mockFileInfo{isDir: true}
	for i := 0; i < b.N; i++ {
		err := matcher("some/path", info, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

type mockFileInfo struct {
	isDir bool
}

func (m *mockFileInfo) Name() string       { return "" }
func (m *mockFileInfo) Size() int64        { return 0 }
func (m *mockFileInfo) Mode() os.FileMode  { return 0 }
func (m *mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() interface{}   { return nil }
