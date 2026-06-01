package instance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	ownerFileName           = "owner.json"
	shutdownRequestFileName = "shutdown.json"
	pollInterval            = 100 * time.Millisecond
	ownerCreateTimeout      = 500 * time.Millisecond
)

var pathCaseInsensitive = detectPathCaseInsensitive

var ErrAlreadyRunning = errors.New("asynk instance already running")

type Policy string

const (
	PolicyAllow   Policy = "allow"
	PolicyBlock   Policy = "block"
	PolicyReplace Policy = "replace"
)

type ProcessProbe interface {
	Status(pid int, startTime time.Time) OwnerStatus
	CurrentStartTime(pid int) (time.Time, bool)
}

type OwnerStatus int

const (
	OwnerStatusDead OwnerStatus = iota
	OwnerStatusMatch
	OwnerStatusStale
	OwnerStatusUnverified
)

type Options struct {
	Context        context.Context
	ConfigDir      string
	Policy         Policy
	ReplaceTimeout time.Duration
	Probe          ProcessProbe
	Now            func() time.Time
	RootDir        string
	PID            int
	Token          string

	beforeStaleRemove     func()
	beforeRequestShutdown func()
}

type Guard struct {
	lockDir         string
	ownerPath       string
	shutdownPath    string
	token           string
	pid             int
	configDir       string
	acquiredAt      time.Time
	shutdownModTime time.Time
}

type Owner struct {
	PID                 int       `json:"pid"`
	StartTime           time.Time `json:"startTime"`
	ConfigDir           string    `json:"configDir"`
	Token               string    `json:"token"`
	ShutdownRequestPath string    `json:"shutdownRequestPath"`
}

type shutdownRequest struct {
	Token       string    `json:"token"`
	RequestedBy int       `json:"requestedBy"`
	RequestedAt time.Time `json:"requestedAt"`
}

