//go:build windows

package instance

import (
	"time"

	"golang.org/x/sys/windows"
)

type OSProcessProbe struct{}

const defaultWindowsProcessAccess = windows.SYNCHRONIZE | windows.PROCESS_QUERY_LIMITED_INFORMATION

func (OSProcessProbe) Matches(pid int, startTime time.Time) bool {
	if pid <= 0 {
		return false
	}

	processStart, ok := OSProcessProbe{}.CurrentStartTime(pid)
	if !ok {
		return false
	}

	return sameStartTime(processStart, startTime)
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
