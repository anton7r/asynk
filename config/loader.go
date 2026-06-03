package config

import (
	"fmt"
	"path/filepath"
	"time"

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

const DefaultFSDebounce = 200 * time.Millisecond

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

type RebuildSuppressionMode string

const (
	RebuildSuppressionMode_SizeAndHash       RebuildSuppressionMode = "size-and-hash"
	RebuildSuppressionMode_SizeAndMTime      RebuildSuppressionMode = "size-and-mtime"
	RebuildSuppressionMode_LanguageAwareHash RebuildSuppressionMode = "language-aware-hash"
)

type InstancePolicy string

const (
	InstancePolicy_Allow   InstancePolicy = "allow"
	InstancePolicy_Block   InstancePolicy = "block"
	InstancePolicy_Replace InstancePolicy = "replace"
)

const DefaultInstanceReplaceTimeout = 6 * time.Second
const DefaultReadinessInterval = 250 * time.Millisecond
const DefaultReadinessTimeout = 30 * time.Second

type RebuildSuppressionConfig struct {
	Enabled *bool                  `yaml:"enabled"`
	Mode    RebuildSuppressionMode `yaml:"mode"`
}

type EffectiveRebuildSuppressionConfig struct {
	Enabled bool
	Mode    RebuildSuppressionMode
}

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

type ReadinessTriggerConfig struct {
	Task    string         `yaml:"task"`
	Include util.GlobArray `yaml:"include"`
	Exclude util.GlobArray `yaml:"exclude"`
}

type ReadinessConfig struct {
	Path     string                   `yaml:"path"`
	URL      string                   `yaml:"url"`
	Interval util.Duration            `yaml:"interval"`
	Timeout  util.Duration            `yaml:"timeout"`
	Triggers []ReadinessTriggerConfig `yaml:"triggers"`
}

type TaskConfig struct {
	Identifier string `yaml:"-"`
	ConfigDir  string `yaml:"-"`
	// Full command with arguments and options
	Type       TaskType    `yaml:"type"`
	AutoStart  *bool       `yaml:"auto-start"`
	Run        RunCommands `yaml:"run"`
	RunWindows RunCommands `yaml:"run-windows"`
	RunLinux   RunCommands `yaml:"run-linux"`
	RunMac     RunCommands `yaml:"run-mac"`
	Cwd        string      `yaml:"cwd"`
	WorkingDir string      `yaml:"working-dir"`
	// Debounce duration for filesystem events affecting this task.
	FSDebounce util.Duration `yaml:"fs-debounce"`
	// Optional suppression of rebuilds when effective task inputs did not change.
	RebuildSuppression RebuildSuppressionConfig `yaml:"rebuild-suppression"`
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
	// Optional HTTP readiness check for continuous tasks.
	Readiness *ReadinessConfig `yaml:"readiness"`
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
	ReloadOnConfigChange bool                     `yaml:"reload-on-config-change"`
	LogLevel             string                   `yaml:"log-level"`
	EnvFiles             util.StringArray         `yaml:"env-files"`
	FSDebounce           util.Duration            `yaml:"fs-debounce"`
	RebuildSuppression   RebuildSuppressionConfig `yaml:"rebuild-suppression"`
	Instance             InstanceConfig           `yaml:"instance"`
}

type InstanceConfig struct {
	Policy         InstancePolicy `yaml:"policy"`
	ReplaceTimeout util.Duration  `yaml:"replace-timeout"`
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

func (config *Config) EffectiveFSDebounceForTask(taskId string) time.Duration {
	if taskConfig, exists := config.Tasks[taskId]; exists && taskConfig.FSDebounce.IsSet() {
		return taskConfig.FSDebounce.Duration
	}

	if config.Shared.FSDebounce.IsSet() {
		return config.Shared.FSDebounce.Duration
	}

	return DefaultFSDebounce
}

func (config *Config) TaskFSDebounces() map[string]time.Duration {
	debounces := make(map[string]time.Duration, len(config.Tasks))
	for taskId := range config.Tasks {
		debounces[taskId] = config.EffectiveFSDebounceForTask(taskId)
	}
	return debounces
}

func (config *Config) EffectiveRebuildSuppressionForTask(taskId string) EffectiveRebuildSuppressionConfig {
	effective := EffectiveRebuildSuppressionConfig{
		Enabled: false,
		Mode:    RebuildSuppressionMode_SizeAndHash,
	}

	shared := config.Shared.RebuildSuppression
	if shared.Enabled != nil {
		effective.Enabled = *shared.Enabled
	}
	if shared.Mode != "" {
		effective.Mode = shared.Mode
	}

	taskConfig := config.Tasks[taskId]
	if taskConfig == nil {
		return effective
	}

	taskSuppression := taskConfig.RebuildSuppression
	if taskSuppression.Enabled != nil {
		effective.Enabled = *taskSuppression.Enabled
	}
	if taskSuppression.Mode != "" {
		effective.Mode = taskSuppression.Mode
	}

	return effective
}

func (config *Config) RebuildSuppressionTasks() map[string]bool {
	tasks := make(map[string]bool)
	for taskId := range config.Tasks {
		if config.EffectiveRebuildSuppressionForTask(taskId).Enabled {
			tasks[taskId] = true
		}
	}
	return tasks
}

func (config *Config) EffectiveInstancePolicy() InstancePolicy {
	if config.Shared.Instance.Policy == "" {
		return InstancePolicy_Allow
	}
	return config.Shared.Instance.Policy
}

func (config *Config) EffectiveInstanceReplaceTimeout() time.Duration {
	if config.Shared.Instance.ReplaceTimeout.IsSet() {
		return config.Shared.Instance.ReplaceTimeout.Duration
	}
	return DefaultInstanceReplaceTimeout
}

func (taskConfig *TaskConfig) AutoStartEnabled() bool {
	return taskConfig == nil || taskConfig.AutoStart == nil || *taskConfig.AutoStart
}

func (readiness *ReadinessConfig) EffectiveInterval() time.Duration {
	if readiness != nil && readiness.Interval.IsSet() {
		return readiness.Interval.Duration
	}
	return DefaultReadinessInterval
}

func (readiness *ReadinessConfig) EffectiveTimeout() time.Duration {
	if readiness != nil && readiness.Timeout.IsSet() {
		return readiness.Timeout.Duration
	}
	return DefaultReadinessTimeout
}
