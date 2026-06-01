//go:build windows

package instance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireUsesSameLockForDifferentPathCasingOnWindows(t *testing.T) {
	root := t.TempDir()
	configDir := t.TempDir()
	if insensitive, ok := detectPathCaseInsensitive(configDir); !ok || !insensitive {
		t.Skip("test requires a case-insensitive temporary directory")
	}
	upperConfigDir := strings.ToUpper(configDir)
	lowerConfigDir := strings.ToLower(configDir)

	guard, err := Acquire(Options{
		ConfigDir: upperConfigDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       1601,
		Token:     "owner-token",
		Now:       func() time.Time { return time.Now().UTC() },
	})
	require.NoError(t, err)
	require.NotNil(t, guard)
	defer guard.Release()

	second, err := Acquire(Options{
		ConfigDir: lowerConfigDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       1602,
		Probe:     fakeProbe{matches: map[int]bool{1601: true}},
	})

	assert.Nil(t, second)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRunning))
}

func TestLockDirNormalizesConfigDirCasingOnWindows(t *testing.T) {
	root := t.TempDir()
	stubPathCaseInsensitive(t, true, true)

	upperLockDir, err := lockDir(root, `C:\Repo\Project`)
	require.NoError(t, err)
	lowerLockDir, err := lockDir(root, `c:\repo\project`)
	require.NoError(t, err)

	assert.Equal(t, upperLockDir, lowerLockDir)
}
