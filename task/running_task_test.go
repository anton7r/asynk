package task

import (
	"os/exec"
	"runtime"
	"sync"
	"testing"

	"github.com/anton7r/asynk/cmdwrap"
	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/util"
	"github.com/anton7r/asynk/util/interpolation/idgen"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// --- helpers ---

// Cross-platform command helpers.
// Unix commands like "false" don't exist on Windows.

func failCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 1"
	}
	return "false"
}

func echoCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c echo hello"
	}
	return "echo hello"
}

func nopLogger() *zap.Logger {
	return zap.NewNop()
}

func nopCallback(taskId string, errored bool) {}

func newTaskConfig(id string) *config.TaskConfig {
	return &config.TaskConfig{
		Identifier: id,
	}
}

// --- mock CommandFactory ---

type mockCommandFactory struct {
	called   bool
	commands config.RunCommands
	cwd      string
}

func (m *mockCommandFactory) ParseAllCommands(
	commands config.RunCommands,
	taskId string,
	log *zap.Logger,
	env map[string]string,
	cwd string,
	genIdInterpolator *idgen.GenIDInterpolator,
) ([]*cmdwrap.CommandWrapper, error) {
	m.called = true
	m.commands = commands
	m.cwd = cwd
	// Return an empty slice (non-nil) so the task is created
	return []*cmdwrap.CommandWrapper{}, nil
}

// --- mock ProcessManager ---

type mockProcessManager struct{}

func (m *mockProcessManager) SetupProcessGroup(cmd *exec.Cmd) {}
func (m *mockProcessManager) CancelProcess(cmd *exec.Cmd) error {
	return nil
}

// ============================================================
// Tests for getCommands
// ============================================================

func TestGetCommands_WindowsPlatform(t *testing.T) {
	log := nopLogger()
	platform := &util.Platform{OS: "windows"}

	tc := newTaskConfig("test-task")
	tc.Run = config.NewLegacyRunCommands("echo fallback")
	tc.RunWindows = config.NewLegacyRunCommands("echo windows")
	tc.RunLinux = config.NewLegacyRunCommands("echo linux")
	tc.RunMac = config.NewLegacyRunCommands("echo mac")

	result := getCommands(tc, log, platform)
	assert.Equal(t, []string{"echo windows"}, result.LegacyStrings())
}

func TestGetCommands_LinuxPlatform(t *testing.T) {
	log := nopLogger()
	platform := &util.Platform{OS: "linux"}

	tc := newTaskConfig("test-task")
	tc.Run = config.NewLegacyRunCommands("echo fallback")
	tc.RunWindows = config.NewLegacyRunCommands("echo windows")
	tc.RunLinux = config.NewLegacyRunCommands("echo linux")
	tc.RunMac = config.NewLegacyRunCommands("echo mac")

	result := getCommands(tc, log, platform)
	assert.Equal(t, []string{"echo linux"}, result.LegacyStrings())
}

func TestGetCommands_MacPlatform(t *testing.T) {
	log := nopLogger()
	platform := &util.Platform{OS: "darwin"}

	tc := newTaskConfig("test-task")
	tc.Run = config.NewLegacyRunCommands("echo fallback")
	tc.RunWindows = config.NewLegacyRunCommands("echo windows")
	tc.RunLinux = config.NewLegacyRunCommands("echo linux")
	tc.RunMac = config.NewLegacyRunCommands("echo mac")

	result := getCommands(tc, log, platform)
	assert.Equal(t, []string{"echo mac"}, result.LegacyStrings())
}

func TestGetCommands_FallbackToRun(t *testing.T) {
	log := nopLogger()
	// Use a platform that has no specific override
	platform := &util.Platform{OS: "linux"}

	tc := newTaskConfig("test-task")
	tc.Run = config.NewLegacyRunCommands("echo fallback")
	// No platform-specific commands set

	result := getCommands(tc, log, platform)
	assert.Equal(t, []string{"echo fallback"}, result.LegacyStrings())
}

func TestGetCommands_FallbackWhenPlatformSpecificEmpty(t *testing.T) {
	log := nopLogger()
	platform := &util.Platform{OS: "windows"}

	tc := newTaskConfig("test-task")
	tc.Run = config.NewLegacyRunCommands("echo fallback")
	// RunWindows is nil/empty, so should fall back to Run

	result := getCommands(tc, log, platform)
	assert.Equal(t, []string{"echo fallback"}, result.LegacyStrings())
}

func TestGetCommands_ReturnsNilWhenNoCommands(t *testing.T) {
	log := nopLogger()
	platform := &util.Platform{OS: "linux"}

	tc := newTaskConfig("test-task")
	// No commands set at all

	result := getCommands(tc, log, platform)
	assert.Nil(t, result)
}

// ============================================================
// Tests for NewRunningTask
// ============================================================

