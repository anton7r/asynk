package task

import (
	"context"
	"time"

	"github.com/anton7r/asynk/cmdwrap"
	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/util"
	envUtil "github.com/anton7r/asynk/util/interpolation/env"
	"github.com/anton7r/asynk/util/interpolation/idgen"

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
	// done is closed when the task's Start() method returns,
	// meaning all processes have actually exited (not just signalled).
	done chan struct{}
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
	platform *util.Platform,
) *RunningTask {
	return NewRunningTaskWithFactory(taskConfig, log, wrapLogger, globalEnv, onTaskFinished, platform, cmdwrap.NewDefaultCommandFactory(cmdwrap.DefaultProcessManager()))
}

func NewRunningTaskWithFactory(
	taskConfig *config.TaskConfig,
	log *zap.Logger,
	wrapLogger *cmdwrap.WrapLogger,
	globalEnv map[string]string,
	onTaskFinished TaskCompletionCallback,
	platform *util.Platform,
	cmdFactory cmdwrap.CommandFactory,
) *RunningTask {
	if cmdFactory == nil {
		cmdFactory = cmdwrap.NewDefaultCommandFactory(cmdwrap.DefaultProcessManager())
	}

	localEnv := envUtil.InterpolateEnvVariablesList(taskConfig.Env, globalEnv)
	taskId := taskConfig.Identifier
	genId := idgen.NewGenIDInterpolator()

	run := getCommands(taskConfig, log, platform)

	log.Debug("Initializing task", zap.String("taskId", taskId))
	if util.Empty(run) {
		log.Debug("No command found for task", zap.String("taskId", taskId))
		return nil
	}

	// This operation could as well be done later on when executing the command
	cmds := cmdFactory.ParseAllCommands(run, taskId, log, globalEnv, genId)

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
		done:           make(chan struct{}),
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
	platform *util.Platform,
) []string {
	if platform == nil {
		platform = util.NewPlatform()
	}

	taskId := tConfig.Identifier

	var run []string

	if platform.IsWindows() && !util.Empty(tConfig.RunWindows) {
		run = tConfig.RunWindows
	} else if platform.IsLinux() && !util.Empty(tConfig.RunLinux) {
		run = tConfig.RunLinux
	} else if platform.IsMac() && !util.Empty(tConfig.RunMac) {
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
	defer close(r.done)

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

	if r.done == nil {
		r.log.Error("Done channel is nil, cannot wait for task completion")
		return
	}

	// Block until Start() has fully returned, meaning all processes
	// have actually exited (been reaped), not just been signalled.
	<-r.done
}

func (r RunningTask) TaskId() string {
	return r.taskConfig.Identifier
}

func (r RunningTask) StartTime() time.Time {
	return r.startTime
}
