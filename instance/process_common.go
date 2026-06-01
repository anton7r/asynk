package instance

import "time"

func sameStartTime(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}

	return a.Equal(b)
}
