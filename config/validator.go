package config

import (
	"fmt"
	"regexp"
	"strings"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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

		if err := validateRequiredPortRange(portConfig.Range, taskConfig.Identifier, "port range"); err != nil {
			return err
		}

		if portConfig.Expose == nil || portConfig.Expose.Proxy == nil || !portConfig.Expose.Proxy.Enabled {
			continue
		}

		proxy := portConfig.Expose.Proxy
		if proxy.Env != "" && !isValidEnvName(proxy.Env) {
			return fmt.Errorf("invalid task configuration, proxy env is invalid for task '%s': %s", taskConfig.Identifier, proxy.Env)
		}
		if proxy.Env != "" && isReservedBuiltInExportName(proxy.Env) {
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

				switch ConsumeExport(exportName) {
				case ConsumeExport_Port, ConsumeExport_URL:
				case ConsumeExport_ProxyURL:
					if !hasEnabledProxy(provider) {
						return fmt.Errorf("invalid task configuration, consumed task '%s' does not expose proxy-url: %s", consume.Task, taskConfig.Identifier)
					}
				default:
					return fmt.Errorf("invalid task configuration, consume export is invalid for task '%s': %s", taskConfig.Identifier, exportName)
				}
			}
		}
	}
	return nil
}

func hasEnabledProxy(taskConfig *TaskConfig) bool {
	return taskConfig != nil &&
		taskConfig.Port != nil &&
		taskConfig.Port.Expose != nil &&
		taskConfig.Port.Expose.Proxy != nil &&
		taskConfig.Port.Expose.Proxy.Enabled
}

func validateRequiredPortRange(portRange *PortRangeConfig, taskId, label string) error {
	if portRange == nil {
		return fmt.Errorf("invalid task configuration, %s is missing: %s", label, taskId)
	}

	return validatePortRange(portRange, taskId, label)
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

func validateConfig(config *Config) error {
	validator := createValidator(config)

	validationSteps := []func() error{
		validator.validateTaskId,
		validator.validateWorkingDirectories,
		validator.validateRunCommand,
		validator.validateTaskTypes,
		validator.validateDependencies,
		validator.validateCleanUpTasks,
		validator.validatePortConfigs,
		validator.validateConsumes,
	}

	for _, step := range validationSteps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}
