package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/anton7r/asynk/cmdwrap"
	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/task"
	"github.com/anton7r/asynk/util"
	"github.com/anton7r/asynk/util/interpolation/idgen"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// ============================================================
// Cross-Platform Scenario Tests
//
// These tests prove that the refactored architecture allows
// testing Windows-specific behavior on Linux (and vice versa)
// by injecting mock implementations of platform abstractions.
// ============================================================

// mockWindowsProcessManager simulates Windows process management on any OS.
type mockWindowsProcessManager struct {
	setupCalled bool
	killCalled  bool
}

func (m *mockWindowsProcessManager) SetupProcessGroup(cmd *exec.Cmd) {
	m.setupCalled = true
	// Windows: no-op for process groups (unlike Unix Setpgid)
}

func (m *mockWindowsProcessManager) CancelProcess(cmd *exec.Cmd) error {
	m.killCalled = true
	// Windows: would call Process.Kill() directly (no SIGTERM)
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Kill()
	}
	return nil
}

// trackingCommandFactory records which commands were parsed and for which task.
type trackingCommandFactory struct {
	calls []trackingFactoryCall
}

type trackingFactoryCall struct {
	commands config.RunCommands
	taskId   string
	cwd      string
}

func (f *trackingCommandFactory) ParseAllCommands(
	commands config.RunCommands,
	taskId string,
	log *zap.Logger,
	env map[string]string,
	cwd string,
	genIdInterpolator *idgen.GenIDInterpolator,
) []*cmdwrap.CommandWrapper {
	f.calls = append(f.calls, trackingFactoryCall{commands: commands, taskId: taskId, cwd: cwd})
	return []*cmdwrap.CommandWrapper{}
}

// ============================================================
// Test: Windows command selection on Linux
// ============================================================

func TestCrossPlatform_WindowsCommandsSelectedOnLinux(t *testing.T) {
	// We're running on Linux, but we inject a Windows platform
	windowsPlatform := &util.Platform{OS: "windows"}
	assert.True(t, windowsPlatform.IsWindows())
	assert.False(t, windowsPlatform.IsLinux())

	taskCfg := &config.TaskConfig{
		Identifier: "build",
		Type:       config.TasKType_Build,
		Run:        config.NewLegacyRunCommands("make build"),
		RunWindows: config.NewLegacyRunCommands("msbuild.exe /p:Configuration=Release"),
		RunLinux:   config.NewLegacyRunCommands("make build-linux"),
	}

	factory := &trackingCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})

	rt := task.NewRunningTaskWithFactory(
		taskCfg, zap.NewNop(), wrapLogger, nil,
		func(string, bool) {}, windowsPlatform, factory,
	)

	// Task should be created (non-nil) and factory should have been called
	// with the Windows-specific commands
	assert.NotNil(t, rt)
	assert.Len(t, factory.calls, 1)
	assert.Equal(t, []string{"msbuild.exe /p:Configuration=Release"}, factory.calls[0].commands.LegacyStrings())
	assert.Equal(t, "build", factory.calls[0].taskId)
}

func TestCrossPlatform_LinuxCommandsSelectedOnLinux(t *testing.T) {
	linuxPlatform := &util.Platform{OS: "linux"}

	taskCfg := &config.TaskConfig{
		Identifier: "build",
		Type:       config.TasKType_Build,
		Run:        config.NewLegacyRunCommands("make build"),
		RunWindows: config.NewLegacyRunCommands("msbuild.exe /p:Configuration=Release"),
		RunLinux:   config.NewLegacyRunCommands("make build-linux"),
	}

	factory := &trackingCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})

	rt := task.NewRunningTaskWithFactory(
		taskCfg, zap.NewNop(), wrapLogger, nil,
		func(string, bool) {}, linuxPlatform, factory,
	)

	assert.NotNil(t, rt)
	assert.Len(t, factory.calls, 1)
	assert.Equal(t, []string{"make build-linux"}, factory.calls[0].commands.LegacyStrings())
}

func TestCrossPlatform_MacCommandsSelectedOnLinux(t *testing.T) {
	macPlatform := &util.Platform{OS: "darwin"}

	taskCfg := &config.TaskConfig{
		Identifier: "build",
		Type:       config.TasKType_Build,
		Run:        config.NewLegacyRunCommands("make build"),
		RunWindows: config.NewLegacyRunCommands("msbuild.exe"),
		RunLinux:   config.NewLegacyRunCommands("make build-linux"),
		RunMac:     config.NewLegacyRunCommands("xcodebuild"),
	}

	factory := &trackingCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})

	rt := task.NewRunningTaskWithFactory(
		taskCfg, zap.NewNop(), wrapLogger, nil,
		func(string, bool) {}, macPlatform, factory,
	)

	assert.NotNil(t, rt)
	assert.Len(t, factory.calls, 1)
	assert.Equal(t, []string{"xcodebuild"}, factory.calls[0].commands.LegacyStrings())
}

