package cmdwrap

import (
	envUtil "asynk/util/interpolation/env"
	"asynk/util/interpolation/idgen"
	"asynk/util/interpolation/newestfile"
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"go.uber.org/zap"
)

type Callback func(errored bool)

type CommandWrapper struct {
	taskId      string
	cmd         *exec.Cmd
	log         *zap.Logger
	readCloser  io.ReadCloser
	writeCloser io.WriteCloser
}

func parseCommand(command string, taskId string, log *zap.Logger) *CommandWrapper {
	// Split the command with its arguments
	commandParts := strings.Split(command, " ")

	// This should never happen, but just in case it does, log an error and return nil
	if len(commandParts) < 1 {
		return nil
	}

	var cmd *exec.Cmd

	if len(commandParts) > 1 {
		cmd = exec.Command(commandParts[0], commandParts[1:]...)
	} else {
		cmd = exec.Command(commandParts[0])
	}

	return &CommandWrapper{
		cmd:    cmd,
		taskId: taskId,
		log:    log,
	}
}

func (cmdWrap *CommandWrapper) Run(ctx context.Context, env []string, logWrap *WrapLogger) error {
	err := cmdWrap.setupPipes()
	if err != nil {
		return fmt.Errorf("error setting up pipes for task %s: %v", cmdWrap.taskId, err)
	}

	cmdWrap.readOutput(logWrap)

	cmdWrap.cmd.Env = append(cmdWrap.cmd.Env, env...)
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
	if cmdWrap.cmd == nil || cmdWrap.cmd.Process == nil {
		return nil
	}

	// Kill the process and its subprocesses
	return cmdWrap.cmd.Process.Kill()
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
			// If it's an ExitError, we'll omit the "exit status 1" message
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
	commands []string,
	taskId string,
	log *zap.Logger,
	env map[string]string,
	genIdInterpolator *idgen.GenIDInterpolator,
) []*CommandWrapper {
	cmds := []*CommandWrapper{}

	for i, command := range commands {
		log.Debug("Parsing command", zap.Int("index", i), zap.String("command", command))

		interpolatedCommand := envUtil.InterpolateEnvVariables(command, env)
		interpolatedCommand, err := genIdInterpolator.Interpolate(interpolatedCommand)
		if err != nil {
			log.Error("Error interpolating command", zap.String("command", command), zap.Error(err))
			return nil
		}
		interpolatedCommand = newestfile.Interpolate(interpolatedCommand)

		cmdWrap := parseCommand(interpolatedCommand, taskId, log)
		if cmdWrap != nil {
			cmds = append(cmds, cmdWrap)
		} else {
			log.Warn("Invalid command to run", zap.Int("index", i), zap.String("command", command))
		}
	}

	return cmds

}
