package util

import "gopkg.in/yaml.v3"

// StringArray is a custom type for handling YAML arrays of strings.
// It can unmarshal either a single string or an array of strings.
type StringArray []string

func (a *StringArray) UnmarshalYAML(value *yaml.Node) error {
	var multi []string
	err := value.Decode(&multi)
	if err != nil {
		var single string
		err := value.Decode(&single)
		if err != nil {
			return err
		}
		*a = []string{single}
	} else {
		*a = multi
	}
	return nil
}
