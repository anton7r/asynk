package watcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWatchableDirectories(t *testing.T) {
	dirs := &WatchableDirectories{
		directories: make(map[string]WatchableDirectory),
	}

	// Add a test directory
	testDir := WatchableDirectory{
		MatchedDirectory: "testdir",
		RelativePath:     "/path/to/testdir",
		TaskIds:          map[string]bool{"task1": true},
	}

	dirs.directories[testDir.RelativePath] = testDir

	// Verify the directory was added
	assert.Contains(t, dirs.directories, testDir.RelativePath)
	assert.Equal(t, testDir, dirs.directories[testDir.RelativePath])

	// Test adding a task to an existing directory
	updatedDir := WatchableDirectory{
		MatchedDirectory: "testdir",
		RelativePath:     "/path/to/testdir",
		TaskIds:          map[string]bool{"task1": true, "task2": true},
	}

	dirs.directories[updatedDir.RelativePath] = updatedDir

	// Verify the directory was updated
	assert.Contains(t, dirs.directories[updatedDir.RelativePath].TaskIds, "task2")
	assert.Equal(t, 2, len(dirs.directories[updatedDir.RelativePath].TaskIds))
}
