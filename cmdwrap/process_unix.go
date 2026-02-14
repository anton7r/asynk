//go:build !windows

package cmdwrap

import (
	"os/exec"
	"syscall"
	"time"
)

const gracefulShutdownTimeout = 5 * time.Second

// UnixProcessManager implements ProcessManager for Unix-like systems.
type UnixProcessManager struct{}

// defaultProcessManager returns the Unix process manager (selected via build tags).
func defaultProcessManager() ProcessManager {
	return &UnixProcessManager{}
}

// SetupProcessGroup configures the command to run in its own process group.
// This ensures that when we kill the process, we can kill the entire tree
// (the direct child and all of its descendants) instead of just the direct child.
func (m *UnixProcessManager) SetupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// CancelProcess sends SIGTERM to the process group first, giving the process tree
// a chance to shut down gracefully. If the process doesn't exit within the grace period,
// it escalates to SIGKILL on the entire process group.
//
// Using the negative PID targets the process group rather than just the direct child,
// which ensures grandchild processes (e.g., a server spawned by "go run .") are also killed.
func (m *UnixProcessManager) CancelProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		// If we can't get the pgid, fall back to killing the process directly
		return cmd.Process.Kill()
	}

	// Send SIGTERM to the entire process group
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		// Process may have already exited, that's fine
		return nil
	}

	// Poll to check if the process has exited after SIGTERM.
	// We check every 100ms up to the graceful shutdown timeout.
	deadline := time.Now().Add(gracefulShutdownTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)

		// Signal 0 is a no-op that checks if the process exists
		if err := syscall.Kill(-pgid, syscall.Signal(0)); err != nil {
			// Process group no longer exists -- exited gracefully
			return nil
		}
	}

	// Escalate to SIGKILL on the entire process group
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		// Process may have exited between the last check and now
		return nil
	}

	return nil
}

// Legacy function aliases for backward compatibility with existing code.
func setupProcessGroup(cmd *exec.Cmd) {
	(&UnixProcessManager{}).SetupProcessGroup(cmd)
}

func cancelProcess(cmd *exec.Cmd) error {
	return (&UnixProcessManager{}).CancelProcess(cmd)
}
