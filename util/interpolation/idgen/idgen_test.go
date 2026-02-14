package idgen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewGenIDInterpolator(t *testing.T) {
	g := NewGenIDInterpolator()
	assert.NotNil(t, g)
	assert.Equal(t, "", g.buildID)
}

func TestGenIDInterpolator_Interpolate_NoPattern(t *testing.T) {
	g := NewGenIDInterpolator()
	result, err := g.Interpolate("hello world")
	assert.NoError(t, err)
	assert.Equal(t, "hello world", result)
}

func TestGenIDInterpolator_Interpolate_GenID(t *testing.T) {
	g := NewGenIDInterpolator()
	result, err := g.Interpolate("build-@{GEN_ID}")
	assert.NoError(t, err)
	// Should replace @{GEN_ID} with a non-empty random string
	assert.NotEqual(t, "build-@{GEN_ID}", result)
	assert.NotEmpty(t, result)
	// The prefix should remain
	assert.Contains(t, result, "build-")
	// Should not contain the pattern marker anymore
	assert.NotContains(t, result, "@{GEN_ID}")
}

func TestGenIDInterpolator_Interpolate_UnknownKey(t *testing.T) {
	g := NewGenIDInterpolator()
	result, err := g.Interpolate("@{UNKNOWN}")
	assert.NoError(t, err)
	assert.Equal(t, "@{UNKNOWN}", result)
}

func TestGenIDInterpolator_Interpolate_MixedKnownAndUnknown(t *testing.T) {
	g := NewGenIDInterpolator()
	result, err := g.Interpolate("id=@{GEN_ID} other=@{OTHER}")
	assert.NoError(t, err)
	assert.NotContains(t, result, "@{GEN_ID}")
	assert.Contains(t, result, "@{OTHER}")
	assert.Contains(t, result, "id=")
	assert.Contains(t, result, "other=")
}

func TestGenIDInterpolator_Interpolate_EmptyInput(t *testing.T) {
	g := NewGenIDInterpolator()
	result, err := g.Interpolate("")
	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestGenIDInterpolator_Interpolate_MultipleGenIDs(t *testing.T) {
	g := NewGenIDInterpolator()
	result, err := g.Interpolate("@{GEN_ID}-@{GEN_ID}")
	assert.NoError(t, err)
	assert.NotContains(t, result, "@{GEN_ID}")
	// Should contain a dash separating two IDs
	assert.Contains(t, result, "-")
}

func TestGenIDInterpolator_WithPresetBuildID(t *testing.T) {
	g := &GenIDInterpolator{buildID: "fixed-id-123"}
	result, err := g.Interpolate("build-@{GEN_ID}")
	assert.NoError(t, err)
	assert.Equal(t, "build-fixed-id-123", result)
}

func TestGenIDInterpolator_WithPresetBuildID_UnknownKey(t *testing.T) {
	g := &GenIDInterpolator{buildID: "fixed-id-123"}
	result, err := g.Interpolate("@{NOPE}")
	assert.NoError(t, err)
	assert.Equal(t, "@{NOPE}", result)
}

func TestGenIDInterpolator_Interpolate_NoPatternInPlainText(t *testing.T) {
	g := NewGenIDInterpolator()
	input := "no patterns here at all"
	result, err := g.Interpolate(input)
	assert.NoError(t, err)
	assert.Equal(t, input, result)
}

func TestGenIDInterpolator_Interpolate_PatternWithSpaces(t *testing.T) {
	g := &GenIDInterpolator{buildID: "myid"}
	// The key will be trimmed, so "  GEN_ID  " becomes "GEN_ID"
	result, err := g.Interpolate("@{  GEN_ID  }")
	assert.NoError(t, err)
	assert.Equal(t, "myid", result)
}