func TestNewRunningTask_ValidConfig(t *testing.T) {
	log := nopLogger()
	platform := util.NewPlatform()
	wrapLogger := cmdwrap.NewWrapLogger([]string{"test-task"})

	tc := newTaskConfig("test-task")
	tc.Run = config.NewLegacyRunCommands(echoCmd())

	rt := NewRunningTask(tc, log, wrapLogger, nil, nopCallback, platform)
	assert.NotNil(t, rt, "NewRunningTask should return a non-nil task for valid config")
}

func TestNewRunningTask_EmptyCommands(t *testing.T) {
	log := nopLogger()
	platform := util.NewPlatform()
	wrapLogger := cmdwrap.NewWrapLogger([]string{"empty-task"})

	tc := newTaskConfig("empty-task")
	// No commands

	rt := NewRunningTask(tc, log, wrapLogger, nil, nopCallback, platform)
	assert.Nil(t, rt, "NewRunningTask should return nil when no commands are configured")
}

// ============================================================
// Tests for NewRunningTaskWithFactory
// ============================================================

func TestNewRunningTaskWithFactory_MockFactory(t *testing.T) {
	log := nopLogger()
	platform := &util.Platform{OS: "linux"}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"factory-task"})
	factory := &mockCommandFactory{}

	tc := newTaskConfig("factory-task")
	tc.Run = config.NewLegacyRunCommands("echo hello", "echo world")

	rt := NewRunningTaskWithFactory(tc, log, wrapLogger, nil, nopCallback, platform, factory)

	assert.True(t, factory.called, "CommandFactory.ParseAllCommands should have been called")
	assert.Equal(t, []string{"echo hello", "echo world"}, factory.commands.LegacyStrings())
	assert.NotNil(t, rt)
}

func TestNewRunningTaskWithFactory_NoCommands(t *testing.T) {
	log := nopLogger()
	platform := &util.Platform{OS: "linux"}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"no-cmd-task"})
	factory := &mockCommandFactory{}

	tc := newTaskConfig("no-cmd-task")
	// No commands set

	rt := NewRunningTaskWithFactory(tc, log, wrapLogger, nil, nopCallback, platform, factory)

	assert.False(t, factory.called, "CommandFactory should not be called when there are no commands")
	assert.Nil(t, rt)
}

// ============================================================
// Tests for RemoveRunningTask
// ============================================================

func TestRemoveRunningTask_ExistingTask(t *testing.T) {
	// Build a small slice of RunningTasks with different identifiers
	tasks := []*RunningTask{
		{taskConfig: &config.TaskConfig{Identifier: "task-a"}},
		{taskConfig: &config.TaskConfig{Identifier: "task-b"}},
		{taskConfig: &config.TaskConfig{Identifier: "task-c"}},
	}

	result := RemoveRunningTask(tasks, "task-b")

	assert.Len(t, result, 2)
	assert.Equal(t, "task-a", result[0].taskConfig.Identifier)
	assert.Equal(t, "task-c", result[1].taskConfig.Identifier)
}

func TestRemoveRunningTask_NonExistentTask(t *testing.T) {
	tasks := []*RunningTask{
		{taskConfig: &config.TaskConfig{Identifier: "task-a"}},
		{taskConfig: &config.TaskConfig{Identifier: "task-b"}},
	}

	result := RemoveRunningTask(tasks, "task-z")

	assert.Len(t, result, 2, "Slice should be unchanged when removing non-existent task")
}

func TestRemoveRunningTask_EmptySlice(t *testing.T) {
	var tasks []*RunningTask
	result := RemoveRunningTask(tasks, "anything")
	assert.Empty(t, result)
}

func TestRemoveRunningTask_SingleElement(t *testing.T) {
	tasks := []*RunningTask{
		{taskConfig: &config.TaskConfig{Identifier: "only"}},
	}

	result := RemoveRunningTask(tasks, "only")
	assert.Empty(t, result)
}

// ============================================================
// Tests for RunningTask.TaskId
// ============================================================

func TestRunningTask_TaskId(t *testing.T) {
	rt := &RunningTask{
		taskConfig: &config.TaskConfig{Identifier: "my-task"},
	}
	assert.Equal(t, "my-task", rt.TaskId())
}

// ============================================================
// Tests for Start + Wait
// ============================================================

func TestRunningTask_StartAndWait(t *testing.T) {
	log := nopLogger()
	platform := util.NewPlatform()
	wrapLogger := cmdwrap.NewWrapLogger([]string{"echo-task"})

	tc := newTaskConfig("echo-task")
	tc.Run = config.NewLegacyRunCommands(echoCmd())

	var mu sync.Mutex
	var finishedTaskId string
	var finishedWithError bool

	callback := func(taskId string, errored bool) {
		mu.Lock()
		defer mu.Unlock()
		finishedTaskId = taskId
		finishedWithError = errored
	}

	rt := NewRunningTask(tc, log, wrapLogger, nil, callback, platform)
	assert.NotNil(t, rt)

	go rt.Start()
	rt.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "echo-task", finishedTaskId)
	assert.False(t, finishedWithError, "echo hello should succeed without error")
}

