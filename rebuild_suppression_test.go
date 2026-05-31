package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/util"
	"github.com/stretchr/testify/assert"
)

type rebuildSuppressionTestFS struct {
	files map[string]rebuildSuppressionTestEntry
}

type rebuildSuppressionTestEntry struct {
	isDir   bool
	modTime time.Time
	content []byte
}

func newRebuildSuppressionTestFS() *rebuildSuppressionTestFS {
	return &rebuildSuppressionTestFS{
		files: map[string]rebuildSuppressionTestEntry{
			"./": {isDir: true, modTime: time.Now()},
		},
	}
}

func (f *rebuildSuppressionTestFS) addDir(path string) {
	f.files[path] = rebuildSuppressionTestEntry{isDir: true, modTime: time.Now()}
}

func (f *rebuildSuppressionTestFS) setFile(path string, content string, modTime time.Time) {
	f.files[path] = rebuildSuppressionTestEntry{
		modTime: modTime,
		content: []byte(content),
	}
}

func (f *rebuildSuppressionTestFS) Lstat(name string) (os.FileInfo, error) {
	entry, ok := f.files[name]
	if !ok {
		return nil, &os.PathError{Op: "lstat", Path: name, Err: os.ErrNotExist}
	}
	return rebuildSuppressionTestInfo{name: filepath.Base(name), entry: entry}, nil
}

func (f *rebuildSuppressionTestFS) ReadFile(name string) ([]byte, error) {
	entry, ok := f.files[name]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", name)
	}
	return append([]byte{}, entry.content...), nil
}

func (f *rebuildSuppressionTestFS) Walk(root string, walkFn filepath.WalkFunc) error {
	paths := make([]string, 0, len(f.files))
	for path := range f.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	skipPrefix := ""
	for _, path := range paths {
		if skipPrefix != "" && strings.HasPrefix(path, skipPrefix) {
			continue
		}
		skipPrefix = ""

		entry := f.files[path]
		info := rebuildSuppressionTestInfo{name: filepath.Base(path), entry: entry}
		if err := walkFn(path, info, nil); err != nil {
			if err == filepath.SkipDir {
				if entry.isDir {
					skipPrefix = strings.TrimRight(path, "/") + "/"
				}
				continue
			}
			return err
		}
	}
	return nil
}

func (f *rebuildSuppressionTestFS) Remove(name string) error {
	delete(f.files, name)
	return nil
}

func (f *rebuildSuppressionTestFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return nil, nil
}

type rebuildSuppressionTestInfo struct {
	name  string
	entry rebuildSuppressionTestEntry
}

func (i rebuildSuppressionTestInfo) Name() string       { return i.name }
func (i rebuildSuppressionTestInfo) Size() int64        { return int64(len(i.entry.content)) }
func (i rebuildSuppressionTestInfo) Mode() os.FileMode  { return 0644 }
func (i rebuildSuppressionTestInfo) ModTime() time.Time { return i.entry.modTime }
func (i rebuildSuppressionTestInfo) IsDir() bool        { return i.entry.isDir }
func (i rebuildSuppressionTestInfo) Sys() interface{}   { return nil }

func TestRebuildSuppression_RawHashSkipsNoOpSave(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
shared:
  rebuild-suppression:
    enabled: true
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)

	runner.recordRebuildSuppressionTaskResult("build", false)
	fs.setFile("./src/main.go", "package main\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Empty(t, result)
}

func TestRebuildSuppression_RawHashSchedulesContentChange(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
shared:
  rebuild-suppression:
    enabled: true
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)

	runner.recordRebuildSuppressionTaskResult("build", false)
	fs.setFile("./src/main.go", "package main\nfunc main() {}\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_WhitespaceNormalizationSkipsWhitespaceOnlyChange(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
      normalize: ignore-whitespace
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\nfunc main() {}\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)

	runner.recordRebuildSuppressionTaskResult("build", false)
	fs.setFile("./src/main.go", "package   main\n\nfunc main() {  }\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Empty(t, result)
}

func TestRebuildSuppression_NonAlnumNormalizationSkipsPunctuationOnlyChange(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.txt"
    rebuild-suppression:
      enabled: true
      normalize: ignore-non-alnum
`)
	fs := newRebuildSuppressionTestFS()
	fs.setFile("./notes.txt", "alpha-beta", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)

	runner.recordRebuildSuppressionTaskResult("build", false)
	fs.setFile("./notes.txt", "alpha_beta!!!", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Empty(t, result)
}

func TestRebuildSuppression_RebuildsUnchangedInputAfterFailureByDefault(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)

	runner.recordRebuildSuppressionTaskResult("build", true)

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_CanSuppressUnchangedInputAfterFailure(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
      after-failure: suppress
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)

	runner.recordRebuildSuppressionTaskResult("build", true)

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Empty(t, result)
}

func TestRebuildSuppression_DeletionChangesFingerprint(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)

	runner.recordRebuildSuppressionTaskResult("build", false)
	delete(fs.files, "./src/main.go")

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func rebuildSuppressionConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, err := config.LoadFromBytes([]byte(yaml))
	assert.NoError(t, err)
	return cfg
}

func newRebuildSuppressionRunner(
	t *testing.T,
	cfg *config.Config,
	fs util.FileSystem,
) *Runner {
	t.Helper()
	runner := NewRunnerWithDeps(cfg, testLogger(), true, RunnerDeps{
		Platform:   testPlatform(),
		FS:         fs,
		CmdFactory: &mockCommandFactory{},
	})
	assert.NotNil(t, runner)
	return runner
}

func schedulableBuildTask(cfg *config.Config) map[string]*SchedulableTask {
	return map[string]*SchedulableTask{
		"build": {
			TaskConfiguration: cfg.Tasks["build"],
			ModificationTime:  time.Now(),
		},
	}
}
