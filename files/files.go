package files

import (
	"os"
	"path/filepath"
	"time"
)

type MatchedFile struct {
	Path    string
	ModTime time.Time
}

func RemoveNewestFile(files []MatchedFile) []MatchedFile {
	if len(files) == 0 {
		return files
	}

	newestFileIndex := 0
	newestFileTime := files[0].ModTime

	for i, file := range files {
		if file.ModTime.After(newestFileTime) {
			newestFileTime = file.ModTime
			newestFileIndex = i
		}
	}

	return append(files[:newestFileIndex], files[newestFileIndex+1:]...)
}

func GetNewestFile(files []MatchedFile) string {
	if len(files) == 0 {
		return ""
	}

	newestFileIndex := 0
	newestFileTime := files[0].ModTime

	for i, file := range files {
		if file.ModTime.After(newestFileTime) {
			newestFileTime = file.ModTime
			newestFileIndex = i
		}
	}

	return files[newestFileIndex].Path
}

func walker(
	addMatch func(string, time.Time),
	originalPath string,
) func(path string, info os.FileInfo, err error) error {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		matchedPath := filepath.ToSlash(path)
		if !info.IsDir() {
			addMatch(matchedPath, info.ModTime())
		} else if path != originalPath {
			return filepath.SkipDir
		}

		return nil
	}
}

func GetFilesInDir(dir string) ([]MatchedFile, error) {
	dir = filepath.FromSlash(dir)

	var matched = make([]MatchedFile, 0)

	addMatch := func(path string, modTime time.Time) {
		matched = append(matched,
			MatchedFile{Path: path, ModTime: modTime})
	}

	err := filepath.Walk(dir, walker(addMatch, dir))
	if err != nil {
		return nil, err
	}

	return matched, nil
}
