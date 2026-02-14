//go:build windows

package cmdwrap

import (
	"os/exec"
)

// WindowsProcessManager implements ProcessManager for Windows systems.
type WindowsProcessManager struct{}

// defaultProcessManager returns the Windows process manager (selected via build tags).
func defaultProcessManager() ProcessManager {
	return &WindowsProcessManager{}
}

// SetupProcessGroup is a no-op on Windows.
// Windows does not use Unix process groups.
func (m *WindowsProcessManager) SetupProcessGroup(cmd *exec.Cmd) {
	// On Windows, process tree management is handled differently.
	// The os/exec package on Windows already creates processes in a job object
	// when using CREATE_NEW_PROCESS_GROUP, but for now we use the simple approach.
}

// CancelProcess kills the process on Windows.
// Windows does not support Unix signals, so we kill the process directly.
// TODO: Consider using "taskkill /T /F /PID" to kill the process tree on Windows.
func (m *WindowsProcessManager) CancelProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// Legacy function aliases for backward compatibility with existing code.
func setupProcessGroup(cmd *exec.Cmd) {
	(&WindowsProcessManager{}).SetupProcessGroup(cmd)
}

func cancelProcess(cmd *exec.Cmd) error {
	return (&WindowsProcessManager{}).CancelProcess(cmd)
}
