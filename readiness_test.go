package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anton7r/asynk/cmdwrap"
	"github.com/anton7r/asynk/config"
	configutil "github.com/anton7r/asynk/config/util"
	"github.com/anton7r/asynk/portmanager"
	"github.com/anton7r/asynk/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type sequenceReadinessChecker struct {
	mu      sync.Mutex
	results []bool
	urls    []string
}

func (c *sequenceReadinessChecker) Check(ctx context.Context, url string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.urls = append(c.urls, url)
	if len(c.results) == 0 {
		return false, nil
	}

	result := c.results[0]
	c.results = c.results[1:]
	return result, nil
}

func (c *sequenceReadinessChecker) URLs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string(nil), c.urls...)
}

func testRunnerForReadiness(
	t *testing.T,
	yml string,
	readinessChecker ReadinessChecker,
	factory cmdwrap.CommandFactory,
) *Runner {
	t.Helper()

	cfg, err := config.LoadFromBytes([]byte(yml))
	require.NoError(t, err)

	if factory == nil {
		factory = &recordingCommandFactory{}
	}

	return NewRunnerWithDeps(cfg, zap.NewNop(), true, RunnerDeps{
		Platform:         testPlatform(),
		FS:               util.NewOSFileSystem(),
		CmdFactory:       factory,
		PortManager:      portmanager.NewManager(&runnerPortChecker{unavailable: map[int]bool{}}),
		ReadinessChecker: readinessChecker,
	})
}

func TestScheduleAllTasksSkipsAutoStartFalseTasks(t *testing.T) {
	runner := testRunnerForReadiness(t, `
tasks:
  backend:
    type: continuous
    run: "echo backend"
  generate:
    type: build
    auto-start: false
    run: "echo generate"
`, &sequenceReadinessChecker{}, nil)

	runner.scheduleAllTasks()

	assert.Contains(t, runner.ScheduledTasks, "backend")
	assert.NotContains(t, runner.ScheduledTasks, "generate")
}

func TestReadinessPollingSchedulesInitialTriggerAfterHTTP200(t *testing.T) {
	readinessChecker := &sequenceReadinessChecker{results: []bool{false, true}}
	factory := &recordingCommandFactory{}
	runner := testRunnerForReadiness(t, `
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
    readiness:
      path: /health
      interval: 1ms
      timeout: 200ms
  generate:
    type: build
    auto-start: false
    run: "echo generate"
    consumes:
      - task: backend
        when: ready
        env:
          API_BASE_URL: url
`, readinessChecker, factory)

	_, _, _, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])
	require.NoError(t, err)

	runner.startReadinessPolling("backend", &ScheduledTask{
		TaskConfiguration: runner.Config.Tasks["backend"],
		StartCause:        TaskStartCauseInitial,
	})

	assert.Eventually(t, func() bool {
		return factory.Called("generate")
	}, time.Second, 5*time.Millisecond)

	assert.Contains(t, readinessChecker.URLs(), "http://127.0.0.1:3000/health")
}

func TestReadinessConsumerRunsOnlyForMatchingRestartFiles(t *testing.T) {
	consume := config.ConsumeConfig{
		Task:    "backend",
		When:    config.ConsumeWhen_Ready,
		Include: configutilGlobArray(t, "internal/routes/**", "internal/models/**"),
		Exclude: configutilGlobArray(t, "**/*_test.go"),
	}

	assert.True(t, readinessConsumerShouldRun(
		consume,
		TaskStartCauseFile,
		[]string{"internal/routes/users.go"},
	))
	assert.False(t, readinessConsumerShouldRun(
		consume,
		TaskStartCauseFile,
		[]string{"internal/routes/users_test.go"},
	))
	assert.False(t, readinessConsumerShouldRun(
		consume,
		TaskStartCauseFile,
		[]string{"README.md"},
	))
	assert.True(t, readinessConsumerShouldRun(
		consume,
		TaskStartCauseInitial,
		nil,
	))
}

func TestReadinessSchedulesMatchingFileTriggeredRestart(t *testing.T) {
	readinessChecker := &sequenceReadinessChecker{results: []bool{true}}
	factory := &recordingCommandFactory{}
	runner := testRunnerForReadiness(t, `
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
    readiness:
      path: /health
      interval: 1ms
      timeout: 200ms
  generate:
    type: build
    auto-start: false
    run: "echo generate"
    consumes:
      - task: backend
        when: ready
        include:
          - "internal/routes/**"
        env:
          API_BASE_URL: url
`, readinessChecker, factory)

	_, _, _, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])
	require.NoError(t, err)

	runner.startReadinessPolling("backend", &ScheduledTask{
		TaskConfiguration: runner.Config.Tasks["backend"],
		StartCause:        TaskStartCauseFile,
		MatchedFiles:      []string{"internal/routes/users.go"},
	})

	assert.Eventually(t, func() bool {
		return factory.Called("generate")
	}, time.Second, 5*time.Millisecond)
}

