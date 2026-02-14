//go:build !windows

package cmdwrap

import "testing"

func TestUnixProcessManagerImplementsProcessManager(t *testing.T) {
	var _ ProcessManager = &UnixProcessManager{}
}
