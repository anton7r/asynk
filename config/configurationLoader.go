package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Configuration struct {
	Tasks map[string]TaskConfiguration
}

type TaskType string

const (
	// Task that is triggered continuously
	// Perfect for web servers
	Continuous TaskType = "continuous"
	// Task that is triggered when files have been modified
	// Perfect for code generation and compilation tasks
	Build TaskType = "build"
)

type TaskConfiguration struct {
	Identifier string
	// Full command with arguments and options
	Type TaskType `yaml:"type"`
	Run  string   `yaml:"run"`
	// Regex pattern to match files to trigger the task
	IncludeRegex string `yaml:"include"`
	// Regex pattern to exclude files from triggering the task
	ExcludeRegex string `yaml:"exclude"`
	// Tasks which this task depends on.
	// Specified tasks should be complete before this task starts.
	Dependencies []string
}

// The tasks id comes from the keys in the YAML file.
type YamlStructure struct {
	Tasks map[string]TaskConfiguration `yaml:"tasks"`
}

func LoadFromYAML() (*Configuration, error) {
	return loadConfigFromYAML("./asynk.yaml")
}

func loadConfigFromYAML(filePath string) (*Configuration, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading YAML file: %w", err)
	}

	var parsedConfiguration *YamlStructure
	err = yaml.Unmarshal(data, &parsedConfiguration)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling YAML data: %w", err)
	}

	err = validateConfiguration(parsedConfiguration)
	if err != nil {
		return nil, err
	}

	return convertYamlStructureToTaskConfigurations(parsedConfiguration), nil
}

func convertYamlStructureToTaskConfigurations(parsedConfiguration *YamlStructure) Configuration {
	var taskConfigurations []TaskConfiguration
	for taskId, taskConfig := range parsedConfiguration.Tasks {
		taskConfig.Identifier = taskId
		taskConfigurations = append(taskConfigurations, taskConfig)
	}
	return taskConfigurations
}

// Validate the task configuration, checking for duplicate identifiers and ensuring required fields are present.
func validateConfiguration(parsedConfiguration *YamlStructure) error {
	taskIdMap := make(map[string]bool)
	for taskId, taskConfig := range *parsedConfiguration {
		if taskId == "" || taskConfig.Run == "" {
			return fmt.Errorf("invalid task configuration, identifier or run command is missing: %s", taskId)
		}

		if taskIdMap[taskId] {
			return fmt.Errorf("duplicate task identifier: %s", taskId)
		}

		taskIdMap[taskId] = true
	}
	return nil
}