func TestRunningTask_StartFailingCommand(t *testing.T) {
	log := nopLogger()
	platform := util.NewPlatform()
	wrapLogger := cmdwrap.NewWrapLogger([]string{"fail-task"})

	tc := newTaskConfig("fail-task")
	tc.Run = config.NewLegacyRunCommands(failCmd()) // exits with code 1

	var mu sync.Mutex
	var finishedWithError bool

	callback := func(taskId string, errored bool) {
		mu.Lock()
		defer mu.Unlock()
		finishedWithError = errored
	}

	rt := NewRunningTask(tc, log, wrapLogger, nil, callback, platform)
	assert.NotNil(t, rt)

	go rt.Start()
	rt.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, finishedWithError, "command 'false' should report an error")
}

func TestRunningTask_StartParseErrorFailsTask(t *testing.T) {
	log := nopLogger()
	platform := util.NewPlatform()
	wrapLogger := cmdwrap.NewWrapLogger([]string{"parse-error-task"})

	tc := newTaskConfig("parse-error-task")
	tc.Run = config.NewLegacyRunCommands(`echo "oops`)

	var mu sync.Mutex
	var finishedTaskId string
	var finishedWithError bool

	callback := func(taskId string, errored bool) {
		mu.Lock()
		defer mu.Unlock()
		finishedTaskId = taskId
		finishedWithError = errored
	}

	rt := NewRunningTask(tc, log, wrapLogger, nil, callback, platform)
	assert.NotNil(t, rt)

	go rt.Start()
	rt.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "parse-error-task", finishedTaskId)
	assert.True(t, finishedWithError, "invalid command syntax should report an error")
}

// ============================================================
// Tests for StopGracefully
// ============================================================

func TestRunningTask_StopGracefully_NilReceiver(t *testing.T) {
	// Should not panic
	var rt *RunningTask
	assert.NotPanics(t, func() {
		rt.StopGracefully()
	})
}

// ============================================================
// Tests for Wait edge cases
// ============================================================

func TestRunningTask_Wait_NilReceiver(t *testing.T) {
	var rt *RunningTask
	assert.NotPanics(t, func() {
		rt.Wait()
	})
}

// ============================================================
// Tests for nil-guard behavior (review comment fixes)
// ============================================================

func TestNewRunningTaskWithFactory_NilCmdFactory(t *testing.T) {
	// Passing nil cmdFactory should not panic; it should default to a real factory.
	log := nopLogger()
	platform := util.NewPlatform()
	wrapLogger := cmdwrap.NewWrapLogger([]string{"nil-factory-task"})

	tc := newTaskConfig("nil-factory-task")
	tc.Run = config.NewLegacyRunCommands(echoCmd())

	assert.NotPanics(t, func() {
		rt := NewRunningTaskWithFactory(tc, log, wrapLogger, nil, nopCallback, platform, nil)
		assert.NotNil(t, rt, "should create a valid task even with nil cmdFactory")
	})
}

func TestNewRunningTaskWithFactory_NilCmdFactory_NoCommands(t *testing.T) {
	// Nil cmdFactory + no commands = should return nil without panic.
	log := nopLogger()
	platform := util.NewPlatform()
	wrapLogger := cmdwrap.NewWrapLogger([]string{"nil-factory-no-cmd"})

	tc := newTaskConfig("nil-factory-no-cmd")

	assert.NotPanics(t, func() {
		rt := NewRunningTaskWithFactory(tc, log, wrapLogger, nil, nopCallback, platform, nil)
		assert.Nil(t, rt)
	})
}

func TestGetCommands_NilPlatform(t *testing.T) {
	// Passing nil platform should not panic; it should default to runtime platform.
	log := nopLogger()

	tc := newTaskConfig("nil-platform-task")
	tc.Run = config.NewLegacyRunCommands("echo fallback")

	assert.NotPanics(t, func() {
		result := getCommands(tc, log, nil)
		// Should still return the generic "run" command since nil defaults to NewPlatform()
		assert.NotNil(t, result)
		assert.Equal(t, []string{"echo fallback"}, result.LegacyStrings())
	})
}

func TestGetCommands_NilPlatform_NoCommands(t *testing.T) {
	log := nopLogger()
	tc := newTaskConfig("nil-platform-no-cmd")

	assert.NotPanics(t, func() {
		result := getCommands(tc, log, nil)
		assert.Nil(t, result)
	})
}
