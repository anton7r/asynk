package main

import (
	"asynk/config"
	"bufio"
	"fmt"
	"os/exec"
	"strings"
)

type ScheduledTask struct {
	// Task configuration
	TaskConfiguration *config.TaskConfiguration
}

type RunningTask struct {
	// Task configuration
	TaskConfiguration *config.TaskConfiguration
}

type AppState struct {
	// Application configuration
	Config *config.Configuration
	// List of scheduled tasks
	ScheduledTasks []ScheduledTask
	RunningTasks   []RunningTask
}

func main() {
	fmt.Println("Asynk is starting...")

	configuration, err := config.LoadFromYAML()
	if err != nil {
		fmt.Printf("Error loading configuration: %v\n", err)
		return
	}

	// Create a new application state
	appState := &AppState{
		Config: configuration,
	}

	// Start all scheduled tasks concurrently
	startScheduledTasks(appState)
}

func startScheduledTasks(appState *AppState) {
	for _, scheduledTask := range appState.ScheduledTasks {
		if canStartTask(scheduledTask) {

			go func(scheduledTask ScheduledTask) {
				// Perform the task's command and dependencies if necessary
				performTask(scheduledTask)
			}(scheduledTask)
		}
	}
}

func isTaskInQueue(taskIdentifier string, scheduledTasks []ScheduledTask) bool {
	for _, scheduledTask := range scheduledTasks {
		if scheduledTask.TaskConfiguration.Identifier == taskIdentifier {
			return true
		}
	}
	return false
}

func canStartTask(scheduledTask ScheduledTask) bool {
	if isTaskInQueue(scheduledTask.TaskConfiguration.Identifier, scheduledTasks) {
		return false
	}

	for _, dependency := range scheduledTask.TaskConfiguration.Dependencies {
		if !isTaskInQueue(dependency, scheduledTasks) {
			return false
		}
	}

	return true
	// Additional checks for task dependencies can be added here, such as file existence or directory status.
	// For example:
	// if _, err := os.Stat(dependencyFilePath); os.IsNotExist(err) {
	//     return false
	// }
	// if err := filepath.Walk(dependencyDirectoryPath, func(path string, info os.FileInfo, err error) error {
	//     if err != nil {
	//         return err
	//     }
	//     if info.IsDir() {
	//         return nil
}

func performTask(scheduledTask ScheduledTask) {
	fmt.Printf("Starting task: %s\n", scheduledTask.TaskConfiguration.Identifier)

	// Split the command with its arguments
	commandParts := strings.Split(scheduledTask.TaskConfiguration.Run, " ")

	if len(commandParts) < 1 {
		fmt.Printf("Invalid command to run: %s\n", scheduledTask.TaskConfiguration.Run)
		return
	}

	var cmd *exec.Cmd

	if len(commandParts) > 1 {
		cmd = exec.Command(commandParts[0], commandParts[1:]...)
	} else {
		cmd = exec.Command(commandParts[0])
	}

	go func() {
		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			fmt.Printf("Error creating stdout pipe for task %s: %v\n", scheduledTask.TaskConfiguration.Identifier, err)
			return
		}

		err = cmd.Start()
		if err != nil {
			fmt.Printf("Error starting task %s: %v\n", scheduledTask.TaskConfiguration.Identifier, err)
		}

		// Create a scanner to read the output line by line
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[%s] %s\n", scheduledTask.TaskConfiguration.Identifier, line)
		}

		if err := scanner.Err(); err != nil {
			fmt.Printf("Error reading stdout for task %s: %v\n", scheduledTask.TaskConfiguration.Identifier, err)
		}

		err = cmd.Wait()
		if err != nil {
			fmt.Printf("Error waiting for task %s to complete: %v\n", scheduledTask.TaskConfiguration.Identifier, err)
		} else {
			fmt.Printf("Task %s completed successfully\n", scheduledTask.TaskConfiguration.Identifier)
		}
	}()

	fmt.Printf("Task %s completed successfully\n", scheduledTask.TaskConfiguration.Identifier)
}