func TestCrossPlatform_FallbackToGenericWhenNoPlatformSpecific(t *testing.T) {
	windowsPlatform := &util.Platform{OS: "windows"}

	// Task only has generic Run, no RunWindows
	taskCfg := &config.TaskConfig{
		Identifier: "lint",
		Type:       config.TasKType_Build,
		Run:        config.NewLegacyRunCommands("golangci-lint run"),
	}

	factory := &trackingCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"lint"})

	rt := task.NewRunningTaskWithFactory(
		taskCfg, zap.NewNop(), wrapLogger, nil,
		func(string, bool) {}, windowsPlatform, factory,
	)

	assert.NotNil(t, rt)
	assert.Len(t, factory.calls, 1)
	assert.Equal(t, []string{"golangci-lint run"}, factory.calls[0].commands.LegacyStrings())
}

// ============================================================
// Test: Mock Windows ProcessManager on Linux
// ============================================================

func TestCrossPlatform_WindowsProcessManagerOnLinux(t *testing.T) {
	winPM := &mockWindowsProcessManager{}

	// Verify it implements ProcessManager
	var _ cmdwrap.ProcessManager = winPM

	// Simulate SetupProcessGroup (Windows no-op)
	cmd := exec.Command("echo", "test")
	winPM.SetupProcessGroup(cmd)
	assert.True(t, winPM.setupCalled)

	// On Windows, SysProcAttr would NOT be set (no Setpgid)
	// This verifies the Windows mock doesn't set it
	assert.Nil(t, cmd.SysProcAttr)
}

func TestCrossPlatform_WindowsProcessManagerUsedInCommandFactory(t *testing.T) {
	winPM := &mockWindowsProcessManager{}
	factory := cmdwrap.NewDefaultCommandFactory(winPM)

	genId := idgen.NewGenIDInterpolator()
	cmds := factory.ParseAllCommands(
		config.NewLegacyRunCommands("echo hello"),
		"test-task",
		zap.NewNop(),
		nil,
		"",
		genId,
	)

	assert.NotNil(t, cmds)
	assert.Len(t, cmds, 1)
	// The Windows PM's SetupProcessGroup should have been called
	assert.True(t, winPM.setupCalled)
}

// ============================================================
// Test: Full runner with simulated Windows environment on Linux
// ============================================================

func TestCrossPlatform_RunnerWithWindowsPlatformOnLinux(t *testing.T) {
	yaml := `
shared:
  log-level: error
tasks:
  build:
    type: build
    run-windows:
      - msbuild.exe /p:Configuration=Release
    run-linux:
      - make build-linux
    include:
      - "**/*.go"
`
	cfg, err := config.LoadFromBytes([]byte(yaml))
	assert.NoError(t, err)

	windowsPlatform := &util.Platform{OS: "windows"}
	factory := &trackingCommandFactory{}

	deps := RunnerDeps{
		Platform:   windowsPlatform,
		FS:         util.NewOSFileSystem(),
		CmdFactory: factory,
	}

	runner := NewRunnerWithDeps(cfg, zap.NewNop(), true, deps)
	assert.NotNil(t, runner)
	assert.Equal(t, "windows", runner.deps.Platform.OS)
	assert.True(t, runner.deps.Platform.IsWindows())

	// Schedule and start tasks — the factory should receive Windows commands
	runner.scheduleAllTasks()
	assert.Len(t, runner.ScheduledTasks, 1)
	assert.Contains(t, runner.ScheduledTasks, "build")

	// Start the scheduled task — this will call the factory
	runner.startScheduledTasks()

	// Factory should have been called with Windows commands
	assert.Len(t, factory.calls, 1)
	assert.Equal(t, []string{"msbuild.exe /p:Configuration=Release"}, factory.calls[0].commands.LegacyStrings())
	assert.Equal(t, "build", factory.calls[0].taskId)
}

func TestCrossPlatform_RunnerWithLinuxPlatformSelectsLinuxCommands(t *testing.T) {
	yaml := `
shared:
  log-level: error
tasks:
  build:
    type: build
    run-linux:
      - make build-linux
    run-windows:
      - msbuild.exe
    include:
      - "**/*.go"
`
	cfg, err := config.LoadFromBytes([]byte(yaml))
	assert.NoError(t, err)

	linuxPlatform := &util.Platform{OS: "linux"}
	factory := &trackingCommandFactory{}

	deps := RunnerDeps{
		Platform:   linuxPlatform,
		FS:         util.NewOSFileSystem(),
		CmdFactory: factory,
	}

	runner := NewRunnerWithDeps(cfg, zap.NewNop(), true, deps)
	runner.scheduleAllTasks()
	runner.startScheduledTasks()

	assert.Len(t, factory.calls, 1)
	assert.Equal(t, []string{"make build-linux"}, factory.calls[0].commands.LegacyStrings())
}

