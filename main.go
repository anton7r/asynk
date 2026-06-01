package main

import (
	"context"
	"flag"
	"fmt"
	"os/signal"
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

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	guard, err := acquireConfiguredInstanceGuard(ctx, configuration, *runOnce, instance.Acquire)
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

	if !*runOnce {
		guard.StartShutdownMonitor(ctx, cancel)
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
		log.Info("Press Ctrl+C to stop Asynk.")

		if ctx.Err() == nil {
			go runner.Start()
		}

		<-ctx.Done()
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
