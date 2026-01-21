package main

import (
	"os"
	"sync"
	"time"

	"github.com/anton7r/asynk/cmdwrap"
	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/files"
	"github.com/anton7r/asynk/task"
	"github.com/anton7r/asynk/util"
	"github.com/anton7r/asynk/watcher"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

type SchedulableTask struct {
	TaskConfiguration *config.TaskConfig
	// The
	ModificationTime time.Time
}

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
	RunningTasks       []*task.RunningTask
	RunningTaskMutex   sync.Mutex
	log                *zap.Logger
	wrapLogger         *cmdwrap.WrapLogger
	watch              *watcher.Watcher
	env                map[string]string
	runOnce            bool
	completionChan     chan struct{}
}

func NewRunner(configuration *config.Config, log *zap.Logger, runOnce bool) *Runner {
	runner := &Runner{
		Config:         configuration,
		ScheduledTasks: make(map[string]*ScheduledTask, 0),
		RunningTasks:   make([]*task.RunningTask, 0),
		log:            log,
		env:            make(map[string]string),
		runOnce:        runOnce,
		completionChan: make(chan struct{}),
	}

	taskIds := make([]string, 0, len(configuration.Tasks))
	for taskId := range configuration.Tasks {
		taskIds = append(taskIds, taskId)
	}

	// Only initialize watcher if not in single-run mode
	if !runOnce {
		watchableDirectories := watcher.MatchWatchableDirectories(log, configuration.Shared.Exclude, configuration.Tasks)
		var err error
		runner.watch, err = watcher.NewWatcher(log, watchableDirectories, runner.onFileChange)
		if err != nil {
			log.Error("Error creating watcher", zap.Error(err))
		}
	}

	runner.wrapLogger = cmdwrap.NewWrapLogger(taskIds)

	var err error
	runner.env, err = godotenv.Read(configuration.Shared.EnvFiles...)
	if err != nil {
		log.Error("Error loading environment variables", zap.Error(err))
	}

	return runner
}

func (runner *Runner) Start() {
	// Only start watcher if not in single-run mode
	if !runner.runOnce && runner.watch != nil {
		runner.watch.Start()
	}

	// Schedule all tasks and start them concurrently
	runner.scheduleAllTasks()

	// Start all scheduled tasks concurrently
	runner.startScheduledTasks()
}

func (runner *Runner) Stop() {
	if runner.watch != nil {
		runner.watch.Close()
	}

	runner.stopRunningTasks()
}

func (runner *Runner) stopRunningTasks() {
	for _, runningTask := range runner.RunningTasks {
		runningTask.StopGracefully()
	}
}

func (runner *Runner) filterRunningTasks(
	taskConfigs map[string]*SchedulableTask) map[string]*SchedulableTask {
	runner.RunningTaskMutex.Lock()
	defer runner.RunningTaskMutex.Unlock()

	runnerTasks := make(map[string]*SchedulableTask, 0)
	for taskId, s := range taskConfigs {
		task := runner.getRunningTask(taskId)
		if task != nil && task.StartTime().After(s.ModificationTime) {
			runner.log.Info("Skipping task as it's already running with up to date information", zap.String("taskId", taskId))
			continue
		}

		runnerTasks[taskId] = s
	}

	return runnerTasks
}

func (runner *Runner) scheduleTasksFromSchedulableMap(tasks map[string]*SchedulableTask) {
	runner.ScheduledTaskMutex.Lock()
	defer runner.ScheduledTaskMutex.Unlock()

	for _, s := range tasks {
		runner.log.Debug("Scheduling task", zap.String("taskId", s.TaskConfiguration.Identifier))
		scheduledTask := &ScheduledTask{TaskConfiguration: s.TaskConfiguration}
		runner.ScheduledTasks[scheduledTask.TaskConfiguration.Identifier] = scheduledTask
	}
}

func (runner *Runner) scheduleTasksFromMap(tasks map[string]*config.TaskConfig) {
	runner.ScheduledTaskMutex.Lock()
	defer runner.ScheduledTaskMutex.Unlock()

	for _, taskConfig := range tasks {
		runner.log.Debug("Scheduling task", zap.String("taskId", taskConfig.Identifier))
		scheduledTask := &ScheduledTask{TaskConfiguration: taskConfig}
		runner.ScheduledTasks[scheduledTask.TaskConfiguration.Identifier] = scheduledTask
	}
}

func (runner *Runner) scheduleAllTasks() {
	runner.scheduleTasksFromMap(runner.Config.Tasks)
}

// findTasksAffectedByFileChanges identifies which tasks should be scheduled based on file changes
func (runner *Runner) findTasksAffectedByFileChanges(
	events map[string]watcher.AggregatedEvent,
) map[string]*SchedulableTask {
	schedulableTasks := make(map[string]*SchedulableTask, 0)

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
func (runner *Runner) processEventTasks(event watcher.AggregatedEvent, schedulableTasks map[string]*SchedulableTask) {
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

		files, time := runner.getMatchedFileChangesForTask(taskConfig, event.Files)
		if len(files) > 0 {
			schedulableTasks[taskId] = &SchedulableTask{taskConfig, time}
		}
	}
}

// shouldScheduleTaskForEvent checks if any of the changed files match the task's include patterns
func (runner *Runner) getMatchedFileChangesForTask(
	taskConfig *config.TaskConfig,
	files map[string]*watcher.UpdatedFile,
) ([]string, time.Time) {
	var matchedFiles []string
	var latestTime time.Time

	for file, updatedEvent := range files {
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

			if updatedEvent.ModifiedTime.After(latestTime) {
				latestTime = updatedEvent.ModifiedTime
			}
		}
	}

	return matchedFiles, latestTime
}

