package main

import (
	"asynk/config"
	"asynk/task/cmdwrap"
	"context"
	"fmt"
	"os/signal"
	"sync"
	"syscall"
)

type TaskCompletionCallback func(taskId string, errored bool)

type ScheduledTask struct {
	// Task configuration
	TaskConfiguration *config.TaskConfig
}

type RunningTask struct {
	// Task configuration
	TaskConfiguration *config.TaskConfig
	// Cancellable context
	ctx    context.Context
	cancel context.CancelFunc
}

type ASYNK struct {
	// Application configuration
	Config *config.Config
	// List of scheduled tasks
	ScheduledTasks     map[string]*ScheduledTask
	ScheduledTaskMutex sync.Mutex
	RunningTasks       []*RunningTask
	RunningTaskMutex   sync.Mutex
}

/*
	Because of the limitations posed by the fsnotify package in Go,
	we cannot watch for changes in the file system using regex with
	a very performance oriented way.

	basically this would have to be implemented in a way that adds
	listeners for directories that have matching file change patterns.

	We could also implement a custom file change detection mechanism,
	that would utilize sha1 checksums or other hashing techniques.

*/

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
		ScheduledTasks: make(map[string]*ScheduledTask, 0),
		RunningTasks:   make([]*RunningTask, 0),
	}

	fmt.Println("Press Ctrl+C to stop Asynk.")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		// Schedule all tasks and start them concurrently
		asynk.scheduleAllTasks()

		// Start all scheduled tasks concurrently
		asynk.startScheduledTasks()

	}()

	<-ctx.Done()
	fmt.Println("Interrupt signal received. Stopping running tasks...")
	asynk.stopRunningTasks()

	fmt.Println("Asynk exited gracefully.")
}

func (asynk *ASYNK) stopRunningTasks() {
	for _, runningTask := range asynk.RunningTasks {
		runningTask.StopGracefully()
	}
}

func (asynk *ASYNK) scheduleAllTasks() {
	for _, taskConfig := range asynk.Config.Tasks {
		fmt.Printf("Scheduling task: %s\n", taskConfig.Identifier)
		scheduledTask := &ScheduledTask{TaskConfiguration: taskConfig}
		asynk.ScheduledTasks[scheduledTask.TaskConfiguration.Identifier] = scheduledTask
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

	if errored {
		return
	}

	// Try to start tasks again if there are any scheduled tasks that can be started
	asynk.startScheduledTasks()
}

// Starts the scheduled tasks concurrently if they can be started
// This will be called from multiple goroutines, so we need to lock the runningTasks
// and scheduled task slice to prevent data races
func (asynk *ASYNK) startScheduledTasks() {
	asynk.ScheduledTaskMutex.Lock()
	defer asynk.ScheduledTaskMutex.Unlock()

	if len(asynk.ScheduledTasks) == 0 {
		fmt.Println("[Debug] No scheduled tasks to start.")
		return
	}

	fmt.Printf("Starting %d scheduled tasks...\n", len(asynk.ScheduledTasks))

	for taskId, scheduledTask := range asynk.ScheduledTasks {
		if asynk.canStartTask(scheduledTask) {
			// Remove from scheduled tasks to prevent duplicate starts
			delete(asynk.ScheduledTasks, taskId)

			// Start the task in a new goroutine to avoid blocking the main thread
			task := startTaskAsync(scheduledTask, asynk.onTaskFinished)
			asynk.onTaskStart(task)
		}
	}

	fmt.Printf("Starting tasks completed. %d scheduled tasks remain.\n", len(asynk.ScheduledTasks))
}

func (asynk *ASYNK) isTaskInQueue(taskIdentifier string) bool {
	return asynk.ScheduledTasks[taskIdentifier] != nil
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
	if scheduledTask == nil {
		fmt.Println("[Debug] Hitting nil scheduled task in canStartTask(), possible bug in code.")
		return false
	}

	id := scheduledTask.TaskConfiguration.Identifier
	if asynk.isTaskRunning(id) {
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
	taskCompletionCallback TaskCompletionCallback,
) *RunningTask {
	taskId := scheduledTask.TaskConfiguration.Identifier

	fmt.Printf("Starting task: %s\n", taskId)

	if len(scheduledTask.TaskConfiguration.Run) == 0 {
		fmt.Printf("No command found for task %s\n", taskId)
		return nil
	}

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	// This operation could as well be done later on when executing the command
	cmds := cmdwrap.ParseAllCommands(scheduledTask.TaskConfiguration.Run, taskId)

	runningTask := &RunningTask{
		TaskConfiguration: scheduledTask.TaskConfiguration,
		ctx:               ctx,
		cancel:            cancel,
	}

	go func() {
		for i, cmd := range cmds {
			err := cmd.Run(ctx)
			if err != nil {
				fmt.Printf("Error executing command %d of task %s: %v\n", i+1, taskId, err)
				taskCompletionCallback(taskId, true)
				return
			}

		}

		fmt.Printf("Task %s completed successfully\n", taskId)
		taskCompletionCallback(taskId, false)
	}()

	return runningTask
}

func (runningTask *RunningTask) StopGracefully() {
	runningTask.cancel()
}
