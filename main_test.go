package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/instance"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireConfiguredInstanceGuardBypassesRunOnce(t *testing.T) {
	called := false
	cfg := &config.Config{
		ConfigDir: "/repo",
		Shared: config.SharedConfig{
			Instance: config.InstanceConfig{Policy: config.InstancePolicy_Block},
		},
	}

	guard, err := acquireConfiguredInstanceGuard(cfg, true, func(options instance.Options) (*instance.Guard, error) {
		called = true
		return nil, nil
	})

	require.NoError(t, err)
	assert.Nil(t, guard)
	assert.False(t, called)
}

func TestMainInstallsSignalContextBeforeInstanceAcquisition(t *testing.T) {
	source, err := os.ReadFile("main.go")
	require.NoError(t, err)

	text := string(source)
	signalIndex := strings.Index(text, "signal.NotifyContext")
	acquireIndex := strings.Index(text, "acquireConfiguredInstanceGuard")

	require.NotEqual(t, -1, signalIndex)
	require.NotEqual(t, -1, acquireIndex)
	assert.Less(t, signalIndex, acquireIndex)
}

func TestAcquireConfiguredInstanceGuardUsesWatchModeConfig(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: "/repo",
		Shared: config.SharedConfig{
			Instance: config.InstanceConfig{
				Policy: config.InstancePolicy_Replace,
			},
		},
	}
	cfg.Shared.Instance.ReplaceTimeout.Duration = 750 * time.Millisecond
	cfg.Shared.Instance.ReplaceTimeout.Set = true

	var captured instance.Options
	guard, err := acquireConfiguredInstanceGuard(cfg, false, func(options instance.Options) (*instance.Guard, error) {
		captured = options
		return nil, nil
	})

	require.NoError(t, err)
	assert.Nil(t, guard)
	assert.Equal(t, "/repo", captured.ConfigDir)
	assert.Equal(t, instance.PolicyReplace, captured.Policy)
	assert.Equal(t, 750*time.Millisecond, captured.ReplaceTimeout)
}
