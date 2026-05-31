package instance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeProbe struct {
	alive map[int]bool
}

func (p fakeProbe) Alive(pid int) bool {
	return p.alive[pid]
}

func TestAcquireSucceedsWithNoOwner(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")

	guard, err := Acquire(Options{
		ConfigDir: configDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       101,
		Token:     "owner-token",
	})

	require.NoError(t, err)
	require.NotNil(t, guard)
	assert.FileExists(t, guard.ownerPath)
	require.NoError(t, guard.Release())
	assert.NoDirExists(t, guard.lockDir)
}

func TestAcquireBlockRejectsLiveOwner(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	existing := createOwner(t, root, configDir, Owner{
		PID:                 201,
		StartTime:           time.Now().UTC(),
		ConfigDir:           configDir,
		Token:               "existing-token",
		ShutdownRequestPath: filepath.Join(root, "unused"),
	})

	guard, err := Acquire(Options{
		ConfigDir: configDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       202,
		Probe:     fakeProbe{alive: map[int]bool{201: true}},
	})

	assert.Nil(t, guard)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRunning))
	assert.DirExists(t, existing)
}

func TestAcquireCleansStaleOwner(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	stale := createOwner(t, root, configDir, Owner{
		PID:                 301,
		StartTime:           time.Now().UTC(),
		ConfigDir:           configDir,
		Token:               "stale-token",
		ShutdownRequestPath: filepath.Join(root, "unused"),
	})

	guard, err := Acquire(Options{
		ConfigDir: configDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       302,
		Token:     "new-token",
		Probe:     fakeProbe{alive: map[int]bool{301: false}},
	})

	require.NoError(t, err)
	require.NotNil(t, guard)
	assert.Equal(t, stale, guard.lockDir)

	owner := readOwnerFile(t, guard.ownerPath)
	assert.Equal(t, 302, owner.PID)
	assert.Equal(t, "new-token", owner.Token)
}

func TestAcquireReplaceRequestsShutdownAndWaitsForRelease(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	lockDir := createOwner(t, root, configDir, Owner{
		PID:                 401,
		StartTime:           time.Now().UTC(),
		ConfigDir:           configDir,
		Token:               "existing-token",
		ShutdownRequestPath: "",
	})
	ownerPath := filepath.Join(lockDir, ownerFileName)
	owner := readOwnerFile(t, ownerPath)
	owner.ShutdownRequestPath = filepath.Join(lockDir, shutdownRequestFileName)
	require.NoError(t, writeJSON(ownerPath, owner))

	released := make(chan struct{})
	go func() {
		defer close(released)
		for {
			if _, err := os.Stat(owner.ShutdownRequestPath); err == nil {
				_ = os.RemoveAll(lockDir)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	guard, err := Acquire(Options{
		ConfigDir:      configDir,
		Policy:         PolicyReplace,
		ReplaceTimeout: time.Second,
		RootDir:        root,
		PID:            402,
		Token:          "new-token",
		Probe:          fakeProbe{alive: map[int]bool{401: true}},
	})

	require.NoError(t, err)
	require.NotNil(t, guard)
	<-released

	owner = readOwnerFile(t, guard.ownerPath)
	assert.Equal(t, 402, owner.PID)
	assert.Equal(t, "new-token", owner.Token)
}

func TestAcquireReplaceTimesOutWithoutForceKill(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	lockDir := createOwner(t, root, configDir, Owner{
		PID:                 501,
		StartTime:           time.Now().UTC(),
		ConfigDir:           configDir,
		Token:               "existing-token",
		ShutdownRequestPath: "",
	})
	ownerPath := filepath.Join(lockDir, ownerFileName)
	owner := readOwnerFile(t, ownerPath)
	owner.ShutdownRequestPath = filepath.Join(lockDir, shutdownRequestFileName)
	require.NoError(t, writeJSON(ownerPath, owner))

	guard, err := Acquire(Options{
		ConfigDir:      configDir,
		Policy:         PolicyReplace,
		ReplaceTimeout: 20 * time.Millisecond,
		RootDir:        root,
		PID:            502,
		Probe:          fakeProbe{alive: map[int]bool{501: true}},
	})

	assert.Nil(t, guard)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRunning))
	assert.DirExists(t, lockDir)
	assert.FileExists(t, owner.ShutdownRequestPath)

	ownerAfter := readOwnerFile(t, ownerPath)
	assert.Equal(t, 501, ownerAfter.PID)
}

func createOwner(t *testing.T, root, configDir string, owner Owner) string {
	t.Helper()

	configDir, err := filepath.Abs(configDir)
	require.NoError(t, err)
	configDir = filepath.Clean(configDir)
	owner.ConfigDir = configDir

	lockDir, err := lockDir(root, configDir)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(lockDir, 0700))
	if owner.ShutdownRequestPath == "" {
		owner.ShutdownRequestPath = filepath.Join(lockDir, shutdownRequestFileName)
	}
	require.NoError(t, writeJSON(filepath.Join(lockDir, ownerFileName), owner))
	return lockDir
}

func readOwnerFile(t *testing.T, path string) Owner {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var owner Owner
	require.NoError(t, json.Unmarshal(data, &owner))
	return owner
}
