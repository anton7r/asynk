package instance

import (
	"context"
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
	statuses       map[int]OwnerStatus
	currentStarts  map[int]time.Time
	currentStartOK bool
}

func (p fakeProbe) Status(pid int, startTime time.Time) OwnerStatus {
	if status, ok := p.statuses[pid]; ok {
		return status
	}
	if p.matches[pid] {
		return OwnerStatusMatch
	}
	return OwnerStatusDead
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

func TestLockKeyConfigDirPreservesDistinctCaseSensitivePaths(t *testing.T) {
	stubPathCaseInsensitive(t, false, true)

	appDir := filepath.Join("work", "App")
	lowerDir := filepath.Join("work", "app")

	assert.NotEqual(t, lockKeyConfigDir(appDir), lockKeyConfigDir(lowerDir))
}

func TestLockKeyConfigDirFoldsCaseForCaseInsensitivePaths(t *testing.T) {
	stubPathCaseInsensitive(t, true, true)

	appDir := filepath.Join("work", "App")
	lowerDir := filepath.Join("work", "app")

	assert.Equal(t, lockKeyConfigDir(appDir), lockKeyConfigDir(lowerDir))
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

func TestAcquireWaitsForMalformedOwnerFileBeingCreated(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(configDir, 0755))
	absConfigDir, err := filepath.Abs(configDir)
	require.NoError(t, err)
	lockDir, err := lockDir(root, filepath.Clean(absConfigDir))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(lockDir, 0700))

	ownerPath := filepath.Join(lockDir, ownerFileName)
	require.NoError(t, os.WriteFile(ownerPath, []byte("{"), 0600))
	owner := Owner{
		PID:                 1201,
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
		PID:       1202,
		Probe:     fakeProbe{matches: map[int]bool{1201: true}},
	})

	require.NoError(t, <-ownerWritten)
	assert.Nil(t, guard)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRunning))
	assert.FileExists(t, ownerPath)
}

func TestAcquireBlockPreservesUnverifiedLiveOwner(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	lockDir := createOwner(t, root, configDir, Owner{
		PID:       1201,
		StartTime: time.Now().Add(-time.Hour).UTC(),
		Token:     "unverified-token",
	})
	ownerPath := filepath.Join(lockDir, ownerFileName)

	guard, err := Acquire(Options{
		ConfigDir: configDir,
		Policy:    PolicyBlock,
		RootDir:   root,
		PID:       1202,
		Probe: fakeProbe{
			statuses: map[int]OwnerStatus{1201: OwnerStatusUnverified},
		},
	})

	assert.Nil(t, guard)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRunning))
	assert.DirExists(t, lockDir)

	owner := readOwnerFile(t, ownerPath)
	assert.Equal(t, 1201, owner.PID)
	assert.Equal(t, "unverified-token", owner.Token)
}

func TestAcquireReplaceRetriesWhenOwnerExitsBeforeShutdownRequest(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	lockDir := createOwner(t, root, configDir, Owner{
		PID:       1301,
		StartTime: time.Now().UTC(),
		Token:     "exiting-token",
	})

	removed := false
	guard, err := Acquire(Options{
		ConfigDir:      configDir,
		Policy:         PolicyReplace,
		ReplaceTimeout: time.Second,
		RootDir:        root,
		PID:            1302,
		Token:          "new-token",
		Probe:          fakeProbe{matches: map[int]bool{1301: true}},
		beforeRequestShutdown: func() {
			if removed {
				return
			}
			removed = true
			require.NoError(t, os.RemoveAll(lockDir))
		},
	})

	require.NoError(t, err)
	require.NotNil(t, guard)

	owner := readOwnerFile(t, guard.ownerPath)
	assert.Equal(t, 1302, owner.PID)
	assert.Equal(t, "new-token", owner.Token)
}

