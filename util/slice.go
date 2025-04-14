package util

type Array[T any] []T

func CollectMapKeys[Value any](m map[string]Value) Array[string] {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func Empty(arr []string) bool {
	if len(arr) == 0 {
		return true
	}

	for _, str := range arr {
		if str != "" {
			return false
		}
	}

	return true
}
