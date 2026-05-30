package cmdwrap

import (
	"context"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/util/interpolation/idgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Cross-platform command helpers.
// Unix commands like "true", "false", "sleep" don't exist on Windows,
// so we pick platform-appropriate equivalents.

func successCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 0"
	}
	return "true"
}

func failCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 1"
	}
	return "false"
}

func longRunningCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c ping -n 11 127.0.0.1"
	}
	return "sleep 10"
}

// --- ProcessManager interface ---

func TestDefaultProcessManagerReturnsNonNil(t *testing.T) {
	pm := DefaultProcessManager()
	assert.NotNil(t, pm)
}

type recordingProcessManager struct {
	setupCalled  atomic.Bool
	cancelCalled atomic.Bool
}

func (m *recordingProcessManager) SetupProcessGroup(cmd *exec.Cmd) {
	m.setupCalled.Store(true)
}

func (m *recordingProcessManager) CancelProcess(cmd *exec.Cmd) error {
	m.cancelCalled.Store(true)
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return nil
}

// --- CommandFactory interface ---

func TestDefaultCommandFactoryCreatesCommands(t *testing.T) {
	pm := DefaultProcessManager()
	factory := NewDefaultCommandFactory(pm)
	assert.NotNil(t, factory)

	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()
	cmds, err := factory.ParseAllCommands(config.NewLegacyRunCommands(echoCmd()), "test-task", log, nil, "", interp)
	require.NoError(t, err)
	assert.Len(t, cmds, 1)
}

func TestDefaultCommandFactoryImplementsInterface(t *testing.T) {
	pm := DefaultProcessManager()
	var _ CommandFactory = NewDefaultCommandFactory(pm)
}

// --- parseCommand ---

func TestParseCommandReturnsWrapperForEmptyString(t *testing.T) {
	log := zap.NewNop()
	pm := DefaultProcessManager()

	result, err := parseCommand(config.CommandConfig{Command: "", Legacy: true}, "task", "", nil, idgen.NewGenIDInterpolator(), log, pm)
	assert.Nil(t, result)
	assert.Error(t, err)
}

func TestParseCommandCreatesValidCommandWrapper(t *testing.T) {
	log := zap.NewNop()
	pm := DefaultProcessManager()

	result, err := parseCommand(config.CommandConfig{Command: echoCmd(), Legacy: true}, "my-task", "", nil, idgen.NewGenIDInterpolator(), log, pm)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "my-task", result.taskId)
	assert.NotNil(t, result.cmd)
}

func TestParseCommandSingleWord(t *testing.T) {
	log := zap.NewNop()
	pm := DefaultProcessManager()

	result, err := parseCommand(config.CommandConfig{Command: successCmd(), Legacy: true}, "task-id", "", nil, idgen.NewGenIDInterpolator(), log, pm)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "task-id", result.taskId)
}

// --- ParseAllCommands ---

func TestParseAllCommandsSimple(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()

	cmds, err := ParseAllCommands(config.NewLegacyRunCommands(echoCmd()), "task1", log, nil, "", interp)
	require.NoError(t, err)
	assert.Len(t, cmds, 1)
}

func TestParseAllCommandsMultiple(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()

	cmds, err := ParseAllCommands(config.NewLegacyRunCommands(echoCmd(), echoCmd(), echoCmd()), "task1", log, nil, "", interp)
	require.NoError(t, err)
	assert.Len(t, cmds, 3)
}

func TestParseAllCommandsInterpolatesEnvVariables(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()

	env := map[string]string{
		"GREETING": "hello",
	}

	baseCmd := "echo"
	if runtime.GOOS == "windows" {
		baseCmd = "cmd /c echo"
	}

	cmds, err := ParseAllCommands(config.NewLegacyRunCommands(baseCmd+" ${GREETING}"), "task1", log, env, "", interp)
	require.NoError(t, err)
	assert.Len(t, cmds, 1)
	// The interpolated command should have "hello" as the argument
	assert.Contains(t, cmds[0].cmd.Args, "hello")
}

func TestParseAllCommandsWithProcessManager(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()
	pm := DefaultProcessManager()

	cmds, err := ParseAllCommandsWithProcessManager(
		config.NewLegacyRunCommands(echoCmd()),
		"task-pm",
		log,
		nil,
		"",
		interp,
		pm,
	)
	require.NoError(t, err)
	assert.Len(t, cmds, 1)
	assert.Equal(t, "task-pm", cmds[0].taskId)
}

