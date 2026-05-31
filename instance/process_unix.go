//go:build !windows

package instance

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type OSProcessProbe struct{}

func (OSProcessProbe) Matches(pid int, startTime time.Time) bool {
	if pid <= 0 {
		return false
	}

	err := syscall.Kill(pid, syscall.Signal(0))
	if err != nil && err != syscall.EPERM {
		return false
	}

	processStart, ok := linuxProcessStartTime(pid)
	if !ok {
		return true
	}

	return sameStartTime(processStart, startTime)
}

func linuxProcessStartTime(pid int) (time.Time, bool) {
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}, false
	}

	stat := string(statData)
	endCommand := strings.LastIndex(stat, ")")
	if endCommand == -1 || endCommand+2 >= len(stat) {
		return time.Time{}, false
	}

	fields := strings.Fields(stat[endCommand+2:])
	if len(fields) < 20 {
		return time.Time{}, false
	}

	startTicks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return time.Time{}, false
	}

	bootTime, ok := linuxBootTime()
	if !ok {
		return time.Time{}, false
	}

	const clockTicksPerSecond = 100
	return bootTime.Add(time.Duration(startTicks) * time.Second / clockTicksPerSecond), true
}

func linuxBootTime() (time.Time, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}

	for _, line := range strings.Split(string(data), "\n") {
		value, ok := strings.CutPrefix(line, "btime ")
		if !ok {
			continue
		}

		seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(seconds, 0), true
	}

	return time.Time{}, false
}
