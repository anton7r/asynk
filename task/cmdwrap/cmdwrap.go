package cmdwrap

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type Callback func(errored bool)

type CommandWrapper struct {
	taskId string
	cmd    *exec.Cmd
}

func ParseCommand(command string, taskId string) *CommandWrapper {
	// Split the command with its arguments
	commandParts := strings.Split(command, " ")

	// This should never happen, but just in case it does, log an error and return nil
	if len(commandParts) < 1 {
		fmt.Printf("Invalid command to run: %s\n", command)
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
	}
}

func (cmdWrap *CommandWrapper) Run(ctx context.Context) error {
	stdoutPipe, stderrPipe, err := cmdWrap.setupPipes()
	if err != nil {
		return fmt.Errorf("error setting up pipes for task %s: %v", cmdWrap.taskId, err)
	}

	if err := cmdWrap.start(); err != nil {
		return fmt.Errorf("error starting task %s: %v", cmdWrap.taskId, err)
	}

	readOutput(stdoutPipe, cmdWrap.taskId, false)
	readOutput(stderrPipe, cmdWrap.taskId, true)

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

func readOutput(pipe io.ReadCloser, taskId string, isError bool) {
	go func() {
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Text()
			if isError {
				fmt.Printf("[%s] [ERROR] %s\n", taskId, line)
			} else {
				fmt.Printf("[%s] %s\n", taskId, line)
			}
		}

		if err := scanner.Err(); err != nil {
			fmt.Printf("Error reading %s for task %s: %v\n",
				getPipeType(isError), taskId, err)
		}
	}()
}

func getPipeType(isError bool) string {
	if isError {
		return "stderr"
	}
	return "stdout"
}

func ParseAllCommands(commands []string, taskId string) []*CommandWrapper {
	cmds := []*CommandWrapper{}

	for i, command := range commands {

		fmt.Printf("[Debug] Parsing command %d: %s\n", i+1, command)
		cmdWrap := ParseCommand(command, taskId)
		if cmdWrap != nil {
			cmds = append(cmds, cmdWrap)
		} else {
			fmt.Printf("Skipping invalid command: %s\n", command)
		}
	}

	return cmds

}
