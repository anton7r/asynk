package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSameStartTimeRequiresExactInstant(t *testing.T) {
	start := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	assert.True(t, sameStartTime(start, start))
	assert.False(t, sameStartTime(start, start.Add(500*time.Millisecond)))
}
