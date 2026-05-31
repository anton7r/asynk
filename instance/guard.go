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
	"time"
)

const (
	ownerFileName           = "owner.json"
	shutdownRequestFileName = "shutdown.json"
	pollInterval            = 100 * time.Millisecond
)

var ErrAlreadyRunning = errors.New("asynk instance already running")

type Policy string

const (
	PolicyAllow   Policy = "allow"
	PolicyBlock   Policy = "block"
	PolicyReplace Policy = "replace"
)

type ProcessProbe interface {
	Alive(pid int) bool
}

type Options struct {
	ConfigDir      string
	Policy         Policy
	ReplaceTimeout time.Duration
	Probe          ProcessProbe
	Now            func() time.Time
	RootDir        string
	PID            int
	Token          string
}

type Guard struct {
	lockDir         string
	ownerPath       string
	shutdownPath    string
	token           string
	pid             int
	configDir       string
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

	if options.Probe == nil {
		options.Probe = OSProcessProbe{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.PID == 0 {
		options.PID = os.Getpid()
	}

	configDir, err := filepath.Abs(options.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("resolving config directory: %w", err)
	}
	configDir = filepath.Clean(configDir)

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
		acquired, err := guard.tryCreate(options.Now)
		if err != nil {
			return nil, err
		}
		if acquired {
			return guard, nil
		}

		owner, err := readOwner(guard.ownerPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if removeErr := os.RemoveAll(lockDir); removeErr != nil {
					return nil, fmt.Errorf("removing incomplete instance lock: %w", removeErr)
				}
				continue
			}
			return nil, fmt.Errorf("reading instance owner: %w", err)
		}

		if !options.Probe.Alive(owner.PID) {
			if err := os.RemoveAll(lockDir); err != nil {
				return nil, fmt.Errorf("removing stale instance lock: %w", err)
			}
			continue
		}

		switch options.Policy {
		case PolicyBlock:
			return nil, alreadyRunningError(owner)
		case PolicyReplace:
			if err := requestShutdown(owner, options.PID, options.Now()); err != nil {
				return nil, err
			}
			if err := waitForRelease(lockDir, options.ReplaceTimeout); err != nil {
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

func (g *Guard) StartShutdownMonitor(ctx context.Context, onShutdown func()) {
	if g == nil || onShutdown == nil {
		return
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
	g.shutdownModTime = info.ModTime()

	data, err := os.ReadFile(g.shutdownPath)
	if err != nil {
		return false
	}

	var request shutdownRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return false
	}

	return request.Token == g.token
}

func (g *Guard) tryCreate(now func() time.Time) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(g.lockDir), 0755); err != nil {
		return false, fmt.Errorf("creating instance lock root: %w", err)
	}

	if err := os.Mkdir(g.lockDir, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("creating instance lock: %w", err)
	}

	owner := Owner{
		PID:                 g.pid,
		StartTime:           now().UTC(),
		ConfigDir:           g.configDir,
		Token:               g.token,
		ShutdownRequestPath: g.shutdownPath,
	}
	if err := writeJSON(g.ownerPath, owner); err != nil {
		_ = os.RemoveAll(g.lockDir)
		return false, fmt.Errorf("writing instance owner: %w", err)
	}

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

	sum := sha256.Sum256([]byte(configDir))
	return filepath.Join(rootDir, hex.EncodeToString(sum[:])), nil
}

func readOwner(path string) (Owner, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Owner{}, err
	}

	var owner Owner
	if err := json.Unmarshal(data, &owner); err != nil {
		return Owner{}, err
	}
	if owner.PID <= 0 || owner.Token == "" {
		return Owner{}, fmt.Errorf("instance owner metadata is incomplete")
	}
	return owner, nil
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

func waitForRelease(lockDir string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(lockDir); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: previous instance did not exit within %s", ErrAlreadyRunning, timeout)
		}
		time.Sleep(pollInterval)
	}
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
