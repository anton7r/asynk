package main

import (
	"sync"
	"time"

	"github.com/anton7r/asynk/cmdwrap"
	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/files"
	"github.com/anton7r/asynk/portmanager"
	asynkproxy "github.com/anton7r/asynk/proxy"
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
	// lastTaskStartTimes records when startTaskAsync was called for each task.
	// This is set synchronously in startScheduledTasks BEFORE the goroutine
	// launches, closing the race window where filterRunningTasks could miss
	// a task that was just started but not yet added to RunningTasks.
	// Protected by RunningTaskMutex.
	lastTaskStartTimes map[string]time.Time
	log                *zap.Logger
	wrapLogger         *cmdwrap.WrapLogger
	watch              *watcher.Watcher
	env                map[string]string
	portManager        *portmanager.Manager
	proxyManager       *asynkproxy.Manager
	serviceExports     map[string]map[string]string
	serviceExportMutex sync.Mutex
	deps               RunnerDeps
	runOnce            bool
	completionChan     chan struct{}
	rebuildSuppression *rebuildSuppressionState
}

// RunnerDeps holds all injectable infrastructure dependencies for the Runner.
// Use DefaultRunnerDeps() for production; provide custom implementations for testing.
type RunnerDeps struct {
	Platform     *util.Platform
	FS           util.FileSystem
	CmdFactory   cmdwrap.CommandFactory
	PortManager  *portmanager.Manager
	ProxyManager *asynkproxy.Manager
}

// DefaultRunnerDeps returns production infrastructure dependencies.
func DefaultRunnerDeps() RunnerDeps {
	return RunnerDeps{
		Platform:     util.NewPlatform(),
		FS:           util.NewOSFileSystem(),
		CmdFactory:   cmdwrap.NewDefaultCommandFactory(cmdwrap.DefaultProcessManager()),
		PortManager:  portmanager.NewManager(nil),
		ProxyManager: asynkproxy.NewManager(),
	}
}

func NewRunner(configuration *config.Config, log *zap.Logger, runOnce bool, platform *util.Platform) *Runner {
	deps := RunnerDeps{
		Platform:     platform,
		FS:           util.NewOSFileSystem(),
		CmdFactory:   cmdwrap.NewDefaultCommandFactory(cmdwrap.DefaultProcessManager()),
		PortManager:  portmanager.NewManager(nil),
		ProxyManager: asynkproxy.NewManager(),
	}
	return NewRunnerWithDeps(configuration, log, runOnce, deps)
}

