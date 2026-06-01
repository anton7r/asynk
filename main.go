package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/instance"
	"github.com/anton7r/asynk/util"
	"go.uber.org/zap"
)

// Version is set at build time via ldflags:
//
//	go build -ldflags "-X main.Version=1.0.0"
var Version = "dev"

func main() {
	// Parse command line flags
	runOnce := flag.Bool("once", false, "Run all tasks once and exit without watching for file changes")
	flag.Parse()

	fmt.Printf("github.com/anton7r/asynk %s is starting...\n", Version)

	configuration, err := config.LoadFromYAML()

	if err != nil {
		fmt.Printf("Error loading configuration: %v\n", err)
		return
	}

	logLevel, err := parseLogLevel(configuration.Shared.LogLevel)
	if err != nil {
		fmt.Printf("Error parsing log level: %v\n", err)
		return
	}
	log := createLogger(logLevel)

	acquireCtx, stopAcquireSignals := signalContextForMode(*runOnce)
	acquireSignalsStopped := false
	stopAcquireSignalsNow := func() {
		if acquireSignalsStopped {
			return
		}
		stopAcquireSignals()
		acquireSignalsStopped = true
	}
	defer stopAcquireSignalsNow()

	guard, err := acquireConfiguredInstanceGuard(acquireCtx, configuration, *runOnce, instance.Acquire)
	if err != nil {
		fmt.Printf("Error acquiring instance guard: %v\n", err)
		log.Error("Error acquiring instance guard", zap.Error(err))
		return
	}
	defer func() {
		if err := guard.Release(); err != nil {
			log.Error("Error releasing instance guard", zap.Error(err))
		}
	}()

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	defer cancelWatch()
	if !*runOnce {
		guard.StartShutdownMonitor(watchCtx, cancelWatch)
		stopAcquireSignalsNow()
		if acquireCtx.Err() != nil {
			cancelWatch()
		}
	}

	// Create a new application state
	platform := util.NewPlatform()
	runner := NewRunner(configuration, log, *runOnce, platform)

	if *runOnce {
		log.Info("Running in single-run mode (no file watching)")
		runner.Start()
		runner.WaitForCompletion()
		log.Info("All tasks completed. Exiting.")
	} else {
		runSignalCtx, stopRunSignals := signalContextForMode(false)
		defer stopRunSignals()
		cancelWhenDone(runSignalCtx, watchCtx, cancelWatch)

		log.Info("Press Ctrl+C to stop Asynk.")

		if watchCtx.Err() == nil {
			go runner.Start()
		}

		<-watchCtx.Done()
		log.Info("Shutdown signal received. Stopping all running tasks...")
		runner.Stop()

		log.Info("github.com/anton7r/asynk exited gracefully. All running tasks stopped.")
	}
}

func acquireConfiguredInstanceGuard(
	ctx context.Context,
	configuration *config.Config,
	runOnce bool,
	acquire func(instance.Options) (*instance.Guard, error),
) (*instance.Guard, error) {
	if runOnce {
		return nil, nil
	}
	if acquire == nil {
		acquire = instance.Acquire
	}

	return acquire(instance.Options{
		Context:        ctx,
		ConfigDir:      configuration.ConfigDir,
		Policy:         instance.Policy(configuration.EffectiveInstancePolicy()),
		ReplaceTimeout: configuration.EffectiveInstanceReplaceTimeout(),
	})
}

func signalContextForMode(runOnce bool) (context.Context, func()) {
	if runOnce {
		return context.Background(), func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	stopped := make(chan struct{})
	var stopOnce sync.Once

	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-signals:
			cancel()
		case <-stopped:
		}
	}()

	return ctx, func() {
		stopOnce.Do(func() {
			signal.Stop(signals)
			select {
			case <-signals:
				cancel()
			default:
			}
			close(stopped)
		})
	}
}

func cancelWhenDone(ctx, stopCtx context.Context, cancel context.CancelFunc) {
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-stopCtx.Done():
		}
	}()
}
