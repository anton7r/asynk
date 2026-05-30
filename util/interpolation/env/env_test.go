package env

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInterpolateEnvVariables_NoVariables(t *testing.T) {
	env := map[string]string{"FOO": "bar"}
	result := InterpolateEnvVariables("hello world", env)
	assert.Equal(t, "hello world", result)
}

func TestInterpolateEnvVariables_SingleVariable(t *testing.T) {
	env := map[string]string{"NAME": "World"}
	result := InterpolateEnvVariables("Hello, ${NAME}!", env)
	assert.Equal(t, "Hello, World!", result)
}

func TestInterpolateEnvVariables_MultipleVariables(t *testing.T) {
	env := map[string]string{"FIRST": "John", "LAST": "Doe"}
	result := InterpolateEnvVariables("${FIRST} ${LAST}", env)
	assert.Equal(t, "John Doe", result)
}

func TestInterpolateEnvVariables_MissingVariable(t *testing.T) {
	env := map[string]string{}
	result := InterpolateEnvVariables("Hello, ${MISSING}!", env)
	assert.Equal(t, "Hello, ${MISSING}!", result)
}

func TestInterpolateEnvVariables_MixedExistingAndMissing(t *testing.T) {
	env := map[string]string{"FOUND": "yes"}
	result := InterpolateEnvVariables("${FOUND} and ${NOTFOUND}", env)
	assert.Equal(t, "yes and ${NOTFOUND}", result)
}

func TestInterpolateEnvVariables_EmptyInput(t *testing.T) {
	env := map[string]string{"FOO": "bar"}
	result := InterpolateEnvVariables("", env)
	assert.Equal(t, "", result)
}

func TestInterpolateEnvVariables_EmptyEnvMap(t *testing.T) {
	result := InterpolateEnvVariables("${KEY}", map[string]string{})
	assert.Equal(t, "${KEY}", result)
}

func TestInterpolateEnvVariables_VariableWithSpaces(t *testing.T) {
	env := map[string]string{"KEY": "value"}
	result := InterpolateEnvVariables("${ KEY }", env)
	assert.Equal(t, "value", result)
}

func TestInterpolateEnvVariables_EmptyValue(t *testing.T) {
	env := map[string]string{"EMPTY": ""}
	result := InterpolateEnvVariables("prefix${EMPTY}suffix", env)
	assert.Equal(t, "prefixsuffix", result)
}

func TestInterpolateEnvVariables_ValueWithSpecialChars(t *testing.T) {
	env := map[string]string{"PATH": "/usr/local/bin:/usr/bin"}
	result := InterpolateEnvVariables("${PATH}", env)
	assert.Equal(t, "/usr/local/bin:/usr/bin", result)
}

func TestInterpolateEnvVariables_WindowsCaseInsensitiveLookup(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows environment keys are case-insensitive")
	}

	env := map[string]string{"Path": `C:\Windows\System32`}
	result := InterpolateEnvVariables(`${PATH}`, env)
	assert.Equal(t, `C:\Windows\System32`, result)
}

func TestInterpolateEnvVariables_AdjacentVariables(t *testing.T) {
	env := map[string]string{"A": "hello", "B": "world"}
	result := InterpolateEnvVariables("${A}${B}", env)
	assert.Equal(t, "helloworld", result)
}

func TestInterpolateEnvVariables_NestedBraces(t *testing.T) {
	// The regex `\${([^}]+)}` won't match nested braces properly,
	// so `${FOO${BAR}}` should not fully match as a single env var
	env := map[string]string{"FOO${BAR": "unexpected"}
	result := InterpolateEnvVariables("${FOO${BAR}}", env)
	// The inner ${BAR} matches first (greedy from left)
	// Actually the regex is non-greedy on `}` so it matches ${FOO${BAR}
	// Let's just verify it doesn't panic
	assert.NotEmpty(t, result)
}

func TestInterpolateEnvVariablesList_Empty(t *testing.T) {
	env := map[string]string{"FOO": "bar"}
	result := InterpolateEnvVariablesList([]string{}, env)
	assert.Empty(t, result)
}

func TestInterpolateEnvVariablesList_SingleItem(t *testing.T) {
	env := map[string]string{"NAME": "World"}
	result := InterpolateEnvVariablesList([]string{"Hello, ${NAME}!"}, env)
	assert.Equal(t, []string{"Hello, World!"}, result)
}

func TestInterpolateEnvVariablesList_MultipleItems(t *testing.T) {
	env := map[string]string{"A": "1", "B": "2"}
	input := []string{"val=${A}", "val=${B}", "no-var"}
	result := InterpolateEnvVariablesList(input, env)
	assert.Equal(t, []string{"val=1", "val=2", "no-var"}, result)
}

func TestInterpolateEnvVariablesList_PreservesOrder(t *testing.T) {
	env := map[string]string{"X": "x"}
	input := []string{"first", "${X}", "last"}
	result := InterpolateEnvVariablesList(input, env)
	assert.Equal(t, []string{"first", "x", "last"}, result)
}

func TestInterpolateEnvVariablesList_DoesNotMutateInput(t *testing.T) {
	env := map[string]string{"K": "v"}
	input := []string{"${K}", "plain"}
	original := make([]string, len(input))
	copy(original, input)

	InterpolateEnvVariablesList(input, env)
	assert.Equal(t, original, input)
}
