package cmdwrap

import (
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
	taskId string
	cmd    *exec.Cmd
	log    *zap.Logger
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

func (cmdWrap *CommandWrapper) Run(ctx context.Context, logWrap *WrapLogger) error {
	stdoutPipe, stderrPipe, err := cmdWrap.setupPipes()
	if err != nil {
		return fmt.Errorf("error setting up pipes for task %s: %v", cmdWrap.taskId, err)
	}

	if err := cmdWrap.start(); err != nil {
		return fmt.Errorf("error starting task %s: %v", cmdWrap.taskId, err)
	}

	readOutput(stdoutPipe, cmdWrap.taskId, false, cmdWrap.log, logWrap)
	readOutput(stderrPipe, cmdWrap.taskId, true, cmdWrap.log, logWrap)

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
	return cmdWrap.cmd.Cancel()
}

func (cmdWrap *CommandWrapper) setupPipes() (io.ReadCloser, io.ReadCloser, error) {
	stdoutPipe, err := cmdWrap.cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	stderrPipe, err := cmdWrap.cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("creating stderr pipe: %w", err)
	}

	return stdoutPipe, stderrPipe, nil
}

func (cmdWrap *CommandWrapper) start() error {
	if err := cmdWrap.cmd.Start(); err != nil {
		return fmt.Errorf("starting command: %w", err)
	}
	return nil
}

func (cmdWrap *CommandWrapper) wait() error {
	err := cmdWrap.cmd.Wait()
	if err != nil {
		return fmt.Errorf("error occured when waiting for task '%s' to complete: %v", cmdWrap.taskId, err)
	}
	return nil
}

func readOutput(pipe io.ReadCloser, taskId string, isError bool, log *zap.Logger, wrapLog *WrapLogger) {
	go func() {
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Text()
			wrapLog.log(taskId, line, isError)
		}

		if err := scanner.Err(); err != nil {
			log.Error("Error reading pipe", zap.String("type", getPipeType(isError)),
				zap.String("taskId", taskId), zap.Error(err))
		}
	}()
}

func getPipeType(isError bool) string {
	if isError {
		return "stderr"
	}
	return "stdout"
}

func ParseAllCommands(commands []string, taskId string, log *zap.Logger) []*CommandWrapper {
	cmds := []*CommandWrapper{}

	for i, command := range commands {
		log.Debug("Parsing command", zap.Int("index", i), zap.String("command", command))
		cmdWrap := parseCommand(command, taskId, log)
		if cmdWrap != nil {
			cmds = append(cmds, cmdWrap)
		} else {
			log.Warn("Invalid command to run", zap.Int("index", i), zap.String("command", command))
		}
	}

	return cmds

}
