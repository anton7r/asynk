package config

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Equal(t, []string{"go run ./cmd/server"}, server.Run.LegacyStrings())

	build := cfg.Tasks["build"]
	assert.NotNil(t, build)
	assert.Equal(t, TasKType_Build, build.Type)
	assert.Equal(t, []string{"go build -o out ./cmd/app"}, build.Run.LegacyStrings())
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

func TestLoadFromBytes_InstanceConfigDefaults(t *testing.T) {
	yml := []byte(`
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	assert.NoError(t, err)
	assert.Equal(t, InstancePolicy_Allow, cfg.EffectiveInstancePolicy())
	assert.Equal(t, 6*time.Second, cfg.EffectiveInstanceReplaceTimeout())
}

func TestLoadFromBytes_InstanceDefaultReplaceTimeoutExceedsTaskShutdownGrace(t *testing.T) {
	yml := []byte(`
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	assert.NoError(t, err)
	assert.Greater(t, cfg.EffectiveInstanceReplaceTimeout(), 5*time.Second)
}

func TestLoadFromBytes_InstanceConfigValidPolicies(t *testing.T) {
	for _, policy := range []InstancePolicy{InstancePolicy_Allow, InstancePolicy_Block, InstancePolicy_Replace} {
		t.Run(string(policy), func(t *testing.T) {
			yml := []byte(`
shared:
  instance:
    policy: ` + string(policy) + `
    replace-timeout: 6s
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
			cfg, err := LoadFromBytes(yml)

			assert.NoError(t, err)
			assert.Equal(t, policy, cfg.EffectiveInstancePolicy())
			assert.Equal(t, 6*time.Second, cfg.EffectiveInstanceReplaceTimeout())
		})
	}
}

func TestLoadFromBytes_InstanceReplaceTimeoutLimitsApplyOnlyToReplacePolicy(t *testing.T) {
	tests := []struct {
		name       string
		instance   string
		wantPolicy InstancePolicy
	}{
		{
			name: "default allow",
			instance: `
    replace-timeout: 1s`,
			wantPolicy: InstancePolicy_Allow,
		},
		{
			name: "block",
			instance: `
    policy: block
    replace-timeout: 1s`,
			wantPolicy: InstancePolicy_Block,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yml := []byte(`
shared:
  instance:` + tt.instance + `
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
			cfg, err := LoadFromBytes(yml)

			if assert.NoError(t, err) && assert.NotNil(t, cfg) {
				assert.Equal(t, tt.wantPolicy, cfg.EffectiveInstancePolicy())
				assert.Equal(t, time.Second, cfg.EffectiveInstanceReplaceTimeout())
			}
		})
	}
}

func TestLoadFromBytes_InstanceConfigRejectsInvalidPolicy(t *testing.T) {
	yml := []byte(`
shared:
  instance:
    policy: steal
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "invalid shared instance policy")
}

func TestLoadFromBytes_InstanceConfigRejectsNegativeReplaceTimeout(t *testing.T) {
	yml := []byte(`
shared:
  instance:
    policy: replace
    replace-timeout: -1ms
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "replace-timeout")
	}
	assert.Nil(t, cfg)
}

func TestLoadFromBytes_InstanceConfigRejectsZeroReplaceTimeout(t *testing.T) {
	yml := []byte(`
shared:
  instance:
    policy: replace
    replace-timeout: 0s
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "replace-timeout")
	}
	assert.Nil(t, cfg)
}

func TestLoadFromBytes_InstanceConfigRejectsReplaceTimeoutBelowPollInterval(t *testing.T) {
	yml := []byte(`
shared:
  instance:
    policy: replace
    replace-timeout: 1ms
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "replace-timeout")
		assert.Contains(t, err.Error(), "5s")
	}
	assert.Nil(t, cfg)
}

