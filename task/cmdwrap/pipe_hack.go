package cmdwrap

import (
	"errors"
	"io"
	"os"
	"os/exec"
)

// Combined stdout and stderr handling
// It is combined so that we can capture both stdout and stderr in the order they are written.
func stdoutAndStderrPipe(c *exec.Cmd) (io.ReadCloser, io.WriteCloser, error) {
	if c.Stdout != nil {
		return nil, nil, errors.New("exec: Stdout already set")
	}
	if c.Process != nil {
		return nil, nil, errors.New("exec: StdoutPipe after process started")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	c.Stdout = pw
	c.Stderr = pw

	return pr, pw, nil
}
