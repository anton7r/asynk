package task

import (
	"asynk/cmdwrap"
	"asynk/config"
	"asynk/util"
	envUtil "asynk/util/interpolation/env"
	"asynk/util/interpolation/idgen"
	"context"
	"time"

	"go.uber.org/zap"
)

type TaskCompletionCallback func(taskId string, errored bool)

type RunningTask struct {
	// Task configuration
	taskConfig *config.TaskConfig
	// Cancellable context
	ctx            context.Context
	cancel         context.CancelFunc
	cmds           []*cmdwrap.CommandWrapper
	localEnv       []string
	log            *zap.Logger
	wrapLogger     *cmdwrap.WrapLogger
	onTaskFinished TaskCompletionCallback
	startTime      time.Time
}

func initializeContext() (context.Context, context.CancelFunc) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	return ctx, cancel
}

func NewRunningTask(
	taskConfig *config.TaskConfig,
	log *zap.Logger,
	wrapLogger *cmdwrap.WrapLogger,
	globalEnv map[string]string,
	onTaskFinished TaskCompletionCallback,
) *RunningTask {
	localEnv := envUtil.InterpolateEnvVariablesList(taskConfig.Env, globalEnv)
	taskId := taskConfig.Identifier
	genId := idgen.NewGenIDInterpolator()

	run := getCommands(taskConfig, log)

	log.Debug("Initializing task", zap.String("taskId", taskId))
	if util.Empty(run) {
		log.Debug("No command found for task", zap.String("taskId", taskId))
		return nil
	}

	// This operation could as well be done later on when executing the command
	cmds := cmdwrap.ParseAllCommands(run, taskId, log, globalEnv, genId)

	ctx, cancel := initializeContext()

	runningTask := &RunningTask{
		taskConfig:     taskConfig,
		ctx:            ctx,
		cancel:         cancel,
		cmds:           cmds,
		localEnv:       localEnv,
		log:            log,
		wrapLogger:     wrapLogger,
		onTaskFinished: onTaskFinished,
		startTime:      time.Now(),
	}

	return runningTask
}

// Note this only removes the array occurrence
func RemoveRunningTask(runningTasks []*RunningTask, taskIdentifier string) []*RunningTask {
	for i, task := range runningTasks {
		if task.taskConfig.Identifier == taskIdentifier {
			// Remove the task by appending the slices before and after the task
			return append(runningTasks[:i], runningTasks[i+1:]...)
		}
	}
	return runningTasks
}

func getCommands(
	tConfig *config.TaskConfig,
	log *zap.Logger,
) []string {
	taskId := tConfig.Identifier

	var run []string

	if util.IsWindows() && !util.Empty(tConfig.RunWindows) {
		run = tConfig.RunWindows
	} else if util.IsLinux() && !util.Empty(tConfig.RunLinux) {
		run = tConfig.RunLinux
	} else if util.IsMac() && !util.Empty(tConfig.RunMac) {
		run = tConfig.RunMac
	} else if !util.Empty(tConfig.Run) {
		run = tConfig.Run
	} else {
		log.Debug("No command found for task", zap.String("taskId", taskId))
		return nil
	}

	return run
}

func (r *RunningTask) Start() {
	taskId := r.TaskId()
	r.log.Info("Starting task", zap.String("taskId", taskId))

	for i, cmd := range r.cmds {
		err := cmd.Run(r.ctx, r.localEnv, r.wrapLogger)
		if err != nil {
			r.log.Error("Error executing command",
				zap.String("taskId", taskId),
				zap.Int("commandIndex", i),
				zap.Error(err),
			)

			r.onTaskFinished(taskId, true)
			r.cancel() // Ensure Done fires on error
			return
		}

	}

	r.log.Info("Task completed successfully", zap.String("taskId", taskId))
	r.onTaskFinished(taskId, false)
	r.cancel() // Ensure Done fires on success
}

func (r *RunningTask) StopGracefully() {
	if r == nil {
		return
	}

	r.cancel()
}

func (r *RunningTask) Wait() {
	if r == nil {
		return
	}

	if r.ctx == nil {
		r.log.Error("Context is nil, cannot wait for task completion")
		return
	}

	<-r.ctx.Done()
}

func (r RunningTask) TaskId() string {
	return r.taskConfig.Identifier
}

func (r RunningTask) StartTime() time.Time {
	return r.startTime
}
