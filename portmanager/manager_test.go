package portmanager

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeChecker struct {
	unavailable map[int]bool
}

func (c *fakeChecker) Available(port int) bool {
	return !c.unavailable[port]
}

func TestAssign_UsesPreferredPort(t *testing.T) {
	manager := NewManager(&fakeChecker{unavailable: map[int]bool{}})

	port, err := manager.Assign("backend", 3000, Range{Start: 3000, End: 3002})

	require.NoError(t, err)
	assert.Equal(t, 3000, port)
}

func TestAssign_FallsBackWhenLastAssignedPortIsOccupied(t *testing.T) {
	checker := &fakeChecker{unavailable: map[int]bool{}}
	manager := NewManager(checker)

	port, err := manager.Assign("backend", 3000, Range{Start: 3000, End: 3002})
	require.NoError(t, err)
	assert.Equal(t, 3000, port)

	manager.Release("backend")
	checker.unavailable[3000] = true

	port, err = manager.Assign("backend", 3000, Range{Start: 3000, End: 3002})

	require.NoError(t, err)
	assert.Equal(t, 3001, port)
}

func TestAssign_PreventsDuplicateReservations(t *testing.T) {
	manager := NewManager(&fakeChecker{unavailable: map[int]bool{}})

	firstPort, err := manager.Assign("backend", 3000, Range{Start: 3000, End: 3001})
	require.NoError(t, err)
	secondPort, err := manager.Assign("frontend", 3000, Range{Start: 3000, End: 3001})

	require.NoError(t, err)
	assert.Equal(t, 3000, firstPort)
	assert.Equal(t, 3001, secondPort)
}

func TestAssign_ReusesSameTaskReservationWithoutRechecking(t *testing.T) {
	checker := &fakeChecker{unavailable: map[int]bool{}}
	manager := NewManager(checker)

	port, err := manager.Assign("backend:proxy", 8080, Range{})
	require.NoError(t, err)
	assert.Equal(t, 8080, port)

	checker.unavailable[8080] = true
	port, err = manager.Assign("backend:proxy", 8080, Range{})

	require.NoError(t, err)
	assert.Equal(t, 8080, port)
}

func TestAssign_ReturnsErrorWhenRangeExhausted(t *testing.T) {
	manager := NewManager(&fakeChecker{unavailable: map[int]bool{
		3000: true,
		3001: true,
	}})

	_, err := manager.Assign("backend", 3000, Range{Start: 3000, End: 3001})

	assert.Error(t, err)
}
