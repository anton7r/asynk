package main

import (
	"asynk/config"
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type ScheduledTask struct {
	// Task configuration
	TaskConfiguration *config.TaskConfiguration
}

type RunningTask struct {
	// Task configuration
	TaskConfiguration *config.TaskConfiguration
	// Executed command
	cmd *exec.Cmd
}

type ASYNK struct {
	// Application configuration
	Config *config.Configuration
	// List of scheduled tasks
	ScheduledTasks     []*ScheduledTask
	ScheduledTaskMutex sync.Mutex
	RunningTasks       []*RunningTask
	RunningTaskMutex   sync.Mutex
}

func main() {
	fmt.Println("Asynk is starting...")

	configuration, err := config.LoadFromYAML()
	if err != nil {
		fmt.Printf("Error loading configuration: %v\n", err)
		return
	}

	// Create a new application state
	asynk := &ASYNK{
		Config:         configuration,
		ScheduledTasks: make([]*ScheduledTask, 0),
		RunningTasks:   make([]*RunningTask, 0),
	}

	// Schedule all tasks and start them concurrently
	asynk.scheduleAllTasks()

	// Start all scheduled tasks concurrently
	asynk.startScheduledTasks()

	// Read interrupt signal
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Press Ctrl+C to stop Asynk: ")
	interruptSignal, _ := reader.ReadString('\n')

	// Stop all running tasks
	asynk.stopRunningTasks()

	fmt.Printf("\nAsynk stopped: %s", strings.TrimSpace(interruptSignal))

}

func (asynk *ASYNK) stopRunningTasks() {
	for _, runningTask := range asynk.RunningTasks {
		runningTask.StopGracefully()
	}
}

func (asynk *ASYNK) scheduleAllTasks() {
	for _, taskConfig := range asynk.Config.Tasks {
		scheduledTask := &ScheduledTask{TaskConfiguration: taskConfig}
		asynk.ScheduledTasks = append(asynk.ScheduledTasks, scheduledTask)
	}
}

func (asynk *ASYNK) onTaskStart(runningTask *RunningTask) {
	asynk.RunningTaskMutex.Lock()
	asynk.RunningTasks = append(asynk.RunningTasks, runningTask)
	asynk.RunningTaskMutex.Unlock()
}

func (asynk *ASYNK) onTaskFinished(taskIdentifier string, errored bool) {
	asynk.RunningTaskMutex.Lock()
	asynk.RunningTasks = removeRunningTask(asynk.RunningTasks, taskIdentifier)
	asynk.RunningTaskMutex.Unlock()

	// Try to start tasks again if there are any scheduled tasks that can be started
	asynk.startScheduledTasks()
}

// Starts the scheduled tasks concurrently if they can be started
// This will be called from multiple goroutines, so we need to lock the runningTasks and scheduled task slice to prevent data races
func (asynk *ASYNK) startScheduledTasks() {
	asynk.ScheduledTaskMutex.Lock()
	for _, scheduledTask := range asynk.ScheduledTasks {
		if asynk.canStartTask(scheduledTask) {
			task := startTaskAsync(scheduledTask, asynk.onTaskFinished)
			asynk.onTaskStart(task)
		}
	}
	asynk.ScheduledTaskMutex.Unlock()
}

func (asynk *ASYNK) isTaskInQueue(taskIdentifier string) bool {
	for _, scheduledTask := range asynk.ScheduledTasks {
		if scheduledTask.TaskConfiguration.Identifier == taskIdentifier {
			return true
		}
	}
	return false
}

func (asynk *ASYNK) isTaskRunning(taskIdentifier string) bool {
	for _, runningTask := range asynk.RunningTasks {
		if runningTask.TaskConfiguration.Identifier == taskIdentifier {
			return true
		}
	}
	return false
}

func (asynk *ASYNK) canStartTask(scheduledTask *ScheduledTask) bool {
	id := scheduledTask.TaskConfiguration.Identifier
	if asynk.isTaskInQueue(id) || asynk.isTaskRunning(id) {
		return false
	}

	for _, dependency := range scheduledTask.TaskConfiguration.Dependencies {
		if asynk.isTaskInQueue(dependency) || asynk.isTaskRunning(dependency) {
			return false
		}
	}

	return true
}

// Note this only removes the array occurrence
func removeRunningTask(runningTasks []*RunningTask, taskIdentifier string) []*RunningTask {
	for i, task := range runningTasks {
		if task.TaskConfiguration.Identifier == taskIdentifier {
			// Remove the task by appending the slices before and after the task
			return append(runningTasks[:i], runningTasks[i+1:]...)
		}
	}
	return runningTasks
}

func startTaskAsync(
	scheduledTask *ScheduledTask,
	taskCompletionCallback func(taskIdentifier string, errored bool),
) *RunningTask {
	fmt.Printf("Starting task: %s\n", scheduledTask.TaskConfiguration.Identifier)

	// Split the command with its arguments
	commandParts := strings.Split(scheduledTask.TaskConfiguration.Run, " ")

	if len(commandParts) < 1 {
		fmt.Printf("Invalid command to run: %s\n", scheduledTask.TaskConfiguration.Run)
		return nil
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

		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			fmt.Printf("Error creating stderr pipe for task %s: %v\n", scheduledTask.TaskConfiguration.Identifier, err)
			return
		}

		err = cmd.Start()
		if err != nil {
			fmt.Printf("Error starting task %s: %v\n", scheduledTask.TaskConfiguration.Identifier, err)
			return
		}

		// Create a scanner to read the stdout line by line
		go func() {
			scanner := bufio.NewScanner(stdoutPipe)
			for scanner.Scan() {
				line := scanner.Text()
				fmt.Printf("[%s] %s\n", scheduledTask.TaskConfiguration.Identifier, line)
			}

			if err := scanner.Err(); err != nil {
				fmt.Printf("Error reading stdout for task %s: %v\n", scheduledTask.TaskConfiguration.Identifier, err)
			}
		}()

		// Create a scanner to read the stderr line by line
		go func() {
			scanner := bufio.NewScanner(stderrPipe)
			for scanner.Scan() {
				line := scanner.Text()
				fmt.Printf("[%s] [ERROR] %s\n", scheduledTask.TaskConfiguration.Identifier, line)
			}

			if err := scanner.Err(); err != nil {
				fmt.Printf("Error reading stderr for task %s: %v\n", scheduledTask.TaskConfiguration.Identifier, err)
			}
		}()

		err = cmd.Wait()
		if err != nil {
			fmt.Printf("Error waiting for task %s to complete: %v\n", scheduledTask.TaskConfiguration.Identifier, err)
			taskCompletionCallback(scheduledTask.TaskConfiguration.Identifier, true)
		} else {
			fmt.Printf("Task %s completed successfully\n", scheduledTask.TaskConfiguration.Identifier)
			taskCompletionCallback(scheduledTask.TaskConfiguration.Identifier, false)
		}

	}()

	return &RunningTask{
		TaskConfiguration: scheduledTask.TaskConfiguration,
		cmd:               cmd,
	}
}

func (runningTask *RunningTask) StopGracefully() {
	if runningTask.cmd != nil {
		runningTask.cmd.Cancel()
	}
}
