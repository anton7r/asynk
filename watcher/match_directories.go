package watcher

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/config/util"
	asynkutil "github.com/anton7r/asynk/util"

	"go.uber.org/zap"
)

type WatchableDirectory struct {
	MatchedDirectory string
	RelativePath     string
	TaskIds          map[string]bool
}

type WatchableDirectories struct {
	directories      map[string]WatchableDirectory
	taskConfigs      TaskConfigMap
	globallyExcluded util.GlobArray
}

type TaskConfigMap = map[string]*config.TaskConfig

func newWatchableDirectoryMatcher(
	watchable *WatchableDirectories,
	globallyExcluded util.GlobArray,
	taskConfigs TaskConfigMap,
	logger *zap.Logger,
) func(pathStr string, info os.FileInfo, err error) error {
	return func(pathStr string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		pathStr = normalizePathForLookup(pathStr)

		// Skip directories that are globally excluded
		isDir := info.IsDir()
		if isDir && globallyExcludedPathMatches(globallyExcluded, pathStr) {
			return filepath.SkipDir
		}

		// Get the directory path
		dirPath := getDirPath(pathStr, isDir)

		// Check if this path should be watched by any task
		matchedTasks := getMatchingTasks(pathStr, taskConfigs)

		// If we have matching tasks, update the watchable directories
		if len(matchedTasks) > 0 {
			logger.Debug("Path matched",
				zap.String("path", pathStr),
				zap.String("dirPath", dirPath))

			updateWatchableDirectoryWithAncestors(watchable, dirPath, matchedTasks)
		} else {
			logger.Debug("Path not matched",
				zap.String("path", pathStr),
				zap.String("dirPath", dirPath))
		}

		return nil
	}
}

// Helper function to get directory path in a consistent way
func getDirPath(pathStr string, isDir bool) string {
	if isDir {
		return pathStr
	}

	// Use path.Dir for OS-agnostic directory paths
	return path.Dir(pathStr)
	//return filepath.Dir(pathStr)
}

// Helper function to find tasks that should watch this path
func getMatchingTasks(pathStr string, taskConfigs TaskConfigMap) map[string]bool {
	matchedTasks := make(map[string]bool)

	for taskId, taskConfig := range taskConfigs {
		// A path is included if it matches an include pattern and doesn't match any exclude pattern
		if !taskConfig.Exclude.AnyMatches(pathStr) && taskConfig.Include.AnyMatches(pathStr) {
			matchedTasks[taskId] = true
		}
	}

	return matchedTasks
}

// Helper function to update the watchable directories map
func updateWatchableDirectory(watchable *WatchableDirectories, dirPath string, matchedTasks map[string]bool) {
	dirPath = normalizePathForLookup(dirPath)
	wDir, dirExists := watchable.directories[dirPath]

	if !dirExists {
		// Create a new watchable directory entry
		relativePath := "./"
		if dirPath != "." {
			relativePath += dirPath
		} else {
			relativePath = "."
		}

		wDir = WatchableDirectory{
			MatchedDirectory: dirPath,
			RelativePath:     relativePath,
			TaskIds:          make(map[string]bool),
		}
	}

	// Add all matching tasks
	for taskId := range matchedTasks {
		wDir.TaskIds[taskId] = true
	}

	// Update the map
	watchable.directories[dirPath] = wDir
}

func updateWatchableDirectoryWithAncestors(watchable *WatchableDirectories, dirPath string, matchedTasks map[string]bool) {
	dirPath = normalizePathForLookup(dirPath)
	for {
		updateWatchableDirectory(watchable, dirPath, matchedTasks)
		if dirPath == "." {
			return
		}

		parentDirPath := path.Dir(dirPath)
		if parentDirPath == dirPath {
			return
		}
		dirPath = parentDirPath
	}
}

func globallyExcludedPathMatches(globallyExcluded util.GlobArray, pathStr string) bool {
	if globallyExcluded.AnyMatches(pathStr) {
		return true
	}

	if pathStr != "." && !strings.HasPrefix(pathStr, "./") {
		return globallyExcluded.AnyMatches("./" + pathStr)
	}

	return false
}

func map2fields[T any](m map[string]T) []zap.Field {
	fields := make([]zap.Field, 0, len(m))
	for k, v := range m {
		fields = append(fields, zap.Any(k, v))
	}
	return fields
}

func MatchWatchableDirectories(
	log *zap.Logger,
	globallyExcluded util.GlobArray,
	taskConfigs TaskConfigMap,
) *WatchableDirectories {
	return MatchWatchableDirectoriesWithFS(log, globallyExcluded, taskConfigs, asynkutil.NewOSFileSystem())
}

func MatchWatchableDirectoriesWithFS(
	log *zap.Logger,
	globallyExcluded util.GlobArray,
	taskConfigs TaskConfigMap,
	fs asynkutil.FileSystem,
) *WatchableDirectories {
	if fs == nil {
		fs = asynkutil.NewOSFileSystem()
	}
	watchable := &WatchableDirectories{
		directories:      make(map[string]WatchableDirectory),
		taskConfigs:      taskConfigs,
		globallyExcluded: globallyExcluded,
	}

	// List all items in the directory - assume current directory
	if err := fs.Walk("./", newWatchableDirectoryMatcher(watchable, globallyExcluded, taskConfigs, log)); err != nil {
		log.Error("Failed to walk filesystem for watchable directories", zap.Error(err))
	}

	log.Info("Matched watchable directories",
		zap.Int("matched_directories", len(watchable.directories)),
		zap.Dict("watchable", map2fields(watchable.directories)...),
		zap.Dict("task_configs", map2fields(taskConfigs)...),
	)

	return watchable
}
