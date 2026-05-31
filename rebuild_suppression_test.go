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
	"github.com/anton7r/asynk/watcher"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
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
	runner.initializeRebuildSuppression()

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
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.go", "package main\nfunc main() {}\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_UnchangedInputSkipsAfterErroredRun(t *testing.T) {
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
	runner.initializeRebuildSuppression()
	runner.onTaskFinished("build", true)

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Empty(t, result)
}

func TestRebuildSuppression_FailedChangedInputRetriesUnchangedFingerprint(t *testing.T) {
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
	fs.setFile("./src/main.go", "package main\nvar value = 1\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.go", "package main\nvar value = 2\n", time.Now().Add(time.Second))

	first := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, first, "build")

	runner.onTaskFinished("build", true)

	second := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, second, "build")
}

func TestRebuildSuppression_RunningBuildDoesNotAcceptQueuedFingerprint(t *testing.T) {
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
	fs.setFile("./src/main.go", "package main\nvar value = 1\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.go", "package main\nvar value = 2\n", time.Now().Add(time.Second))

	first := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, first, "build")

	runner.onTaskFinished("build", false)

	second := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, second, "build")
}

func TestRebuildSuppression_LanguageAwareGoSkipsFormattingOnlyChange(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\nfunc main(){println(\"x\")}\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.go", "package main\n\nfunc main() {\n\tprintln(\"x\")\n}\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Empty(t, result)
}

func TestRebuildSuppression_LanguageAwareGoPreservesSemicolonSensitiveBraceNewline(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\nfunc f() bool { return false }\nfunc main() { switch f() { case true: println(\"tag\") } }\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.go", "package main\nfunc f() bool { return false }\nfunc main() { switch f()\n{ case true: println(\"tag\") } }\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareGoPreservesInsertedSemicolonAfterReturn(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\nfunc value() int { return 1 }\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.go", "package main\nfunc value() int { return\n1 }\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareCacheUsesContentHash(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	modTime := time.Unix(1_700_000_000, 0)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\nvar A = 1\n", modTime)
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.go", "package main\nvar A = 2\n", modTime)

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareInvalidGoFallsBackToRaw(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\nfunc main(){println(\"unterminated)}\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.go", "package main\n\nfunc main() { println(\"unterminated) }\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareJSSkipsSafeWhitespaceAndLineEndSemicolons(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.js"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.js", "const x = 1;\nconst y = x + 1;\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.js", "const   x=1\nconst y=x+1\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Empty(t, result)
}

func TestRebuildSuppression_LanguageAwareJSPreservesMiddleSemicolons(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.js"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.js", "for (let i = 0; i < 3; i++) console.log(i)\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.js", "for (let i = 0 i < 3; i++) console.log(i)\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareJSPreservesASIRiskySemicolons(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.js"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.js", "const fn = function() {};\n(function(){})()\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.js", "const fn = function() {}\n(function(){})()\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareJSPreservesASILineTerminators(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.js"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.js", "function f() { return value }\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.js", "function f() { return\nvalue }\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareJSXPreservesTextWhitespace(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.jsx"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/component.jsx", "const view = <><span>A </span>B</>\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/component.jsx", "const view = <><span>A</span>B</>\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareJSPreservesComments(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.ts"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.ts", "const x = 1 // old\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.ts", "const x = 1 // new\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareSQLSkipsWhitespaceOnlyChange(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.sql"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./schema")
	fs.setFile("./schema/query.sql", "SELECT id, name FROM users WHERE id = $1;\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./schema/query.sql", "SELECT\n  id,\n  name\nFROM users\nWHERE id=$1;\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Empty(t, result)
}

func TestRebuildSuppression_LanguageAwareSQLPreservesNewlineBetweenAdjacentStrings(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.sql"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./schema")
	fs.setFile("./schema/query.sql", "SELECT 'a'\n'b';\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./schema/query.sql", "SELECT 'a' 'b';\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareSQLPreservesComments(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.sql"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./schema")
	fs.setFile("./schema/query.sql", "-- name: GetUser :one\nSELECT * FROM users;\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./schema/query.sql", "-- name: ListUsers :many\nSELECT * FROM users;\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareSQLPreservesSemicolonChanges(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.sql"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./schema")
	fs.setFile("./schema/migration.sql", "SELECT 1;\nSELECT 2;\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./schema/migration.sql", "SELECT 1\nSELECT 2\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareSQLHandlesStringsAndQuotedIdentifiers(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.sql"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./schema")
	fs.setFile(
		"./schema/query.sql",
		"SELECT 'a b', \"User Name\", [order item], $$literal value$$ FROM `user table` WHERE name = 'O''Reilly';\n",
		time.Now(),
	)
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile(
		"./schema/query.sql",
		"SELECT\n'a b',\n\"User Name\",\n[order item],\n$$literal value$$\nFROM `user table`\nWHERE name='O''Reilly';\n",
		time.Now().Add(time.Second),
	)

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Empty(t, result)
}

func TestRebuildSuppression_LanguageAwareInvalidSQLFallsBackToRaw(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.sql"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./schema")
	fs.setFile("./schema/query.sql", "SELECT 'unterminated\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./schema/query.sql", "SELECT   'unterminated\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareUnknownExtensionFallsBackToRaw(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.txt"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.setFile("./notes.txt", "alpha beta\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./notes.txt", "alphabeta\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_LanguageAwareInvalidJSFallsBackToRaw(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.js"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.setFile("./main.js", "const x = \"unterminated\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./main.js", "const   x = \"unterminated\n", time.Now().Add(time.Second))

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
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
	runner.initializeRebuildSuppression()

	delete(fs.files, "./src/main.go")

	result := runner.filterUnchangedRebuildInputs(schedulableBuildTask(cfg))
	assert.Contains(t, result, "build")
}

func TestRebuildSuppression_SkippedProviderDoesNotScheduleConsumers(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  provider:
    type: continuous
    run: "echo provider"
    include:
      - "**/*.go"
    port:
      preferred: 3000
    rebuild-suppression:
      enabled: true
  consumer:
    type: continuous
    run: "echo consumer"
    consumes:
      - task: provider
        env:
          PROVIDER_URL: url
        on-change: restart
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	runner.onFileChange(map[string]watcher.AggregatedEvent{
		"src": {
			Dir: "src",
			Files: map[string]*watcher.UpdatedFile{
				"src/main.go": {ModifiedTime: time.Now()},
			},
			Tasks: map[string]bool{"provider": true},
		},
	})

	runner.ScheduledTaskMutex.Lock()
	defer runner.ScheduledTaskMutex.Unlock()
	assert.Empty(t, runner.ScheduledTasks)
}

func TestRebuildSuppression_ContinuousRestartAcceptsStartedFingerprint(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  provider:
    type: continuous
    run: "echo provider"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\nvar value = 1\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.go", "package main\nvar value = 2\n", time.Now().Add(time.Second))
	first := runner.filterUnchangedRebuildInputs(map[string]*SchedulableTask{
		"provider": {
			TaskConfiguration: cfg.Tasks["provider"],
			ModificationTime:  time.Now(),
		},
	})
	assert.Contains(t, first, "provider")

	runner.onTaskFinished("provider", true)
	runner.startScheduledTask("provider", &ScheduledTask{TaskConfiguration: cfg.Tasks["provider"]})

	second := runner.filterUnchangedRebuildInputs(map[string]*SchedulableTask{
		"provider": {
			TaskConfiguration: cfg.Tasks["provider"],
			ModificationTime:  time.Now(),
		},
	})
	assert.Empty(t, second)
}

func TestRebuildSuppression_ContinuousFailedStartDoesNotAcceptFingerprint(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  provider:
    type: continuous
    run: "echo provider"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
`)
	fs := newRebuildSuppressionTestFS()
	fs.addDir("./src")
	fs.setFile("./src/main.go", "package main\nvar value = 1\n", time.Now())
	runner := newRebuildSuppressionRunner(t, cfg, fs)
	runner.initializeRebuildSuppression()

	fs.setFile("./src/main.go", "package main\nvar value = 2\n", time.Now().Add(time.Second))
	first := runner.filterUnchangedRebuildInputs(map[string]*SchedulableTask{
		"provider": {
			TaskConfiguration: cfg.Tasks["provider"],
			ModificationTime:  time.Now(),
		},
	})
	assert.Contains(t, first, "provider")

	runner.recordStartedRebuildSuppressionTask("provider")
	runner.onTaskFinished("provider", true)

	second := runner.filterUnchangedRebuildInputs(map[string]*SchedulableTask{
		"provider": {
			TaskConfiguration: cfg.Tasks["provider"],
			ModificationTime:  time.Now(),
		},
	})
	assert.Contains(t, second, "provider")
}

func TestRebuildSuppression_LanguageAwareLogsDuration(t *testing.T) {
	cfg := rebuildSuppressionConfig(t, `
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
    rebuild-suppression:
      enabled: true
      mode: language-aware-hash
`)
	fs := newRebuildSuppressionTestFS()
	fs.setFile("./main.go", "package main\n", time.Now())
	core, logs := observer.New(zap.InfoLevel)
	runner := newRebuildSuppressionRunnerWithLogger(t, cfg, fs, zap.New(core))

	runner.initializeRebuildSuppression()

	matching := logs.FilterMessage("Constructed language-aware rebuild suppression fingerprint").All()
	if assert.Len(t, matching, 1) {
		fields := matching[0].ContextMap()
		assert.Equal(t, "build", fields["taskId"])
		assert.Equal(t, int64(1), fields["totalFiles"])
		assert.Equal(t, int64(1), fields["languageAwareFiles"])
		assert.Equal(t, int64(0), fields["fallbackFiles"])
		assert.Contains(t, fields, "durationMs")
	}
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
	return newRebuildSuppressionRunnerWithLogger(t, cfg, fs, testLogger())
}

func newRebuildSuppressionRunnerWithLogger(
	t *testing.T,
	cfg *config.Config,
	fs util.FileSystem,
	logger *zap.Logger,
) *Runner {
	t.Helper()
	runner := NewRunnerWithDeps(cfg, logger, true, RunnerDeps{
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
