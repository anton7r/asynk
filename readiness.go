package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anton7r/asynk/config"
	"go.uber.org/zap"
)

type ReadinessChecker interface {
	Check(ctx context.Context, url string) (bool, error)
}

type defaultReadinessChecker struct{}

func (defaultReadinessChecker) Check(ctx context.Context, url string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)

	return response.StatusCode == http.StatusOK, nil
}

func (runner *Runner) startReadinessPolling(taskID string, scheduledTask *ScheduledTask) {
	if scheduledTask == nil ||
		scheduledTask.TaskConfiguration == nil ||
		scheduledTask.TaskConfiguration.Type != config.TaskType_Continuous ||
		scheduledTask.TaskConfiguration.Readiness == nil {
		return
	}

	readiness := scheduledTask.TaskConfiguration.Readiness
	healthURL, err := runner.readinessURL(taskID, readiness)
	if err != nil {
		runner.log.Warn("Could not start readiness polling",
			zap.String("taskId", taskID),
			zap.Error(err),
		)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	generation := runner.registerReadinessPoller(taskID, cancel)
	startCause := scheduledTask.StartCause
	matchedFiles := append([]string(nil), scheduledTask.MatchedFiles...)

	go runner.pollReadiness(ctx, taskID, healthURL, readiness, startCause, matchedFiles, generation)
}

func (runner *Runner) pollReadiness(
	ctx context.Context,
	taskID string,
	healthURL string,
	readiness *config.ReadinessConfig,
	startCause TaskStartCause,
	matchedFiles []string,
	generation int64,
) {
	defer runner.finishReadinessPoller(taskID, generation)

	pollCtx, cancel := context.WithTimeout(ctx, readiness.EffectiveTimeout())
	defer cancel()

	interval := time.NewTicker(readiness.EffectiveInterval())
	defer interval.Stop()

	for {
		ready, err := runner.deps.ReadinessChecker.Check(pollCtx, healthURL)
		if ready {
			runner.log.Info("Task readiness check passed",
				zap.String("taskId", taskID),
				zap.String("url", healthURL),
			)
			runner.markReadinessReady(taskID, generation)
			runner.scheduleReadinessConsumers(taskID, startCause, matchedFiles, generation)
			return
		}

		if err != nil && ctx.Err() == nil {
			runner.log.Debug("Task readiness check did not pass",
				zap.String("taskId", taskID),
				zap.String("url", healthURL),
				zap.Error(err),
			)
		}

		select {
		case <-ctx.Done():
			return
		case <-pollCtx.Done():
			if pollCtx.Err() == context.DeadlineExceeded {
				runner.log.Warn("Task readiness check timed out",
					zap.String("taskId", taskID),
					zap.String("url", healthURL),
					zap.Duration("timeout", readiness.EffectiveTimeout()),
				)
			}
			return
		case <-interval.C:
		}
	}
}

func (runner *Runner) readinessURL(taskID string, readiness *config.ReadinessConfig) (string, error) {
	if readiness.URL != "" {
		return strings.TrimSpace(readiness.URL), nil
	}

	runner.serviceExportMutex.Lock()
	exports := runner.serviceExports[taskID]
	baseURL := ""
	if exports != nil {
		baseURL = exports[string(config.ConsumeExport_URL)]
	}
	runner.serviceExportMutex.Unlock()

	if baseURL == "" {
		return "", fmt.Errorf("task %s has no direct url export for readiness path", taskID)
	}

	return joinReadinessPath(baseURL, strings.TrimSpace(readiness.Path)), nil
}

func joinReadinessPath(baseURL, path string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func (runner *Runner) registerReadinessPoller(taskID string, cancel context.CancelFunc) int64 {
	runner.readinessMutex.Lock()
	defer runner.readinessMutex.Unlock()

	if previous := runner.readinessPollers[taskID]; previous != nil {
		previous()
	}

	runner.readinessGeneration[taskID]++
	generation := runner.readinessGeneration[taskID]
	runner.readinessReady[taskID] = 0
	runner.readinessPollers[taskID] = cancel
	return generation
}

func (runner *Runner) finishReadinessPoller(taskID string, generation int64) {
	runner.readinessMutex.Lock()
	defer runner.readinessMutex.Unlock()

	if runner.readinessGeneration[taskID] != generation {
		return
	}

	delete(runner.readinessPollers, taskID)
}

func (runner *Runner) cancelReadinessPoller(taskID string) {
	var cancel context.CancelFunc

	runner.ScheduledTaskMutex.Lock()
	runner.readinessMutex.Lock()

	cancel = runner.readinessPollers[taskID]
	if cancel != nil {
		delete(runner.readinessPollers, taskID)
	}

	runner.readinessGeneration[taskID]++
	runner.readinessReady[taskID] = 0
	runner.readinessMutex.Unlock()

	runner.removeScheduledReadinessConsumersForProviderLocked(taskID)
	runner.ScheduledTaskMutex.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (runner *Runner) cancelAllReadinessPollers() {
	cancels := make([]context.CancelFunc, 0)

	runner.ScheduledTaskMutex.Lock()
	runner.readinessMutex.Lock()

	for taskID, cancel := range runner.readinessPollers {
		cancels = append(cancels, cancel)
		delete(runner.readinessPollers, taskID)
	}
	for taskID := range runner.readinessGeneration {
		runner.readinessGeneration[taskID]++
		runner.readinessReady[taskID] = 0
	}
	runner.readinessMutex.Unlock()

	runner.removeScheduledReadinessConsumersLocked()
	runner.ScheduledTaskMutex.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

func (runner *Runner) markReadinessReady(taskID string, generation int64) {
	runner.readinessMutex.Lock()
	defer runner.readinessMutex.Unlock()

	if runner.readinessGeneration[taskID] != generation {
		return
	}

	runner.readinessReady[taskID] = generation
}

func (runner *Runner) providerReadinessReady(taskID string) bool {
	runner.readinessMutex.Lock()
	defer runner.readinessMutex.Unlock()

	generation := runner.readinessGeneration[taskID]
	return generation != 0 && runner.readinessReady[taskID] == generation
}

func (runner *Runner) scheduleReadinessConsumers(
	providerTaskID string,
	startCause TaskStartCause,
	matchedFiles []string,
	generation int64,
) {
	scheduled := false

	runner.ScheduledTaskMutex.Lock()
	runner.readinessMutex.Lock()
	if runner.readinessGeneration[providerTaskID] != generation {
		runner.readinessMutex.Unlock()
		runner.ScheduledTaskMutex.Unlock()
		return
	}

	for taskID, taskConfig := range runner.Config.Tasks {
		for _, consume := range taskConfig.Consumes {
			if consume.Task != providerTaskID || consume.When != config.ConsumeWhen_Ready {
				continue
			}

			if !readinessConsumerShouldRun(consume, startCause, matchedFiles) {
				continue
			}

			runner.ScheduledTasks[taskID] = &ScheduledTask{
				TaskConfiguration: taskConfig,
				StartCause:        TaskStartCauseReadiness,
			}
			scheduled = true
			runner.log.Info("Scheduling readiness consumer",
				zap.String("taskId", taskID),
				zap.String("providerTaskId", providerTaskID),
			)
			break
		}
	}
	runner.readinessMutex.Unlock()
	runner.ScheduledTaskMutex.Unlock()

	if scheduled {
		go runner.startScheduledTasks()
	}
}

func (runner *Runner) readinessGenerationCurrent(taskID string, generation int64) bool {
	runner.readinessMutex.Lock()
	defer runner.readinessMutex.Unlock()

	return runner.readinessGeneration[taskID] == generation
}

func readinessConsumerShouldRun(
	consume config.ConsumeConfig,
	startCause TaskStartCause,
	matchedFiles []string,
) bool {
	if startCause == TaskStartCauseInitial {
		return true
	}

	if len(matchedFiles) == 0 {
		return false
	}

	for _, file := range matchedFiles {
		normalized := normalizePathSlashes(file)
		if pathMatchesAny(consume.Exclude, file, normalized) {
			continue
		}

		if len(consume.Include) == 0 || pathMatchesAny(consume.Include, file, normalized) {
			return true
		}
	}

	return false
}

func (runner *Runner) removeScheduledReadinessConsumersForProviderLocked(providerTaskID string) {
	for taskID, scheduledTask := range runner.ScheduledTasks {
		if scheduledTask == nil ||
			scheduledTask.StartCause != TaskStartCauseReadiness ||
			!taskConsumesReadinessProvider(scheduledTask.TaskConfiguration, providerTaskID) {
			continue
		}

		delete(runner.ScheduledTasks, taskID)
	}
}

func (runner *Runner) removeScheduledReadinessConsumersLocked() {
	for taskID, scheduledTask := range runner.ScheduledTasks {
		if scheduledTask == nil || scheduledTask.StartCause != TaskStartCauseReadiness {
			continue
		}

		delete(runner.ScheduledTasks, taskID)
	}
}

func taskConsumesReadinessProvider(taskConfig *config.TaskConfig, providerTaskID string) bool {
	if taskConfig == nil {
		return false
	}

	for _, consume := range taskConfig.Consumes {
		if consume.Task == providerTaskID && consume.When == config.ConsumeWhen_Ready {
			return true
		}
	}
	return false
}
