package main

import (
	"testing"

	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/portmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type runnerPortChecker struct {
	unavailable map[int]bool
}

func (c *runnerPortChecker) Available(port int) bool {
	return !c.unavailable[port]
}

func testRunnerForPorts(t *testing.T, yml string, checker *runnerPortChecker) *Runner {
	t.Helper()

	cfg, err := config.LoadFromBytes([]byte(yml))
	require.NoError(t, err)

	deps := RunnerDeps{
		Platform:    testPlatform(),
		FS:          nil,
		CmdFactory:  &mockCommandFactory{},
		PortManager: portmanager.NewManager(checker),
	}

	return NewRunnerWithDeps(cfg, zap.NewNop(), true, deps)
}

func TestPrepareTaskForStart_AssignsPortToEnvAndInterpolationMap(t *testing.T) {
	checker := &runnerPortChecker{unavailable: map[int]bool{}}
	runner := testRunnerForPorts(t, `
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
      range:
        start: 3000
        end: 3002
`, checker)

	preparedTask, globalEnv, changed, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])

	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "3000", globalEnv["PORT"])
	assert.Contains(t, []string(preparedTask.Env), "PORT=3000")
	assert.NotContains(t, []string(runner.Config.Tasks["backend"].Env), "PORT=3000")
}

func TestPrepareTaskForStart_FallsBackWhenLastPortIsOccupied(t *testing.T) {
	checker := &runnerPortChecker{unavailable: map[int]bool{}}
	runner := testRunnerForPorts(t, `
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
      range:
        start: 3000
        end: 3002
`, checker)

	_, globalEnv, _, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])
	require.NoError(t, err)
	assert.Equal(t, "3000", globalEnv["PORT"])

	runner.releaseTaskPorts("backend")
	checker.unavailable[3000] = true

	_, globalEnv, changed, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])

	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "3001", globalEnv["PORT"])
}

func TestPrepareTaskForStart_InjectsDirectConsumerEnv(t *testing.T) {
	checker := &runnerPortChecker{unavailable: map[int]bool{}}
	runner := testRunnerForPorts(t, `
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
      range:
        start: 3000
        end: 3002
  frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        mode: direct
        env:
          VITE_API_URL: url
`, checker)

	assert.False(t, runner.canStartTask(&ScheduledTask{TaskConfiguration: runner.Config.Tasks["frontend"]}))

	_, _, _, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])
	require.NoError(t, err)

	preparedTask, globalEnv, _, err := runner.prepareTaskForStart(runner.Config.Tasks["frontend"])

	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:3000", globalEnv["VITE_API_URL"])
	assert.Contains(t, []string(preparedTask.Env), "VITE_API_URL=http://127.0.0.1:3000")
}

func TestShouldRestartOnProviderChange_DefaultsByConsumeMode(t *testing.T) {
	checker := &runnerPortChecker{unavailable: map[int]bool{}}
	runner := testRunnerForPorts(t, `
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
      range:
        start: 3000
        end: 3002
      expose:
        proxy:
          enabled: true
          preferred: 8080
  direct-frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        mode: direct
        env:
          VITE_API_URL: url
  proxy-frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        env:
          VITE_API_URL: proxy-url
`, checker)

	assert.True(t, runner.shouldRestartOnProviderChange(runner.Config.Tasks["direct-frontend"].Consumes[0]))
	assert.False(t, runner.shouldRestartOnProviderChange(runner.Config.Tasks["proxy-frontend"].Consumes[0]))
}
