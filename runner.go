package main

import (
	"asynk/config"
	"asynk/task/cmdwrap"
	"context"
	"fmt"
	"sync"
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

type Runner struct {
	// Application configuration
	Config *config.Config
	// List of scheduled tasks
	ScheduledTasks     map[string]*ScheduledTask
	ScheduledTaskMutex sync.Mutex
	RunningTasks       []*RunningTask
	RunningTaskMutex   sync.Mutex
}

func NewRunner(configuration *config.Config) *Runner {
	return &Runner{
		Config:         configuration,
		ScheduledTasks: make(map[string]*ScheduledTask, 0),
		RunningTasks:   make([]*RunningTask, 0),
	}
}

func (runner *Runner) Start() {
	// Schedule all tasks and start them concurrently
	runner.scheduleAllTasks()

	// Start all scheduled tasks concurrently
	runner.startScheduledTasks()
}

func (runner *Runner) stopRunningTasks() {
	for _, runningTask := range runner.RunningTasks {
		runningTask.StopGracefully()
	}
}

func (runner *Runner) scheduleAllTasks() {
	for _, taskConfig := range runner.Config.Tasks {
		fmt.Printf("Scheduling task: %s\n", taskConfig.Identifier)
		scheduledTask := &ScheduledTask{TaskConfiguration: taskConfig}
		runner.ScheduledTasks[scheduledTask.TaskConfiguration.Identifier] = scheduledTask
	}
}

func (runner *Runner) onTaskStart(runningTask *RunningTask) {
	runner.RunningTaskMutex.Lock()
	runner.RunningTasks = append(runner.RunningTasks, runningTask)
	runner.RunningTaskMutex.Unlock()
}

func (runner *Runner) onTaskFinished(taskIdentifier string, errored bool) {
	runner.RunningTaskMutex.Lock()
	runner.RunningTasks = removeRunningTask(runner.RunningTasks, taskIdentifier)
	runner.RunningTaskMutex.Unlock()

	if errored {
		return
	}

	// Try to start tasks again if there are any scheduled tasks that can be started
	runner.startScheduledTasks()
}

// Starts the scheduled tasks concurrently if they can be started
// This will be called from multiple goroutines, so we need to lock the runningTasks
// and scheduled task slice to prevent data races
func (runner *Runner) startScheduledTasks() {
	runner.ScheduledTaskMutex.Lock()
	defer runner.ScheduledTaskMutex.Unlock()

	if len(runner.ScheduledTasks) == 0 {
		fmt.Println("[Debug] No scheduled tasks to start.")
		return
	}

	fmt.Printf("Starting %d scheduled tasks...\n", len(runner.ScheduledTasks))

	for taskId, scheduledTask := range runner.ScheduledTasks {
		if runner.canStartTask(scheduledTask) {
			// Remove from scheduled tasks to prevent duplicate starts
			delete(runner.ScheduledTasks, taskId)

			// Start the task in a new goroutine to avoid blocking the main thread
			task := startTaskAsync(scheduledTask, runner.onTaskFinished)
			runner.onTaskStart(task)
		}
	}

	fmt.Printf("Starting tasks completed. %d scheduled tasks remain.\n", len(runner.ScheduledTasks))
}

func (runner *Runner) isTaskInQueue(taskIdentifier string) bool {
	return runner.ScheduledTasks[taskIdentifier] != nil
}

func (runner *Runner) isTaskRunning(taskIdentifier string) bool {
	for _, runningTask := range runner.RunningTasks {
		if runningTask.TaskConfiguration.Identifier == taskIdentifier {
			return true
		}
	}
	return false
}

func (runner *Runner) canStartTask(scheduledTask *ScheduledTask) bool {
	if scheduledTask == nil {
		fmt.Println("[Debug] Hitting nil scheduled task in canStartTask(), possible bug in code.")
		return false
	}

	id := scheduledTask.TaskConfiguration.Identifier
	if runner.isTaskRunning(id) {
		return false
	}

	for _, dependency := range scheduledTask.TaskConfiguration.Dependencies {
		if runner.isTaskInQueue(dependency) || runner.isTaskRunning(dependency) {
			return false
		}
	}

	return true
}

func (runningTask *RunningTask) StopGracefully() {
	runningTask.cancel()
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
