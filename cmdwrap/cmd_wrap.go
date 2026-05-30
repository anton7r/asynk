package cmdwrap

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"

	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/util"
	envUtil "github.com/anton7r/asynk/util/interpolation/env"
	"github.com/anton7r/asynk/util/interpolation/idgen"
	"github.com/anton7r/asynk/util/interpolation/newestfile"

	"go.uber.org/zap"
)

type Callback func(errored bool)

type CommandWrapper struct {
	taskId      string
	executable  string
	args        []string
	cwd         string
	cmd         *exec.Cmd
	log         *zap.Logger
	readCloser  io.ReadCloser
	writeCloser io.WriteCloser
	procMgr     ProcessManager
}

func parseCommand(
	command config.CommandConfig,
	taskId string,
	cwd string,
	env map[string]string,
	genIdInterpolator *idgen.GenIDInterpolator,
	log *zap.Logger,
	procMgr ProcessManager,
) (*CommandWrapper, error) {
	if procMgr == nil {
		procMgr = DefaultProcessManager()
	}

	executable, args, err := resolveCommand(command, env, genIdInterpolator)
	if err != nil {
		return nil, err
	}

	if executable == "" {
		return nil, fmt.Errorf("command is empty")
	}

	cmd := exec.Command(executable, args...)
	cmd.Dir = cwd
	procMgr.SetupProcessGroup(cmd)

	return &CommandWrapper{
		executable: executable,
		args:       args,
		cwd:        cwd,
		cmd:        cmd,
		taskId:     taskId,
		log:        log,
		procMgr:    procMgr,
	}, nil
}

func resolveCommand(command config.CommandConfig, env map[string]string, genIdInterpolator *idgen.GenIDInterpolator) (string, []string, error) {
	interpolatedCommand, err := interpolateCommandValue(command.Command, env, genIdInterpolator)
	if err != nil {
		return "", nil, err
	}

	if command.Shell {
		if util.IsWindows() {
			return "cmd.exe", []string{"/C", interpolatedCommand}, nil
		}

		return "sh", []string{"-c", interpolatedCommand}, nil
	}

	if command.Legacy {
		commandParts, err := splitShellFields(interpolatedCommand)
		if err != nil {
			return "", nil, err
		}

		if len(commandParts) == 0 {
			return "", nil, fmt.Errorf("command is empty")
		}

		return commandParts[0], commandParts[1:], nil
	}

	args := make([]string, len(command.Args))
	for i, arg := range command.Args {
		args[i], err = interpolateCommandValue(arg, env, genIdInterpolator)
		if err != nil {
			return "", nil, err
		}
	}

	return interpolatedCommand, args, nil
}

func interpolateCommandValue(value string, env map[string]string, genIdInterpolator *idgen.GenIDInterpolator) (string, error) {
	interpolated := envUtil.InterpolateEnvVariables(value, env)
	interpolated, err := genIdInterpolator.Interpolate(interpolated)
	if err != nil {
		return "", err
	}

	return newestfile.Interpolate(interpolated), nil
}

func (cmdWrap *CommandWrapper) Run(ctx context.Context, env []string, logWrap *WrapLogger) error {
	if cmdWrap.procMgr == nil {
		cmdWrap.procMgr = DefaultProcessManager()
	}

	cmdWrap.cmd = exec.CommandContext(ctx, cmdWrap.executable, cmdWrap.args...)
	cmdWrap.cmd.Dir = cmdWrap.cwd
	cmdWrap.cmd.Env = commandEnv(cmdWrap.cmd, env, cmdWrap.cwd)
	cmdWrap.procMgr.SetupProcessGroup(cmdWrap.cmd)
	cmdWrap.cmd.Cancel = func() error {
		return cmdWrap.procMgr.CancelProcess(cmdWrap.cmd)
	}

	err := cmdWrap.setupPipes()
	if err != nil {
		return fmt.Errorf("error setting up pipes for task %s: %v", cmdWrap.taskId, err)
	}

	cmdWrap.readOutput(logWrap)

	if err := cmdWrap.start(); err != nil {
		return fmt.Errorf("error starting task %s: %v", cmdWrap.taskId, err)
	}

	wait := make(chan error)

	go func() {
		wait <- cmdWrap.wait()
	}()

	select {
	case <-ctx.Done():
		return cmdWrap.Cancel()
	case err := <-wait:
		return err
	}
}

func (cmdWrap *CommandWrapper) Cancel() error {
	if cmdWrap.procMgr == nil {
		cmdWrap.procMgr = DefaultProcessManager()
	}

	return cmdWrap.procMgr.CancelProcess(cmdWrap.cmd)
}

