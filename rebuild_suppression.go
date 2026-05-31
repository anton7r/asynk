package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anton7r/asynk/config"
	configutil "github.com/anton7r/asynk/config/util"
	"github.com/anton7r/asynk/util"
	"go.uber.org/zap"
)

type TaskFingerprint struct {
	Files map[string]FileFingerprint
}

type FileFingerprint struct {
	Size        int64
	ModTimeNano int64
	Hash        string
}

type rebuildSuppressionTaskState struct {
	LastAccepted *TaskFingerprint
}

type rebuildSuppressionState struct {
	mu    sync.Mutex
	tasks map[string]*rebuildSuppressionTaskState
}

type rebuildSuppressionFingerprintStats struct {
	TotalFiles         int
	LanguageAwareFiles int
	FallbackFiles      int
	Duration           time.Duration
}

type fileFingerprintMethod int

const (
	fileFingerprintMethodRaw fileFingerprintMethod = iota
	fileFingerprintMethodMetadata
	fileFingerprintMethodLanguageAware
)

func newRebuildSuppressionState() *rebuildSuppressionState {
	return &rebuildSuppressionState{
		tasks: make(map[string]*rebuildSuppressionTaskState),
	}
}

func (runner *Runner) initializeRebuildSuppression() {
	for taskId, taskConfig := range runner.Config.Tasks {
		effective := runner.Config.EffectiveRebuildSuppressionForTask(taskId)
		if !effective.Enabled {
			continue
		}

		fingerprint, err := runner.computeRebuildSuppressionFingerprint(
			taskId,
			taskConfig,
			effective,
		)
		if err != nil {
			runner.log.Warn("Failed to initialize rebuild suppression fingerprint",
				zap.String("taskId", taskId),
				zap.Error(err),
			)
			continue
		}

		runner.rebuildSuppression.mu.Lock()
		state := runner.rebuildSuppressionTaskStateLocked(taskId)
		state.LastAccepted = fingerprint
		runner.rebuildSuppression.mu.Unlock()
	}
}

func (runner *Runner) filterUnchangedRebuildInputs(
	tasks map[string]*SchedulableTask,
) map[string]*SchedulableTask {
	filtered := make(map[string]*SchedulableTask, len(tasks))
	for taskId, schedulable := range tasks {
		if runner.shouldScheduleAfterRebuildSuppression(taskId, schedulable.TaskConfiguration) {
			filtered[taskId] = schedulable
		}
	}
	return filtered
}

func (runner *Runner) shouldScheduleAfterRebuildSuppression(
	taskId string,
	taskConfig *config.TaskConfig,
) bool {
	effective := runner.Config.EffectiveRebuildSuppressionForTask(taskId)
	if !effective.Enabled {
		return true
	}

	fingerprint, err := runner.computeRebuildSuppressionFingerprint(
		taskId,
		taskConfig,
		effective,
	)
	if err != nil {
		runner.log.Warn("Failed to compute rebuild suppression fingerprint; scheduling task",
			zap.String("taskId", taskId),
			zap.Error(err),
		)
		return true
	}

	runner.rebuildSuppression.mu.Lock()
	defer runner.rebuildSuppression.mu.Unlock()

	state := runner.rebuildSuppressionTaskStateLocked(taskId)
	if fingerprintsEqual(state.LastAccepted, fingerprint) {
		runner.log.Info("Skipping task because effective inputs are unchanged",
			zap.String("taskId", taskId),
		)
		return false
	}

	state.LastAccepted = fingerprint
	return true
}

func (runner *Runner) computeRebuildSuppressionFingerprint(
	taskId string,
	taskConfig *config.TaskConfig,
	effective config.EffectiveRebuildSuppressionConfig,
) (*TaskFingerprint, error) {
	fingerprint, stats, err := fingerprintTaskInputs(
		taskConfig,
		runner.Config.Shared.Exclude,
		effective,
		runner.deps.FS,
	)

	if effective.Mode == config.RebuildSuppressionMode_LanguageAwareHash {
		runner.log.Info("Constructed language-aware rebuild suppression fingerprint",
			zap.String("taskId", taskId),
			zap.Int("totalFiles", stats.TotalFiles),
			zap.Int("languageAwareFiles", stats.LanguageAwareFiles),
			zap.Int("fallbackFiles", stats.FallbackFiles),
			zap.Int64("durationMs", stats.Duration.Milliseconds()),
		)
	}

	return fingerprint, err
}

func (runner *Runner) rebuildSuppressionTaskStateLocked(taskId string) *rebuildSuppressionTaskState {
	state := runner.rebuildSuppression.tasks[taskId]
	if state == nil {
		state = &rebuildSuppressionTaskState{}
		runner.rebuildSuppression.tasks[taskId] = state
	}
	return state
}

