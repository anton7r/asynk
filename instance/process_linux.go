//go:build linux

package instance

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func platformProcessStartTime(pid int) (time.Time, bool) {
	fields, ok := linuxProcessStatFields(pid)
	if !ok {
		return time.Time{}, false
	}

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

func platformProcessZombie(pid int) (bool, bool) {
	fields, ok := linuxProcessStatFields(pid)
	if !ok || len(fields) == 0 {
		return false, false
	}

	return fields[0] == "Z", true
}

func linuxProcessStatFields(pid int) ([]string, bool) {
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil, false
	}

	stat := string(statData)
	endCommand := strings.LastIndex(stat, ")")
	if endCommand == -1 || endCommand+2 >= len(stat) {
		return nil, false
	}

	return strings.Fields(stat[endCommand+2:]), true
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
