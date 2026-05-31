//go:build windows

package instance

import (
	"time"

	"golang.org/x/sys/windows"
)

type OSProcessProbe struct{}

func (OSProcessProbe) Matches(pid int, startTime time.Time) bool {
	if pid <= 0 {
		return false
	}

	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false
	}

	if status != uint32(windows.WAIT_TIMEOUT) {
		return false
	}

	var creation windows.Filetime
	var exit windows.Filetime
	var kernel windows.Filetime
	var user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return false
	}

	return sameStartTime(time.Unix(0, creation.Nanoseconds()), startTime)
}