func TestLoadFromBytes_InstanceConfigRejectsReplaceTimeoutAtPollInterval(t *testing.T) {
	yml := []byte(`
shared:
  instance:
    policy: replace
    replace-timeout: 100ms
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "replace-timeout")
		assert.Contains(t, err.Error(), "5s")
	}
	assert.Nil(t, cfg)
}

func TestLoadFromBytes_InstanceConfigRejectsReplaceTimeoutAtTaskShutdownGrace(t *testing.T) {
	yml := []byte(`
shared:
  instance:
    policy: replace
    replace-timeout: 5s
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "replace-timeout")
		assert.Contains(t, err.Error(), "5s")
	}
	assert.Nil(t, cfg)
}

func TestLoadFromBytes_FSDebounce(t *testing.T) {
	yml := []byte(`
shared:
  fs-debounce: 300ms

tasks:
  server:
    type: continuous
    fs-debounce: 1.5s
    run: "go run ."
  build:
    type: build
    run: "go build ."
`)
	cfg, err := LoadFromBytes(yml)

	assert.NoError(t, err)
	assert.True(t, cfg.Shared.FSDebounce.IsSet())
	assert.Equal(t, 300*time.Millisecond, cfg.Shared.FSDebounce.Duration)
	assert.True(t, cfg.Tasks["server"].FSDebounce.IsSet())
	assert.Equal(t, 1500*time.Millisecond, cfg.Tasks["server"].FSDebounce.Duration)
	assert.Equal(t, 1500*time.Millisecond, cfg.EffectiveFSDebounceForTask("server"))
	assert.Equal(t, 300*time.Millisecond, cfg.EffectiveFSDebounceForTask("build"))
	assert.Equal(t, map[string]time.Duration{
		"server": 1500 * time.Millisecond,
		"build":  300 * time.Millisecond,
	}, cfg.TaskFSDebounces())
}

