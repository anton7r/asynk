package util

import (
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a YAML duration that distinguishes an omitted value from 0.
type Duration struct {
	Duration time.Duration
	Set      bool
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return err
	}

	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return err
	}

	d.Duration = parsed
	d.Set = true
	return nil
}

func (d Duration) IsSet() bool {
	return d.Set
}