func (runner *Runner) onFileChange(events map[string]watcher.AggregatedEvent) {
	// Find tasks affected by file changes
	// TODO: We could optimize this by passing range funcs
	schedulableTasks := runner.findTasksAffectedByFileChanges(events)
	schedulableTasks = runner.filterRunningTasks(schedulableTasks)

	if len(schedulableTasks) == 0 {
		runner.log.Info("No tasks to schedule based on file changes",
			zap.Any("files", events),
			zap.Int("count", len(schedulableTasks)),
		)
		return
	}

	runner.log.Info(
		"Propagated file change event handled, scheduling tasks",
		zap.Int("count", len(schedulableTasks)),
		zap.Strings("tasks", util.CollectMapKeys(schedulableTasks)),
		zap.Any("files", events),
	)

	// Schedule and start the affected tasks
	runner.scheduleTasksFromSchedulableMap(schedulableTasks)
	runner.startScheduledTasks()
}

func (runner *Runner) onTaskStart(runningTask *task.RunningTask) {
	runner.RunningTaskMutex.Lock()
	runner.RunningTasks = append(runner.RunningTasks, runningTask)
	runner.RunningTaskMutex.Unlock()
}

func (runner *Runner) onTaskFinished(taskIdentifier string, errored bool) {
	runner.RunningTaskMutex.Lock()
	runner.RunningTasks = task.RemoveRunningTask(runner.RunningTasks, taskIdentifier)
	runningCount := len(runner.RunningTasks)
	runner.RunningTaskMutex.Unlock()

	if errored {
		// In single-run mode, check if all tasks are done
		if runner.runOnce {
			runner.checkCompletion()
		}
		return
	}

	// Try to start tasks again if there are any scheduled tasks that can be started
	runner.startScheduledTasks()
	runner.startCleanUp()

	// In single-run mode, check if all tasks are done
	if runner.runOnce {
		runner.ScheduledTaskMutex.Lock()
		scheduledCount := len(runner.ScheduledTasks)
		runner.ScheduledTaskMutex.Unlock()

		if runningCount == 0 && scheduledCount == 0 {
			runner.log.Info("All tasks completed in single-run mode")
			select {
			case runner.completionChan <- struct{}{}:
			default:
			}
		}
	}
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

		runner.log.Debug("Files to delete for cleanup task",
			zap.String("taskId", cleanUpTask.Identifier),
			zap.Any("files", matchedFiles),
		)

		// Remove the newest file from the list of files to delete
		matchedFiles = files.RemoveNewestFile(matchedFiles)

		for _, file := range matchedFiles {
			runner.log.Debug("Deleting file",
				zap.String("file", file.Path),
				zap.String("cleanUpTaskId", cleanUpTask.Identifier),
			)

			err := os.Remove(file.Path)
			if err != nil {
				runner.log.Info("Could not delete file",
					zap.String("file", file.Path),
					zap.String("cleanUpTaskId", cleanUpTask.Identifier),
					zap.Error(err),
				)
				continue
			}

			runner.log.Debug("Deleted file",
				zap.String("file", file.Path),
				zap.String("cleanUpTaskId", cleanUpTask.Identifier),
			)
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

			if taskType == config.TaskType_Continuous && runner.isTaskRunning(taskId) {
				// Restart the running continuous task
				runner.RunningTaskMutex.Lock()
				runningTask := runner.getRunningTask(taskId)
				runner.RunningTaskMutex.Unlock()
				if runningTask != nil {
					runningTask.StopGracefully()
					// Wait for the task to exit before starting a new one
					runner.log.Info("Waiting for running task to finish before starting a new one",
						zap.String("taskId", taskId))
					runningTask.Wait()
					runner.log.Info("Running task finished, starting new instance")
					runner.RunningTaskMutex.Lock()
					runner.RunningTasks = task.RemoveRunningTask(runner.RunningTasks, taskId)
					runner.RunningTaskMutex.Unlock()
				}
				// Start a new instance
				newTask := runner.startTaskAsync(scheduledTask)
				if newTask != nil {
					runner.onTaskStart(newTask)
				}
			} else {
				newTask := runner.startTaskAsync(scheduledTask)
				if newTask != nil {
					runner.onTaskStart(newTask)
				}
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

func (runner *Runner) getRunningTask(taskIdentifier string) *task.RunningTask {
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
) *task.RunningTask {
	runningTask := task.NewRunningTask(
		scheduledTask.TaskConfiguration,
		r.log,
		r.wrapLogger,
		r.env,
		r.onTaskFinished,
	)

	// NewRunningTask can return nil if no command is found for the task
	if runningTask == nil {
		r.log.Warn("Failed to create running task, no command found",
			zap.String("taskId", scheduledTask.TaskConfiguration.Identifier))
		// Notify that the task has finished (with error) to clean up scheduling state
		r.onTaskFinished(scheduledTask.TaskConfiguration.Identifier, true)
		return nil
	}

	// Start the task in a new goroutine to avoid blocking the main thread
	go runningTask.Start()
	return runningTask
}

// WaitForCompletion blocks until all tasks have completed in single-run mode
func (runner *Runner) WaitForCompletion() {
	<-runner.completionChan
}

// checkCompletion checks if all tasks are done and signals completion if in single-run mode
func (runner *Runner) checkCompletion() {
	runner.RunningTaskMutex.Lock()
	runningCount := len(runner.RunningTasks)
	runner.RunningTaskMutex.Unlock()

	runner.ScheduledTaskMutex.Lock()
	scheduledCount := len(runner.ScheduledTasks)
	runner.ScheduledTaskMutex.Unlock()

	if runningCount == 0 && scheduledCount == 0 {
		runner.log.Info("All tasks completed in single-run mode")
		select {
		case runner.completionChan <- struct{}{}:
		default:
		}
	}
}
