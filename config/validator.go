package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const minimumInstanceReplaceTimeout = 5 * time.Second

type validator struct {
	config    *Config
	taskIdMap map[string]bool
}

func createValidator(config *Config) *validator {
	taskIdMap := make(map[string]bool)
	for taskId, _ := range config.Tasks {
		taskIdMap[taskId] = true
	}
	return &validator{
		taskIdMap: taskIdMap,
		config:    config,
	}
}

// Validate the task configuration, checking for duplicate identifiers and ensuring required fields are present.
func (v *validator) validateTaskId() error {
	for taskId, _ := range v.config.Tasks {
		if taskId == "" {
			return fmt.Errorf("The given task id is invalid: \"%s\"", taskId)
		}
	}

	return nil
}

func (v *validator) validateRunCommand() error {
	for taskId, taskConfig := range v.config.Tasks {
		runEmpty := taskConfig.Run.IsEmpty()
		runWindowsEmpty := taskConfig.RunWindows.IsEmpty()
		runLinuxEmpty := taskConfig.RunLinux.IsEmpty()
		runMacEmpty := taskConfig.RunMac.IsEmpty()

		if runEmpty &&
			(runWindowsEmpty &&
				runLinuxEmpty &&
				runMacEmpty) {
			return fmt.Errorf("invalid task configuration, run command is missing: %s", taskId)
		}

		if !runEmpty &&
			(!runWindowsEmpty ||
				!runLinuxEmpty ||
				!runMacEmpty) {
			return fmt.Errorf("invalid task configuration, run command is duplicated, you have defined the global run command and the platform specific run commands: %s", taskId)
		}

		if err := validateRunCommands(taskId, taskConfig.Run); err != nil {
			return err
		}
		if err := validateRunCommands(taskId, taskConfig.RunWindows); err != nil {
			return err
		}
		if err := validateRunCommands(taskId, taskConfig.RunLinux); err != nil {
			return err
		}
		if err := validateRunCommands(taskId, taskConfig.RunMac); err != nil {
			return err
		}
	}
	return nil
}

func validateRunCommands(taskId string, commands RunCommands) error {
	for _, command := range commands {
		if strings.TrimSpace(command.Command) == "" {
			return fmt.Errorf("invalid task configuration, run command is empty: %s", taskId)
		}

		if command.Shell && len(command.Args) > 0 {
			return fmt.Errorf("invalid task configuration, shell commands cannot define args: %s", taskId)
		}
	}

	return nil
}

func (v *validator) validateWorkingDirectories() error {
	for _, taskConfig := range v.config.Tasks {
		cwd := strings.TrimSpace(taskConfig.Cwd)
		workingDir := strings.TrimSpace(taskConfig.WorkingDir)

		if cwd != "" && workingDir != "" && cwd != workingDir {
			return fmt.Errorf(
				"invalid task configuration, cwd and working-dir have different values: %s",
				taskConfig.Identifier,
			)
		}

		if cwd == "" {
			taskConfig.Cwd = workingDir
		} else {
			taskConfig.Cwd = cwd
		}
	}

	return nil
}

func (v *validator) validateTaskTypes() error {
	for _, taskConfig := range v.config.Tasks {
		if taskConfig.Type == "" {
			return fmt.Errorf("invalid task configuration, type is missing: %s", taskConfig.Identifier)
		}

		if taskConfig.Type != TaskType_Continuous && taskConfig.Type != TasKType_Build {
			return fmt.Errorf(
				"invalid task type: '%s'. Expected the task type to be either '%s' or '%s'",
				taskConfig.Identifier, TaskType_Continuous, TasKType_Build,
			)
		}
	}
	return nil
}

func (v *validator) validateDependencies() error {
	for _, taskConfig := range v.config.Tasks {
		for _, dependency := range taskConfig.Dependencies {
			if _, exists := v.config.Tasks[dependency]; !exists {
				return fmt.Errorf(
					"invalid task configuration, dependency '%s' does not exist: %s",
					dependency, taskConfig.Identifier,
				)
			}
		}
	}
	return nil
}

func (v *validator) validateCleanUpTasks() error {
	for _, taskConfig := range v.config.CleanUpTasks {
		if taskConfig.Strategy != CleanUpStrategy_KeepLatest {
			return fmt.Errorf(
				"invalid task type: '%s'. Expected the task type to be '%s'",
				taskConfig.Identifier, CleanUpStrategy_KeepLatest,
			)
		}
	}
	return nil
}