func TestLoadFromBytes_FSDebounceDefaultsTo200Milliseconds(t *testing.T) {
	yml := []byte(`
tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	assert.NoError(t, err)
	assert.False(t, cfg.Shared.FSDebounce.IsSet())
	assert.False(t, cfg.Tasks["app"].FSDebounce.IsSet())
	assert.Equal(t, 200*time.Millisecond, cfg.EffectiveFSDebounceForTask("app"))
}

func TestLoadFromBytes_FSDebounceAllowsZero(t *testing.T) {
	yml := []byte(`
shared:
  fs-debounce: 0ms

tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	assert.NoError(t, err)
	assert.True(t, cfg.Shared.FSDebounce.IsSet())
	assert.Equal(t, time.Duration(0), cfg.EffectiveFSDebounceForTask("app"))
}

func TestLoadFromBytes_FSDebounceInvalid(t *testing.T) {
	yml := []byte(`
shared:
  fs-debounce: tomorrow

tasks:
  app:
    type: continuous
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	assert.Error(t, err)
	assert.Nil(t, cfg)
}

func TestLoadFromBytes_FSDebounceNegative(t *testing.T) {
	yml := []byte(`
tasks:
  app:
    type: continuous
    fs-debounce: -1ms
    run: "echo hello"
`)
	cfg, err := LoadFromBytes(yml)

	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "fs-debounce cannot be negative")
}

func TestLoadFromBytes_RebuildSuppressionInheritance(t *testing.T) {
	yml := []byte(`
shared:
  rebuild-suppression:
    enabled: true
    mode: language-aware-hash
tasks:
  inherited:
    type: build
    run: "echo inherited"
  disabled:
    type: build
    run: "echo disabled"
    rebuild-suppression:
      enabled: false
  override:
    type: build
    run: "echo override"
    rebuild-suppression:
      mode: size-and-mtime
`)

	cfg, err := LoadFromBytes(yml)
	assert.NoError(t, err)

	inherited := cfg.EffectiveRebuildSuppressionForTask("inherited")
	assert.True(t, inherited.Enabled)
	assert.Equal(t, RebuildSuppressionMode_LanguageAwareHash, inherited.Mode)

	disabled := cfg.EffectiveRebuildSuppressionForTask("disabled")
	assert.False(t, disabled.Enabled)

	override := cfg.EffectiveRebuildSuppressionForTask("override")
	assert.True(t, override.Enabled)
	assert.Equal(t, RebuildSuppressionMode_SizeAndMTime, override.Mode)
}

func TestLoadFromBytes_RebuildSuppressionTaskDefaults(t *testing.T) {
	yml := []byte(`
tasks:
  build:
    type: build
    run: "echo build"
    rebuild-suppression:
      enabled: true
`)

	cfg, err := LoadFromBytes(yml)
	assert.NoError(t, err)

	effective := cfg.EffectiveRebuildSuppressionForTask("build")
	assert.True(t, effective.Enabled)
	assert.Equal(t, RebuildSuppressionMode_SizeAndHash, effective.Mode)
}

func TestLoadFromBytes_RebuildSuppressionInvalid(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "mode",
			yaml: `
shared:
  rebuild-suppression:
    mode: checksum
tasks:
  build:
    type: build
    run: "echo build"
`,
			want: "rebuild-suppression mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadFromBytes([]byte(tt.yaml))
			assert.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
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
	assert.Equal(t, []string{"go run . -linux"}, server.RunLinux.LegacyStrings())
	assert.Equal(t, []string{"go run . -windows"}, server.RunWindows.LegacyStrings())
	assert.Equal(t, []string{"go run . -mac"}, server.RunMac.LegacyStrings())
}

func TestLoadFromBytes_PortConfig(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      env: PORT
      preferred: 3000
      range:
        start: 3000
        end: 3099
      expose:
        name: backend
        proxy:
          enabled: true
          preferred: 8080
          range:
            start: 8080
            end: 8099
  frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        mode: proxy
        env:
          VITE_API_URL: proxy-url
        on-change: none
`)
	cfg, err := LoadFromBytes(yml)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)

	backend := cfg.Tasks["backend"]
	assert.Equal(t, "PORT", backend.Port.Env)
	assert.Equal(t, 3000, backend.Port.Preferred)
	assert.Equal(t, 3000, backend.Port.Range.Start)
	assert.Equal(t, "backend", backend.Port.Expose.Name)
	assert.True(t, backend.Port.Expose.Proxy.Enabled)
	assert.Equal(t, 8080, backend.Port.Expose.Proxy.Preferred)

	frontend := cfg.Tasks["frontend"]
	assert.Len(t, frontend.Consumes, 1)
	assert.Equal(t, "backend", frontend.Consumes[0].Task)
	assert.Equal(t, ConsumeMode_Proxy, frontend.Consumes[0].Mode)
	assert.Equal(t, "proxy-url", frontend.Consumes[0].Env["VITE_API_URL"])
	assert.Equal(t, ConsumeOnChange_None, frontend.Consumes[0].OnChange)
}

func TestLoadFromBytes_AutoStartFalse(t *testing.T) {
	yml := []byte(`
tasks:
  generate:
    type: build
    auto-start: false
    run: "echo generate"
`)

	cfg, err := LoadFromBytes(yml)

	require.NoError(t, err)
	require.NotNil(t, cfg.Tasks["generate"].AutoStart)
	assert.False(t, cfg.Tasks["generate"].AutoStartEnabled())
}

func TestLoadFromBytes_ReadinessWithManagedPath(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
    readiness:
      path: /health
      interval: 10ms
      timeout: 1s
      triggers:
        - task: generate
          include:
            - "internal/routes/**"
          exclude:
            - "**/*_test.go"
  generate:
    type: build
    auto-start: false
    run: "echo generate"
`)

	cfg, err := LoadFromBytes(yml)

	require.NoError(t, err)
	readiness := cfg.Tasks["backend"].Readiness
	require.NotNil(t, readiness)
	assert.Equal(t, "/health", readiness.Path)
	assert.Equal(t, 10*time.Millisecond, readiness.EffectiveInterval())
	assert.Equal(t, time.Second, readiness.EffectiveTimeout())
	require.Len(t, readiness.Triggers, 1)
	assert.Equal(t, "generate", readiness.Triggers[0].Task)
	assert.True(t, readiness.Triggers[0].Include.AnyMatches("internal/routes/users.go"))
	assert.True(t, readiness.Triggers[0].Exclude.AnyMatches("internal/routes/users_test.go"))
}

func TestLoadFromBytes_ReadinessWithExplicitURL(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "go run ."
    readiness:
      url: http://127.0.0.1:3000/health
      triggers:
        - task: generate
  generate:
    type: build
    auto-start: false
    run: "echo generate"
`)

	cfg, err := LoadFromBytes(yml)

	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:3000/health", cfg.Tasks["backend"].Readiness.URL)
	assert.Equal(t, DefaultReadinessInterval, cfg.Tasks["backend"].Readiness.EffectiveInterval())
	assert.Equal(t, DefaultReadinessTimeout, cfg.Tasks["backend"].Readiness.EffectiveTimeout())
}

