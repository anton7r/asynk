package config

import (
	"os"
	"path/filepath"
	"testing"

	asynkutil "github.com/anton7r/asynk/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigSupportsStructuredRunAndCwd(t *testing.T) {
	configPath := writeTestConfig(t, `
tasks:
  frontend:
    type: continuous
    cwd: ../client
    run:
      command: pnpm
      args: [run, dev]
`)

	cfg, err := loadConfigFromYAML(configPath, asynkutil.NewOSFileSystem())
	require.NoError(t, err)

	task := cfg.Tasks["frontend"]
	require.NotNil(t, task)
	require.Len(t, task.Run, 1)

	assert.Equal(t, filepath.Dir(configPath), cfg.ConfigDir)
	assert.Equal(t, filepath.Dir(configPath), task.ConfigDir)
	assert.Equal(t, "../client", task.Cwd)
	assert.False(t, task.Run[0].Legacy)
	assert.Equal(t, "pnpm", task.Run[0].Command)
	assert.Equal(t, []string{"run", "dev"}, []string(task.Run[0].Args))
}

func TestLoadConfigSupportsSequentialStructuredRun(t *testing.T) {
	configPath := writeTestConfig(t, `
tasks:
  build:
    type: build
    run:
      - command: goose
        args: [up]
      - command: jet
        args:
          - -path=./gen/jet/
`)

	cfg, err := loadConfigFromYAML(configPath, asynkutil.NewOSFileSystem())
	require.NoError(t, err)

	run := cfg.Tasks["build"].Run
	require.Len(t, run, 2)
	assert.Equal(t, "goose", run[0].Command)
	assert.Equal(t, []string{"up"}, []string(run[0].Args))
	assert.Equal(t, "jet", run[1].Command)
	assert.Equal(t, []string{"-path=./gen/jet/"}, []string(run[1].Args))
}

func TestLoadConfigKeepsLegacyRunStrings(t *testing.T) {
	configPath := writeTestConfig(t, `
tasks:
  build:
    type: build
    run:
      - go build -o "./tmp dir/app.exe" .
      - echo "hello world"
`)

	cfg, err := loadConfigFromYAML(configPath, asynkutil.NewOSFileSystem())
	require.NoError(t, err)

	run := cfg.Tasks["build"].Run
	require.Len(t, run, 2)
	assert.True(t, run[0].Legacy)
	assert.Equal(t, `go build -o "./tmp dir/app.exe" .`, run[0].Command)
	assert.True(t, run[1].Legacy)
	assert.Equal(t, `echo "hello world"`, run[1].Command)
}

func TestLoadConfigSupportsPlatformSpecificStructuredRun(t *testing.T) {
	configPath := writeTestConfig(t, `
tasks:
  build:
    type: build
    run-windows:
      command: go
      args: [test, ./...]
`)

	cfg, err := loadConfigFromYAML(configPath, asynkutil.NewOSFileSystem())
	require.NoError(t, err)

	run := cfg.Tasks["build"].RunWindows
	require.Len(t, run, 1)
	assert.Equal(t, "go", run[0].Command)
	assert.Equal(t, []string{"test", "./..."}, []string(run[0].Args))
}

func TestLoadConfigCopiesWorkingDirAliasToCwd(t *testing.T) {
	configPath := writeTestConfig(t, `
tasks:
  api:
    type: build
    working-dir: services/api
    run:
      command: go
      args: [test, ./...]
`)

	cfg, err := loadConfigFromYAML(configPath, asynkutil.NewOSFileSystem())
	require.NoError(t, err)

	assert.Equal(t, "services/api", cfg.Tasks["api"].Cwd)
}

func TestLoadConfigRejectsConflictingCwdAliases(t *testing.T) {
	configPath := writeTestConfig(t, `
tasks:
  api:
    type: build
    cwd: services/api
    working-dir: other
    run: go test ./...
`)

	_, err := loadConfigFromYAML(configPath, asynkutil.NewOSFileSystem())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cwd and working-dir have different values")
}

func TestLoadConfigRejectsShellCommandWithArgs(t *testing.T) {
	configPath := writeTestConfig(t, `
tasks:
  dev:
    type: continuous
    run:
      shell: true
      command: cd ../client && pnpm run dev
      args: [ignored]
`)

	_, err := loadConfigFromYAML(configPath, asynkutil.NewOSFileSystem())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shell commands cannot define args")
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "asynk.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0644))
	return configPath
}
