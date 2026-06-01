package main

import (
	"context"
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

	guard, err := acquireConfiguredInstanceGuard(context.Background(), cfg, true, func(options instance.Options) (*instance.Guard, error) {
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
	signalIndex := strings.Index(text, "signalContextForMode")
	acquireIndex := strings.Index(text, "acquireConfiguredInstanceGuard")

	require.NotEqual(t, -1, signalIndex)
	require.NotEqual(t, -1, acquireIndex)
	assert.Less(t, signalIndex, acquireIndex)
}

func TestSignalContextForRunOnceDoesNotInstallCancelableContext(t *testing.T) {
	ctx, stop := signalContextForMode(true)
	defer stop()

	stop()

	assert.NoError(t, ctx.Err())
}

func TestSignalContextStopDoesNotCancelWatchContext(t *testing.T) {
	ctx, stop := signalContextForMode(false)
	defer stop()

	stop()

	assert.NoError(t, ctx.Err())
}

func TestMainStartsShutdownMonitorBeforeRunnerInitialization(t *testing.T) {
	source, err := os.ReadFile("main.go")
	require.NoError(t, err)

	text := string(source)
	monitorIndex := strings.Index(text, "StartShutdownMonitor")
	runnerIndex := strings.Index(text, "NewRunner")

	require.NotEqual(t, -1, monitorIndex)
	require.NotEqual(t, -1, runnerIndex)
	assert.Less(t, monitorIndex, runnerIndex)
}

func TestMainRestoresSignalDefaultsBeforeRunnerInitialization(t *testing.T) {
	source, err := os.ReadFile("main.go")
	require.NoError(t, err)

	text := string(source)
	stopAcquireSignalsIndex := strings.Index(text, "guard.StartShutdownMonitor(watchCtx, cancelWatch)\n\t\tstopAcquireSignalsNow()")
	runnerIndex := strings.Index(text, "NewRunner")

	require.NotEqual(t, -1, stopAcquireSignalsIndex)
	require.NotEqual(t, -1, runnerIndex)
	assert.Less(t, stopAcquireSignalsIndex, runnerIndex)
}

func TestAcquireConfiguredInstanceGuardPassesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := &config.Config{
		ConfigDir: "/repo",
		Shared: config.SharedConfig{
			Instance: config.InstanceConfig{Policy: config.InstancePolicy_Block},
		},
	}

	var captured instance.Options
	_, err := acquireConfiguredInstanceGuard(ctx, cfg, false, func(options instance.Options) (*instance.Guard, error) {
		captured = options
		return nil, nil
	})

	require.NoError(t, err)
	assert.Same(t, ctx, captured.Context)
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
	guard, err := acquireConfiguredInstanceGuard(context.Background(), cfg, false, func(options instance.Options) (*instance.Guard, error) {
		captured = options
		return nil, nil
	})

	require.NoError(t, err)
	assert.Nil(t, guard)
	assert.Equal(t, "/repo", captured.ConfigDir)
	assert.Equal(t, instance.PolicyReplace, captured.Policy)
	assert.Equal(t, 750*time.Millisecond, captured.ReplaceTimeout)
}
