package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func validYAML() []byte {
	return []byte(`
shared:
  exclude:
    - "node_modules/**"
  log-level: debug

tasks:
  server:
    type: continuous
    run: "go run ./cmd/server"
    include:
      - "**/*.go"
    exclude:
      - "**/*_test.go"

  build:
    type: build
    run:
      - "go build -o out ./cmd/app"
    include:
      - "**/*.go"
`)
}

func TestLoadFromBytes_ValidConfig(t *testing.T) {
	cfg, err := LoadFromBytes(validYAML())
	assert.NoError(t, err)
	assert.NotNil(t, cfg)

	assert.Equal(t, "debug", cfg.Shared.LogLevel)
	assert.True(t, cfg.Shared.Exclude.AnyMatches("node_modules/foo/bar.js"))

	assert.Len(t, cfg.Tasks, 2)

	server := cfg.Tasks["server"]
	assert.NotNil(t, server)
	assert.Equal(t, TaskType_Continuous, server.Type)
	assert.Equal(t, []string{"go run ./cmd/server"}, []string(server.Run))

	build := cfg.Tasks["build"]
	assert.NotNil(t, build)
	assert.Equal(t, TasKType_Build, build.Type)
	assert.Equal(t, []string{"go build -o out ./cmd/app"}, []string(build.Run))
}

func TestLoadFromBytes_FillsTaskIds(t *testing.T) {
	cfg, err := LoadFromBytes(validYAML())
	assert.NoError(t, err)
	assert.NotNil(t, cfg)

	for id, task := range cfg.Tasks {
		assert.Equal(t, id, task.Identifier, "task identifier should match map key")
	}
}

func TestLoadFromBytes_DefaultLogLevel(t *testing.T) {
	yml := []byte(`
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)
	assert.NoError(t, err)
	assert.Equal(t, "info", cfg.Shared.LogLevel)
}

func TestLoadFromBytes_InvalidYAML(t *testing.T) {
	yml := []byte(`invalid: [yaml: broken`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "error unmarshalling YAML data")
}

func TestLoadFromBytes_MissingRunCommand(t *testing.T) {
	yml := []byte(`
tasks:
  server:
    type: continuous
    include:
      - "**/*.go"
`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "run command is missing")
}

func TestLoadFromBytes_MissingType(t *testing.T) {
	yml := []byte(`
tasks:
  server:
    run: "go run ."
`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "type is missing")
}

func TestLoadFromBytes_InvalidTaskType(t *testing.T) {
	yml := []byte(`
tasks:
  server:
    type: invalid-type
    run: "go run ."
`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "invalid task type")
}

func TestLoadFromBytes_DuplicateRunCommand(t *testing.T) {
	yml := []byte(`
tasks:
  server:
    type: continuous
    run: "go run ."
    run-linux: "go run ."
`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "run command is duplicated")
}

func TestLoadFromBytes_InvalidDependency(t *testing.T) {
	yml := []byte(`
tasks:
  server:
    type: continuous
    run: "go run ."
    dependencies:
      - nonexistent-task
`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "dependency 'nonexistent-task' does not exist")
}

func TestLoadFromBytes_ValidDependency(t *testing.T) {
	yml := []byte(`
tasks:
  build:
    type: build
    run: "go build ."
    include:
      - "**/*.go"
  server:
    type: continuous
    run: "go run ."
    dependencies:
      - build
`)
	cfg, err := LoadFromBytes(yml)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
}

func TestLoadFromBytes_CleanUpTasks(t *testing.T) {
	yml := []byte(`
tasks:
  build:
    type: build
    run: "go build ."
    include:
      - "**/*.go"

cleanup-tasks:
  clean-logs:
    include:
      - "logs/**"
    strategy: keep-latest
`)
	cfg, err := LoadFromBytes(yml)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Len(t, cfg.CleanUpTasks, 1)
	assert.Equal(t, "clean-logs", cfg.CleanUpTasks["clean-logs"].Identifier)
	assert.Equal(t, CleanUpStrategy_KeepLatest, cfg.CleanUpTasks["clean-logs"].Strategy)
}

func TestLoadFromBytes_CleanUpTasks_InvalidStrategy(t *testing.T) {
	yml := []byte(`
tasks:
  build:
    type: build
    run: "go build ."
    include:
      - "**/*.go"

cleanup-tasks:
  clean-logs:
    include:
      - "logs/**"
    strategy: delete-all
`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "invalid task type")
}

func TestLoadFromBytes_EmptyTasks(t *testing.T) {
	yml := []byte(`
tasks: {}
`)
	cfg, err := LoadFromBytes(yml)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Empty(t, cfg.Tasks)
}

func TestLoadFromBytes_PlatformSpecificRun(t *testing.T) {
	yml := []byte(`
tasks:
  server:
    type: continuous
    run-linux: "go run . -linux"
    run-windows: "go run . -windows"
    run-mac: "go run . -mac"
`)
	cfg, err := LoadFromBytes(yml)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	server := cfg.Tasks["server"]
	assert.Equal(t, []string{"go run . -linux"}, []string(server.RunLinux))
	assert.Equal(t, []string{"go run . -windows"}, []string(server.RunWindows))
	assert.Equal(t, []string{"go run . -mac"}, []string(server.RunMac))
}

func TestFillTaskIds(t *testing.T) {
	cfg := &Config{
		Tasks: map[string]*TaskConfig{
			"alpha": {Run: []string{"echo alpha"}},
			"beta":  {Run: []string{"echo beta"}},
		},
		CleanUpTasks: map[string]*CleanUpTaskConfig{
			"cleanup-x": {Strategy: CleanUpStrategy_KeepLatest},
		},
	}

	fillTaskIds(cfg)

	assert.Equal(t, "alpha", cfg.Tasks["alpha"].Identifier)
	assert.Equal(t, "beta", cfg.Tasks["beta"].Identifier)
	assert.Equal(t, "cleanup-x", cfg.CleanUpTasks["cleanup-x"].Identifier)
}

func TestFillTaskIds_NilCleanUpTasks(t *testing.T) {
	cfg := &Config{
		Tasks: map[string]*TaskConfig{
			"app": {Run: []string{"echo app"}},
		},
		CleanUpTasks: nil,
	}

	// Should not panic
	fillTaskIds(cfg)
	assert.Equal(t, "app", cfg.Tasks["app"].Identifier)
}

func TestLoadFromYAMLWithFS_NilFS(t *testing.T) {
	// Passing nil fs should not panic; it should default to NewOSFileSystem().
	// The call will fail because asynk.yaml likely doesn't exist in the test dir,
	// but the important thing is it doesn't panic from nil dereference.
	assert.NotPanics(t, func() {
		_, _ = LoadFromYAMLWithFS(nil)
	})
}