func commandEnv(cmd *exec.Cmd, env []string, cwd string) []string {
	if cwd == "" {
		return env
	}

	baseEnv := env
	if baseEnv == nil {
		baseEnv = cmd.Environ()
	}

	return withPwdEnv(baseEnv, cwd)
}

func withPwdEnv(env []string, cwd string) []string {
	result := make([]string, 0, len(env)+1)
	found := false

	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && sameEnvKey(key, "PWD") {
			if !found {
				result = append(result, "PWD="+cwd)
				found = true
			}
			continue
		}

		result = append(result, entry)
	}

	if !found {
		result = append(result, "PWD="+cwd)
	}

	return result
}

func sameEnvKey(a string, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}

	return a == b
}

func (cmdWrap *CommandWrapper) Executable() string {
	return cmdWrap.executable
}

func (cmdWrap *CommandWrapper) Args() []string {
	return append([]string(nil), cmdWrap.args...)
}

func (cmdWrap *CommandWrapper) Cwd() string {
	return cmdWrap.cwd
}

func (cmdWrap *CommandWrapper) setupPipes() error {
	readPipe, writePipe, err := stdoutAndStderrPipe(cmdWrap.cmd)
	if err != nil {
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	cmdWrap.readCloser = readPipe
	cmdWrap.writeCloser = writePipe

	return nil
}

func (cmdWrap *CommandWrapper) start() error {
	if err := cmdWrap.cmd.Start(); err != nil {
		return fmt.Errorf("starting command: %w", err)
	}
	return nil
}

func (cmdWrap *CommandWrapper) wait() error {
	err := cmdWrap.cmd.Wait()
	if cmdWrap.writeCloser != nil {
		closeErr := cmdWrap.writeCloser.Close()
		if closeErr != nil && err == nil {
			err = fmt.Errorf("error closing pipe: %v", closeErr)
		}
	}

	if cmdWrap.readCloser != nil {
		closeErr := cmdWrap.readCloser.Close()
		if closeErr != nil && err == nil {
			err = fmt.Errorf("error closing pipe: %v", closeErr)
		}
	}

	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			// If it's an ExitError, we'll omit the "exit status 1" message.
			return fmt.Errorf("task '%s' failed to complete successfully", cmdWrap.taskId)
		}
		return fmt.Errorf("error occurred when waiting for task '%s' to complete: %v", cmdWrap.taskId, err)
	}
	return nil
}

func (cmdWrap *CommandWrapper) readOutput(wrapLog *WrapLogger) {
	go func() {
		scanner := bufio.NewScanner(cmdWrap.readCloser)
		for scanner.Scan() {
			line := scanner.Text()
			wrapLog.log(cmdWrap.taskId, line)
		}

		if err := scanner.Err(); err != nil {
			cmdWrap.log.Error("Error reading pipe", zap.String("taskId", cmdWrap.taskId), zap.Error(err))
		}
	}()
}

func ParseAllCommands(
	commands config.RunCommands,
	taskId string,
	log *zap.Logger,
	env map[string]string,
	cwd string,
	genIdInterpolator *idgen.GenIDInterpolator,
) ([]*CommandWrapper, error) {
	return ParseAllCommandsWithProcessManager(commands, taskId, log, env, cwd, genIdInterpolator, DefaultProcessManager())
}

func ParseAllCommandsWithProcessManager(
	commands config.RunCommands,
	taskId string,
	log *zap.Logger,
	env map[string]string,
	cwd string,
	genIdInterpolator *idgen.GenIDInterpolator,
	procMgr ProcessManager,
) ([]*CommandWrapper, error) {
	if procMgr == nil {
		procMgr = DefaultProcessManager()
	}

	cmds := []*CommandWrapper{}

	for i, command := range commands {
		log.Debug("Parsing command", zap.Int("index", i), zap.String("command", command.Command))

		cmdWrap, err := parseCommand(command, taskId, cwd, env, genIdInterpolator, log, procMgr)
		if err != nil {
			log.Warn("Invalid command to run", zap.Int("index", i), zap.String("command", command.Command), zap.Error(err))
			return nil, fmt.Errorf("invalid command %d for task %s: %w", i, taskId, err)
		}

		if cmdWrap == nil {
			return nil, fmt.Errorf("invalid command %d for task %s", i, taskId)
		}

		cmds = append(cmds, cmdWrap)
	}

	return cmds, nil
}
