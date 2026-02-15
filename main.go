package main

import (
	"context"
	"flag"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/util"
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
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		go runner.Start()

		<-ctx.Done()
		log.Info("Interrupt signal received . Stopping all running tasks...")
		runner.Stop()

		log.Info("github.com/anton7r/asynk exited gracefully. All running tasks stopped.")
	}
}
