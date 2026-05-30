package task

import (
	"runtime"
	"sort"
	"strings"

	envUtil "github.com/anton7r/asynk/util/interpolation/env"
)

func MergeEnv(parentEnv []string, envFileValues map[string]string, taskEnv []string) map[string]string {
	env := newEnvBuilder()

	for _, entry := range parentEnv {
		key, value, ok := parseEnvEntry(entry)
		if ok {
			env.set(key, value)
		}
	}

	for key, value := range envFileValues {
		env.set(key, value)
	}

	for _, entry := range taskEnv {
		key, value, ok := parseEnvEntry(entry)
		if !ok {
			continue
		}

		env.set(key, envUtil.InterpolateEnvVariables(value, env.values))
	}

	return env.values
}

func EnvMapToList(env map[string]string) []string {
	result := make([]string, 0, len(env))
	for key, value := range env {
		result = append(result, key+"="+value)
	}

	sort.Strings(result)
	return result
}

type envBuilder struct {
	values        map[string]string
	normalizedKey map[string]string
}

func newEnvBuilder() *envBuilder {
	return &envBuilder{
		values:        make(map[string]string),
		normalizedKey: make(map[string]string),
	}
}

func (e *envBuilder) set(key string, value string) {
	normalized := normalizeEnvKey(key)
	if previousKey, exists := e.normalizedKey[normalized]; exists && previousKey != key {
		delete(e.values, previousKey)
	}

	e.normalizedKey[normalized] = key
	e.values[key] = value
}

func parseEnvEntry(entry string) (string, string, bool) {
	key, value, ok := strings.Cut(entry, "=")
	if !ok || key == "" {
		return "", "", false
	}

	return key, value, true
}

func normalizeEnvKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}

	return key
}