func (v *validator) validateFSDebounce() error {
	if v.config.Shared.FSDebounce.IsSet() && v.config.Shared.FSDebounce.Duration < 0 {
		return fmt.Errorf("invalid shared fs-debounce configuration, duration cannot be negative")
	}

	for _, taskConfig := range v.config.Tasks {
		if taskConfig.FSDebounce.IsSet() && taskConfig.FSDebounce.Duration < 0 {
			return fmt.Errorf(
				"invalid task configuration, fs-debounce cannot be negative: %s",
				taskConfig.Identifier,
			)
		}
	}

	return nil
}

func (v *validator) validateRebuildSuppression() error {
	if err := validateRebuildSuppressionConfig("shared", v.config.Shared.RebuildSuppression); err != nil {
		return err
	}

	for _, taskConfig := range v.config.Tasks {
		if err := validateRebuildSuppressionConfig(
			fmt.Sprintf("task %s", taskConfig.Identifier),
			taskConfig.RebuildSuppression,
		); err != nil {
			return err
		}
	}

	return nil
}

func (v *validator) validateInstanceConfig() error {
	switch v.config.Shared.Instance.Policy {
	case "", InstancePolicy_Allow, InstancePolicy_Block, InstancePolicy_Replace:
	default:
		return fmt.Errorf("invalid shared instance policy: %s", v.config.Shared.Instance.Policy)
	}

	if v.config.Shared.Instance.ReplaceTimeout.IsSet() &&
		v.config.EffectiveInstancePolicy() == InstancePolicy_Replace &&
		v.config.Shared.Instance.ReplaceTimeout.Duration <= 0 {
		return fmt.Errorf("invalid shared instance replace-timeout configuration, duration must be positive")
	}
	if v.config.Shared.Instance.ReplaceTimeout.IsSet() &&
		v.config.EffectiveInstancePolicy() == InstancePolicy_Replace &&
		v.config.Shared.Instance.ReplaceTimeout.Duration <= minimumInstanceReplaceTimeout {
		return fmt.Errorf("invalid shared instance replace-timeout configuration, duration must be longer than %s", minimumInstanceReplaceTimeout)
	}

	return nil
}

func validateRebuildSuppressionConfig(scope string, suppression RebuildSuppressionConfig) error {
	switch suppression.Mode {
	case "", RebuildSuppressionMode_SizeAndHash, RebuildSuppressionMode_SizeAndMTime, RebuildSuppressionMode_LanguageAwareHash:
	default:
		return fmt.Errorf("invalid %s rebuild-suppression mode: %s", scope, suppression.Mode)
	}

	return nil
}

