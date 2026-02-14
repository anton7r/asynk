//go:build windows

package cmdwrap

import (
	"os/exec"
)

// setupProcessGroup is a no-op on Windows.
// Windows does not use Unix process groups.
func setupProcessGroup(cmd *exec.Cmd) {
	// On Windows, process tree management is handled differently.
	// The os/exec package on Windows already creates processes in a job object
	// when using CREATE_NEW_PROCESS_GROUP, but for now we use the simple approach.
}

// cancelProcess kills the process on Windows.
// Windows does not support Unix signals, so we kill the process directly.
// TODO: Consider using "taskkill /T /F /PID" to kill the process tree on Windows.
func cancelProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
