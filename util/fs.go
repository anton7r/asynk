package util

import (
	"io/fs"
	"os"
	"path/filepath"
)

// FileSystem abstracts file system operations, enabling tests to mock
// OS interactions without touching the real file system.
type FileSystem interface {
	// Lstat returns file info without following symlinks.
	Lstat(name string) (os.FileInfo, error)

	// ReadFile reads the entire contents of a file.
	ReadFile(name string) ([]byte, error)

	// Walk walks the file tree rooted at root, calling fn for each file or directory.
	Walk(root string, fn filepath.WalkFunc) error

	// Remove removes the named file or empty directory.
	Remove(name string) error

	// ReadDir reads the named directory and returns a list of directory entries.
	ReadDir(name string) ([]fs.DirEntry, error)
}

// OSFileSystem is the real file system implementation that delegates to the os package.
type OSFileSystem struct{}

func NewOSFileSystem() *OSFileSystem {
	return &OSFileSystem{}
}

func (f *OSFileSystem) Lstat(name string) (os.FileInfo, error) {
	return os.Lstat(name)
}

func (f *OSFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (f *OSFileSystem) Walk(root string, fn filepath.WalkFunc) error {
	return filepath.Walk(root, fn)
}

func (f *OSFileSystem) Remove(name string) error {
	return os.Remove(name)
}

func (f *OSFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(name)
}