func Acquire(options Options) (*Guard, error) {
	if options.Policy == "" || options.Policy == PolicyAllow {
		return nil, nil
	}

	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.Probe == nil {
		options.Probe = OSProcessProbe{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.PID == 0 {
		options.PID = os.Getpid()
	}

	configDir, err := canonicalConfigDir(options.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("resolving config directory: %w", err)
	}

	lockDir, err := lockDir(options.RootDir, configDir)
	if err != nil {
		return nil, err
	}

	guard := &Guard{
		lockDir:      lockDir,
		ownerPath:    filepath.Join(lockDir, ownerFileName),
		shutdownPath: filepath.Join(lockDir, shutdownRequestFileName),
		token:        options.Token,
		pid:          options.PID,
		configDir:    configDir,
	}
	if guard.token == "" {
		guard.token, err = randomToken()
		if err != nil {
			return nil, fmt.Errorf("creating owner token: %w", err)
		}
	}

	for {
		if err := options.Context.Err(); err != nil {
			return nil, err
		}

		acquired, err := guard.tryCreate(options.Now, options.Probe)
		if err != nil {
			return nil, err
		}
		if acquired {
			return guard, nil
		}

		owner, ownerData, err := readOwnerData(guard.ownerPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || isMalformedOwner(err) {
				if _, waitOwnerData, waitErr := waitForOwnerData(options.Context, guard.ownerPath, ownerCreateTimeout); waitErr == nil {
					continue
				} else if !errors.Is(waitErr, os.ErrNotExist) && !isMalformedOwner(waitErr) {
					return nil, fmt.Errorf("reading instance owner: %w", waitErr)
				} else {
					ownerData = waitOwnerData
				}
			}
			if errors.Is(err, os.ErrNotExist) || isMalformedOwner(err) {
				removed, removeErr := guard.removeStaleLockIfUnchanged(ownerData, options.beforeStaleRemove)
				if removeErr != nil {
					return nil, fmt.Errorf("removing incomplete instance lock: %w", removeErr)
				}
				if !removed {
					continue
				}
				continue
			}
			return nil, fmt.Errorf("reading instance owner: %w", err)
		}

		ownerStatus := options.Probe.Status(owner.PID, owner.StartTime)
		if ownerStatus == OwnerStatusDead || ownerStatus == OwnerStatusStale {
			removed, err := guard.removeStaleLockIfUnchanged(ownerData, options.beforeStaleRemove)
			if err != nil {
				return nil, fmt.Errorf("removing stale instance lock: %w", err)
			}
			if !removed {
				continue
			}
			continue
		}

		switch options.Policy {
		case PolicyBlock:
			return nil, alreadyRunningError(owner)
		case PolicyReplace:
			if options.beforeRequestShutdown != nil {
				options.beforeRequestShutdown()
			}
			if err := options.Context.Err(); err != nil {
				return nil, err
			}
			if err := requestShutdown(owner, options.PID, options.Now()); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, err
			}
			if err := waitForReleaseOrOwnerChange(options.Context, lockDir, guard.ownerPath, owner, options.ReplaceTimeout); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported instance policy: %s", options.Policy)
		}
	}
}

func (g *Guard) Release() error {
	if g == nil {
		return nil
	}

	owner, err := readOwner(g.ownerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	if owner.Token != g.token || owner.PID != g.pid {
		return nil
	}

	return os.RemoveAll(g.lockDir)
}

func (g *Guard) removeStaleLockIfUnchanged(expectedOwnerData []byte, beforeRemove func()) (bool, error) {
	if beforeRemove != nil {
		beforeRemove()
	}

	currentOwnerData, err := os.ReadFile(g.ownerPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		if expectedOwnerData != nil {
			return false, nil
		}
	} else if string(currentOwnerData) != string(expectedOwnerData) {
		return false, nil
	}

	if err := os.RemoveAll(g.lockDir); err != nil {
		return false, err
	}
	return true, nil
}

func (g *Guard) StartShutdownMonitor(ctx context.Context, onShutdown func()) {
	if g == nil || onShutdown == nil {
		return
	}
	if info, err := os.Stat(g.shutdownPath); err == nil {
		if g.acquiredAt.IsZero() || !info.ModTime().After(g.acquiredAt) {
			g.shutdownModTime = info.ModTime()
		}
	}

	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if g.shutdownRequested() {
					onShutdown()
					return
				}
			}
		}
	}()
}

func (g *Guard) shutdownRequested() bool {
	info, err := os.Stat(g.shutdownPath)
	if err != nil {
		return false
	}
	if !info.ModTime().After(g.shutdownModTime) && !g.shutdownModTime.IsZero() {
		return false
	}

	data, err := os.ReadFile(g.shutdownPath)
	if err != nil {
		return false
	}

	var request shutdownRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return false
	}

	if request.Token != g.token {
		return false
	}

	g.shutdownModTime = info.ModTime()
	return true
}

func (g *Guard) tryCreate(now func() time.Time, probe ProcessProbe) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(g.lockDir), 0755); err != nil {
		return false, fmt.Errorf("creating instance lock root: %w", err)
	}

	if err := os.Mkdir(g.lockDir, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("creating instance lock: %w", err)
	}

	acquiredAt := now().UTC()
	startTime := acquiredAt
	if processStart, ok := probe.CurrentStartTime(g.pid); ok {
		startTime = processStart.UTC()
	}

	owner := Owner{
		PID:                 g.pid,
		StartTime:           startTime,
		ConfigDir:           g.configDir,
		Token:               g.token,
		ShutdownRequestPath: g.shutdownPath,
	}
	if err := writeJSON(g.ownerPath, owner); err != nil {
		_ = os.RemoveAll(g.lockDir)
		return false, fmt.Errorf("writing instance owner: %w", err)
	}

	g.acquiredAt = acquiredAt
	return true, nil
}

func lockDir(rootDir, configDir string) (string, error) {
	if rootDir == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			cacheDir = os.TempDir()
		}
		rootDir = filepath.Join(cacheDir, "asynk", "instances")
	}

	sum := sha256.Sum256([]byte(lockKeyConfigDir(configDir)))
	return filepath.Join(rootDir, hex.EncodeToString(sum[:])), nil
}