func TestAcquireReplaceRetriesWhenOwnerChangesBeforeShutdownRequest(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	lockDir := createOwner(t, root, configDir, Owner{
		PID:       1401,
		StartTime: time.Now().UTC(),
		Token:     "old-token",
	})
	ownerPath := filepath.Join(lockDir, ownerFileName)
	shutdownPath := filepath.Join(lockDir, shutdownRequestFileName)

	replaced := false
	released := make(chan struct{})
	go func() {
		defer close(released)
		for {
			data, err := os.ReadFile(shutdownPath)
			if err == nil {
				var request shutdownRequest
				if json.Unmarshal(data, &request) == nil && request.Token == "current-token" {
					_ = os.RemoveAll(lockDir)
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	guard, err := Acquire(Options{
		ConfigDir:      configDir,
		Policy:         PolicyReplace,
		ReplaceTimeout: time.Second,
		RootDir:        root,
		PID:            1403,
		Token:          "new-token",
		Probe: fakeProbe{matches: map[int]bool{
			1401: true,
			1402: true,
		}},
		beforeRequestShutdown: func() {
			if replaced {
				return
			}
			replaced = true
			require.NoError(t, writeJSON(ownerPath, Owner{
				PID:                 1402,
				StartTime:           time.Now().UTC(),
				ConfigDir:           filepath.Clean(configDir),
				Token:               "current-token",
				ShutdownRequestPath: shutdownPath,
			}))
		},
	})

	require.NoError(t, err)
	require.NotNil(t, guard)
	<-released

	owner := readOwnerFile(t, guard.ownerPath)
	assert.Equal(t, 1403, owner.PID)
	assert.Equal(t, "new-token", owner.Token)
}

func TestAcquireReplaceCanceledBeforeShutdownDoesNotWriteRequest(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	lockDir := createOwner(t, root, configDir, Owner{
		PID:       1451,
		StartTime: time.Now().UTC(),
		Token:     "existing-token",
	})
	shutdownPath := filepath.Join(lockDir, shutdownRequestFileName)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	guard, err := Acquire(Options{
		Context:        ctx,
		ConfigDir:      configDir,
		Policy:         PolicyReplace,
		ReplaceTimeout: time.Second,
		RootDir:        root,
		PID:            1452,
		Probe:          fakeProbe{matches: map[int]bool{1451: true}},
	})

	assert.Nil(t, guard)
	require.ErrorIs(t, err, context.Canceled)
	assert.NoFileExists(t, shutdownPath)
}

func TestAcquireReplaceWaitObservesContextCancellation(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "repo")
	lockDir := createOwner(t, root, configDir, Owner{
		PID:       1461,
		StartTime: time.Now().UTC(),
		Token:     "existing-token",
	})
	shutdownPath := filepath.Join(lockDir, shutdownRequestFileName)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan struct{})
	go func() {
		defer close(cancelled)
		for {
			if _, err := os.Stat(shutdownPath); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	guard, err := Acquire(Options{
		Context:        ctx,
		ConfigDir:      configDir,
		Policy:         PolicyReplace,
		ReplaceTimeout: time.Second,
		RootDir:        root,
		PID:            1462,
		Probe:          fakeProbe{matches: map[int]bool{1461: true}},
	})

	assert.Nil(t, guard)
	require.ErrorIs(t, err, context.Canceled)
	<-cancelled
}

func TestShutdownRequestedRetriesMalformedRequestWithSameModTime(t *testing.T) {
	root := t.TempDir()
	shutdownPath := filepath.Join(root, shutdownRequestFileName)
	guard := &Guard{
		shutdownPath: shutdownPath,
		token:        "owner-token",
	}
	modTime := time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC)

	require.NoError(t, os.WriteFile(shutdownPath, []byte("{"), 0600))
	require.NoError(t, os.Chtimes(shutdownPath, modTime, modTime))
	assert.False(t, guard.shutdownRequested())

	require.NoError(t, writeJSON(shutdownPath, shutdownRequest{
		Token:       "owner-token",
		RequestedBy: 1401,
		RequestedAt: modTime,
	}))
	require.NoError(t, os.Chtimes(shutdownPath, modTime, modTime))

	assert.True(t, guard.shutdownRequested())
}

func TestStartShutdownMonitorIgnoresRequestsWrittenBeforeMonitorStarts(t *testing.T) {
	root := t.TempDir()
	shutdownPath := filepath.Join(root, shutdownRequestFileName)
	requestedAt := time.Now().Add(-time.Second)
	guard := &Guard{
		shutdownPath: shutdownPath,
		token:        "owner-token",
		acquiredAt:   requestedAt.Add(time.Second),
	}
	require.NoError(t, writeJSON(shutdownPath, shutdownRequest{
		Token:       "owner-token",
		RequestedBy: 1501,
		RequestedAt: requestedAt,
	}))
	require.NoError(t, os.Chtimes(shutdownPath, requestedAt, requestedAt))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{}, 1)
	guard.StartShutdownMonitor(ctx, func() {
		called <- struct{}{}
	})

	select {
	case <-called:
		t.Fatal("shutdown monitor should ignore requests created before monitoring starts")
	case <-time.After(pollInterval + 50*time.Millisecond):
	}
}

func TestStartShutdownMonitorHandlesRequestsWrittenAfterAcquisitionBeforeMonitorStarts(t *testing.T) {
	root := t.TempDir()
	shutdownPath := filepath.Join(root, shutdownRequestFileName)
	acquiredAt := time.Now().Add(-time.Second)
	guard := &Guard{
		shutdownPath: shutdownPath,
		token:        "owner-token",
		acquiredAt:   acquiredAt,
	}
	require.NoError(t, writeJSON(shutdownPath, shutdownRequest{
		Token:       "owner-token",
		RequestedBy: 1502,
		RequestedAt: time.Now(),
	}))
	requestModTime := acquiredAt.Add(500 * time.Millisecond)
	require.NoError(t, os.Chtimes(shutdownPath, requestModTime, requestModTime))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{}, 1)
	guard.StartShutdownMonitor(ctx, func() {
		called <- struct{}{}
	})

	select {
	case <-called:
	case <-time.After(pollInterval + 50*time.Millisecond):
		t.Fatal("shutdown monitor should handle requests written after guard acquisition")
	}
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

func stubPathCaseInsensitive(t *testing.T, insensitive, ok bool) {
	t.Helper()

	original := pathCaseInsensitive
	pathCaseInsensitive = func(string) (bool, bool) {
		return insensitive, ok
	}
	t.Cleanup(func() {
		pathCaseInsensitive = original
	})
}
