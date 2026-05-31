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
	matches        map[int]bool
	currentStarts  map[int]time.Time
	currentStartOK bool
}

func (p fakeProbe) Matches(pid int, startTime time.Time) bool {
	return p.matches[pid]
}

func (p fakeProbe) CurrentStartTime(pid int) (time.Time, bool) {
	start, ok := p.currentStarts[pid]
	if !ok {
		return time.Time{}, p.currentStartOK
	}
	return start, true
}

func TestAcquireSucceedsWithNoOwner(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(configDir, 0755))

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
		Probe:     fakeProbe{matches: map[int]bool{201: true}},
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
		Probe:     fakeProbe{matches: map[int]bool{301: false}},
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
		Probe:          fakeProbe{matches: map[int]bool{401: true}},
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
		Probe:          fakeProbe{matches: map[int]bool{501: true}},
	})

	assert.Nil(t, guard)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRunning))
	assert.DirExists(t, lockDir)
	assert.FileExists(t, owner.ShutdownRequestPath)

	ownerAfter := readOwnerFile(t, ownerPath)
	assert.Equal(t, 501, ownerAfter.PID)
}

func TestAcquireCanonicalizesSymlinkedConfigDirectory(t *testing.T) {
	root := t.TempDir()
	realConfigDir := filepath.Join(root, "real")
	linkConfigDir := filepath.Join(root, "link")
	require.NoError(t, os.Mkdir(realConfigDir, 0755))
	if err := os.Symlink(realConfigDir, linkConfigDir); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}

	guard, err := Acquire(Options{
		ConfigDir: realConfigDir,
		Policy:    PolicyBlock,
		RootDir:   filepath.Join(root, "locks"),
		PID:       601,
		Token:     "real-token",
	})
	require.NoError(t, err)
	defer guard.Release()

	second, err := Acquire(Options{
		ConfigDir: linkConfigDir,
		Policy:    PolicyBlock,
		RootDir:   filepath.Join(root, "locks"),
		PID:       602,
		Probe:     fakeProbe{matches: map[int]bool{601: true}},
	})

	assert.Nil(t, second)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRunning))
}

func TestAcquireTreatsReusedPIDWithDifferentStartTimeAsStale(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	lockDir := createOwner(t, root, configDir, Owner{
		PID:       701,
		StartTime: time.Now().Add(-time.Hour).UTC(),
		Token:     "old-token",
	})

	guard, err := Acquire(Options{
		ConfigDir: configDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       702,
		Token:     "new-token",
		Probe:     fakeProbe{matches: map[int]bool{701: false}},
	})

	require.NoError(t, err)
	require.NotNil(t, guard)
	assert.Equal(t, lockDir, guard.lockDir)

	owner := readOwnerFile(t, guard.ownerPath)
	assert.Equal(t, 702, owner.PID)
	assert.Equal(t, "new-token", owner.Token)
}

func TestAcquireDoesNotRemoveFreshOwnerAfterStaleOwnerRace(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	lockDir := createOwner(t, root, configDir, Owner{
		PID:       801,
		StartTime: time.Now().Add(-time.Hour).UTC(),
		Token:     "stale-token",
	})
	ownerPath := filepath.Join(lockDir, ownerFileName)

	replaced := false
	guard, err := Acquire(Options{
		ConfigDir: configDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       803,
		Probe: fakeProbe{matches: map[int]bool{
			801: false,
			802: true,
		}},
		beforeStaleRemove: func() {
			if replaced {
				return
			}
			replaced = true
			require.NoError(t, writeJSON(ownerPath, Owner{
				PID:                 802,
				StartTime:           time.Now().UTC(),
				ConfigDir:           filepath.Clean(configDir),
				Token:               "fresh-token",
				ShutdownRequestPath: filepath.Join(lockDir, shutdownRequestFileName),
			}))
		},
	})

	assert.Nil(t, guard)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRunning))

	owner := readOwnerFile(t, ownerPath)
	assert.Equal(t, 802, owner.PID)
	assert.Equal(t, "fresh-token", owner.Token)
}

func TestAcquireRecoversMalformedOwnerMetadata(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(configDir, 0755))
	absConfigDir, err := filepath.Abs(configDir)
	require.NoError(t, err)
	lockDir, err := lockDir(root, filepath.Clean(absConfigDir))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(lockDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(lockDir, ownerFileName), []byte("{broken"), 0600))

	guard, err := Acquire(Options{
		ConfigDir: configDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       901,
		Token:     "new-token",
	})

	require.NoError(t, err)
	require.NotNil(t, guard)

	owner := readOwnerFile(t, guard.ownerPath)
	assert.Equal(t, 901, owner.PID)
	assert.Equal(t, "new-token", owner.Token)
}

func TestAcquireRecordsCurrentProcessStartTime(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(configDir, 0755))
	processStart := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	acquireTime := processStart.Add(5 * time.Second)

	guard, err := Acquire(Options{
		ConfigDir: configDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       1001,
		Token:     "owner-token",
		Now:       func() time.Time { return acquireTime },
		Probe: fakeProbe{
			currentStarts: map[int]time.Time{1001: processStart},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, guard)

	owner := readOwnerFile(t, guard.ownerPath)
	assert.Equal(t, processStart, owner.StartTime)
}

func TestAcquireWaitsForOwnerFileBeingCreated(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(configDir, 0755))
	absConfigDir, err := filepath.Abs(configDir)
	require.NoError(t, err)
	lockDir, err := lockDir(root, filepath.Clean(absConfigDir))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(lockDir, 0700))

	ownerPath := filepath.Join(lockDir, ownerFileName)
	owner := Owner{
		PID:                 1101,
		StartTime:           time.Now().UTC(),
		ConfigDir:           filepath.Clean(absConfigDir),
		Token:               "creating-token",
		ShutdownRequestPath: filepath.Join(lockDir, shutdownRequestFileName),
	}
	ownerWritten := make(chan error, 1)
	go func() {
		time.Sleep(25 * time.Millisecond)
		ownerWritten <- writeJSON(ownerPath, owner)
	}()

	guard, err := Acquire(Options{
		ConfigDir: configDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       1102,
		Probe:     fakeProbe{matches: map[int]bool{1101: true}},
	})

	require.NoError(t, <-ownerWritten)
	assert.Nil(t, guard)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRunning))
	assert.FileExists(t, ownerPath)
}

func createOwner(t *testing.T, root, configDir string, owner Owner) string {
	t.Helper()

	configDir, err := filepath.Abs(configDir)
	require.NoError(t, err)
	configDir = filepath.Clean(configDir)
	owner.ConfigDir = configDir
	require.NoError(t, os.MkdirAll(configDir, 0755))

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
