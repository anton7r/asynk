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
	Type       TaskType         `yaml:"type"`
	Run        util.StringArray `yaml:"run"`
	RunWindows util.StringArray `yaml:"run-windows"`
	RunLinux   util.StringArray `yaml:"run-linux"`
	RunMac     util.StringArray `yaml:"run-mac"`
	// Glob pattern to match files to trigger the task
	Include util.GlobArray `yaml:"include"`
	// Glob pattern to exclude files from triggering the task
	Exclude util.GlobArray `yaml:"exclude"`
	// Tasks which this task depends on.
	// Specified tasks should be complete before this task starts.
	Dependencies util.StringArray `yaml:"dependencies"`
	// Environment variables to set for the task.
	Env util.StringArray `yaml:"env"`
}

type SharedConfig struct {
	// Shared configuration settings
	Exclude util.GlobArray `yaml:"exclude"`
	// TODO: this is not yet implemented
	ReloadOnConfigChange bool             `yaml:"reload-on-config-change"`
	LogLevel             string           `yaml:"log-level"`
	EnvFiles             util.StringArray `yaml:"env-files"`
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

	config := &Config{
		Shared: SharedConfig{
			Exclude:  util.NewGlobArray(),
			LogLevel: "info",
		},
		Tasks: make(map[string]*TaskConfig),
	}

	err = yaml.Unmarshal(data, config)
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

// persistent hashes could be a useful feature for large projects.