func TestReadinessDoesNotScheduleNonMatchingFileTriggeredRestart(t *testing.T) {
	readinessChecker := &sequenceReadinessChecker{results: []bool{true}}
	factory := &recordingCommandFactory{}
	runner := testRunnerForReadiness(t, `
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
    readiness:
      path: /health
      interval: 1ms
      timeout: 200ms
  generate:
    type: build
    auto-start: false
    run: "echo generate"
    consumes:
      - task: backend
        when: ready
        include:
          - "internal/routes/**"
        env:
          API_BASE_URL: url
`, readinessChecker, factory)

	_, _, _, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])
	require.NoError(t, err)

	runner.startReadinessPolling("backend", &ScheduledTask{
		TaskConfiguration: runner.Config.Tasks["backend"],
		StartCause:        TaskStartCauseFile,
		MatchedFiles:      []string{"README.md"},
	})

	assert.Never(t, func() bool {
		return factory.Called("generate")
	}, 100*time.Millisecond, 5*time.Millisecond)
}

func TestReadinessStaleGenerationCannotScheduleTrigger(t *testing.T) {
	factory := &recordingCommandFactory{}
	runner := testRunnerForReadiness(t, `
tasks:
  backend:
    type: continuous
    run: "go run ."
    port:
      preferred: 3000
    readiness:
      url: http://127.0.0.1:3000/health
  generate:
    type: build
    auto-start: false
    run: "echo generate"
    consumes:
      - task: backend
        when: ready
        env:
          API_BASE_URL: url
`, &sequenceReadinessChecker{}, factory)

	runner.readinessGeneration["backend"] = 2
	runner.scheduleReadinessConsumers(
		"backend",
		TaskStartCauseInitial,
		nil,
		1,
	)

	assert.Empty(t, runner.ScheduledTasks)
	assert.False(t, factory.Called("generate"))
}

func TestReadinessTimeoutDoesNotMarkBackendFailed(t *testing.T) {
	readinessChecker := &sequenceReadinessChecker{}
	runner := testRunnerForReadiness(t, `
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
    readiness:
      path: /health
      interval: 1ms
      timeout: 5ms
  generate:
    type: build
    auto-start: false
    run: "echo generate"
    consumes:
      - task: backend
        when: ready
        env:
          API_BASE_URL: url
`, readinessChecker, nil)

	_, _, _, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])
	require.NoError(t, err)

	runner.startReadinessPolling("backend", &ScheduledTask{
		TaskConfiguration: runner.Config.Tasks["backend"],
		StartCause:        TaskStartCauseInitial,
	})

	assert.Eventually(t, func() bool {
		runner.readinessMutex.Lock()
		defer runner.readinessMutex.Unlock()
		return runner.readinessPollers["backend"] == nil
	}, time.Second, 5*time.Millisecond)

	runner.serviceExportMutex.Lock()
	exports := runner.serviceExports["backend"]
	runner.serviceExportMutex.Unlock()
	assert.NotEmpty(t, exports, "readiness timeout should not clear provider exports or fail the backend")
}

func TestReadinessConsumerCannotStartBeforeProviderReady(t *testing.T) {
	runner := testRunnerForReadiness(t, `
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
    readiness:
      path: /health
  generate:
    type: build
    auto-start: false
    run: "echo generate"
    consumes:
      - task: backend
        when: ready
        env:
          API_BASE_URL: url
`, &sequenceReadinessChecker{}, nil)

	_, _, _, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])
	require.NoError(t, err)

	assert.False(t, runner.canStartTask(&ScheduledTask{TaskConfiguration: runner.Config.Tasks["generate"]}))

	runner.readinessMutex.Lock()
	runner.readinessGeneration["backend"] = 1
	runner.readinessReady["backend"] = 1
	runner.readinessMutex.Unlock()

	assert.True(t, runner.canStartTask(&ScheduledTask{TaskConfiguration: runner.Config.Tasks["generate"]}))
}

func configutilGlobArray(t *testing.T, patterns ...string) configutil.GlobArray {
	t.Helper()

	globs := configutil.NewGlobArray(patterns...)
	require.NotNil(t, globs)
	return globs
}
