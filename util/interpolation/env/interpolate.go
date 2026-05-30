package env

import (
	"regexp"
	"runtime"
	"strings"
)

var envVarPattern = regexp.MustCompile(`\${([^}]+)}`)

func InterpolateEnvVariablesList(input []string, env map[string]string) []string {
	result := make([]string, len(input))
	for i, v := range input {
		result[i] = InterpolateEnvVariables(v, env)
	}
	return result
}

// interpolateEnvVariables interpolates environment variables in a given string
// and replaces them with their corresponding values from the provided map.
// If an environment variable is not found, it remains unchanged in the result.
// The pattern is ${KEY}, where KEY is the name of the environment variable.
// For example, if input is "Hello, ${WORLD}!" and env is {"WORLD": "World"}, the result will be "Hello, World!".
// If the environment variable is not found in the map, the original pattern is returned.
// This function uses a regular expression to find and replace environment variables.
func InterpolateEnvVariables(input string, env map[string]string) string {
	return envVarPattern.ReplaceAllStringFunc(input, func(match string) string {
		key := strings.Trim(match[2:len(match)-1], " ")
		if value, exists := lookupEnvValue(env, key); exists {
			return value
		}
		return match // Return the original pattern if the env variable is not found
	})
}

func lookupEnvValue(env map[string]string, key string) (string, bool) {
	value, exists := env[key]
	if exists || runtime.GOOS != "windows" {
		return value, exists
	}

	for envKey, envValue := range env {
		if strings.EqualFold(envKey, key) {
			return envValue, true
		}
	}

	return "", false
}
