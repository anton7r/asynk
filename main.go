package main

import (
	"asynk/config"
	"context"
	"flag"
	"fmt"
	"os/signal"
	"syscall"
)

func main() {
	// Parse command line flags
	runOnce := flag.Bool("once", false, "Run all tasks once and exit without watching for file changes")
	flag.Parse()

	fmt.Println("Asynk is starting...")

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
	runner := NewRunner(configuration, log, *runOnce)

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

		log.Info("Asynk exited gracefully. All running tasks stopped.")
	}
}