func TestLoadFromBytes_ReadinessRejectsBuildTask(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: build
    run: "echo backend"
    readiness:
      url: http://127.0.0.1:3000/health
  generate:
    type: build
    run: "echo generate"
`)

	_, err := LoadFromBytes(yml)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "readiness can only be configured for continuous tasks")
}

func TestLoadFromBytes_ReadinessRejectsMissingAndDuplicatedHealthSource(t *testing.T) {
	cases := []string{
		`
tasks:
  backend:
    type: continuous
    run: "echo backend"
    readiness:
      triggers:
        - task: generate
  generate:
    type: build
    run: "echo generate"
`,
		`
tasks:
  backend:
    type: continuous
    run: "echo backend"
    port:
      preferred: 3000
    readiness:
      path: /health
      url: http://127.0.0.1:3000/health
      triggers:
        - task: generate
  generate:
    type: build
    run: "echo generate"
`,
	}

	for _, yml := range cases {
		_, err := LoadFromBytes([]byte(yml))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "readiness needs exactly one of path or url")
	}
}

func TestLoadFromBytes_ReadinessPathRequiresPort(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "echo backend"
    readiness:
      path: /health
      triggers:
        - task: generate
  generate:
    type: build
    run: "echo generate"
`)

	_, err := LoadFromBytes(yml)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "readiness path requires port")
}

