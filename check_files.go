package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/config/util"
	"github.com/anton7r/asynk/files"
)

func walker(
	addMatch func(string, time.Time),
	cleanUpTask *config.CleanUpTaskConfig,
	globallyExcluded util.GlobArray,
) func(path string, info os.FileInfo, err error) error {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		path = filepath.ToSlash(path)
		if !info.IsDir() {
			// Check if the file is globally excluded
			if globallyExcluded.AnyMatches(path) {
				return nil
			}

			// Check if the file matches the cleanUpTask's include and exclude patterns
			if cleanUpTask.Include.AnyMatches(path) && !cleanUpTask.Exclude.AnyMatches(path) {
				addMatch(path, info.ModTime())
			}
		} else {
			// Skip directories that are globally excluded
			if globallyExcluded.AnyMatches(path) {
				return filepath.SkipDir
			}
		}

		return nil
	}
}

func GetMatchedFiles(
	cleanUpTask *config.CleanUpTaskConfig,
	globallyExcluded util.GlobArray,
) ([]files.MatchedFile, error) {
	var matched = make([]files.MatchedFile, 0)

	addMatch := func(path string, modTime time.Time) {
		matched = append(matched,
			files.MatchedFile{Path: path, ModTime: modTime})
	}

	err := filepath.Walk(".", walker(addMatch, cleanUpTask, globallyExcluded))
	if err != nil {
		return nil, err
	}

	return matched, nil
}
