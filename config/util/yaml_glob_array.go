package util

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type GlobArray []*Glob

func NewGlobArray(patterns ...string) GlobArray {
	g := new(GlobArray)
	for _, pattern := range patterns {
		glob, err := NewGlob(pattern)
		if err != nil {
			return nil
		}
		*g = append(*g, glob)
	}

	return *g
}

func (g *GlobArray) UnmarshalYAML(value *yaml.Node) error {
	var multi []string
	err := value.Decode(&multi)
	if err != nil {
		var single string
		err := value.Decode(&single)
		if err != nil {
			return err
		}
		multi = []string{single}
	}

	for _, pattern := range multi {
		glob, err := NewGlob(pattern)
		if err != nil {
			return err
		}

		*g = append(*g, glob)
	}

	return nil
}

func (globs GlobArray) AnyMatches(path string) bool {
	if len(globs) == 0 {
		return false
	}

	for _, glob := range globs {
		if glob.Match(path) {
			return true
		}
	}

	return false
}

func (globs GlobArray) String() string {
	var patterns []string
	for _, glob := range globs {
		patterns = append(patterns, glob.String())
	}

	return "[" + strings.Join(patterns, ", ") + "]"
}
