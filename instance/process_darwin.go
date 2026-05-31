//go:build darwin

package instance

import (
	"time"

	"golang.org/x/sys/unix"
)

func platformProcessStartTime(pid int) (time.Time, bool) {
	kinfo, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kinfo == nil {
		return time.Time{}, false
	}

	start := kinfo.Proc.P_starttime
	if start.Sec <= 0 {
		return time.Time{}, false
	}
	return time.Unix(start.Sec, int64(start.Usec)*int64(time.Microsecond)), true
}
