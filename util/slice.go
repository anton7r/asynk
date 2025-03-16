package util

type Array[T any] []T

func CollectMapKeys[Value any](m map[string]Value) Array[string] {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