func TestParseAllCommandsEmptySlice(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()

	cmds, err := ParseAllCommands(config.RunCommands{}, "task1", log, nil, "", interp)
	require.NoError(t, err)
	assert.Empty(t, cmds)
}

func TestParseAllCommandsReturnsErrorForInvalidLegacyCommand(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()

	cmds, err := ParseAllCommands(config.NewLegacyRunCommands(`echo "oops`), "task1", log, nil, "", interp)
	assert.Nil(t, cmds)
	assert.Error(t, err)
}

// --- WrapLogger ---

func TestNewWrapLoggerCreatesLoggerWithCorrectTaskIds(t *testing.T) {
	taskIds := []string{"build", "test", "lint"}
	logger := NewWrapLogger(taskIds)

	assert.NotNil(t, logger)
	assert.Equal(t, taskIds, logger.taskIds)
	assert.Len(t, logger.taskColors, 3)
	// maxTaskIdLength should be 5 ("build" and "test" -> max is "build" = 5)
	assert.Equal(t, 5, logger.maxTaskIdLength)
}

func TestNewWrapLoggerMaxTaskIdLength(t *testing.T) {
	taskIds := []string{"a", "abc", "abcdefgh"}
	logger := NewWrapLogger(taskIds)

	assert.Equal(t, 8, logger.maxTaskIdLength)
}

func TestNewWrapLoggerEmptySlice(t *testing.T) {
	logger := NewWrapLogger([]string{})
	assert.NotNil(t, logger)
	assert.Equal(t, 0, logger.maxTaskIdLength)
	assert.Empty(t, logger.taskColors)
}

func TestGenerateRgbColorDeterministic(t *testing.T) {
	r1, g1, b1 := generateRgbColor("task1")
	r2, g2, b2 := generateRgbColor("task1")

	assert.Equal(t, r1, r2)
	assert.Equal(t, g1, g2)
	assert.Equal(t, b1, b2)
}

func TestGenerateRgbColorDifferentInputs(t *testing.T) {
	r1, g1, b1 := generateRgbColor("task1")
	r2, g2, b2 := generateRgbColor("task2")

	// Different inputs should (very likely) produce different colors
	differentColor := r1 != r2 || g1 != g2 || b1 != b2
	assert.True(t, differentColor, "different task IDs should produce different colors")
}

func TestGenerateRgbColorValuesInRange(t *testing.T) {
	r, g, b := generateRgbColor("some-task")
	assert.GreaterOrEqual(t, r, 0)
	assert.LessOrEqual(t, r, 255)
	assert.GreaterOrEqual(t, g, 0)
	assert.LessOrEqual(t, g, 255)
	assert.GreaterOrEqual(t, b, 0)
	assert.LessOrEqual(t, b, 255)
}

// --- CommandWrapper.Run ---

func echoCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c echo hello"
	}
	return "echo hello"
}

func TestCommandWrapperRunSimpleCommand(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()
	wrapLog := NewWrapLogger([]string{"run-test"})

	cmds, err := ParseAllCommands(config.NewLegacyRunCommands(echoCmd()), "run-test", log, nil, "", interp)
	require.NoError(t, err)
	assert.Len(t, cmds, 1)

	ctx := context.Background()
	err = cmds[0].Run(ctx, nil, wrapLog)
	assert.NoError(t, err)
}

func TestCommandWrapperRunTrueCommand(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()
	wrapLog := NewWrapLogger([]string{"true-test"})

	cmds, err := ParseAllCommands(config.NewLegacyRunCommands(successCmd()), "true-test", log, nil, "", interp)
	require.NoError(t, err)
	assert.Len(t, cmds, 1)

	ctx := context.Background()
	err = cmds[0].Run(ctx, nil, wrapLog)
	assert.NoError(t, err)
}

func TestCommandWrapperRunFailingCommand(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()
	wrapLog := NewWrapLogger([]string{"fail-test"})

	cmds, err := ParseAllCommands(config.NewLegacyRunCommands(failCmd()), "fail-test", log, nil, "", interp)
	require.NoError(t, err)
	assert.Len(t, cmds, 1)

	ctx := context.Background()
	err = cmds[0].Run(ctx, nil, wrapLog)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fail-test")
}

// --- CommandWrapper.Cancel ---

