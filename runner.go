package main

import (
	"asynk/cmdwrap"
	"asynk/config"
	"asynk/util"
	envUtil "asynk/util/env"
	"asynk/watcher"
	"context"
	"sync"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
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
	log                *zap.Logger
	wrapLogger         *cmdwrap.WrapLogger
	watch              *watcher.Watcher
	env                map[string]string
}

func NewRunner(configuration *config.Config, log *zap.Logger) *Runner {
	runner := &Runner{
		Config:         configuration,
		ScheduledTasks: make(map[string]*ScheduledTask, 0),
		RunningTasks:   make([]*RunningTask, 0),
		log:            log,
		env:            make(map[string]string),
	}

	taskIds := make([]string, 0, len(configuration.Tasks))
	for taskId := range configuration.Tasks {
		taskIds = append(taskIds, taskId)
	}

	watchableDirectories := watcher.MatchWatchableDirectories(log, configuration.Shared.Exclude, configuration.Tasks)
	var err error
	runner.watch, err = watcher.NewWatcher(log, watchableDirectories, runner.onFileChange)
	if err != nil {
		log.Error("Error creating watcher", zap.Error(err))
	}

	runner.wrapLogger = cmdwrap.NewWrapLogger(taskIds)

	runner.env, err = godotenv.Read(configuration.Shared.EnvFiles...)
	if err != nil {
		log.Error("Error loading environment variables", zap.Error(err))
	}

	return runner
}

func (runner *Runner) Start() {
	runner.watch.Start()

	// Schedule all tasks and start them concurrently
	runner.scheduleAllTasks()

	// Start all scheduled tasks concurrently
	runner.startScheduledTasks()
}

func (runner *Runner) Stop() {
	runner.watch.Close()

	runner.stopRunningTasks()
}

func (runner *Runner) stopRunningTasks() {
	for _, runningTask := range runner.RunningTasks {
		runningTask.StopGracefully()
	}
}

func (runner *Runner) scheduleTasksFromMap(tasks map[string]*config.TaskConfig) {
	runner.ScheduledTaskMutex.Lock()

	for _, taskConfig := range tasks {
		runner.log.Debug("Scheduling task", zap.String("taskId", taskConfig.Identifier))
		scheduledTask := &ScheduledTask{TaskConfiguration: taskConfig}
		runner.ScheduledTasks[scheduledTask.TaskConfiguration.Identifier] = scheduledTask

	}

	runner.ScheduledTaskMutex.Unlock()
}

func (runner *Runner) scheduleAllTasks() {
	runner.scheduleTasksFromMap(runner.Config.Tasks)
}

// findTasksAffectedByFileChanges identifies which tasks should be scheduled based on file changes
func (runner *Runner) findTasksAffectedByFileChanges(
	events map[string]watcher.AggregatedEvent,
) map[string]*config.TaskConfig {
	schedulableTasks := make(map[string]*config.TaskConfig, 0)

	for _, event := range events {
		if len(schedulableTasks) == len(runner.Config.Tasks) {
			// No more tasks to schedule anymore
			runner.log.Debug("No more tasks to schedule, propagation stopped")
			break
		}

		runner.processEventTasks(event, schedulableTasks)
	}

	return schedulableTasks
}

// processEventTasks processes tasks affected by a single event
func (runner *Runner) processEventTasks(event watcher.AggregatedEvent, schedulableTasks map[string]*config.TaskConfig) {
	for taskId := range event.Tasks {
		runner.log.Debug("Checking if task should be scheduled", zap.String("taskId", taskId))

		if _, exists := schedulableTasks[taskId]; exists {
			runner.log.Debug("Skipping task as it's already scheduled", zap.String("taskId", taskId))
			continue
		}

		taskConfig, exists := runner.Config.Tasks[taskId]
		if !exists {
			runner.log.Warn("Task not found in configuration", zap.String("taskId", taskId))
			continue
		}

		if runner.shouldScheduleTaskForEvent(taskConfig, event.Files) {
			schedulableTasks[taskId] = taskConfig
		}
	}
}