func NewRunnerWithDeps(configuration *config.Config, log *zap.Logger, runOnce bool, deps RunnerDeps) *Runner {
	if deps.Platform == nil {
		deps.Platform = util.NewPlatform()
	}
	if deps.FS == nil {
		deps.FS = util.NewOSFileSystem()
	}
	if deps.CmdFactory == nil {
		deps.CmdFactory = cmdwrap.NewDefaultCommandFactory(cmdwrap.DefaultProcessManager())
	}
	if deps.PortManager == nil {
		deps.PortManager = portmanager.NewManager(nil)
	}
	if deps.ProxyManager == nil {
		deps.ProxyManager = asynkproxy.NewManager()
	}

	runner := &Runner{
		Config:             configuration,
		ScheduledTasks:     make(map[string]*ScheduledTask, 0),
		RunningTasks:       make([]*task.RunningTask, 0),
		lastTaskStartTimes: make(map[string]time.Time),
		log:                log,
		env:                make(map[string]string),
		portManager:        deps.PortManager,
		proxyManager:       deps.ProxyManager,
		serviceExports:     make(map[string]map[string]string),
		deps:               deps,
		runOnce:            runOnce,
		completionChan:     make(chan struct{}, 1),
		rebuildSuppression: newRebuildSuppressionState(),
	}

	taskIds := make([]string, 0, len(configuration.Tasks))
	for taskId := range configuration.Tasks {
		taskIds = append(taskIds, taskId)
	}

	// Only initialize watcher if not in single-run mode
	if !runOnce {
		watchableDirectories := watcher.MatchWatchableDirectoriesWithFS(log, configuration.Shared.Exclude, configuration.Tasks, deps.FS)
		var err error
		defaultFSDebounce := config.DefaultFSDebounce
		if configuration.Shared.FSDebounce.IsSet() {
			defaultFSDebounce = configuration.Shared.FSDebounce.Duration
		}
		runner.watch, err = watcher.NewWatcherWithDepsAndOptions(
			log,
			watchableDirectories,
			runner.onFileChange,
			deps.FS,
			nil,
			watcher.WatcherOptions{
				DefaultFSDebounce:       defaultFSDebounce,
				DefaultFSDebounceSet:    true,
				TaskFSDebounces:         configuration.TaskFSDebounces(),
				RebuildSuppressionTasks: configuration.RebuildSuppressionTasks(),
			},
		)
		if err != nil {
			log.Error("Error creating watcher", zap.Error(err))
		}

		runner.initializeRebuildSuppression()
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
	runner.closeManagedProxies()
}

func (runner *Runner) stopRunningTasks() {
	// First, signal all running tasks to stop
	for _, runningTask := range runner.RunningTasks {
		runningTask.StopGracefully()
	}

	// Then, wait for all tasks to actually exit.
	// This ensures processes are fully reaped before we return.
	for _, runningTask := range runner.RunningTasks {
		runningTask.Wait()
	}
}

func (runner *Runner) filterRunningTasks(
	taskConfigs map[string]*SchedulableTask) map[string]*SchedulableTask {
	runner.RunningTaskMutex.Lock()
	defer runner.RunningTaskMutex.Unlock()

	runnerTasks := make(map[string]*SchedulableTask, 0)
	for taskId, s := range taskConfigs {
		// Check if the task is already in RunningTasks with a newer start time.
		task := runner.getRunningTask(taskId)
		if task != nil && task.StartTime().After(s.ModificationTime) {
			runner.log.Info("Skipping task as it's already running with up to date information", zap.String("taskId", taskId))
			continue
		}

		// Also check lastTaskStartTimes — this covers the race window where
		// startScheduledTasks has called startTaskAsync but the RunningTask
		// hasn't been added to RunningTasks yet (goroutine hasn't started).
		if startTime, ok := runner.lastTaskStartTimes[taskId]; ok && startTime.After(s.ModificationTime) {
			runner.log.Info("Skipping task as it was recently started (pending goroutine launch)", zap.String("taskId", taskId))
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
	schedulableTasks = runner.filterUnchangedRebuildInputs(schedulableTasks)
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
	runner.recordRebuildSuppressionTaskResult(taskIdentifier, errored)

	runner.RunningTaskMutex.Lock()
	runner.RunningTasks = task.RemoveRunningTask(runner.RunningTasks, taskIdentifier)
	runningCount := len(runner.RunningTasks)
	runner.RunningTaskMutex.Unlock()

	runner.releaseFinishedTaskPorts(taskIdentifier, errored)

	if errored {
		runner.removeScheduledConsumersForFailedProvider(taskIdentifier)

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

func (runner *Runner) removeScheduledConsumersForFailedProvider(providerTaskID string) {
	runner.ScheduledTaskMutex.Lock()
	defer runner.ScheduledTaskMutex.Unlock()

	runner.removeScheduledConsumersForFailedProviderLocked(providerTaskID)
}

func (runner *Runner) removeScheduledConsumersForFailedProviderLocked(providerTaskID string) {
	consumerTaskIDs := make([]string, 0)
	for taskID, scheduledTask := range runner.ScheduledTasks {
		if scheduledTask == nil || !taskConsumesProvider(scheduledTask.TaskConfiguration, providerTaskID) {
			continue
		}
		consumerTaskIDs = append(consumerTaskIDs, taskID)
	}

	for _, taskID := range consumerTaskIDs {
		delete(runner.ScheduledTasks, taskID)
		runner.log.Warn("Removing queued consumer because provider failed",
			zap.String("taskId", taskID),
			zap.String("providerTaskId", providerTaskID),
		)
		runner.removeScheduledConsumersForFailedProviderLocked(taskID)
	}
}

func taskConsumesProvider(taskConfig *config.TaskConfig, providerTaskID string) bool {
	if taskConfig == nil {
		return false
	}

	for _, consume := range taskConfig.Consumes {
		if consume.Task == providerTaskID {
			return true
		}
	}
	return false
}

func (runner *Runner) startCleanUp() {
	for _, cleanUpTask := range runner.Config.CleanUpTasks {
		runner.log.Debug("Starting cleanup task", zap.String("cleanUpTaskId", cleanUpTask.Identifier))

		matchedFiles, err := GetMatchedFilesWithFS(cleanUpTask, runner.Config.Shared.Exclude, runner.deps.FS)
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

			err := runner.deps.FS.Remove(file.Path)
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

// Starts scheduled tasks as soon as dependencies and consumed exports are ready.
// This can be called from multiple goroutines, so task queues and running task
// state are guarded separately.
func (runner *Runner) startScheduledTasks() {
	for {
		runner.ScheduledTaskMutex.Lock()

		if len(runner.ScheduledTasks) == 0 {
			runner.ScheduledTaskMutex.Unlock()
			runner.log.Debug("No scheduled tasks to start.")
			return
		}

		var selectedTaskID string
		var selectedTask *ScheduledTask
		for taskId, scheduledTask := range runner.ScheduledTasks {
			if runner.canStartTask(scheduledTask) {
				selectedTaskID = taskId
				selectedTask = scheduledTask
				delete(runner.ScheduledTasks, taskId)
				break
			}
		}

		remainingCount := len(runner.ScheduledTasks)
		runner.ScheduledTaskMutex.Unlock()

		if selectedTask == nil {
			runner.log.Debug("No startable scheduled tasks.", zap.Int("count", remainingCount))
			return
		}

		runner.startScheduledTask(selectedTaskID, selectedTask)
	}
}

func (runner *Runner) startScheduledTask(taskId string, scheduledTask *ScheduledTask) {
	taskType := scheduledTask.TaskConfiguration.Type

	if taskType == config.TaskType_Continuous {
		runner.RunningTaskMutex.Lock()
		runningTask := runner.getRunningTask(taskId)
		runner.RunningTaskMutex.Unlock()
		if runningTask != nil {
			runningTask.StopGracefully()
			runner.log.Info("Waiting for running task to finish before starting a new one",
				zap.String("taskId", taskId))
			runningTask.Wait()
			runner.log.Info("Running task finished, starting new instance")
			runner.RunningTaskMutex.Lock()
			runner.RunningTasks = task.RemoveRunningTask(runner.RunningTasks, taskId)
			runner.RunningTaskMutex.Unlock()
		}
	}

	preparedTaskConfig, globalEnv, exportsChanged, err := runner.prepareTaskForStart(scheduledTask.TaskConfiguration)
	if err != nil {
		runner.log.Error("Failed to prepare task for start", zap.String("taskId", taskId), zap.Error(err))
		runner.onTaskFinished(taskId, true)
		return
	}

	runner.recordTaskStartTime(taskId)
	newTask := runner.startTaskAsync(&ScheduledTask{TaskConfiguration: preparedTaskConfig}, globalEnv)
	if newTask != nil {
		runner.onTaskStart(newTask)
		if exportsChanged {
			runner.scheduleConsumersForProvider(taskId)
		} else {
			runner.scheduleExplicitRestartConsumersForProvider(taskId)
		}
	}
}

func (runner *Runner) isTaskInQueue(taskIdentifier string) bool {
	return runner.ScheduledTasks[taskIdentifier] != nil
}

// recordTaskStartTime records the current time as the start time for a task.
// This must be called synchronously (under ScheduledTaskMutex) BEFORE
// startTaskAsync, so that filterRunningTasks can see the start time even
// before the RunningTask is added to RunningTasks in the goroutine.
// Protected by RunningTaskMutex.
func (runner *Runner) recordTaskStartTime(taskId string) {
	runner.RunningTaskMutex.Lock()
	runner.lastTaskStartTimes[taskId] = time.Now()
	runner.RunningTaskMutex.Unlock()
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

	if !runner.consumesReady(scheduledTask.TaskConfiguration) {
		return false
	}

	runner.log.Info("Task can be started",
		zap.String("taskId", id))
	return true
}

func (r *Runner) startTaskAsync(
	scheduledTask *ScheduledTask,
	globalEnv map[string]string,
) *task.RunningTask {
	runningTask := task.NewRunningTaskWithFactory(
		scheduledTask.TaskConfiguration,
		r.log,
		r.wrapLogger,
		globalEnv,
		r.onTaskFinished,
		r.deps.Platform,
		r.deps.CmdFactory,
	)

	// NewRunningTask can return nil if no command is found for the task
	if runningTask == nil {
		r.log.Warn("Failed to create running task, no command found",
			zap.String("taskId", scheduledTask.TaskConfiguration.Identifier))
		// Notify that the task has finished (with error) to clean up scheduling state
		r.onTaskFinished(scheduledTask.TaskConfiguration.Identifier, true)
		return nil
	}

	if err := runningTask.StartupError(); err != nil {
		r.log.Error("Failed to create running task",
			zap.String("taskId", scheduledTask.TaskConfiguration.Identifier),
			zap.Error(err))
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
