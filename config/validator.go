package config

import (
	"fmt"

	"github.com/anton7r/asynk/util"
)

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
		runEmpty := util.Empty(taskConfig.Run)
		runWindowsEmpty := util.Empty(taskConfig.RunWindows)
		runLinuxEmpty := util.Empty(taskConfig.RunLinux)
		runMacEmpty := util.Empty(taskConfig.RunMac)

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

func validateConfig(config *Config) error {
	validator := createValidator(config)

	validationSteps := []func() error{
		validator.validateTaskId,
		validator.validateRunCommand,
		validator.validateTaskTypes,
		validator.validateDependencies,
		validator.validateCleanUpTasks,
	}

	for _, step := range validationSteps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}