// shouldScheduleTaskForEvent checks if any of the changed files match the task's include patterns
func (runner *Runner) shouldScheduleTaskForEvent(taskConfig *config.TaskConfig, files map[string]bool) bool {
	for file := range files {
		runner.log.Debug("Checking if should propagate",
			zap.String("taskId", taskConfig.Identifier),
			zap.String("file", file),
		)
		if taskConfig.Include.AnyMatches(file) {
			return true
		}
	}
	return false
}

func (runner *Runner) onFileChange(events map[string]watcher.AggregatedEvent) {
	// Maybe unnecessary
	//runner.log.Info("Handling Propagated file change event")

	// Find tasks affected by file changes
	schedulableTasks := runner.findTasksAffectedByFileChanges(events)

	runner.log.Info(
		"Propagated file change event handled, scheduling tasks",
		zap.Int("count", len(schedulableTasks)),
		zap.Strings("tasks", util.CollectMapKeys(schedulableTasks)),
	)

	// Schedule and start the affected tasks
	runner.scheduleTasksFromMap(schedulableTasks)
	runner.startScheduledTasks()
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
		runner.log.Debug("No scheduled tasks to start.")
		return
	}

	runner.log.Debug("Starting scheduled tasks concurrently...", zap.Int("count", len(runner.ScheduledTasks)))

	for taskId, scheduledTask := range runner.ScheduledTasks {
		if runner.canStartTask(scheduledTask) {
			// Remove from scheduled tasks to prevent duplicate starts
			delete(runner.ScheduledTasks, taskId)

			task := runner.startTaskAsync(scheduledTask)
			runner.onTaskStart(task)
		}
	}

	runner.log.Debug("Started scheduled tasks concurrently.", zap.Int("count", len(runner.ScheduledTasks)))
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
		runner.log.Debug("Hitting nil scheduled task in canStartTask(), possible bug in code.")
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
	if runningTask == nil {
		return
	}

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

func (runner *Runner) getCommands(
	taskConfig *config.TaskConfig,
) []string {
	taskId := taskConfig.Identifier

	var run []string

	if util.IsWindows() && !util.Empty(taskConfig.RunWindows) {
		run = taskConfig.RunWindows
	} else if util.IsLinux() && !util.Empty(taskConfig.RunLinux) {
		run = taskConfig.RunLinux
	} else if util.IsMac() && !util.Empty(taskConfig.RunMac) {
		run = taskConfig.RunMac
	} else if !util.Empty(taskConfig.Run) {
		run = taskConfig.Run
	} else {
		runner.log.Debug("No command found for task", zap.String("taskId", taskId))
		return nil
	}

	return run
}

func (runner *Runner) startTaskAsync(
	scheduledTask *ScheduledTask,
) *RunningTask {
	taskId := scheduledTask.TaskConfiguration.Identifier

	run := runner.getCommands(scheduledTask.TaskConfiguration)

	runner.log.Debug("Starting task", zap.String("taskId", taskId))
	if util.Empty(run) {
		runner.log.Debug("No command found for task", zap.String("taskId", taskId))
		return nil
	}

	env := envUtil.InterpolateEnvVariablesList(scheduledTask.TaskConfiguration.Env, runner.env)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	// This operation could as well be done later on when executing the command
	cmds := cmdwrap.ParseAllCommands(run, taskId, runner.log, runner.env)

	runningTask := &RunningTask{
		TaskConfiguration: scheduledTask.TaskConfiguration,
		ctx:               ctx,
		cancel:            cancel,
	}

	// Start the task in a new goroutine to avoid blocking the main thread
	go func() {
		for i, cmd := range cmds {
			err := cmd.Run(ctx, env, runner.wrapLogger)
			if err != nil {
				runner.log.Error("Error executing command",
					zap.String("taskId", taskId),
					zap.Int("commandIndex", i),
					zap.Error(err),
				)

				runner.onTaskFinished(taskId, true)
				return
			}

		}

		runner.log.Debug("Task completed successfully", zap.String("taskId", taskId))
		runner.onTaskFinished(taskId, false)
	}()

	return runningTask
}
