package config

import (
	"fmt"

	"github.com/anton7r/asynk/config/util"
	"gopkg.in/yaml.v3"
)

type CommandConfig struct {
	Command string           `yaml:"command"`
	Args    util.StringArray `yaml:"args"`
	Shell   bool             `yaml:"shell"`
	Legacy  bool             `yaml:"-"`
}

type RunCommands []CommandConfig

func NewLegacyRunCommands(commands ...string) RunCommands {
	result := make(RunCommands, 0, len(commands))
	for _, command := range commands {
		result = append(result, CommandConfig{Command: command, Legacy: true})
	}

	return result
}

func (commands RunCommands) LegacyStrings() []string {
	result := make([]string, 0, len(commands))
	for _, command := range commands {
		result = append(result, command.Command)
	}

	return result
}

func (commands RunCommands) IsEmpty() bool {
	if len(commands) == 0 {
		return true
	}

	for _, command := range commands {
		if command.Command != "" {
			return false
		}
	}

	return true
}

func (commands *RunCommands) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		command, err := commandFromScalar(value)
		if err != nil {
			return err
		}
		*commands = []CommandConfig{command}
		return nil
	case yaml.MappingNode:
		command, err := commandFromMapping(value)
		if err != nil {
			return err
		}
		*commands = []CommandConfig{command}
		return nil
	case yaml.SequenceNode:
		result := make([]CommandConfig, 0, len(value.Content))
		for _, item := range value.Content {
			command, err := commandFromNode(item)
			if err != nil {
				return err
			}
			result = append(result, command)
		}
		*commands = result
		return nil
	default:
		return fmt.Errorf("run command must be a string, mapping, or sequence")
	}
}

func commandFromNode(value *yaml.Node) (CommandConfig, error) {
	switch value.Kind {
	case yaml.ScalarNode:
		return commandFromScalar(value)
	case yaml.MappingNode:
		return commandFromMapping(value)
	default:
		return CommandConfig{}, fmt.Errorf("run command item must be a string or mapping")
	}
}

func commandFromScalar(value *yaml.Node) (CommandConfig, error) {
	var command string
	if err := value.Decode(&command); err != nil {
		return CommandConfig{}, err
	}

	return CommandConfig{Command: command, Legacy: true}, nil
}

func commandFromMapping(value *yaml.Node) (CommandConfig, error) {
	var command CommandConfig
	if err := value.Decode(&command); err != nil {
		return CommandConfig{}, err
	}

	command.Legacy = false
	return command, nil
}