func TestLoadFromBytes_ReadinessRejectsInvalidTrigger(t *testing.T) {
	cases := []struct {
		name    string
		yml     string
		message string
	}{
		{
			name: "missing target",
			yml: `
tasks:
  backend:
    type: continuous
    run: "echo backend"
    readiness:
      url: http://127.0.0.1:3000/health
      triggers:
        - task: generate
`,
			message: "does not exist",
		},
		{
			name: "continuous target",
			yml: `
tasks:
  backend:
    type: continuous
    run: "echo backend"
    readiness:
      url: http://127.0.0.1:3000/health
      triggers:
        - task: frontend
  frontend:
    type: continuous
    run: "echo frontend"
`,
			message: "must be a build task",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFromBytes([]byte(tt.yml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestLoadFromBytes_ReadinessRejectsNonPositiveDurations(t *testing.T) {
	cases := []struct {
		name    string
		field   string
		message string
	}{
		{name: "interval", field: "interval: 0s", message: "readiness interval must be positive"},
		{name: "timeout", field: "timeout: -1ms", message: "readiness timeout must be positive"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			yml := []byte(fmt.Sprintf(`
tasks:
  backend:
    type: continuous
    run: "echo backend"
    readiness:
      url: http://127.0.0.1:3000/health
      %s
      triggers:
        - task: generate
  generate:
    type: build
    run: "echo generate"
`, tt.field))

			_, err := LoadFromBytes(yml)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestLoadFromBytes_PortConfigAllowsPreferredWithoutRange(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "go run . --port ${PORT}"
    port:
      preferred: 3000
`)
	cfg, err := LoadFromBytes(yml)
	if assert.NoError(t, err) && assert.NotNil(t, cfg) {
		assert.Equal(t, 3000, cfg.Tasks["backend"].Port.Preferred)
		assert.Nil(t, cfg.Tasks["backend"].Port.Range)
	}
}

func TestLoadFromBytes_PortConfigRejectsBuildTask(t *testing.T) {
	yml := []byte(`
tasks:
  build:
    type: build
    run: "go build ."
    port:
      range:
        start: 3000
        end: 3001
`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "port can only be configured for continuous tasks")
}

func TestLoadFromBytes_PortConfigRejectsInvalidRange(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "go run ."
    port:
      range:
        start: 3002
        end: 3001
`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "start must be less than or equal to end")
}

func TestLoadFromBytes_ConsumesRejectsMissingProvider(t *testing.T) {
	yml := []byte(`
tasks:
  frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        env:
          VITE_API_URL: url
`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "consumed task 'backend' does not exist")
}

func TestLoadFromBytes_ConsumesRejectsProxyModeWithoutProxy(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "go run ."
    port:
      range:
        start: 3000
        end: 3001
  frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        mode: proxy
        env:
          VITE_API_URL: proxy-url
`)
	cfg, err := LoadFromBytes(yml)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "does not expose a proxy")
}

func TestLoadFromBytes_ConsumesAcceptsGeneratedServiceExportNames(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "go run ."
    port:
      preferred: 3000
      expose:
        name: backend
        proxy:
          enabled: true
          env: API_PROXY_URL
          preferred: 8080
  frontend:
    type: continuous
    run: "npm run dev"
    consumes:
      - task: backend
        env:
          VITE_API_PORT: BACKEND_PORT
          VITE_API_URL: BACKEND_URL
          VITE_API_PROXY_URL: API_PROXY_URL
`)
	cfg, err := LoadFromBytes(yml)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
}

func TestLoadFromBytes_ConsumesRejectsSelfReference(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "go run ."
    port:
      preferred: 3000
    consumes:
      - task: backend
        env:
          API_URL: url
`)
	cfg, err := LoadFromBytes(yml)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "cannot consume itself")
	}
	assert.Nil(t, cfg)
}

func TestLoadFromBytes_ConsumesRejectsCycles(t *testing.T) {
	yml := []byte(`
tasks:
  api:
    type: continuous
    run: "go run ./api"
    port:
      preferred: 3000
    consumes:
      - task: web
        env:
          WEB_URL: url
  web:
    type: continuous
    run: "npm run dev"
    port:
      preferred: 3001
    consumes:
      - task: api
        env:
          API_URL: url
`)
	cfg, err := LoadFromBytes(yml)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "consume cycle")
	}
	assert.Nil(t, cfg)
}

func TestLoadFromBytes_PortConfigRejectsProxyEnvShadowingBuiltInExport(t *testing.T) {
	yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "go run ."
    port:
      range:
        start: 3000
        end: 3001
      expose:
        proxy:
          enabled: true
          env: port
          preferred: 8080
`)
	cfg, err := LoadFromBytes(yml)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "proxy env shadows reserved export")
	}
	assert.Nil(t, cfg)
}

func TestLoadFromBytes_PortConfigRejectsProxyEnvShadowingNamedDirectExport(t *testing.T) {
	for _, proxyEnv := range []string{"BACKEND_URL", "BACKEND_PORT"} {
		t.Run(proxyEnv, func(t *testing.T) {
			yml := []byte(`
tasks:
  backend:
    type: continuous
    run: "go run ."
    port:
      preferred: 3000
      expose:
        name: backend
        proxy:
          enabled: true
          env: ` + proxyEnv + `
          preferred: 8080
`)
			cfg, err := LoadFromBytes(yml)
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), "proxy env shadows reserved export")
			}
			assert.Nil(t, cfg)
		})
	}
}

func TestFillTaskIds(t *testing.T) {
	cfg := &Config{
		Tasks: map[string]*TaskConfig{
			"alpha": {Run: NewLegacyRunCommands("echo alpha")},
			"beta":  {Run: NewLegacyRunCommands("echo beta")},
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
			"app": {Run: NewLegacyRunCommands("echo app")},
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
