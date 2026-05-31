package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/anton7r/asynk/config"
	configutil "github.com/anton7r/asynk/config/util"
	"github.com/anton7r/asynk/portmanager"
	"go.uber.org/zap"
)

const localHTTPHost = "127.0.0.1"
const proxyReservationPrefix = "\x00asynk-proxy:"

func (runner *Runner) prepareTaskForStart(taskConfig *config.TaskConfig) (*config.TaskConfig, map[string]string, bool, error) {
	generatedEnv, err := runner.generatedConsumerEnv(taskConfig)
	if err != nil {
		return nil, nil, false, err
	}

	exportsChanged := false
	if taskConfig.Port != nil {
		portEnv, portExports, changed, err := runner.assignPortExports(taskConfig)
		if err != nil {
			return nil, nil, false, err
		}

		generatedEnv[portEnv] = portExports[string(config.ConsumeExport_Port)]
		exportsChanged = changed
	}

	clonedTask := cloneTaskConfig(taskConfig)
	clonedTask.Env = append(clonedTask.Env, envEntries(generatedEnv)...)

	globalEnv := mergeEnv(runner.env, generatedEnv)
	return clonedTask, globalEnv, exportsChanged, nil
}

func (runner *Runner) generatedConsumerEnv(taskConfig *config.TaskConfig) (map[string]string, error) {
	result := make(map[string]string)

	runner.serviceExportMutex.Lock()
	defer runner.serviceExportMutex.Unlock()

	for _, consume := range taskConfig.Consumes {
		exports := runner.serviceExports[consume.Task]
		if exports == nil {
			return nil, fmt.Errorf("task %s is waiting for exports from %s", taskConfig.Identifier, consume.Task)
		}

		for envName, exportName := range consume.Env {
			value, exists := exports[exportName]
			if !exists || value == "" {
				return nil, fmt.Errorf("task %s is waiting for export %s from %s", taskConfig.Identifier, exportName, consume.Task)
			}
			result[envName] = value
		}
	}

	return result, nil
}

func (runner *Runner) consumesReady(taskConfig *config.TaskConfig) bool {
	if len(taskConfig.Consumes) == 0 {
		return true
	}

	_, err := runner.generatedConsumerEnv(taskConfig)
	if err != nil {
		runner.log.Debug("Task is waiting for consumed exports", zap.String("taskId", taskConfig.Identifier), zap.Error(err))
		return false
	}

	return true
}

func (runner *Runner) assignPortExports(taskConfig *config.TaskConfig) (string, map[string]string, bool, error) {
	portConfig := taskConfig.Port
	portEnv := portConfig.Env
	if portEnv == "" {
		portEnv = "PORT"
	}

	assignedPort, err := runner.portManager.Assign(
		taskConfig.Identifier,
		portConfig.Preferred,
		portRangeFromConfig(portConfig.Range),
	)
	if err != nil {
		return "", nil, false, err
	}

	serviceName := taskConfig.Identifier
	if portConfig.Expose != nil && portConfig.Expose.Name != "" {
		serviceName = portConfig.Expose.Name
	}

	url := fmt.Sprintf("http://%s:%d", localHTTPHost, assignedPort)
	exports := map[string]string{
		string(config.ConsumeExport_Port):  strconv.Itoa(assignedPort),
		string(config.ConsumeExport_URL):   url,
		exportEnvName(serviceName, "PORT"): strconv.Itoa(assignedPort),
		exportEnvName(serviceName, "URL"):  url,
	}

	proxyConfig := (*config.ProxyConfig)(nil)
	if portConfig.Expose != nil {
		proxyConfig = portConfig.Expose.Proxy
	}

	if proxyConfig != nil && proxyConfig.Enabled {
		proxyPort, err := runner.portManager.Assign(
			proxyReservationID(taskConfig.Identifier),
			proxyConfig.Preferred,
			portRangeFromConfig(proxyConfig.Range),
		)
		if err != nil {
			runner.portManager.Release(taskConfig.Identifier)
			return "", nil, false, err
		}

		proxyURL, err := runner.proxyManager.StartOrUpdate(taskConfig.Identifier, proxyPort, url)
		if err != nil {
			runner.portManager.Release(taskConfig.Identifier)
			runner.portManager.Release(proxyReservationID(taskConfig.Identifier))
			return "", nil, false, err
		}

		proxyEnv := proxyConfig.Env
		if proxyEnv == "" {
			proxyEnv = exportEnvName(serviceName, "PROXY_URL")
		}

		exports[string(config.ConsumeExport_ProxyURL)] = proxyURL
		exports[proxyEnv] = proxyURL
	}

	changed := runner.setServiceExports(taskConfig.Identifier, exports)
	runner.log.Info("Assigned task port",
		zap.String("taskId", taskConfig.Identifier),
		zap.Int("port", assignedPort),
	)

	return portEnv, exports, changed, nil
}

