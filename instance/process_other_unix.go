//go:build !windows && !linux && !darwin

package instance

import "time"

func platformProcessStartTime(pid int) (time.Time, bool) {
	return time.Time{}, false
}