func (v *validator) validatePortConfigs() error {
	for _, taskConfig := range v.config.Tasks {
		portConfig := taskConfig.Port
		if portConfig == nil {
			continue
		}

		if taskConfig.Type != TaskType_Continuous {
			return fmt.Errorf("invalid task configuration, port can only be configured for continuous tasks: %s", taskConfig.Identifier)
		}

		if portConfig.Env != "" && !isValidEnvName(portConfig.Env) {
			return fmt.Errorf("invalid task configuration, port env is invalid for task '%s': %s", taskConfig.Identifier, portConfig.Env)
		}

		if portConfig.Preferred != 0 && !isValidPort(portConfig.Preferred) {
			return fmt.Errorf("invalid task configuration, preferred port is invalid for task '%s': %d", taskConfig.Identifier, portConfig.Preferred)
		}

		if portConfig.Preferred == 0 && portConfig.Range == nil {
			return fmt.Errorf("invalid task configuration, port needs a preferred port or range for task: %s", taskConfig.Identifier)
		}

		if portConfig.Range != nil {
			if err := validatePortRange(portConfig.Range, taskConfig.Identifier, "port range"); err != nil {
				return err
			}
		}

		if portConfig.Expose == nil || portConfig.Expose.Proxy == nil || !portConfig.Expose.Proxy.Enabled {
			continue
		}

		proxy := portConfig.Expose.Proxy
		if proxy.Env != "" && !isValidEnvName(proxy.Env) {
			return fmt.Errorf("invalid task configuration, proxy env is invalid for task '%s': %s", taskConfig.Identifier, proxy.Env)
		}
		if proxy.Env != "" && isReservedProxyExportName(taskConfig, proxy.Env) {
			return fmt.Errorf("invalid task configuration, proxy env shadows reserved export for task '%s': %s", taskConfig.Identifier, proxy.Env)
		}

		if proxy.Preferred != 0 && !isValidPort(proxy.Preferred) {
			return fmt.Errorf("invalid task configuration, preferred proxy port is invalid for task '%s': %d", taskConfig.Identifier, proxy.Preferred)
		}

		if proxy.Preferred == 0 && proxy.Range == nil {
			return fmt.Errorf("invalid task configuration, proxy needs a preferred port or range for task: %s", taskConfig.Identifier)
		}

		if proxy.Range != nil {
			if err := validatePortRange(proxy.Range, taskConfig.Identifier, "proxy range"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *validator) validateConsumes() error {
	for _, taskConfig := range v.config.Tasks {
		for _, consume := range taskConfig.Consumes {
			if consume.Task == "" {
				return fmt.Errorf("invalid task configuration, consume task is missing: %s", taskConfig.Identifier)
			}

			provider, exists := v.config.Tasks[consume.Task]
			if !exists {
				return fmt.Errorf(
					"invalid task configuration, consumed task '%s' does not exist: %s",
					consume.Task, taskConfig.Identifier,
				)
			}

			if consume.Task == taskConfig.Identifier {
				return fmt.Errorf("invalid task configuration, task cannot consume itself: %s", taskConfig.Identifier)
			}

			if provider.Port == nil {
				return fmt.Errorf("invalid task configuration, consumed task '%s' does not expose a port: %s", consume.Task, taskConfig.Identifier)
			}

			if consume.Mode != "" && consume.Mode != ConsumeMode_Direct && consume.Mode != ConsumeMode_Proxy {
				return fmt.Errorf("invalid task configuration, consume mode is invalid for task '%s': %s", taskConfig.Identifier, consume.Mode)
			}

			if consume.Mode == ConsumeMode_Proxy && !hasEnabledProxy(provider) {
				return fmt.Errorf("invalid task configuration, consumed task '%s' does not expose a proxy: %s", consume.Task, taskConfig.Identifier)
			}

			if consume.OnChange != "" &&
				consume.OnChange != ConsumeOnChange_None &&
				consume.OnChange != ConsumeOnChange_Restart {
				return fmt.Errorf("invalid task configuration, consume on-change is invalid for task '%s': %s", taskConfig.Identifier, consume.OnChange)
			}

			if len(consume.Env) == 0 {
				return fmt.Errorf("invalid task configuration, consume env mappings are missing: %s", taskConfig.Identifier)
			}

			for envName, exportName := range consume.Env {
				if !isValidEnvName(envName) {
					return fmt.Errorf("invalid task configuration, consume env is invalid for task '%s': %s", taskConfig.Identifier, envName)
				}

				if !isValidConsumeExport(provider, exportName) {
					return fmt.Errorf("invalid task configuration, consume export is invalid for task '%s': %s", taskConfig.Identifier, exportName)
				}
			}
		}
	}
	return nil
}

func (v *validator) validateReadiness() error {
	for _, taskConfig := range v.config.Tasks {
		readiness := taskConfig.Readiness
		if readiness == nil {
			continue
		}

		if taskConfig.Type != TaskType_Continuous {
			return fmt.Errorf("invalid task configuration, readiness can only be configured for continuous tasks: %s", taskConfig.Identifier)
		}

		hasPath := strings.TrimSpace(readiness.Path) != ""
		hasURL := strings.TrimSpace(readiness.URL) != ""
		if hasPath == hasURL {
			return fmt.Errorf("invalid task configuration, readiness needs exactly one of path or url: %s", taskConfig.Identifier)
		}

		if hasPath && taskConfig.Port == nil {
			return fmt.Errorf("invalid task configuration, readiness path requires port configuration: %s", taskConfig.Identifier)
		}

		if readiness.Interval.IsSet() && readiness.Interval.Duration <= 0 {
			return fmt.Errorf("invalid task configuration, readiness interval must be positive: %s", taskConfig.Identifier)
		}

		if readiness.Timeout.IsSet() && readiness.Timeout.Duration <= 0 {
			return fmt.Errorf("invalid task configuration, readiness timeout must be positive: %s", taskConfig.Identifier)
		}

		for _, trigger := range readiness.Triggers {
			if trigger.Task == "" {
				return fmt.Errorf("invalid task configuration, readiness trigger task is missing: %s", taskConfig.Identifier)
			}

			target, exists := v.config.Tasks[trigger.Task]
			if !exists {
				return fmt.Errorf("invalid task configuration, readiness trigger task '%s' does not exist: %s", trigger.Task, taskConfig.Identifier)
			}

			if target.Type != TasKType_Build {
				return fmt.Errorf("invalid task configuration, readiness trigger task '%s' must be a build task: %s", trigger.Task, taskConfig.Identifier)
			}
		}
	}

	return nil
}

func (v *validator) validateConsumeCycles() error {
	visited := make(map[string]bool)
	visiting := make(map[string]bool)
	stack := make([]string, 0)

	taskIDs := make([]string, 0, len(v.config.Tasks))
	for taskID := range v.config.Tasks {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)

	var visit func(taskID string) error
	visit = func(taskID string) error {
		if visiting[taskID] {
			return fmt.Errorf("invalid task configuration, consume cycle detected: %s", consumeCyclePath(stack, taskID))
		}
		if visited[taskID] {
			return nil
		}

		visiting[taskID] = true
		stack = append(stack, taskID)

		consumes := append([]ConsumeConfig{}, v.config.Tasks[taskID].Consumes...)
		sort.Slice(consumes, func(i, j int) bool {
			return consumes[i].Task < consumes[j].Task
		})
		for _, consume := range consumes {
			if _, exists := v.config.Tasks[consume.Task]; !exists {
				continue
			}
			if err := visit(consume.Task); err != nil {
				return err
			}
		}

		stack = stack[:len(stack)-1]
		visiting[taskID] = false
		visited[taskID] = true
		return nil
	}

	for _, taskID := range taskIDs {
		if err := visit(taskID); err != nil {
			return err
		}
	}
	return nil
}

func consumeCyclePath(stack []string, repeated string) string {
	start := 0
	for i, taskID := range stack {
		if taskID == repeated {
			start = i
			break
		}
	}

	cycle := append([]string{}, stack[start:]...)
	cycle = append(cycle, repeated)
	return strings.Join(cycle, " -> ")
}

func hasEnabledProxy(taskConfig *TaskConfig) bool {
	return taskConfig != nil &&
		taskConfig.Port != nil &&
		taskConfig.Port.Expose != nil &&
		taskConfig.Port.Expose.Proxy != nil &&
		taskConfig.Port.Expose.Proxy.Enabled
}

func isValidConsumeExport(provider *TaskConfig, exportName string) bool {
	switch ConsumeExport(exportName) {
	case ConsumeExport_Port, ConsumeExport_URL:
		return true
	case ConsumeExport_ProxyURL:
		return hasEnabledProxy(provider)
	}

	serviceName := provider.Identifier
	if provider.Port != nil && provider.Port.Expose != nil && provider.Port.Expose.Name != "" {
		serviceName = provider.Port.Expose.Name
	}

	if exportName == exportEnvName(serviceName, "PORT") || exportName == exportEnvName(serviceName, "URL") {
		return true
	}

	if !hasEnabledProxy(provider) {
		return false
	}

	proxyEnv := provider.Port.Expose.Proxy.Env
	if proxyEnv == "" {
		proxyEnv = exportEnvName(serviceName, "PROXY_URL")
	}
	return exportName == proxyEnv
}

func serviceNameForTask(taskConfig *TaskConfig) string {
	serviceName := taskConfig.Identifier
	if taskConfig.Port != nil && taskConfig.Port.Expose != nil && taskConfig.Port.Expose.Name != "" {
		serviceName = taskConfig.Port.Expose.Name
	}
	return serviceName
}

func validatePortRange(portRange *PortRangeConfig, taskId, label string) error {
	if !isValidPort(portRange.Start) || !isValidPort(portRange.End) {
		return fmt.Errorf("invalid task configuration, %s has invalid port values for task: %s", label, taskId)
	}

	if portRange.Start > portRange.End {
		return fmt.Errorf("invalid task configuration, %s start must be less than or equal to end for task: %s", label, taskId)
	}

	return nil
}

func isValidPort(port int) bool {
	return port >= 1 && port <= 65535
}

func isValidEnvName(name string) bool {
	return envNamePattern.MatchString(name)
}

func isReservedBuiltInExportName(name string) bool {
	switch ConsumeExport(name) {
	case ConsumeExport_Port, ConsumeExport_URL, ConsumeExport_ProxyURL:
		return true
	default:
		return false
	}
}

func isReservedProxyExportName(taskConfig *TaskConfig, name string) bool {
	if isReservedBuiltInExportName(name) {
		return true
	}

	serviceName := serviceNameForTask(taskConfig)
	return name == exportEnvName(serviceName, "PORT") || name == exportEnvName(serviceName, "URL")
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

func validateConfig(config *Config) error {
	validator := createValidator(config)

	validationSteps := []func() error{
		validator.validateTaskId,
		validator.validateFSDebounce,
		validator.validateRebuildSuppression,
		validator.validateInstanceConfig,
		validator.validateWorkingDirectories,
		validator.validateRunCommand,
		validator.validateTaskTypes,
		validator.validateDependencies,
		validator.validateCleanUpTasks,
		validator.validatePortConfigs,
		validator.validateConsumes,
		validator.validateReadiness,
		validator.validateConsumeCycles,
	}

	for _, step := range validationSteps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}