func TestCommandWrapperCancel(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()
	wrapLog := NewWrapLogger([]string{"cancel-test"})

	cmds, err := ParseAllCommands(config.NewLegacyRunCommands(longRunningCmd()), "cancel-test", log, nil, "", interp)
	require.NoError(t, err)
	assert.Len(t, cmds, 1)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- cmds[0].Run(ctx, nil, wrapLog)
	}()

	// Give the process a moment to start
	time.Sleep(200 * time.Millisecond)

	// Cancel the context, which should trigger cancellation
	cancel()

	select {
	case err := <-done:
		// The command was cancelled; we just verify it returned (possibly with an error)
		// CancelProcess returns nil on success, and Run returns the cancel result
		_ = err
	case <-time.After(10 * time.Second):
		t.Fatal("command did not terminate after cancel within timeout")
	}
}

func TestCommandWrapperRunUsesProcessManagerForContextCancellation(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()
	wrapLog := NewWrapLogger([]string{"cancel-manager-test"})
	pm := &recordingProcessManager{}

	cmds, err := ParseAllCommandsWithProcessManager(
		config.NewLegacyRunCommands(longRunningCmd()),
		"cancel-manager-test",
		log,
		nil,
		"",
		interp,
		pm,
	)
	require.NoError(t, err)
	require.Len(t, cmds, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- cmds[0].Run(ctx, nil, wrapLog)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("command did not terminate after cancel within timeout")
	}

	assert.True(t, pm.cancelCalled.Load(), "context cancellation should use ProcessManager.CancelProcess")
}

func TestCommandWrapperCancelViaDirectCall(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()
	wrapLog := NewWrapLogger([]string{"direct-cancel"})

	cmds, err := ParseAllCommands(config.NewLegacyRunCommands(longRunningCmd()), "direct-cancel", log, nil, "", interp)
	require.NoError(t, err)
	assert.Len(t, cmds, 1)

	cmd := cmds[0]

	// Set up pipes and start the command manually to test Cancel directly
	err = cmd.setupPipes()
	assert.NoError(t, err)

	cmd.readOutput(wrapLog)

	err = cmd.start()
	assert.NoError(t, err)

	// Give it a moment to be running
	time.Sleep(200 * time.Millisecond)

	// Cancel directly
	err = cmd.Cancel()
	assert.NoError(t, err)

	// Ensure the process is reaped after cancellation to avoid leaking it.
	done := make(chan error, 1)
	go func() {
		done <- cmd.wait()
	}()

	select {
	case <-done:
		// Process was reaped successfully (error from wait is expected after cancel)
	case <-time.After(2 * time.Second):
		t.Fatalf("command did not exit within timeout after Cancel()")
	}
}

// ============================================================
// Tests for nil-guard behavior (review comment fixes)
// ============================================================

func TestParseAllCommandsWithProcessManager_NilProcMgr(t *testing.T) {
	// Passing nil procMgr should not panic; it should default to DefaultProcessManager().
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()

	assert.NotPanics(t, func() {
		cmds, err := ParseAllCommandsWithProcessManager(
			config.NewLegacyRunCommands(echoCmd()),
			"nil-pm-task",
			log,
			nil,
			"",
			interp,
			nil, // nil ProcessManager
		)
		require.NoError(t, err)
		assert.Len(t, cmds, 1)
		assert.Equal(t, "nil-pm-task", cmds[0].taskId)
	})
}

func TestParseAllCommandsWithProcessManager_NilProcMgr_EmptyCommands(t *testing.T) {
	log := zap.NewNop()
	interp := idgen.NewGenIDInterpolator()

	assert.NotPanics(t, func() {
		cmds, err := ParseAllCommandsWithProcessManager(
			config.RunCommands{},
			"nil-pm-empty",
			log,
			nil,
			"",
			interp,
			nil,
		)
		require.NoError(t, err)
		assert.Empty(t, cmds)
	})
}

func TestNewDefaultCommandFactory_NilProcMgr(t *testing.T) {
	// Passing nil procMgr should not panic; it should default to DefaultProcessManager().
	assert.NotPanics(t, func() {
		factory := NewDefaultCommandFactory(nil)
		assert.NotNil(t, factory)

		// Verify the factory actually works by parsing a command
		log := zap.NewNop()
		interp := idgen.NewGenIDInterpolator()
		cmds, err := factory.ParseAllCommands(config.NewLegacyRunCommands(echoCmd()), "nil-factory", log, nil, "", interp)
		require.NoError(t, err)
		assert.Len(t, cmds, 1)
	})
}
