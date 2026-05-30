package config

import (
	"fmt"
	"strings"
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

func validateConfig(config *Config) error {
	validator := createValidator(config)

	validationSteps := []func() error{
		validator.validateTaskId,
		validator.validateWorkingDirectories,
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
