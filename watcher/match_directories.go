package watcher

import (
	"asynk/config"
	"asynk/config/util"
	"os"
	"path"
	"path/filepath"
)

type WatchableDirectory struct {
	Directory string
	TaskIds   map[string]bool
}

type WatchableDirectories struct {
	directories map[string]WatchableDirectory
}

type TaskConfigMap = map[string]*config.TaskConfig

func newWatchableDirectoryMatcher(
	watchable *WatchableDirectories, globallyExcluded util.GlobArray, taskConfigs TaskConfigMap,
) func(pathStr string, info os.FileInfo, err error) error {
	return func(pathStr string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		isDir := info.IsDir()

		if isDir {
			if anyMatches(globallyExcluded, pathStr) {
				return filepath.SkipDir
			}
		}

		var dirPath string
		if isDir {
			dirPath = pathStr
		} else {
			// We exclusively use the directory path for task identification.
			// and therefore have to use a operation system agnostic way to get the directory path.
			// Because filepath.Dir() would format the path differently on different systems, because of
			// filepath.FromSlash.
			dirPath = path.Dir(pathStr)
		}

		wDir, exists := watchable.directories[dirPath]

		for taskId, taskConfig := range taskConfigs {
			// Already matched so we skip it. If not, check if it should be included.
			if exists && wDir.TaskIds[taskId] {
				continue
			}

			if !anyMatches(taskConfig.Exclude, pathStr) && anyMatches(taskConfig.Include, pathStr) {
				if !exists {
					wDir = WatchableDirectory{
						Directory: dirPath,
						TaskIds:   make(map[string]bool),
					}
				}
				wDir.TaskIds[taskId] = true
			}
		}

		if len(wDir.TaskIds) > 0 {
			watchable.directories[dirPath] = wDir

		}

		return nil
	}
}

func MatchWatchableDirectories(
	globallyExcluded util.GlobArray, taskConfigs TaskConfigMap,
) *WatchableDirectories {

	watchable := &WatchableDirectories{directories: make(map[string]WatchableDirectory)}

	// List all items in the directory - assume current directory
	filepath.Walk("./", newWatchableDirectoryMatcher(watchable, globallyExcluded, taskConfigs))

	return nil
}

func anyMatches(globs util.GlobArray, path string) bool {
	if len(globs) == 0 {
		return false
	}

	for _, glob := range globs {
		if glob.Match(path) {
			return true
		}
	}

	return false
}
