package newestfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestInterpolate_NoPattern(t *testing.T) {
	result := Interpolate("hello world")
	assert.Equal(t, "hello world", result)
}

func TestInterpolate_EmptyInput(t *testing.T) {
	result := Interpolate("")
	assert.Equal(t, "", result)
}

func TestInterpolate_NonExistentDir(t *testing.T) {
	result := Interpolate("~{/nonexistent/path/that/does/not/exist}")
	// Should return the original pattern when dir doesn't exist
	assert.Equal(t, "~{/nonexistent/path/that/does/not/exist}", result)
}

func TestInterpolate_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	result := Interpolate("~{" + tmpDir + "}")
	// Empty dir returns original pattern since no files found
	assert.Equal(t, "~{"+tmpDir+"}", result)
}

func TestInterpolate_DirWithFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files with different modification times
	file1 := filepath.Join(tmpDir, "old.txt")
	file2 := filepath.Join(tmpDir, "new.txt")

	err := os.WriteFile(file1, []byte("old"), 0644)
	assert.NoError(t, err)
	// Set an older mod time on file1
	oldTime := time.Now().Add(-1 * time.Hour)
	err = os.Chtimes(file1, oldTime, oldTime)
	assert.NoError(t, err)

	err = os.WriteFile(file2, []byte("new"), 0644)
	assert.NoError(t, err)

	result := Interpolate("~{" + tmpDir + "}")
	// Should return the path of the newest file (file2)
	assert.Contains(t, result, "new.txt")
	assert.NotContains(t, result, "~{")
}

func TestInterpolate_WithSurroundingText(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tmpDir, "only.txt"), []byte("content"), 0644)
	assert.NoError(t, err)

	result := Interpolate("prefix-~{" + tmpDir + "}-suffix")
	assert.Contains(t, result, "prefix-")
	assert.Contains(t, result, "-suffix")
	assert.Contains(t, result, "only.txt")
	assert.NotContains(t, result, "~{")
}

func TestInterpolate_MultiplePatterns(t *testing.T) {
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	err := os.WriteFile(filepath.Join(tmpDir1, "a.txt"), []byte("a"), 0644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir2, "b.txt"), []byte("b"), 0644)
	assert.NoError(t, err)

	input := "~{" + tmpDir1 + "} and ~{" + tmpDir2 + "}"
	result := Interpolate(input)
	assert.Contains(t, result, "a.txt")
	assert.Contains(t, result, "b.txt")
	assert.Contains(t, result, " and ")
}

func TestInterpolate_SingleFile(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "single.txt"), []byte("content"), 0644)
	assert.NoError(t, err)

	result := Interpolate("~{" + tmpDir + "}")
	assert.Contains(t, result, "single.txt")
}