func fingerprintTaskInputs(
	taskConfig *config.TaskConfig,
	globalExclude configutil.GlobArray,
	effective config.EffectiveRebuildSuppressionConfig,
	fs util.FileSystem,
) (*TaskFingerprint, rebuildSuppressionFingerprintStats, error) {
	if fs == nil {
		fs = util.NewOSFileSystem()
	}

	stats := rebuildSuppressionFingerprintStats{}
	start := time.Now()

	fingerprint := &TaskFingerprint{Files: make(map[string]FileFingerprint)}
	err := fs.Walk("./", func(pathStr string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		normalizedPath := normalizeFingerprintPath(pathStr)
		if info.IsDir() {
			if pathMatchesAny(globalExclude, pathStr, normalizedPath) {
				return filepath.SkipDir
			}
			return nil
		}

		if pathMatchesAny(globalExclude, pathStr, normalizedPath) ||
			pathMatchesAny(taskConfig.Exclude, pathStr, normalizedPath) ||
			!pathMatchesAny(taskConfig.Include, pathStr, normalizedPath) {
			return nil
		}

		stats.TotalFiles++
		fileFingerprint, method, err := fingerprintFile(pathStr, info, effective, fs)
		if err != nil {
			return err
		}
		if effective.Mode == config.RebuildSuppressionMode_LanguageAwareHash {
			switch method {
			case fileFingerprintMethodLanguageAware:
				stats.LanguageAwareFiles++
			case fileFingerprintMethodRaw:
				stats.FallbackFiles++
			}
		}
		fingerprint.Files[normalizedPath] = fileFingerprint
		return nil
	})
	if err != nil {
		stats.Duration = time.Since(start)
		return nil, stats, err
	}

	stats.Duration = time.Since(start)
	return fingerprint, stats, nil
}

func fingerprintFile(
	pathStr string,
	info os.FileInfo,
	effective config.EffectiveRebuildSuppressionConfig,
	fs util.FileSystem,
) (FileFingerprint, fileFingerprintMethod, error) {
	fileFingerprint := FileFingerprint{
		Size:        info.Size(),
		ModTimeNano: info.ModTime().UnixNano(),
	}

	if effective.Mode == config.RebuildSuppressionMode_SizeAndMTime {
		return fileFingerprint, fileFingerprintMethodMetadata, nil
	}

	content, err := fs.ReadFile(pathStr)
	if err != nil {
		return FileFingerprint{}, fileFingerprintMethodRaw, fmt.Errorf("read %s: %w", pathStr, err)
	}

	method := fileFingerprintMethodRaw
	if effective.Mode == config.RebuildSuppressionMode_LanguageAwareHash {
		if fingerprint, ok := languageAwareFingerprintHash(pathStr, content); ok {
			fileFingerprint.Size = fingerprint.Size
			fileFingerprint.ModTimeNano = 0
			fileFingerprint.Hash = fingerprint.Hash
			return fileFingerprint, fileFingerprintMethodLanguageAware, nil
		}
	}

	fileFingerprint.Size = int64(len(content))
	fileFingerprint.ModTimeNano = 0
	sum := sha256.Sum256(content)
	fileFingerprint.Hash = hex.EncodeToString(sum[:])
	return fileFingerprint, method, nil
}

func pathMatchesAny(globs configutil.GlobArray, originalPath string, normalizedPath string) bool {
	if len(globs) == 0 {
		return false
	}

	candidates := []string{
		normalizePathSlashes(originalPath),
		normalizedPath,
		"./" + normalizedPath,
	}
	for _, candidate := range candidates {
		if globs.AnyMatches(candidate) {
			return true
		}
	}
	return false
}

func normalizeFingerprintPath(pathStr string) string {
	pathStr = normalizePathSlashes(pathStr)
	pathStr = strings.TrimPrefix(pathStr, "./")
	if pathStr == "" {
		return "."
	}
	return pathStr
}

func normalizePathSlashes(pathStr string) string {
	pathStr = filepath.ToSlash(pathStr)
	return strings.ReplaceAll(pathStr, "\\", "/")
}

func fingerprintsEqual(a *TaskFingerprint, b *TaskFingerprint) bool {
	if a == nil || b == nil {
		return false
	}
	if len(a.Files) != len(b.Files) {
		return false
	}

	keys := make([]string, 0, len(a.Files))
	for path := range a.Files {
		keys = append(keys, path)
	}
	sort.Strings(keys)

	for _, path := range keys {
		aFile := a.Files[path]
		bFile, exists := b.Files[path]
		if !exists || aFile != bFile {
			return false
		}
	}
	return true
}
