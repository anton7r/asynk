package files

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetNewestFile_Empty(t *testing.T) {
	result := GetNewestFile([]MatchedFile{})
	assert.Equal(t, "", result)
}

func TestGetNewestFile_SingleFile(t *testing.T) {
	files := []MatchedFile{
		{Path: "a.txt", ModTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	result := GetNewestFile(files)
	assert.Equal(t, "a.txt", result)
}

func TestGetNewestFile_MultipleFiles(t *testing.T) {
	files := []MatchedFile{
		{Path: "old.txt", ModTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Path: "newest.txt", ModTime: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)},
		{Path: "mid.txt", ModTime: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)},
	}
	result := GetNewestFile(files)
	assert.Equal(t, "newest.txt", result)
}

func TestGetNewestFile_FirstElementIsNewest(t *testing.T) {
	files := []MatchedFile{
		{Path: "newest.txt", ModTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Path: "old.txt", ModTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	result := GetNewestFile(files)
	assert.Equal(t, "newest.txt", result)
}

func TestGetNewestFile_LastElementIsNewest(t *testing.T) {
	files := []MatchedFile{
		{Path: "old.txt", ModTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Path: "newest.txt", ModTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	result := GetNewestFile(files)
	assert.Equal(t, "newest.txt", result)
}

func TestRemoveNewestFile_Empty(t *testing.T) {
	result := RemoveNewestFile([]MatchedFile{})
	assert.Empty(t, result)
}

func TestRemoveNewestFile_SingleFile(t *testing.T) {
	files := []MatchedFile{
		{Path: "only.txt", ModTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	result := RemoveNewestFile(files)
	assert.Empty(t, result)
}

func TestRemoveNewestFile_MultipleFiles(t *testing.T) {
	input := []MatchedFile{
		{Path: "old.txt", ModTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Path: "newest.txt", ModTime: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)},
		{Path: "mid.txt", ModTime: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)},
	}
	// Make a copy since RemoveNewestFile modifies the underlying slice
	inputCopy := make([]MatchedFile, len(input))
	copy(inputCopy, input)

	result := RemoveNewestFile(inputCopy)
	assert.Len(t, result, 2)

	paths := []string{result[0].Path, result[1].Path}
	assert.Contains(t, paths, "old.txt")
	assert.Contains(t, paths, "mid.txt")
	assert.NotContains(t, paths, "newest.txt")
}

func TestRemoveNewestFile_FirstIsNewest(t *testing.T) {
	input := []MatchedFile{
		{Path: "newest.txt", ModTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Path: "old.txt", ModTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	inputCopy := make([]MatchedFile, len(input))
	copy(inputCopy, input)

	result := RemoveNewestFile(inputCopy)
	assert.Len(t, result, 1)
	assert.Equal(t, "old.txt", result[0].Path)
}

func TestGetFilesInDir(t *testing.T) {
	// Create a temp directory with some files
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")

	err := os.WriteFile(file1, []byte("content1"), 0644)
	assert.NoError(t, err)

	// Sleep briefly to ensure different mod times
	time.Sleep(10 * time.Millisecond)

	err = os.WriteFile(file2, []byte("content2"), 0644)
	assert.NoError(t, err)

	files, err := GetFilesInDir(tmpDir)
	assert.NoError(t, err)
	assert.Len(t, files, 2)

	// Check that both files are present
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	assert.Contains(t, paths, filepath.ToSlash(file1))
	assert.Contains(t, paths, filepath.ToSlash(file2))
}

func TestGetFilesInDir_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	files, err := GetFilesInDir(tmpDir)
	assert.NoError(t, err)
	assert.Empty(t, files)
}

func TestGetFilesInDir_SkipsSubdirectories(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file in the root
	err := os.WriteFile(filepath.Join(tmpDir, "root.txt"), []byte("root"), 0644)
	assert.NoError(t, err)

	// Create a subdirectory with a file
	subDir := filepath.Join(tmpDir, "subdir")
	err = os.Mkdir(subDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("nested"), 0644)
	assert.NoError(t, err)

	files, err := GetFilesInDir(tmpDir)
	assert.NoError(t, err)
	// Should only contain the root file, not the nested file
	assert.Len(t, files, 1)
	assert.Contains(t, files[0].Path, "root.txt")
}

func TestGetFilesInDir_NonExistentDir(t *testing.T) {
	_, err := GetFilesInDir("/nonexistent/path/that/does/not/exist")
	assert.Error(t, err)
}

func TestMatchedFile_Struct(t *testing.T) {
	now := time.Now()
	mf := MatchedFile{
		Path:    "/some/path/file.txt",
		ModTime: now,
	}
	assert.Equal(t, "/some/path/file.txt", mf.Path)
	assert.Equal(t, now, mf.ModTime)
}

func TestGetNewestFile_SameModTimes(t *testing.T) {
	sameTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	files := []MatchedFile{
		{Path: "a.txt", ModTime: sameTime},
		{Path: "b.txt", ModTime: sameTime},
		{Path: "c.txt", ModTime: sameTime},
	}
	// When all times are equal, the first file wins (After is strict)
	result := GetNewestFile(files)
	assert.Equal(t, "a.txt", result)
}
