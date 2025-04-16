package main

import (
	"asynk/cmdwrap"
	"asynk/config"
	"asynk/files"
	"asynk/util"
	"asynk/watcher"
	"sync"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

type TaskCompletionCallback func(taskId string, errored bool)

type ScheduledTask struct {
	// Task configuration
	TaskConfiguration *config.TaskConfig
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

func (runner *Runner) scheduleTasksFromSchedulableMap(tasks map[string]*config.TaskConfig) {
	runner.ScheduledTaskMutex.Lock()

	for _, task := range tasks {
		runner.log.Debug("Scheduling task", zap.String("taskId", task.Identifier))
		scheduledTask := &ScheduledTask{TaskConfiguration: task}
		runner.ScheduledTasks[scheduledTask.TaskConfiguration.Identifier] = scheduledTask
	}

	runner.ScheduledTaskMutex.Unlock()
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

		files := runner.getMatchedFileChangesForTask(taskConfig, event.Files)
		if len(files) > 0 {
			schedulableTasks[taskId] = taskConfig
		}
	}
}

// shouldScheduleTaskForEvent checks if any of the changed files match the task's include patterns
func (runner *Runner) getMatchedFileChangesForTask(taskConfig *config.TaskConfig, files map[string]bool) []string {
	var matchedFiles []string

	for file := range files {
		runner.log.Debug("Checking if should propagate",
			zap.String("taskId", taskConfig.Identifier),
			zap.String("file", file),
		)

		if taskConfig.Exclude.AnyMatches(file) {
			runner.log.Debug("File excluded from task", zap.String("file", file))
			continue
		}

		if taskConfig.Include.AnyMatches(file) {
			runner.log.Debug("File matched task include pattern", zap.String("file", file))
			matchedFiles = append(matchedFiles, file)
		}
	}

	return matchedFiles
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
	runner.startCleanUp()
}

func (runner *Runner) startCleanUp() {
	for _, cleanUpTask := range runner.Config.CleanUpTasks {
		runner.log.Debug("Starting cleanup task", zap.String("cleanUpTaskId", cleanUpTask.Identifier))

		matchedFiles, err := GetMatchedFiles(cleanUpTask, runner.Config.Shared.Exclude)
		if err != nil {
			runner.log.Error("Error getting deletable files", zap.Error(err))
			continue
		}

		if len(matchedFiles) <= 1 {
			runner.log.Debug("No files to delete for cleanup task", zap.String("taskId", cleanUpTask.Identifier))
			continue
		}

		runner.log.Info("Files to delete for cleanup task",
			zap.String("taskId", cleanUpTask.Identifier),
			zap.Any("files", matchedFiles),
		)

		// Remove the newest file from the list of files to delete
		matchedFiles = files.RemoveNewestFile(matchedFiles)

		for _, file := range matchedFiles {
			runner.log.Info("Deleting file",
				zap.String("file", file.Path),
				zap.String("cleanUpTaskId", cleanUpTask.Identifier),
			)
			/*
				err := os.Remove(file.Path)
				if err != nil {
					runner.log.Error("Error deleting file",
						zap.String("file", file.Path),
						zap.String("cleanUpTaskId", cleanUpTask.Identifier),
						zap.Error(err),
					)
					continue
				}

				runner.log.Info("Deleted file",
					zap.String("file", file.Path),
					zap.String("cleanUpTaskId", cleanUpTask.Identifier),
				)*/
		}
	}
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

			taskType := scheduledTask.TaskConfiguration.Type
			runner.log.Info("Starting task",
				zap.String("taskId", taskId))

			if taskType == config.TaskType_Continuous && runner.isTaskRunning(taskId) {
				task := runner.getRunningTask(taskId)
				if task != nil {
					task.Restart()
				}
			} else {
				task := runner.startTaskAsync(scheduledTask)
				runner.onTaskStart(task)
			}
		}
	}

	runner.log.Debug("Started scheduled tasks concurrently.", zap.Int("count", len(runner.ScheduledTasks)))
}

func (runner *Runner) isTaskInQueue(taskIdentifier string) bool {
	return runner.ScheduledTasks[taskIdentifier] != nil
}

func (runner *Runner) isTaskRunning(taskIdentifier string) bool {
	for _, runningTask := range runner.RunningTasks {
		if runningTask.TaskId() == taskIdentifier {
			return true
		}
	}
	return false
}

func (runner *Runner) getRunningTask(taskIdentifier string) *RunningTask {
	for _, runningTask := range runner.RunningTasks {
		if runningTask.TaskId() == taskIdentifier {
			return runningTask
		}
	}
	return nil
}

func (runner *Runner) canStartTask(scheduledTask *ScheduledTask) bool {
	if scheduledTask == nil {
		runner.log.Debug("Hitting nil scheduled task in canStartTask(), possible bug in code.")
		return false
	}

	id := scheduledTask.TaskConfiguration.Identifier
	taskType := scheduledTask.TaskConfiguration.Type

	// Continuous tasks are always running, so we don't need to check for them
	// and they should be restarted if they are not running
	if taskType != config.TaskType_Continuous && runner.isTaskRunning(id) {
		return false
	}

	for _, dependency := range scheduledTask.TaskConfiguration.Dependencies {
		if runner.isTaskInQueue(dependency) || runner.isTaskRunning(dependency) {
			return false
		}
	}

	runner.log.Info("Task can be started",
		zap.String("taskId", id))
	return true
}

func (r *Runner) startTaskAsync(
	scheduledTask *ScheduledTask,
) *RunningTask {
	task := NewRunningTask(
		scheduledTask.TaskConfiguration,
		r.log,
		r.wrapLogger,
		r.env,
		r.onTaskFinished,
	)

	// Start the task in a new goroutine to avoid blocking the main thread
	go task.Start()
	return task
}
