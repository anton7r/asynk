//go:build windows

package instance

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/windows"
)

func TestWindowsProcessAccessIncludesQueryRights(t *testing.T) {
	assert.NotZero(t, defaultWindowsProcessAccess&windows.PROCESS_QUERY_LIMITED_INFORMATION)
}