func lockKeyConfigDir(configDir string) string {
	if insensitive, ok := pathCaseInsensitive(configDir); ok && insensitive {
		return strings.ToLower(configDir)
	}
	return configDir
}

func detectPathCaseInsensitive(path string) (bool, bool) {
	variant, changed := swapPathCase(path)
	if !changed {
		return false, false
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, false
	}

	variantInfo, err := os.Stat(variant)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, true
		}
		return false, false
	}

	return os.SameFile(info, variantInfo), true
}

func swapPathCase(path string) (string, bool) {
	var builder strings.Builder
	changed := false

	for _, r := range path {
		lower := unicode.ToLower(r)
		upper := unicode.ToUpper(r)
		if lower == upper {
			builder.WriteRune(r)
			continue
		}

		changed = true
		if r == lower {
			builder.WriteRune(upper)
		} else {
			builder.WriteRune(lower)
		}
	}

	return builder.String(), changed
}

func readOwner(path string) (Owner, error) {
	owner, _, err := readOwnerData(path)
	return owner, err
}

func readOwnerData(path string) (Owner, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Owner{}, nil, err
	}

	var owner Owner
	if err := json.Unmarshal(data, &owner); err != nil {
		return Owner{}, data, malformedOwnerError{err}
	}
	if owner.PID <= 0 || owner.Token == "" {
		return Owner{}, data, malformedOwnerError{fmt.Errorf("instance owner metadata is incomplete")}
	}
	return owner, data, nil
}

func waitForOwnerData(ctx context.Context, path string, timeout time.Duration) (Owner, []byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return Owner{}, nil, err
		}
		owner, data, err := readOwnerData(path)
		if err == nil || (!errors.Is(err, os.ErrNotExist) && !isMalformedOwner(err)) {
			return owner, data, err
		}
		if time.Now().After(deadline) {
			return owner, data, err
		}
		if err := sleepContext(ctx, 10*time.Millisecond); err != nil {
			return Owner{}, nil, err
		}
	}
}

type malformedOwnerError struct {
	err error
}

func (e malformedOwnerError) Error() string {
	return e.err.Error()
}

func (e malformedOwnerError) Unwrap() error {
	return e.err
}

func isMalformedOwner(err error) bool {
	var malformed malformedOwnerError
	return errors.As(err, &malformed)
}

func requestShutdown(owner Owner, requestedBy int, requestedAt time.Time) error {
	request := shutdownRequest{
		Token:       owner.Token,
		RequestedBy: requestedBy,
		RequestedAt: requestedAt.UTC(),
	}
	if err := writeJSON(owner.ShutdownRequestPath, request); err != nil {
		return fmt.Errorf("requesting previous instance shutdown: %w", err)
	}
	return nil
}

func waitForReleaseOrOwnerChange(ctx context.Context, lockDir, ownerPath string, expectedOwner Owner, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Stat(lockDir); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return fmt.Errorf("checking instance lock: %w", err)
		}

		owner, _, err := readOwnerData(ownerPath)
		if err == nil && ownerChanged(owner, expectedOwner) {
			return nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) && !isMalformedOwner(err) {
			return fmt.Errorf("reading instance owner while waiting for replacement: %w", err)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("%w: previous instance did not exit within %s", ErrAlreadyRunning, timeout)
		}
		if err := sleepContext(ctx, pollInterval); err != nil {
			return err
		}
	}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func ownerChanged(owner, expectedOwner Owner) bool {
	return owner.PID != expectedOwner.PID || owner.Token != expectedOwner.Token
}

func alreadyRunningError(owner Owner) error {
	return fmt.Errorf("%w for config %s with pid %d", ErrAlreadyRunning, owner.ConfigDir, owner.PID)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func randomToken() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func canonicalConfigDir(configDir string) (string, error) {
	absolute, err := filepath.Abs(configDir)
	if err != nil {
		return "", err
	}

	realPath, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}

	return filepath.Clean(realPath), nil
}
