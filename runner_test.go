package main

import (
	"testing"
	"time"

	"github.com/anton7r/asynk/cmdwrap"
	"github.com/anton7r/asynk/config"
	configutil "github.com/anton7r/asynk/config/util"
	"github.com/anton7r/asynk/task"
	"github.com/anton7r/asynk/util"
	"github.com/anton7r/asynk/util/interpolation/idgen"
	"github.com/anton7r/asynk/watcher"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// ============================================================
// Mocks
// ============================================================

// mockCommandFactory returns empty command slices so no real processes spawn.
type mockCommandFactory struct {
	called   bool
	commands []string
}

func (m *mockCommandFactory) ParseAllCommands(
	commands []string,
	taskId string,
	log *zap.Logger,
	env map[string]string,
	genIdInterpolator *idgen.GenIDInterpolator,
) []*cmdwrap.CommandWrapper {
	m.called = true
	m.commands = commands
	return []*cmdwrap.CommandWrapper{}
}

// ============================================================
// Test helpers
// ============================================================

func testLogger() *zap.Logger {
	return zap.NewNop()
}

func testPlatform() *util.Platform {
	return &util.Platform{OS: "linux"}
}

// testConfig creates a minimal valid config using LoadFromBytes.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	yml := []byte(`
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
  server:
    type: continuous
    run: "echo server"
    include:
      - "**/*.go"
`)
	cfg, err := config.LoadFromBytes(yml)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	return cfg
}

// testConfigWithDeps creates a config where "server" depends on "build".
func testConfigWithDeps(t *testing.T) *config.Config {
	t.Helper()
	yml := []byte(`
tasks:
  build:
    type: build
    run: "echo build"
    include:
      - "**/*.go"
  server:
    type: continuous
    run: "echo server"
    include:
      - "**/*.go"
    dependencies:
      - build
`)
	cfg, err := config.LoadFromBytes(yml)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	return cfg
}

// testRunnerWithDeps creates a Runner using runOnce=true and a mock command factory.
func testRunnerWithDeps(t *testing.T, cfg *config.Config) (*Runner, *mockCommandFactory) {
	t.Helper()
	factory := &mockCommandFactory{}
	deps := RunnerDeps{
		Platform:   testPlatform(),
		FS:         util.NewOSFileSystem(),
		CmdFactory: factory,
	}
	runner := NewRunnerWithDeps(cfg, testLogger(), true, deps)
	assert.NotNil(t, runner)
	return runner, factory
}

// ============================================================
// Tests for NewRunner
// ============================================================

func TestNewRunner_CreatesValidRunner(t *testing.T) {
	cfg := testConfig(t)
	runner := NewRunner(cfg, testLogger(), true, testPlatform())

	assert.NotNil(t, runner)
	assert.Equal(t, cfg, runner.Config)
	assert.NotNil(t, runner.ScheduledTasks)
	assert.NotNil(t, runner.RunningTasks)
	assert.True(t, runner.runOnce)
	// Watcher should be nil in runOnce mode
	assert.Nil(t, runner.watch)
}

func TestNewRunner_WatcherNilInRunOnceMode(t *testing.T) {
	cfg := testConfig(t)
	runner := NewRunner(cfg, testLogger(), true, testPlatform())
	assert.Nil(t, runner.watch, "watcher should be nil when runOnce=true")
}

// ============================================================
// Tests for NewRunnerWithDeps
// ============================================================

func TestNewRunnerWithDeps_CustomDeps(t *testing.T) {
	cfg := testConfig(t)
	factory := &mockCommandFactory{}
	platform := &util.Platform{OS: "darwin"}
	deps := RunnerDeps{
		Platform:   platform,
		FS:         util.NewOSFileSystem(),
		CmdFactory: factory,
	}

	runner := NewRunnerWithDeps(cfg, testLogger(), true, deps)

	assert.NotNil(t, runner)
	assert.Equal(t, platform, runner.deps.Platform)
	assert.Equal(t, factory, runner.deps.CmdFactory)
	assert.True(t, runner.runOnce)
	assert.NotNil(t, runner.ScheduledTasks)
	assert.Empty(t, runner.ScheduledTasks)
	assert.NotNil(t, runner.RunningTasks)
	assert.Empty(t, runner.RunningTasks)
}

func TestNewRunnerWithDeps_InitializesEmptyEnvMap(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)
	// env may be populated or empty depending on .env files, but should not be nil
	assert.NotNil(t, runner.env)
}

func TestNewRunnerWithDeps_CompletionChanCreated(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)
	assert.NotNil(t, runner.completionChan)
}

// ============================================================
// Tests for DefaultRunnerDeps
// ============================================================

func TestDefaultRunnerDeps_AllFieldsNonNil(t *testing.T) {
	deps := DefaultRunnerDeps()
	assert.NotNil(t, deps.Platform)
	assert.NotNil(t, deps.FS)
	assert.NotNil(t, deps.CmdFactory)
}

func TestDefaultRunnerDeps_PlatformMatchesRuntime(t *testing.T) {
	deps := DefaultRunnerDeps()
	// It should match the current runtime platform
	expected := util.NewPlatform()
	assert.Equal(t, expected.OS, deps.Platform.OS)
}

// ============================================================
// Tests for scheduleAllTasks
// ============================================================

func TestScheduleAllTasks_SchedulesAllConfigTasks(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	assert.Empty(t, runner.ScheduledTasks, "should start with no scheduled tasks")

	runner.scheduleAllTasks()

	assert.Len(t, runner.ScheduledTasks, len(cfg.Tasks))
	for taskId := range cfg.Tasks {
		_, exists := runner.ScheduledTasks[taskId]
		assert.True(t, exists, "task %s should be scheduled", taskId)
	}
}

func TestScheduleAllTasks_TaskConfigurationsMatch(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	runner.scheduleAllTasks()

	for taskId, taskCfg := range cfg.Tasks {
		scheduled := runner.ScheduledTasks[taskId]
		assert.NotNil(t, scheduled)
		assert.Equal(t, taskCfg, scheduled.TaskConfiguration)
	}
}

func TestScheduleAllTasks_EmptyConfig(t *testing.T) {
	yml := []byte(`
tasks: {}
`)
	cfg, err := config.LoadFromBytes(yml)
	assert.NoError(t, err)

	runner, _ := testRunnerWithDeps(t, cfg)
	runner.scheduleAllTasks()

	assert.Empty(t, runner.ScheduledTasks)
}

// ============================================================
// Tests for canStartTask
// ============================================================

func TestCanStartTask_ReadyTaskNoDependencies(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]
	scheduled := &ScheduledTask{TaskConfiguration: buildTask}

	assert.True(t, runner.canStartTask(scheduled),
		"task with no dependencies and not running should be startable")
}

func TestCanStartTask_ReturnsFalseForRunningNonContinuousTask(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]
	// Simulate the task already running
	runner.RunningTasks = append(runner.RunningTasks, &task.RunningTask{})
	// We need a proper RunningTask with the right TaskId. Let's construct one via
	// the exposed fields. Since RunningTask fields are unexported, we need to use
	// NewRunningTaskWithFactory with our mock factory.
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})
	rt := task.NewRunningTaskWithFactory(
		buildTask, testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	runner.RunningTasks = []*task.RunningTask{rt}

	scheduled := &ScheduledTask{TaskConfiguration: buildTask}
	assert.False(t, runner.canStartTask(scheduled),
		"non-continuous task that is already running should not be startable")
}

func TestCanStartTask_ReturnsTrueForContinuousTaskEvenIfRunning(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	serverTask := cfg.Tasks["server"]
	assert.Equal(t, config.TaskType_Continuous, serverTask.Type)

	// Simulate the continuous task already running
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"server"})
	rt := task.NewRunningTaskWithFactory(
		serverTask, testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	runner.RunningTasks = []*task.RunningTask{rt}

	scheduled := &ScheduledTask{TaskConfiguration: serverTask}
	assert.True(t, runner.canStartTask(scheduled),
		"continuous task should be startable even if already running")
}

func TestCanStartTask_ReturnsFalseWhenDependencyRunning(t *testing.T) {
	cfg := testConfigWithDeps(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]
	serverTask := cfg.Tasks["server"]

	// Simulate build task running
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build", "server"})
	rt := task.NewRunningTaskWithFactory(
		buildTask, testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	runner.RunningTasks = []*task.RunningTask{rt}

	scheduled := &ScheduledTask{TaskConfiguration: serverTask}
	assert.False(t, runner.canStartTask(scheduled),
		"task should not start when its dependency is still running")
}

func TestCanStartTask_ReturnsFalseWhenDependencyQueued(t *testing.T) {
	cfg := testConfigWithDeps(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]
	serverTask := cfg.Tasks["server"]

	// Simulate build task queued (in ScheduledTasks)
	runner.ScheduledTasks["build"] = &ScheduledTask{TaskConfiguration: buildTask}

	scheduled := &ScheduledTask{TaskConfiguration: serverTask}
	assert.False(t, runner.canStartTask(scheduled),
		"task should not start when its dependency is still queued")
}

func TestCanStartTask_ReturnsFalseForNilScheduledTask(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	assert.False(t, runner.canStartTask(nil),
		"nil scheduled task should return false")
}

func TestCanStartTask_TrueWhenDependencyCompleted(t *testing.T) {
	cfg := testConfigWithDeps(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	serverTask := cfg.Tasks["server"]

	// build is neither running nor scheduled — it's "completed"
	scheduled := &ScheduledTask{TaskConfiguration: serverTask}
	assert.True(t, runner.canStartTask(scheduled),
		"task should be startable when dependency is completed (not running, not queued)")
}

// ============================================================
// Tests for findTasksAffectedByFileChanges
// ============================================================

func TestFindTasksAffectedByFileChanges_MatchingEvent(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	now := time.Now()
	events := map[string]watcher.AggregatedEvent{
		"src": {
			Dir: "src",
			Files: map[string]*watcher.UpdatedFile{
				"src/main.go": {ModifiedTime: now},
			},
			Tasks: map[string]bool{
				"build":  true,
				"server": true,
			},
		},
	}

	result := runner.findTasksAffectedByFileChanges(events)

	// Both tasks have include "**/*.go" which matches "src/main.go"
	assert.Len(t, result, 2)
	assert.Contains(t, result, "build")
	assert.Contains(t, result, "server")
}

func TestFindTasksAffectedByFileChanges_NoMatchingFiles(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	now := time.Now()
	events := map[string]watcher.AggregatedEvent{
		"assets": {
			Dir: "assets",
			Files: map[string]*watcher.UpdatedFile{
				"assets/style.css": {ModifiedTime: now},
			},
			Tasks: map[string]bool{
				"build": true,
			},
		},
	}

	result := runner.findTasksAffectedByFileChanges(events)

	// Tasks include "**/*.go" which does NOT match "assets/style.css"
	assert.Empty(t, result)
}

func TestFindTasksAffectedByFileChanges_EmptyEvents(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	events := map[string]watcher.AggregatedEvent{}
	result := runner.findTasksAffectedByFileChanges(events)

	assert.Empty(t, result)
}

func TestFindTasksAffectedByFileChanges_ExcludedFile(t *testing.T) {
	// Config where "server" excludes test files
	yml := []byte(`
tasks:
  server:
    type: continuous
    run: "echo server"
    include:
      - "**/*.go"
    exclude:
      - "**/*_test.go"
`)
	cfg, err := config.LoadFromBytes(yml)
	assert.NoError(t, err)

	runner, _ := testRunnerWithDeps(t, cfg)

	now := time.Now()
	events := map[string]watcher.AggregatedEvent{
		"src": {
			Dir: "src",
			Files: map[string]*watcher.UpdatedFile{
				"src/main_test.go": {ModifiedTime: now},
			},
			Tasks: map[string]bool{
				"server": true,
			},
		},
	}

	result := runner.findTasksAffectedByFileChanges(events)
	assert.Empty(t, result, "excluded test files should not trigger the task")
}

func TestFindTasksAffectedByFileChanges_TracksModificationTime(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	modTime := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	events := map[string]watcher.AggregatedEvent{
		"src": {
			Dir: "src",
			Files: map[string]*watcher.UpdatedFile{
				"src/app.go": {ModifiedTime: modTime},
			},
			Tasks: map[string]bool{
				"build": true,
			},
		},
	}

	result := runner.findTasksAffectedByFileChanges(events)
	assert.Contains(t, result, "build")
	assert.Equal(t, modTime, result["build"].ModificationTime)
}

// ============================================================
// Tests for onTaskFinished
// ============================================================

func TestOnTaskFinished_RemovesTaskFromRunningList(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	// Add a mock running task
	buildTask := cfg.Tasks["build"]
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})
	rt := task.NewRunningTaskWithFactory(
		buildTask, testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	runner.RunningTasks = []*task.RunningTask{rt}
	assert.Len(t, runner.RunningTasks, 1)

	runner.onTaskFinished("build", false)

	assert.Empty(t, runner.RunningTasks, "task should be removed from running list after finishing")
}

func TestOnTaskFinished_ErroredTask_RemovesFromRunning(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})
	rt := task.NewRunningTaskWithFactory(
		buildTask, testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	runner.RunningTasks = []*task.RunningTask{rt}

	runner.onTaskFinished("build", true)

	assert.Empty(t, runner.RunningTasks, "errored task should also be removed from running list")
}

func TestOnTaskFinished_SingleRunCompletion(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)
	assert.True(t, runner.runOnce)

	// No running tasks, no scheduled tasks — completion should signal
	done := make(chan bool, 1)
	go func() {
		runner.WaitForCompletion()
		done <- true
	}()

	// Give the goroutine time to block on the channel before we send
	time.Sleep(50 * time.Millisecond)

	// Trigger onTaskFinished with no running/scheduled tasks remaining
	runner.onTaskFinished("nonexistent", false)

	select {
	case <-done:
		// Success — completion was signaled
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForCompletion did not return in time — completion not signaled")
	}
}

func TestOnTaskFinished_SingleRunDoesNotCompleteWhenTasksRemain(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	// Add a running task that won't be removed by onTaskFinished("server", true)
	// We need a real RunningTask for this, so we'll use a mock factory
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})
	rt := task.NewRunningTaskWithFactory(
		cfg.Tasks["build"], testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	if rt != nil {
		runner.RunningTasks = append(runner.RunningTasks, rt)
	}

	// Schedule another task so completion should NOT fire
	runner.ScheduledTasks["server"] = &ScheduledTask{TaskConfiguration: cfg.Tasks["server"]}

	done := make(chan bool, 1)
	go func() {
		runner.WaitForCompletion()
		done <- true
	}()

	// Use errored=true so it doesn't trigger startScheduledTasks cascade
	runner.onTaskFinished("nonexistent", true)

	select {
	case <-done:
		t.Fatal("WaitForCompletion should not have returned while tasks are still scheduled")
	case <-time.After(200 * time.Millisecond):
		// Good — completion was not signaled
	}
}

// ============================================================
// Tests for filterRunningTasks
// ============================================================

func TestFilterRunningTasks_FiltersAlreadyRunningWithNewerStartTime(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]

	// Create a running task (its startTime will be time.Now())
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})
	rt := task.NewRunningTaskWithFactory(
		buildTask, testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	runner.RunningTasks = []*task.RunningTask{rt}

	// The schedulable task has a modification time BEFORE the running task's start time
	oldModTime := time.Now().Add(-1 * time.Hour)
	schedulable := map[string]*SchedulableTask{
		"build": {
			TaskConfiguration: buildTask,
			ModificationTime:  oldModTime,
		},
	}

	result := runner.filterRunningTasks(schedulable)
	assert.Empty(t, result, "task should be filtered out when running task started after modification time")
}

func TestFilterRunningTasks_KeepsTaskWhenModificationIsNewer(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]

	// Create a running task
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})
	rt := task.NewRunningTaskWithFactory(
		buildTask, testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	runner.RunningTasks = []*task.RunningTask{rt}

	// The schedulable task has a modification time AFTER the running task's start time
	futureModTime := time.Now().Add(1 * time.Hour)
	schedulable := map[string]*SchedulableTask{
		"build": {
			TaskConfiguration: buildTask,
			ModificationTime:  futureModTime,
		},
	}

	result := runner.filterRunningTasks(schedulable)
	assert.Len(t, result, 1, "task should NOT be filtered out when modification is newer than running start time")
	assert.Contains(t, result, "build")
}

func TestFilterRunningTasks_KeepsTaskNotCurrentlyRunning(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]

	// No running tasks
	schedulable := map[string]*SchedulableTask{
		"build": {
			TaskConfiguration: buildTask,
			ModificationTime:  time.Now(),
		},
	}

	result := runner.filterRunningTasks(schedulable)
	assert.Len(t, result, 1, "task should be kept when it is not currently running")
}

func TestFilterRunningTasks_EmptyInput(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	result := runner.filterRunningTasks(map[string]*SchedulableTask{})
	assert.Empty(t, result)
}

// ============================================================
// Tests for scheduleTasksFromMap
// ============================================================

func TestScheduleTasksFromMap(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	runner.scheduleTasksFromMap(cfg.Tasks)

	assert.Len(t, runner.ScheduledTasks, len(cfg.Tasks))
	for taskId := range cfg.Tasks {
		assert.Contains(t, runner.ScheduledTasks, taskId)
	}
}

// ============================================================
// Tests for scheduleTasksFromSchedulableMap
// ============================================================

func TestScheduleTasksFromSchedulableMap(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]
	schedulable := map[string]*SchedulableTask{
		"build": {
			TaskConfiguration: buildTask,
			ModificationTime:  time.Now(),
		},
	}

	runner.scheduleTasksFromSchedulableMap(schedulable)

	assert.Len(t, runner.ScheduledTasks, 1)
	assert.Contains(t, runner.ScheduledTasks, "build")
	assert.Equal(t, buildTask, runner.ScheduledTasks["build"].TaskConfiguration)
}

// ============================================================
// Tests for isTaskRunning / isTaskInQueue / getRunningTask
// ============================================================

func TestIsTaskRunning_TrueWhenRunning(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})
	rt := task.NewRunningTaskWithFactory(
		buildTask, testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	runner.RunningTasks = []*task.RunningTask{rt}

	assert.True(t, runner.isTaskRunning("build"))
	assert.False(t, runner.isTaskRunning("server"))
}

func TestIsTaskInQueue_TrueWhenQueued(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	runner.ScheduledTasks["build"] = &ScheduledTask{
		TaskConfiguration: cfg.Tasks["build"],
	}

	assert.True(t, runner.isTaskInQueue("build"))
	assert.False(t, runner.isTaskInQueue("server"))
}

func TestGetRunningTask_ReturnsCorrectTask(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})
	rt := task.NewRunningTaskWithFactory(
		buildTask, testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	runner.RunningTasks = []*task.RunningTask{rt}

	found := runner.getRunningTask("build")
	assert.NotNil(t, found)
	assert.Equal(t, "build", found.TaskId())

	notFound := runner.getRunningTask("nonexistent")
	assert.Nil(t, notFound)
}

// ============================================================
// Tests for multiple events and task scheduling integration
// ============================================================

func TestFindTasksAffectedByFileChanges_MultipleEvents(t *testing.T) {
	yml := []byte(`
tasks:
  css-build:
    type: build
    run: "echo css"
    include:
      - "**/*.css"
  go-build:
    type: build
    run: "echo go"
    include:
      - "**/*.go"
`)
	cfg, err := config.LoadFromBytes(yml)
	assert.NoError(t, err)

	runner, _ := testRunnerWithDeps(t, cfg)

	now := time.Now()
	events := map[string]watcher.AggregatedEvent{
		"src": {
			Dir: "src",
			Files: map[string]*watcher.UpdatedFile{
				"src/main.go": {ModifiedTime: now},
			},
			Tasks: map[string]bool{
				"go-build": true,
			},
		},
		"styles": {
			Dir: "styles",
			Files: map[string]*watcher.UpdatedFile{
				"styles/app.css": {ModifiedTime: now},
			},
			Tasks: map[string]bool{
				"css-build": true,
			},
		},
	}

	result := runner.findTasksAffectedByFileChanges(events)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "go-build")
	assert.Contains(t, result, "css-build")
}

func TestFindTasksAffectedByFileChanges_TaskNotInConfig(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	now := time.Now()
	events := map[string]watcher.AggregatedEvent{
		"src": {
			Dir: "src",
			Files: map[string]*watcher.UpdatedFile{
				"src/main.go": {ModifiedTime: now},
			},
			Tasks: map[string]bool{
				"nonexistent-task": true,
			},
		},
	}

	result := runner.findTasksAffectedByFileChanges(events)
	assert.Empty(t, result, "tasks not in config should be skipped")
}

// ============================================================
// Tests for checkCompletion
// ============================================================

func TestCheckCompletion_SignalsWhenAllDone(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	done := make(chan bool, 1)
	go func() {
		runner.WaitForCompletion()
		done <- true
	}()

	// Give the goroutine time to block on the channel before we send
	time.Sleep(50 * time.Millisecond)

	// No running tasks, no scheduled tasks
	runner.checkCompletion()

	select {
	case <-done:
		// Expected
	case <-time.After(2 * time.Second):
		t.Fatal("checkCompletion should signal when all tasks are done")
	}
}

func TestCheckCompletion_DoesNotSignalWhenTasksRunning(t *testing.T) {
	cfg := testConfig(t)
	runner, _ := testRunnerWithDeps(t, cfg)

	buildTask := cfg.Tasks["build"]
	factory := &mockCommandFactory{}
	wrapLogger := cmdwrap.NewWrapLogger([]string{"build"})
	rt := task.NewRunningTaskWithFactory(
		buildTask, testLogger(), wrapLogger, nil,
		func(string, bool) {}, testPlatform(), factory,
	)
	runner.RunningTasks = []*task.RunningTask{rt}

	done := make(chan bool, 1)
	go func() {
		runner.WaitForCompletion()
		done <- true
	}()

	runner.checkCompletion()

	select {
	case <-done:
		t.Fatal("checkCompletion should not signal when tasks are still running")
	case <-time.After(200 * time.Millisecond):
		// Expected
	}
}

// ============================================================
// Tests for ScheduledTask / SchedulableTask types
// ============================================================

func TestScheduledTask_HoldsConfiguration(t *testing.T) {
	taskCfg := &config.TaskConfig{
		Identifier: "my-task",
		Type:       config.TasKType_Build,
		Run:        configutil.StringArray{"echo hello"},
	}

	st := &ScheduledTask{TaskConfiguration: taskCfg}
	assert.Equal(t, "my-task", st.TaskConfiguration.Identifier)
	assert.Equal(t, config.TasKType_Build, st.TaskConfiguration.Type)
}

func TestSchedulableTask_HoldsConfigAndModTime(t *testing.T) {
	modTime := time.Date(2026, 2, 14, 12, 0, 0, 0, time.UTC)
	taskCfg := &config.TaskConfig{
		Identifier: "schedulable-task",
		Type:       config.TasKType_Build,
	}

	st := &SchedulableTask{
		TaskConfiguration: taskCfg,
		ModificationTime:  modTime,
	}
	assert.Equal(t, "schedulable-task", st.TaskConfiguration.Identifier)
	assert.Equal(t, modTime, st.ModificationTime)
}

// ============================================================
// Tests for nil-guard behavior (review comment fixes)
// ============================================================

func TestNewRunnerWithDeps_NilPlatform(t *testing.T) {
	// Passing nil Platform in deps should not panic; should default to NewPlatform().
	cfg := testConfig(t)
	factory := &mockCommandFactory{}
	deps := RunnerDeps{
		Platform:   nil,
		FS:         util.NewOSFileSystem(),
		CmdFactory: factory,
	}

	assert.NotPanics(t, func() {
		runner := NewRunnerWithDeps(cfg, testLogger(), true, deps)
		assert.NotNil(t, runner)
		assert.NotNil(t, runner.deps.Platform, "Platform should be defaulted")
	})
}

func TestNewRunnerWithDeps_NilFS(t *testing.T) {
	cfg := testConfig(t)
	factory := &mockCommandFactory{}
	deps := RunnerDeps{
		Platform:   testPlatform(),
		FS:         nil,
		CmdFactory: factory,
	}

	assert.NotPanics(t, func() {
		runner := NewRunnerWithDeps(cfg, testLogger(), true, deps)
		assert.NotNil(t, runner)
		assert.NotNil(t, runner.deps.FS, "FS should be defaulted")
	})
}

func TestNewRunnerWithDeps_NilCmdFactory(t *testing.T) {
	cfg := testConfig(t)
	deps := RunnerDeps{
		Platform:   testPlatform(),
		FS:         util.NewOSFileSystem(),
		CmdFactory: nil,
	}

	assert.NotPanics(t, func() {
		runner := NewRunnerWithDeps(cfg, testLogger(), true, deps)
		assert.NotNil(t, runner)
		assert.NotNil(t, runner.deps.CmdFactory, "CmdFactory should be defaulted")
	})
}

func TestNewRunnerWithDeps_AllNilDeps(t *testing.T) {
	// Passing a completely zero RunnerDeps should not panic.
	cfg := testConfig(t)
	deps := RunnerDeps{}

	assert.NotPanics(t, func() {
		runner := NewRunnerWithDeps(cfg, testLogger(), true, deps)
		assert.NotNil(t, runner)
		assert.NotNil(t, runner.deps.Platform)
		assert.NotNil(t, runner.deps.FS)
		assert.NotNil(t, runner.deps.CmdFactory)
	})
}

func TestGetMatchedFilesWithFS_NilFS(t *testing.T) {
	// Passing nil fs should not panic; it should default to NewOSFileSystem().
	cleanUpTask := &config.CleanUpTaskConfig{
		Identifier: "test-cleanup",
		Include:    configutil.NewGlobArray("**/*.log"),
		Exclude:    configutil.NewGlobArray(),
		Strategy:   config.CleanUpStrategy_KeepLatest,
	}

	assert.NotPanics(t, func() {
		// This will walk the real filesystem (since nil defaults to OS),
		// but should not panic.
		result, err := GetMatchedFilesWithFS(cleanUpTask, configutil.GlobArray{}, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result, "result should be non-nil even with nil fs")
	})
}

// ============================================================
// Tests for Go compiler temp file scenario
// These reproduce the issue where Go 1.26 creates transient temp
// files like "tmp/bin/<random>-go-tmp-umask" that should be
// excludable by user-defined patterns.
// ============================================================

func TestFindTasksAffectedByFileChanges_GoCompilerTempFile_ExcludePattern(t *testing.T) {
	// Scenario: user has a continuous "backend" task that watches tmp/bin/**
	// and has an exclude pattern for Go compiler temp files.
	// The temp file should be excluded and NOT trigger a restart.
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "echo backend"
    include:
      - "tmp/bin/**"
    exclude:
      - "*-go-tmp-*"
`)
	cfg, err := config.LoadFromBytes(yml)
	assert.NoError(t, err)

	runner, _ := testRunnerWithDeps(t, cfg)

	now := time.Now()
	events := map[string]watcher.AggregatedEvent{
		"tmp/bin": {
			Dir: "tmp/bin",
			Files: map[string]*watcher.UpdatedFile{
				"tmp/bin/adasdasderii4o4jro-go-tmp-umask": {ModifiedTime: now},
			},
			Tasks: map[string]bool{
				"backend": true,
			},
		},
	}

	result := runner.findTasksAffectedByFileChanges(events)
	assert.Empty(t, result,
		"Go compiler temp file matching exclude pattern should not trigger backend restart")
}

