package task

import (
	"context"
	"os"
	"path/filepath"
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
	env            map[string]string
	cwd            string
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

	env := MergeEnv(os.Environ(), globalEnv, taskConfig.Env)
	localEnv := EnvMapToList(env)
	cwd, err := resolveCwd(taskConfig, env)
	if err != nil {
		log.Error("Error resolving task cwd", zap.String("taskId", taskConfig.Identifier), zap.Error(err))
		return nil
	}

	taskId := taskConfig.Identifier
	genId := idgen.NewGenIDInterpolator()

	run := getCommands(taskConfig, log, platform)

	log.Debug("Initializing task", zap.String("taskId", taskId))
	if run.IsEmpty() {
		log.Debug("No command found for task", zap.String("taskId", taskId))
		return nil
	}

	// This operation could as well be done later on when executing the command
	cmds := cmdFactory.ParseAllCommands(run, taskId, log, env, cwd, genId)

	ctx, cancel := initializeContext()

	runningTask := &RunningTask{
		taskConfig:     taskConfig,
		ctx:            ctx,
		cancel:         cancel,
		cmds:           cmds,
		localEnv:       localEnv,
		env:            env,
		cwd:            cwd,
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
) config.RunCommands {
	if platform == nil {
		platform = util.NewPlatform()
	}

	taskId := tConfig.Identifier

	var run config.RunCommands

	if platform.IsWindows() && !tConfig.RunWindows.IsEmpty() {
		run = tConfig.RunWindows
	} else if platform.IsLinux() && !tConfig.RunLinux.IsEmpty() {
		run = tConfig.RunLinux
	} else if platform.IsMac() && !tConfig.RunMac.IsEmpty() {
		run = tConfig.RunMac
	} else if !tConfig.Run.IsEmpty() {
		run = tConfig.Run
	} else {
		log.Debug("No command found for task", zap.String("taskId", taskId))
		return nil
	}

	return run
}

func resolveCwd(taskConfig *config.TaskConfig, env map[string]string) (string, error) {
	if taskConfig.Cwd == "" {
		return "", nil
	}

	cwd := envUtil.InterpolateEnvVariables(taskConfig.Cwd, env)
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd), nil
	}

	baseDir := taskConfig.ConfigDir
	if baseDir == "" {
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}

	return filepath.Clean(filepath.Join(baseDir, cwd)), nil
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
