package instance

import "time"

func sameStartTime(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}

	delta := a.Sub(b)
	if delta < 0 {
		delta = -delta
	}
	return delta <= time.Second
}