// ============================================================
// Test: Mock FileSystem for cross-platform testing
// ============================================================

func TestCrossPlatform_MockFileSystemInRunner(t *testing.T) {
	yaml := `
shared:
  log-level: error
tasks:
  build:
    type: build
    run:
      - echo build
    include:
      - "**/*.go"
`
	cfg, err := config.LoadFromBytes([]byte(yaml))
	assert.NoError(t, err)

	// Create a mock FS that tracks Remove calls
	mockFS := &crossPlatformMockFS{removedPaths: make([]string, 0)}

	deps := RunnerDeps{
		Platform:   &util.Platform{OS: "windows"},
		FS:         mockFS,
		CmdFactory: &mockCommandFactory{},
	}

	runner := NewRunnerWithDeps(cfg, zap.NewNop(), true, deps)
	assert.NotNil(t, runner)

	// The FS is injected — verify we can access it through the runner
	// This proves that cleanup tasks would use the mock FS
	assert.Equal(t, mockFS, runner.deps.FS)
}

type crossPlatformMockFS struct {
	removedPaths []string
}

func (m *crossPlatformMockFS) Lstat(name string) (os.FileInfo, error) {
	return nil, os.ErrNotExist
}

func (m *crossPlatformMockFS) ReadFile(name string) ([]byte, error) {
	return nil, os.ErrNotExist
}

func (m *crossPlatformMockFS) Walk(root string, fn filepath.WalkFunc) error {
	return nil
}

func (m *crossPlatformMockFS) Remove(name string) error {
	m.removedPaths = append(m.removedPaths, name)
	return nil
}

func (m *crossPlatformMockFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return nil, nil
}

// ============================================================
// Test: Multiple platform tasks in same config
// ============================================================

func TestCrossPlatform_MultiplePlatformTasksInSameRunner(t *testing.T) {
	yaml := `
shared:
  log-level: error
tasks:
  build:
    type: build
    run-windows:
      - msbuild.exe
    run-linux:
      - make build
    include:
      - "**/*.go"
  test:
    type: build
    run-windows:
      - go test -tags windows ./...
    run-linux:
      - go test ./...
    include:
      - "**/*_test.go"
`
	cfg, err := config.LoadFromBytes([]byte(yaml))
	assert.NoError(t, err)

	windowsPlatform := &util.Platform{OS: "windows"}
	factory := &trackingCommandFactory{}

	deps := RunnerDeps{
		Platform:   windowsPlatform,
		FS:         util.NewOSFileSystem(),
		CmdFactory: factory,
	}

	runner := NewRunnerWithDeps(cfg, zap.NewNop(), true, deps)
	runner.scheduleAllTasks()
	runner.startScheduledTasks()

	// Both tasks should have been started with Windows commands
	assert.Len(t, factory.calls, 2)

	// Collect what was called (order may vary due to map iteration)
	callsByTask := make(map[string][]string)
	for _, call := range factory.calls {
		callsByTask[call.taskId] = call.commands.LegacyStrings()
	}

	assert.Equal(t, []string{"msbuild.exe"}, callsByTask["build"])
	assert.Equal(t, []string{"go test -tags windows ./..."}, callsByTask["test"])
}

// ============================================================
// Test: Platform switching proves architecture works
// ============================================================

func TestCrossPlatform_SamConfigDifferentPlatformsProduceDifferentCommands(t *testing.T) {
	taskCfg := &config.TaskConfig{
		Identifier: "serve",
		Type:       config.TaskType_Continuous,
		Run:        config.NewLegacyRunCommands("./server"),
		RunWindows: config.NewLegacyRunCommands("server.exe"),
		RunLinux:   config.NewLegacyRunCommands("./server-linux"),
		RunMac:     config.NewLegacyRunCommands("./server-mac"),
	}

	platforms := map[string]struct {
		platform *util.Platform
		expected []string
	}{
		"windows": {&util.Platform{OS: "windows"}, []string{"server.exe"}},
		"linux":   {&util.Platform{OS: "linux"}, []string{"./server-linux"}},
		"darwin":  {&util.Platform{OS: "darwin"}, []string{"./server-mac"}},
		"freebsd": {&util.Platform{OS: "freebsd"}, []string{"./server"}}, // falls back to generic
	}

	for name, tc := range platforms {
		t.Run(name, func(t *testing.T) {
			factory := &trackingCommandFactory{}
			wrapLogger := cmdwrap.NewWrapLogger([]string{"serve"})

			rt := task.NewRunningTaskWithFactory(
				taskCfg, zap.NewNop(), wrapLogger, nil,
				func(string, bool) {}, tc.platform, factory,
			)

			assert.NotNil(t, rt, "RunningTask should be created for platform %s", name)
			assert.Len(t, factory.calls, 1)
			assert.Equal(t, tc.expected, factory.calls[0].commands.LegacyStrings(),
				"Platform %s should select correct commands", name)
		})
	}
}