func TestFindTasksAffectedByFileChanges_GoCompilerTempFile_NoExclude_MatchesInclude(t *testing.T) {
	// Without an exclude pattern, the temp file SHOULD trigger a restart
	// if it matches the include pattern. This confirms the include works.
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "echo backend"
    include:
      - "tmp/bin/**"
`)
	cfg, err := config.LoadFromBytes(yml)
	assert.NoError(t, err)

	runner, _ := testRunnerWithDeps(t, cfg)

	now := time.Now()
	events := map[string]watcher.AggregatedEvent{
		"tmp/bin": {
			Dir: "tmp/bin",
			Files: map[string]*watcher.UpdatedFile{
				"tmp/bin/adasdasderii4o4jro-go-tmp-umask": {ModifiedTime: now},
			},
			Tasks: map[string]bool{
				"backend": true,
			},
		},
	}

	result := runner.findTasksAffectedByFileChanges(events)
	assert.Len(t, result, 1,
		"Without exclude, temp file matching include should trigger restart")
	assert.Contains(t, result, "backend")
}

func TestFindTasksAffectedByFileChanges_GoCompilerTempFile_MixedWithBinary(t *testing.T) {
	// Scenario: both the temp file AND the real binary appear in the same
	// aggregated event batch. The temp file should be excluded but the
	// binary should still trigger the restart.
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "echo backend"
    include:
      - "tmp/bin/**"
    exclude:
      - "*-go-tmp-*"
`)
	cfg, err := config.LoadFromBytes(yml)
	assert.NoError(t, err)

	runner, _ := testRunnerWithDeps(t, cfg)

	now := time.Now()
	events := map[string]watcher.AggregatedEvent{
		"tmp/bin": {
			Dir: "tmp/bin",
			Files: map[string]*watcher.UpdatedFile{
				"tmp/bin/adasdasderii4o4jro-go-tmp-umask": {ModifiedTime: now},
				"tmp/bin/myapp": {ModifiedTime: now},
			},
			Tasks: map[string]bool{
				"backend": true,
			},
		},
	}

	result := runner.findTasksAffectedByFileChanges(events)
	assert.Len(t, result, 1, "Binary change should still trigger restart")
	assert.Contains(t, result, "backend")
}

func TestNewRunner_NilPlatform(t *testing.T) {
	// NewRunner with nil platform should not panic.
	cfg := testConfig(t)

	assert.NotPanics(t, func() {
		runner := NewRunner(cfg, testLogger(), true, nil)
		assert.NotNil(t, runner)
		assert.NotNil(t, runner.deps.Platform, "Platform should be defaulted in NewRunnerWithDeps")
	})
}
