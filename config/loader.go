package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"asynk/config/util"
)

type TaskType string

const (
	// Task that is triggered continuously
	// Perfect for web servers
	TaskType_Continuous TaskType = "continuous"
	// Task that is triggered when files have been modified
	// Perfect for code generation and compilation tasks
	TasKType_Build TaskType = "build"
)

type TaskConfig struct {
	Identifier string
	// Full command with arguments and options
	Type TaskType         `yaml:"type"`
	Run  util.StringArray `yaml:"run"`
	// Regex pattern to match files to trigger the task
	IncludeRegex string `yaml:"include"`
	// Regex pattern to exclude files from triggering the task
	ExcludeRegex string `yaml:"exclude"`
	// Tasks which this task depends on.
	// Specified tasks should be complete before this task starts.
	Dependencies []string `yaml:"dependencies"`
}

type SharedConfig struct {
	// Shared configuration settings
	ExcludedDirs         []string `yaml:"excluded-dirs"`
	ReloadOnConfigChange bool     `yaml:"reload-on-config-change"`
}

// The tasks id comes from the keys in the YAML file.
type Config struct {
	Shared SharedConfig           `yaml:"shared"`
	Tasks  map[string]*TaskConfig `yaml:"tasks"`
}

func LoadFromYAML() (*Config, error) {
	return loadConfigFromYAML("./asynk.yaml")
}

func loadConfigFromYAML(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading YAML file: %w", err)
	}

	var config *Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling YAML data: %w", err)
	}

	fillTaskIds(config)

	err = validateConfig(config)
	if err != nil {
		return nil, err
	}

	return config, nil
}

func fillTaskIds(config *Config) {
	for taskId, taskConfig := range config.Tasks {
		taskConfig.Identifier = taskId
	}
}
