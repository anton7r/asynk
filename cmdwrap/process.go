package cmdwrap

import "os/exec"

// ProcessManager abstracts platform-specific process group management.
// This allows testing Windows and Unix process management behavior
// on any platform by injecting mock implementations.
type ProcessManager interface {
	// SetupProcessGroup configures the command to run in its own process group.
	// On Unix, this sets Setpgid. On Windows, this is currently a no-op.
	SetupProcessGroup(cmd *exec.Cmd)

	// CancelProcess terminates the process (and ideally its entire process tree).
	// On Unix, this sends SIGTERM then escalates to SIGKILL.
	// On Windows, this calls Process.Kill() directly.
	CancelProcess(cmd *exec.Cmd) error
}

// DefaultProcessManager returns the ProcessManager for the current platform.
// This is resolved at compile time via build tags.
func DefaultProcessManager() ProcessManager {
	return defaultProcessManager()
}
