package main

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/anton7r/asynk/cmdwrap"
	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/portmanager"
	"github.com/anton7r/asynk/util/interpolation/idgen"
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

func freeRunnerPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}

func testRunnerForPorts(t *testing.T, yml string, checker *runnerPortChecker) *Runner {
	t.Helper()

	return testRunnerForPortsWithFactory(t, yml, checker, &mockCommandFactory{})
}

func testRunnerForPortsWithFactory(
	t *testing.T,
	yml string,
	checker *runnerPortChecker,
	factory cmdwrap.CommandFactory,
) *Runner {
	t.Helper()

	cfg, err := config.LoadFromBytes([]byte(yml))
	require.NoError(t, err)

	deps := RunnerDeps{
		Platform:    testPlatform(),
		FS:          nil,
		CmdFactory:  factory,
		PortManager: portmanager.NewManager(checker),
	}

	return NewRunnerWithDeps(cfg, zap.NewNop(), true, deps)
}

type recordingCommandFactory struct {
	mu    sync.Mutex
	calls []string
}

func (f *recordingCommandFactory) ParseAllCommands(
	commands config.RunCommands,
	taskId string,
	log *zap.Logger,
	env map[string]string,
	cwd string,
	genIdInterpolator *idgen.GenIDInterpolator,
) ([]*cmdwrap.CommandWrapper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, taskId)
	return []*cmdwrap.CommandWrapper{}, nil
}

func (f *recordingCommandFactory) Called(taskID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, call := range f.calls {
		if call == taskID {
			return true
		}
	}
	return false
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

func TestShouldRestartOnProviderChange_DefaultsToDirectForDirectExports(t *testing.T) {
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
  url-frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        env:
          VITE_API_URL: url
  port-frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        env:
          API_PORT: port
`, checker)

	assert.True(t, runner.shouldRestartOnProviderChange(runner.Config.Tasks["url-frontend"].Consumes[0]))
	assert.True(t, runner.shouldRestartOnProviderChange(runner.Config.Tasks["port-frontend"].Consumes[0]))
}

func TestShouldRestartOnProviderChange_RestartsDirectExportsWithExplicitProxyMode(t *testing.T) {
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
  frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        mode: proxy
        env:
          VITE_API_URL: url
`, checker)

	assert.True(t, runner.shouldRestartOnProviderChange(runner.Config.Tasks["frontend"].Consumes[0]))
}

func TestShouldRestartOnProviderChange_RestartsNamedDirectExportsWithExplicitProxyMode(t *testing.T) {
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
        name: backend
        proxy:
          enabled: true
          preferred: 8080
  url-frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        mode: proxy
        env:
          VITE_API_URL: BACKEND_URL
  port-frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        mode: proxy
        env:
          API_PORT: BACKEND_PORT
`, checker)

	assert.True(t, runner.shouldRestartOnProviderChange(runner.Config.Tasks["url-frontend"].Consumes[0]))
	assert.True(t, runner.shouldRestartOnProviderChange(runner.Config.Tasks["port-frontend"].Consumes[0]))
}

func TestShouldRestartOnProviderChange_TreatsCustomProxyExportAsProxyBacked(t *testing.T) {
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
        name: backend
        proxy:
          enabled: true
          env: API_PROXY_URL
          preferred: 8080
  default-backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3010
      range:
        start: 3010
        end: 3012
      expose:
        name: backend
        proxy:
          enabled: true
          preferred: 8081
  custom-proxy-frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        env:
          VITE_API_URL: API_PROXY_URL
  default-proxy-frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: default-backend
        env:
          VITE_API_URL: BACKEND_PROXY_URL
`, checker)

	assert.False(t, runner.shouldRestartOnProviderChange(runner.Config.Tasks["custom-proxy-frontend"].Consumes[0]))
	assert.False(t, runner.shouldRestartOnProviderChange(runner.Config.Tasks["default-proxy-frontend"].Consumes[0]))
}

func TestReleaseTaskPorts_ClearsDirectExportsForConsumers(t *testing.T) {
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
        env:
          VITE_API_URL: url
`, checker)

	_, _, _, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])
	require.NoError(t, err)
	assert.True(t, runner.canStartTask(&ScheduledTask{TaskConfiguration: runner.Config.Tasks["frontend"]}))

	runner.releaseTaskPorts("backend")

	assert.False(t, runner.canStartTask(&ScheduledTask{TaskConfiguration: runner.Config.Tasks["frontend"]}))
}

func TestStartScheduledTask_SchedulesExplicitRestartConsumerWhenExportsStayStable(t *testing.T) {
	checker := &runnerPortChecker{unavailable: map[int]bool{}}
	factory := &recordingCommandFactory{}
	runner := testRunnerForPortsWithFactory(t, fmt.Sprintf(`
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
          preferred: %d
  frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        env:
          VITE_API_URL: proxy-url
        on-change: restart
`, freeRunnerPort(t)), checker, factory)

	_, _, changed, err := runner.prepareTaskForStart(runner.Config.Tasks["backend"])
	require.NoError(t, err)
	require.True(t, changed)

	runner.startScheduledTask("backend", &ScheduledTask{TaskConfiguration: runner.Config.Tasks["backend"]})

	assert.Eventually(t, func() bool {
		runner.ScheduledTaskMutex.Lock()
		queued := runner.ScheduledTasks["frontend"] != nil
		runner.ScheduledTaskMutex.Unlock()

		return queued || factory.Called("frontend")
	}, time.Second, 10*time.Millisecond)
}
