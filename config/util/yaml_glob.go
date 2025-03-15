package util

import (
	"github.com/gobwas/glob"
	"gopkg.in/yaml.v3"
)

type Glob struct {
	glob    glob.Glob
	pattern string
}

func NewGlob(pattern string) (*Glob, error) {
	g, err := glob.Compile(pattern)
	return &Glob{glob: g, pattern: pattern}, err
}

func (g *Glob) UnmarshalYAML(value *yaml.Node) error {
	err := value.Decode(&g.pattern)
	if err != nil {
		return err
	}

	g.glob, err = glob.Compile(g.pattern)
	return err
}

func (g Glob) Match(path string) bool {
	return g.glob.Match(path)
}

func (g Glob) String() string {
	return g.pattern
}

// DirMatcher returns a new Glob that matches directories at the given level.
/*
func (g Glob) DirMatcher(dirLevel int) (*Glob, error) {
	parts := strings.Split(g.pattern, "/")
	if len(parts) < dirLevel {
		last := parts[len(parts)-1]
		// Last part should obviously match any directory.
		if last == "**" {
			return &g, nil
		}

		return nil, nil
	}

	return NewGlob(strings.Join(parts[:dirLevel], "/"))
}
*/
