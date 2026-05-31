//go:build !windows

package instance

import (
	"syscall"
	"time"
)

type OSProcessProbe struct{}

var (
	osProcessAlive     = func(pid int) error { return syscall.Kill(pid, syscall.Signal(0)) }
	osProcessStartTime = platformProcessStartTime
)

func (OSProcessProbe) Matches(pid int, startTime time.Time) bool {
	if pid <= 0 {
		return false
	}

	err := osProcessAlive(pid)
	if err != nil && err != syscall.EPERM {
		return false
	}

	processStart, ok := osProcessStartTime(pid)
	if !ok {
		return false
	}

	return sameStartTime(processStart, startTime)
}

func (OSProcessProbe) CurrentStartTime(pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	return osProcessStartTime(pid)
}