func (runner *Runner) setServiceExports(taskID string, exports map[string]string) bool {
	runner.serviceExportMutex.Lock()
	defer runner.serviceExportMutex.Unlock()

	previous := runner.serviceExports[taskID]
	changed := !stringMapsEqual(previous, exports)

	copied := make(map[string]string, len(exports))
	for key, value := range exports {
		copied[key] = value
	}
	runner.serviceExports[taskID] = copied

	return changed
}

func (runner *Runner) releaseTaskPorts(taskID string) {
	runner.releaseFinishedTaskPorts(taskID, false)
}

func (runner *Runner) releaseFinishedTaskPorts(taskID string, errored bool) {
	runner.portManager.Release(taskID)
	runner.proxyManager.UpdateTarget(taskID, "")

	if errored {
		runner.clearServiceExports(taskID)
		return
	}

	runner.clearDirectServiceExports(taskID)
}

func (runner *Runner) clearServiceExports(taskID string) {
	runner.serviceExportMutex.Lock()
	defer runner.serviceExportMutex.Unlock()

	delete(runner.serviceExports, taskID)
}

func (runner *Runner) clearDirectServiceExports(taskID string) {
	runner.serviceExportMutex.Lock()
	defer runner.serviceExportMutex.Unlock()

	exports := runner.serviceExports[taskID]
	if exports == nil {
		return
	}

	delete(exports, string(config.ConsumeExport_Port))
	delete(exports, string(config.ConsumeExport_URL))

	serviceName := taskID
	if taskConfig := runner.Config.Tasks[taskID]; taskConfig != nil &&
		taskConfig.Port != nil &&
		taskConfig.Port.Expose != nil &&
		taskConfig.Port.Expose.Name != "" {
		serviceName = taskConfig.Port.Expose.Name
	}
	delete(exports, exportEnvName(serviceName, "PORT"))
	delete(exports, exportEnvName(serviceName, "URL"))

	if len(exports) == 0 {
		delete(runner.serviceExports, taskID)
	}
}

func (runner *Runner) closeManagedProxies() {
	runner.proxyManager.CloseAll()

	for taskID, taskConfig := range runner.Config.Tasks {
		if taskConfig.Port != nil && taskConfig.Port.Expose != nil && taskConfig.Port.Expose.Proxy != nil {
			runner.portManager.Release(proxyReservationID(taskID))
		}
	}
}

func (runner *Runner) scheduleConsumersForProvider(providerTaskID string) {
	runner.ScheduledTaskMutex.Lock()
	defer runner.ScheduledTaskMutex.Unlock()

	runner.scheduleConsumersForProviderLocked(providerTaskID)
}

func (runner *Runner) scheduleExplicitRestartConsumersForProvider(providerTaskID string) {
	runner.ScheduledTaskMutex.Lock()
	defer runner.ScheduledTaskMutex.Unlock()

	for taskID, taskConfig := range runner.Config.Tasks {
		for _, consume := range taskConfig.Consumes {
			if consume.Task != providerTaskID || consume.OnChange != config.ConsumeOnChange_Restart {
				continue
			}

			runner.ScheduledTasks[taskID] = &ScheduledTask{TaskConfiguration: taskConfig}
		}
	}
}

func (runner *Runner) scheduleConsumersForProviderLocked(providerTaskID string) {
	for taskID, taskConfig := range runner.Config.Tasks {
		for _, consume := range taskConfig.Consumes {
			if consume.Task != providerTaskID || !runner.shouldRestartOnProviderChange(consume) {
				continue
			}

			runner.ScheduledTasks[taskID] = &ScheduledTask{TaskConfiguration: taskConfig}
		}
	}
}

func (runner *Runner) shouldRestartOnProviderChange(consume config.ConsumeConfig) bool {
	if consume.OnChange == config.ConsumeOnChange_Restart {
		return true
	}

	if consume.OnChange == config.ConsumeOnChange_None {
		return false
	}

	provider := runner.Config.Tasks[consume.Task]
	if consumeUsesDirectExports(consume, provider) {
		return true
	}

	mode := consume.Mode
	if mode == "" {
		if consumeUsesOnlyProxyExports(consume, provider) && providerHasProxy(provider) {
			mode = config.ConsumeMode_Proxy
		} else {
			mode = config.ConsumeMode_Direct
		}
	}

	return mode == config.ConsumeMode_Direct
}

