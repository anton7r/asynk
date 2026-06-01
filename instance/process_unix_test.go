//go:build !windows

package instance

import (
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestOSProcessProbeDoesNotMatchWhenStartTimeCannotBeVerified(t *testing.T) {
	originalAlive := osProcessAlive
	originalStart := osProcessStartTime
	defer func() {
		osProcessAlive = originalAlive
		osProcessStartTime = originalStart
	}()

	osProcessAlive = func(pid int) error {
		return nil
	}
	osProcessStartTime = func(pid int) (time.Time, bool) {
		return time.Time{}, false
	}

	assert.False(t, OSProcessProbe{}.Matches(1201, time.Now()))
}

func TestOSProcessProbeMatchesWhenStartTimeMatches(t *testing.T) {
	originalAlive := osProcessAlive
	originalStart := osProcessStartTime
	originalZombie := osProcessZombie
	defer func() {
		osProcessAlive = originalAlive
		osProcessStartTime = originalStart
		osProcessZombie = originalZombie
	}()

	start := time.Now().UTC()
	osProcessAlive = func(pid int) error {
		return syscall.EPERM
	}
	osProcessStartTime = func(pid int) (time.Time, bool) {
		return start, true
	}
	osProcessZombie = func(pid int) (bool, bool) {
		return false, true
	}

	assert.True(t, OSProcessProbe{}.Matches(1202, start))
}

func TestOSProcessProbeTreatsZombieOwnerAsDead(t *testing.T) {
	originalAlive := osProcessAlive
	originalStart := osProcessStartTime
	originalZombie := osProcessZombie
	defer func() {
		osProcessAlive = originalAlive
		osProcessStartTime = originalStart
		osProcessZombie = originalZombie
	}()

	start := time.Now().UTC()
	osProcessAlive = func(pid int) error {
		return nil
	}
	osProcessStartTime = func(pid int) (time.Time, bool) {
		return start, true
	}
	osProcessZombie = func(pid int) (bool, bool) {
		return true, true
	}

	assert.Equal(t, OwnerStatusDead, OSProcessProbe{}.Status(1203, start))
}
