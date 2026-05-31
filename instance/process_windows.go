//go:build windows

package instance

import (
	"time"

	"golang.org/x/sys/windows"
)

type OSProcessProbe struct{}

const defaultWindowsProcessAccess = windows.SYNCHRONIZE | windows.PROCESS_QUERY_LIMITED_INFORMATION

func (probe OSProcessProbe) Status(pid int, startTime time.Time) OwnerStatus {
	if pid <= 0 {
		return OwnerStatusDead
	}

	processStart, ok := probe.CurrentStartTime(pid)
	if !ok {
		return OwnerStatusDead
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

	handle, err := windows.OpenProcess(defaultWindowsProcessAccess, false, uint32(pid))
	if err != nil {
		return time.Time{}, false
	}
	defer windows.CloseHandle(handle)

	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return time.Time{}, false
	}

	if status != uint32(windows.WAIT_TIMEOUT) {
		return time.Time{}, false
	}

	var creation windows.Filetime
	var exit windows.Filetime
	var kernel windows.Filetime
	var user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, false
	}

	return time.Unix(0, creation.Nanoseconds()), true
}