func consumeUsesDirectExports(consume config.ConsumeConfig, provider *config.TaskConfig) bool {
	for _, exportName := range consume.Env {
		if isDirectExportName(provider, exportName) {
			return true
		}
	}
	return false
}

func consumeUsesOnlyProxyExports(consume config.ConsumeConfig, provider *config.TaskConfig) bool {
	if len(consume.Env) == 0 {
		return false
	}

	for _, exportName := range consume.Env {
		if !isProxyExportName(provider, exportName) {
			return false
		}
	}
	return true
}

func isDirectExportName(provider *config.TaskConfig, exportName string) bool {
	switch config.ConsumeExport(exportName) {
	case config.ConsumeExport_Port, config.ConsumeExport_URL:
		return true
	}

	if provider == nil {
		return false
	}

	serviceName := serviceNameForTask(provider)
	return exportName == exportEnvName(serviceName, "PORT") || exportName == exportEnvName(serviceName, "URL")
}

func isProxyExportName(provider *config.TaskConfig, exportName string) bool {
	if config.ConsumeExport(exportName) == config.ConsumeExport_ProxyURL {
		return true
	}

	if !providerHasProxy(provider) {
		return false
	}

	proxyEnv := provider.Port.Expose.Proxy.Env
	if proxyEnv == "" {
		proxyEnv = exportEnvName(serviceNameForTask(provider), "PROXY_URL")
	}
	return exportName == proxyEnv
}

func serviceNameForTask(taskConfig *config.TaskConfig) string {
	serviceName := taskConfig.Identifier
	if taskConfig.Port != nil && taskConfig.Port.Expose != nil && taskConfig.Port.Expose.Name != "" {
		serviceName = taskConfig.Port.Expose.Name
	}
	return serviceName
}

func cloneTaskConfig(taskConfig *config.TaskConfig) *config.TaskConfig {
	clone := *taskConfig
	clone.Run = append(config.RunCommands{}, taskConfig.Run...)
	clone.RunWindows = append(config.RunCommands{}, taskConfig.RunWindows...)
	clone.RunLinux = append(config.RunCommands{}, taskConfig.RunLinux...)
	clone.RunMac = append(config.RunCommands{}, taskConfig.RunMac...)
	clone.Include = append(configutil.GlobArray{}, taskConfig.Include...)
	clone.Exclude = append(configutil.GlobArray{}, taskConfig.Exclude...)
	clone.RebuildSuppression = taskConfig.RebuildSuppression
	clone.Dependencies = append(configutil.StringArray{}, taskConfig.Dependencies...)
	clone.Env = append(configutil.StringArray{}, taskConfig.Env...)
	clone.Consumes = make([]config.ConsumeConfig, len(taskConfig.Consumes))
	for i, consume := range taskConfig.Consumes {
		clone.Consumes[i] = consume
		if consume.Env != nil {
			clone.Consumes[i].Env = make(map[string]string, len(consume.Env))
			for key, value := range consume.Env {
				clone.Consumes[i].Env[key] = value
			}
		}
	}
	return &clone
}

func envEntries(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, fmt.Sprintf("%s=%s", key, values[key]))
	}
	return result
}

func mergeEnv(base map[string]string, overrides map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range overrides {
		result[key] = value
	}
	return result
}

func portRangeFromConfig(portRange *config.PortRangeConfig) portmanager.Range {
	if portRange == nil {
		return portmanager.Range{}
	}

	return portmanager.Range{Start: portRange.Start, End: portRange.End}
}

func exportEnvName(serviceName string, suffix string) string {
	prefix := strings.Trim(sanitizeEnvSegment(serviceName), "_")
	if prefix == "" {
		prefix = "SERVICE"
	}
	return prefix + "_" + suffix
}

func sanitizeEnvSegment(value string) string {
	var builder strings.Builder
	lastUnderscore := false

	for _, r := range strings.ToUpper(value) {
		valid := r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
		if !valid {
			if !lastUnderscore {
				builder.WriteRune('_')
				lastUnderscore = true
			}
			continue
		}

		builder.WriteRune(r)
		lastUnderscore = r == '_'
	}

	return builder.String()
}

func providerHasProxy(taskConfig *config.TaskConfig) bool {
	return taskConfig != nil &&
		taskConfig.Port != nil &&
		taskConfig.Port.Expose != nil &&
		taskConfig.Port.Expose.Proxy != nil &&
		taskConfig.Port.Expose.Proxy.Enabled
}

func proxyReservationID(taskID string) string {
	return proxyReservationPrefix + taskID
}

func stringMapsEqual(a map[string]string, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for key, value := range a {
		if b[key] != value {
			return false
		}
	}

	return true
}
