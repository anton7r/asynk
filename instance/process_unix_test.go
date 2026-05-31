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
	defer func() {
		osProcessAlive = originalAlive
		osProcessStartTime = originalStart
	}()

	start := time.Now().UTC()
	osProcessAlive = func(pid int) error {
		return syscall.EPERM
	}
	osProcessStartTime = func(pid int) (time.Time, bool) {
		return start, true
	}

	assert.True(t, OSProcessProbe{}.Matches(1202, start))
}
