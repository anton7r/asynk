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
	"unicode"
	"unicode/utf8"

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
	LastObserved   *TaskFingerprint
	LastSuccessful *TaskFingerprint
	LastFailed     *TaskFingerprint
}

type rebuildSuppressionState struct {
	mu    sync.Mutex
	tasks map[string]*rebuildSuppressionTaskState
}

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

		fingerprint, err := fingerprintTaskInputs(
			taskConfig,
			runner.Config.Shared.Exclude,
			effective,
			runner.deps.FS,
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
		state.LastObserved = fingerprint
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

	fingerprint, err := fingerprintTaskInputs(
		taskConfig,
		runner.Config.Shared.Exclude,
		effective,
		runner.deps.FS,
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
	state.LastObserved = fingerprint

	if fingerprintsEqual(state.LastFailed, fingerprint) {
		if effective.AfterFailure == config.RebuildSuppressionAfterFailure_Suppress {
			runner.log.Info("Skipping task because failed effective inputs are unchanged",
				zap.String("taskId", taskId),
			)
			return false
		}
		return true
	}

	if fingerprintsEqual(state.LastSuccessful, fingerprint) {
		runner.log.Info("Skipping task because effective inputs are unchanged",
			zap.String("taskId", taskId),
		)
		return false
	}

	return true
}

func (runner *Runner) recordRebuildSuppressionTaskResult(taskId string, errored bool) {
	taskConfig := runner.Config.Tasks[taskId]
	if taskConfig == nil {
		return
	}

	effective := runner.Config.EffectiveRebuildSuppressionForTask(taskId)
	if !effective.Enabled {
		return
	}

	fingerprint, err := fingerprintTaskInputs(
		taskConfig,
		runner.Config.Shared.Exclude,
		effective,
		runner.deps.FS,
	)
	if err != nil {
		runner.log.Warn("Failed to record rebuild suppression task result",
			zap.String("taskId", taskId),
			zap.Bool("errored", errored),
			zap.Error(err),
		)
		return
	}

	runner.rebuildSuppression.mu.Lock()
	defer runner.rebuildSuppression.mu.Unlock()

	state := runner.rebuildSuppressionTaskStateLocked(taskId)
	state.LastObserved = fingerprint
	if errored {
		state.LastFailed = fingerprint
		return
	}

	state.LastSuccessful = fingerprint
	if fingerprintsEqual(state.LastFailed, fingerprint) {
		state.LastFailed = nil
	}
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
) (*TaskFingerprint, error) {
	if fs == nil {
		fs = util.NewOSFileSystem()
	}

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

		fileFingerprint, err := fingerprintFile(pathStr, info, effective, fs)
		if err != nil {
			return err
		}
		fingerprint.Files[normalizedPath] = fileFingerprint
		return nil
	})
	if err != nil {
		return nil, err
	}

	return fingerprint, nil
}

func fingerprintFile(
	pathStr string,
	info os.FileInfo,
	effective config.EffectiveRebuildSuppressionConfig,
	fs util.FileSystem,
) (FileFingerprint, error) {
	fileFingerprint := FileFingerprint{
		Size:        info.Size(),
		ModTimeNano: info.ModTime().UnixNano(),
	}

	if effective.Mode == config.RebuildSuppressionMode_SizeAndMTime {
		return fileFingerprint, nil
	}

	content, err := fs.ReadFile(pathStr)
	if err != nil {
		return FileFingerprint{}, fmt.Errorf("read %s: %w", pathStr, err)
	}
	content = normalizeFingerprintContent(content, effective.Normalize)

	fileFingerprint.Size = int64(len(content))
	fileFingerprint.ModTimeNano = 0
	sum := sha256.Sum256(content)
	fileFingerprint.Hash = hex.EncodeToString(sum[:])
	return fileFingerprint, nil
}

func normalizeFingerprintContent(content []byte, normalize config.RebuildSuppressionNormalize) []byte {
	switch normalize {
	case config.RebuildSuppressionNormalize_IgnoreWS:
		return filterUTF8Content(content, func(r rune) bool {
			return !unicode.IsSpace(r)
		})
	case config.RebuildSuppressionNormalize_IgnoreNonAlnum:
		return filterUTF8Content(content, func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsDigit(r)
		})
	default:
		return content
	}
}

func filterUTF8Content(content []byte, keep func(rune) bool) []byte {
	if !utf8.Valid(content) {
		return content
	}

	var builder strings.Builder
	for _, r := range string(content) {
		if keep(r) {
			builder.WriteRune(r)
		}
	}
	return []byte(builder.String())
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
