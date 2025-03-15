package main

import (
	"asynk/config"
	"asynk/watcher"
	"context"
	"fmt"
	"os/signal"
	"syscall"
)

func main() {
	fmt.Println("Asynk is starting...")

	configuration, err := config.LoadFromYAML()
	if err != nil {
		fmt.Printf("Error loading configuration: %v\n", err)
		return
	}

	// TODO: pass directoriesToWatch to watcher.NewWatcher()
	_ = watcher.MatchWatchableDirectories(configuration.Shared.Exclude, configuration.Tasks)
	w, err := watcher.NewWatcher()
	defer w.Close()
	if err != nil {
		fmt.Printf("Error starting watcher: %v\n", err)
		return
	}

	// Create a new application state
	runner := NewRunner(configuration)

	fmt.Println("Press Ctrl+C to stop Asynk.")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runner.Start()

	<-ctx.Done()
	fmt.Println("Interrupt signal received. Stopping running tasks...")
	runner.stopRunningTasks()

	fmt.Println("Asynk exited gracefully.")
}
