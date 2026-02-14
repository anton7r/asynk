package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

// --- StringArray tests ---

func TestStringArray_UnmarshalYAML_SingleString(t *testing.T) {
	input := `value: "hello"`
	var out struct {
		Value StringArray `yaml:"value"`
	}
	err := yaml.Unmarshal([]byte(input), &out)
	assert.NoError(t, err)
	assert.Equal(t, StringArray{"hello"}, out.Value)
}

func TestStringArray_UnmarshalYAML_MultipleStrings(t *testing.T) {
	input := `value:
  - "one"
  - "two"
  - "three"`
	var out struct {
		Value StringArray `yaml:"value"`
	}
	err := yaml.Unmarshal([]byte(input), &out)
	assert.NoError(t, err)
	assert.Equal(t, StringArray{"one", "two", "three"}, out.Value)
}

func TestStringArray_UnmarshalYAML_EmptyArray(t *testing.T) {
	input := `value: []`
	var out struct {
		Value StringArray `yaml:"value"`
	}
	err := yaml.Unmarshal([]byte(input), &out)
	assert.NoError(t, err)
	assert.Equal(t, StringArray{}, out.Value)
}

func TestStringArray_UnmarshalYAML_InvalidType(t *testing.T) {
	input := `value: 123`
	var out struct {
		Value StringArray `yaml:"value"`
	}
	// 123 decodes as a string "123" by yaml, so this should still work
	err := yaml.Unmarshal([]byte(input), &out)
	assert.NoError(t, err)
	assert.Equal(t, StringArray{"123"}, out.Value)
}

// --- Glob tests ---

func TestNewGlob(t *testing.T) {
	g, err := NewGlob("*.go")
	assert.NoError(t, err)
	assert.NotNil(t, g)
	assert.Equal(t, "*.go", g.String())
}

func TestNewGlob_InvalidPattern(t *testing.T) {
	g, err := NewGlob("[invalid")
	assert.Error(t, err)
	assert.NotNil(t, g) // struct is still returned, just with error
}

func TestGlob_Match(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"exact match", "main.go", "main.go", true},
		{"wildcard extension", "*.go", "main.go", true},
		{"wildcard extension no match", "*.go", "main.rs", false},
		{"directory wildcard", "src/**/*.go", "src/pkg/main.go", true},
		{"directory wildcard no match", "src/**/*.go", "lib/main.go", false},
		{"double star matches deep", "**/*.go", "a/b/c/main.go", true},
		{"single star matches across slash in gobwas", "*.go", "dir/main.go", true},
		{"prefix pattern", "src/*", "src/file.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := NewGlob(tt.pattern)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, g.Match(tt.path))
		})
	}
}

func TestGlob_String(t *testing.T) {
	g, err := NewGlob("**/*.txt")
	assert.NoError(t, err)
	assert.Equal(t, "**/*.txt", g.String())
}

func TestGlob_UnmarshalYAML(t *testing.T) {
	input := `pattern: "*.go"`
	var out struct {
		Pattern Glob `yaml:"pattern"`
	}
	err := yaml.Unmarshal([]byte(input), &out)
	assert.NoError(t, err)
	assert.Equal(t, "*.go", out.Pattern.String())
	assert.True(t, out.Pattern.Match("main.go"))
	assert.False(t, out.Pattern.Match("main.rs"))
}

// --- GlobArray tests ---

func TestNewGlobArray(t *testing.T) {
	ga := NewGlobArray("*.go", "*.txt")
	assert.Len(t, ga, 2)
	assert.Equal(t, "*.go", ga[0].String())
	assert.Equal(t, "*.txt", ga[1].String())
}

func TestNewGlobArray_Empty(t *testing.T) {
	ga := NewGlobArray()
	assert.Len(t, ga, 0)
}

func TestNewGlobArray_InvalidPattern(t *testing.T) {
	ga := NewGlobArray("*.go", "[invalid")
	assert.Nil(t, ga)
}

func TestGlobArray_AnyMatches(t *testing.T) {
	ga := NewGlobArray("*.go", "*.txt")

	assert.True(t, ga.AnyMatches("main.go"))
	assert.True(t, ga.AnyMatches("readme.txt"))
	assert.False(t, ga.AnyMatches("main.rs"))
}

func TestGlobArray_AnyMatches_Empty(t *testing.T) {
	ga := NewGlobArray()
	assert.False(t, ga.AnyMatches("anything"))
}

func TestGlobArray_AnyMatches_DeepPaths(t *testing.T) {
	ga := NewGlobArray("**/*.go")
	assert.True(t, ga.AnyMatches("src/pkg/main.go"))
	assert.False(t, ga.AnyMatches("src/pkg/main.rs"))
}

func TestGlobArray_String(t *testing.T) {
	ga := NewGlobArray("*.go", "*.txt")
	assert.Equal(t, "[*.go, *.txt]", ga.String())
}

func TestGlobArray_String_Empty(t *testing.T) {
	ga := NewGlobArray()
	assert.Equal(t, "[]", ga.String())
}

func TestGlobArray_UnmarshalYAML_SingleString(t *testing.T) {
	input := `patterns: "*.go"`
	var out struct {
		Patterns GlobArray `yaml:"patterns"`
	}
	err := yaml.Unmarshal([]byte(input), &out)
	assert.NoError(t, err)
	assert.Len(t, out.Patterns, 1)
	assert.True(t, out.Patterns.AnyMatches("main.go"))
}

func TestGlobArray_UnmarshalYAML_MultipleStrings(t *testing.T) {
	input := `patterns:
  - "*.go"
  - "*.txt"`
	var out struct {
		Patterns GlobArray `yaml:"patterns"`
	}
	err := yaml.Unmarshal([]byte(input), &out)
	assert.NoError(t, err)
	assert.Len(t, out.Patterns, 2)
	assert.True(t, out.Patterns.AnyMatches("main.go"))
	assert.True(t, out.Patterns.AnyMatches("file.txt"))
	assert.False(t, out.Patterns.AnyMatches("file.rs"))
}
