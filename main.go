package main

import (
	"asynk/config"
	"asynk/watcher"
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
)

func main() {
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

	// TODO: pass directoriesToWatch to watcher.NewWatcher()
	_ = watcher.MatchWatchableDirectories(configuration.Shared.Exclude, configuration.Tasks)
	w, err := watcher.NewWatcher(log)
	defer w.Close()
	if err != nil {
		log.Error("Error creating watcher", zap.Error(err))
		return
	}

	// Create a new application state
	runner := NewRunner(configuration, log)

	log.Info("Press Ctrl+C to stop Asynk.")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runner.Start()

	<-ctx.Done()
	log.Info("Interrupt signal received . Stopping all running tasks...")
	runner.stopRunningTasks()

	log.Info("Asynk exited gracefully. All running tasks stopped.")
}
