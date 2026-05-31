//go:build !windows

package instance

import "syscall"

type OSProcessProbe struct{}

func (OSProcessProbe) Alive(pid int) bool {
	if pid <= 0 {
		return false
	}

	err := syscall.Kill(pid, syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}
