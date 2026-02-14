package util

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ===== Platform tests =====

func TestNewPlatform(t *testing.T) {
	p := NewPlatform()
	assert.NotNil(t, p)
	assert.Equal(t, runtime.GOOS, p.OS)
}

func TestPlatform_IsWindows(t *testing.T) {
	p := &Platform{OS: "windows"}
	assert.True(t, p.IsWindows())
	assert.False(t, p.IsLinux())
	assert.False(t, p.IsMac())
}

func TestPlatform_IsLinux(t *testing.T) {
	p := &Platform{OS: "linux"}
	assert.False(t, p.IsWindows())
	assert.True(t, p.IsLinux())
	assert.False(t, p.IsMac())
}

func TestPlatform_IsMac(t *testing.T) {
	p := &Platform{OS: "darwin"}
	assert.False(t, p.IsWindows())
	assert.False(t, p.IsLinux())
	assert.True(t, p.IsMac())
}

func TestPlatform_UnknownOS(t *testing.T) {
	p := &Platform{OS: "freebsd"}
	assert.False(t, p.IsWindows())
	assert.False(t, p.IsLinux())
	assert.False(t, p.IsMac())
}

func TestPlatform_EmptyOS(t *testing.T) {
	p := &Platform{OS: ""}
	assert.False(t, p.IsWindows())
	assert.False(t, p.IsLinux())
	assert.False(t, p.IsMac())
}

// Test deprecated global functions
func TestIsWindows(t *testing.T) {
	expected := runtime.GOOS == "windows"
	assert.Equal(t, expected, IsWindows())
}

func TestIsLinux(t *testing.T) {
	expected := runtime.GOOS == "linux"
	assert.Equal(t, expected, IsLinux())
}

func TestIsMac(t *testing.T) {
	expected := runtime.GOOS == "darwin"
	assert.Equal(t, expected, IsMac())
}

// ===== FileSystem tests =====

func TestNewOSFileSystem(t *testing.T) {
	fs := NewOSFileSystem()
	assert.NotNil(t, fs)
}

func TestOSFileSystem_ImplementsInterface(t *testing.T) {
	var _ FileSystem = NewOSFileSystem()
}

func TestOSFileSystem_Lstat(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	err := os.WriteFile(filePath, []byte("hello"), 0644)
	assert.NoError(t, err)

	fs := NewOSFileSystem()
	info, err := fs.Lstat(filePath)
	assert.NoError(t, err)
	assert.NotNil(t, info)
	assert.Equal(t, "test.txt", info.Name())
	assert.False(t, info.IsDir())
}

func TestOSFileSystem_Lstat_NonExistent(t *testing.T) {
	fs := NewOSFileSystem()
	_, err := fs.Lstat("/nonexistent/path/file.txt")
	assert.Error(t, err)
}

func TestOSFileSystem_ReadFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	expected := []byte("hello world")
	err := os.WriteFile(filePath, expected, 0644)
	assert.NoError(t, err)

	fs := NewOSFileSystem()
	content, err := fs.ReadFile(filePath)
	assert.NoError(t, err)
	assert.Equal(t, expected, content)
}

func TestOSFileSystem_ReadFile_NonExistent(t *testing.T) {
	fs := NewOSFileSystem()
	_, err := fs.ReadFile("/nonexistent/file.txt")
	assert.Error(t, err)
}

func TestOSFileSystem_Walk(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("b"), 0644)
	assert.NoError(t, err)

	fs := NewOSFileSystem()
	var visited []string
	err = fs.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			visited = append(visited, info.Name())
		}
		return nil
	})
	assert.NoError(t, err)
	sort.Strings(visited)
	assert.Equal(t, []string{"a.txt", "b.txt"}, visited)
}

func TestOSFileSystem_Remove(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "to_remove.txt")
	err := os.WriteFile(filePath, []byte("delete me"), 0644)
	assert.NoError(t, err)

	fs := NewOSFileSystem()
	err = fs.Remove(filePath)
	assert.NoError(t, err)

	_, err = os.Stat(filePath)
	assert.True(t, os.IsNotExist(err))
}

func TestOSFileSystem_Remove_NonExistent(t *testing.T) {
	fs := NewOSFileSystem()
	err := fs.Remove("/nonexistent/file.txt")
	assert.Error(t, err)
}

func TestOSFileSystem_ReadDir(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("1"), 0644)
	assert.NoError(t, err)
	err = os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)
	assert.NoError(t, err)

	fs := NewOSFileSystem()
	entries, err := fs.ReadDir(tmpDir)
	assert.NoError(t, err)
	assert.Len(t, entries, 2)

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	sort.Strings(names)
	assert.Equal(t, []string{"file1.txt", "subdir"}, names)
}

func TestOSFileSystem_ReadDir_NonExistent(t *testing.T) {
	fs := NewOSFileSystem()
	_, err := fs.ReadDir("/nonexistent/dir")
	assert.Error(t, err)
}

// ===== Slice utility tests =====

func TestEmpty_NilSlice(t *testing.T) {
	assert.True(t, Empty(nil))
}

func TestEmpty_EmptySlice(t *testing.T) {
	assert.True(t, Empty([]string{}))
}

func TestEmpty_AllEmptyStrings(t *testing.T) {
	assert.True(t, Empty([]string{"", "", ""}))
}

func TestEmpty_WithNonEmptyString(t *testing.T) {
	assert.False(t, Empty([]string{"hello"}))
}

func TestEmpty_MixedEmptyAndNonEmpty(t *testing.T) {
	assert.False(t, Empty([]string{"", "hello", ""}))
}

func TestEmpty_SingleNonEmpty(t *testing.T) {
	assert.False(t, Empty([]string{"a"}))
}

func TestCollectMapKeys_EmptyMap(t *testing.T) {
	m := map[string]int{}
	keys := CollectMapKeys(m)
	assert.Empty(t, keys)
}

func TestCollectMapKeys_SingleKey(t *testing.T) {
	m := map[string]int{"hello": 1}
	keys := CollectMapKeys(m)
	assert.Len(t, keys, 1)
	assert.Contains(t, []string(keys), "hello")
}

func TestCollectMapKeys_MultipleKeys(t *testing.T) {
	m := map[string]string{"a": "1", "b": "2", "c": "3"}
	keys := CollectMapKeys(m)
	assert.Len(t, keys, 3)
	sorted := []string(keys)
	sort.Strings(sorted)
	assert.Equal(t, []string{"a", "b", "c"}, sorted)
}

func TestCollectMapKeys_WithDifferentValueTypes(t *testing.T) {
	m := map[string]bool{"x": true, "y": false}
	keys := CollectMapKeys(m)
	assert.Len(t, keys, 2)
	sorted := []string(keys)
	sort.Strings(sorted)
	assert.Equal(t, []string{"x", "y"}, sorted)
}

func TestArrayType(t *testing.T) {
	var a Array[string] = []string{"a", "b"}
	assert.Len(t, a, 2)
	assert.Equal(t, "a", a[0])
	assert.Equal(t, "b", a[1])
}
