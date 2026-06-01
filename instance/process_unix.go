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
	osProcessZombie    = platformProcessZombie
)

func (probe OSProcessProbe) Status(pid int, startTime time.Time) OwnerStatus {
	if pid <= 0 {
		return OwnerStatusDead
	}

	err := osProcessAlive(pid)
	if err != nil && err != syscall.EPERM {
		return OwnerStatusDead
	}

	if zombie, ok := osProcessZombie(pid); ok && zombie {
		return OwnerStatusDead
	}

	processStart, ok := osProcessStartTime(pid)
	if !ok {
		return OwnerStatusUnverified
	}

	if sameStartTime(processStart, startTime) {
		return OwnerStatusMatch
	}
	return OwnerStatusStale
}

func (probe OSProcessProbe) Matches(pid int, startTime time.Time) bool {
	return probe.Status(pid, startTime) == OwnerStatusMatch
}

func (OSProcessProbe) CurrentStartTime(pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	return osProcessStartTime(pid)
}
