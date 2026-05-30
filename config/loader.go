package config

import (
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/anton7r/asynk/config/util"
	asynkutil "github.com/anton7r/asynk/util"
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

type CleanUpStrategy string

const (
	// Clean up strategy that removes all files matching the glob pattern except the latest one
	CleanUpStrategy_KeepLatest CleanUpStrategy = "keep-latest"
)

type ConsumeMode string

const (
	ConsumeMode_Direct ConsumeMode = "direct"
	ConsumeMode_Proxy  ConsumeMode = "proxy"
)

type ConsumeOnChange string

const (
	ConsumeOnChange_None    ConsumeOnChange = "none"
	ConsumeOnChange_Restart ConsumeOnChange = "restart"
)

type ConsumeExport string

const (
	ConsumeExport_Port     ConsumeExport = "port"
	ConsumeExport_URL      ConsumeExport = "url"
	ConsumeExport_ProxyURL ConsumeExport = "proxy-url"
)

type PortRangeConfig struct {
	Start int `yaml:"start"`
	End   int `yaml:"end"`
}

type ProxyConfig struct {
	Enabled   bool             `yaml:"enabled"`
	Env       string           `yaml:"env"`
	Preferred int              `yaml:"preferred"`
	Range     *PortRangeConfig `yaml:"range"`
}

type PortExposeConfig struct {
	Name  string       `yaml:"name"`
	Proxy *ProxyConfig `yaml:"proxy"`
}

type PortConfig struct {
	Env       string            `yaml:"env"`
	Preferred int               `yaml:"preferred"`
	Range     *PortRangeConfig  `yaml:"range"`
	Expose    *PortExposeConfig `yaml:"expose"`
}

type ConsumeConfig struct {
	Task     string            `yaml:"task"`
	Mode     ConsumeMode       `yaml:"mode"`
	Env      map[string]string `yaml:"env"`
	OnChange ConsumeOnChange   `yaml:"on-change"`
}

type TaskConfig struct {
	Identifier string `yaml:"-"`
	ConfigDir  string `yaml:"-"`
	// Full command with arguments and options
	Type       TaskType    `yaml:"type"`
	Run        RunCommands `yaml:"run"`
	RunWindows RunCommands `yaml:"run-windows"`
	RunLinux   RunCommands `yaml:"run-linux"`
	RunMac     RunCommands `yaml:"run-mac"`
	Cwd        string      `yaml:"cwd"`
	WorkingDir string      `yaml:"working-dir"`
	// Glob pattern to match files to trigger the task
	Include util.GlobArray `yaml:"include"`
	// Glob pattern to exclude files from triggering the task
	Exclude util.GlobArray `yaml:"exclude"`
	// Tasks which this task depends on.
	// Specified tasks should be complete before this task starts.
	Dependencies util.StringArray `yaml:"dependencies"`
	// Environment variables to set for the task.
	Env util.StringArray `yaml:"env"`
	// Optional port assignment for long-running application tasks.
	Port *PortConfig `yaml:"port"`
	// Runtime values this task consumes from other tasks.
	Consumes []ConsumeConfig `yaml:"consumes"`
}

type CleanUpTaskConfig struct {
	Identifier string
	Include    util.GlobArray  `yaml:"include"`
	Exclude    util.GlobArray  `yaml:"exclude"`
	Strategy   CleanUpStrategy `yaml:"strategy"`
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
	ConfigDir    string                        `yaml:"-"`
	Shared       SharedConfig                  `yaml:"shared"`
	Tasks        map[string]*TaskConfig        `yaml:"tasks"`
	CleanUpTasks map[string]*CleanUpTaskConfig `yaml:"cleanup-tasks"`
}

func LoadFromYAML() (*Config, error) {
	return LoadFromYAMLWithFS(asynkutil.NewOSFileSystem())
}

func LoadFromYAMLWithFS(fs asynkutil.FileSystem) (*Config, error) {
	if fs == nil {
		fs = asynkutil.NewOSFileSystem()
	}
	return loadConfigFromYAML("./asynk.yaml", fs)
}

func loadConfigFromYAML(filePath string, fs asynkutil.FileSystem) (*Config, error) {
	if fs == nil {
		fs = asynkutil.NewOSFileSystem()
	}
	data, err := fs.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading YAML file: %w", err)
	}

	configDir, err := filepath.Abs(filepath.Dir(filePath))
	if err != nil {
		return nil, fmt.Errorf("error resolving config directory: %w", err)
	}

	return loadFromBytes(data, configDir)
}

// LoadFromBytes parses config from raw YAML bytes. Useful for testing
// without needing a real file system.
func LoadFromBytes(data []byte) (*Config, error) {
	return loadFromBytes(data, "")
}

func loadFromBytes(data []byte, configDir string) (*Config, error) {
	config := &Config{
		ConfigDir: configDir,
		Shared: SharedConfig{
			Exclude:  util.NewGlobArray(),
			LogLevel: "info",
		},
		Tasks: make(map[string]*TaskConfig),
	}

	err := yaml.Unmarshal(data, config)
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
		taskConfig.ConfigDir = config.ConfigDir
	}

	for taskId, taskConfig := range config.CleanUpTasks {
		taskConfig.Identifier = taskId
	}
}

// persistent hashes could be a useful feature for large projects.
