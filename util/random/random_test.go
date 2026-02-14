package random

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandomBase64String_ReturnsNonEmpty(t *testing.T) {
	result, err := RandomBase64String(16)
	assert.NoError(t, err)
	assert.NotEmpty(t, result)
}

func TestRandomBase64String_IsValidBase64URL(t *testing.T) {
	result, err := RandomBase64String(32)
	assert.NoError(t, err)

	// Should be valid raw URL-safe base64
	_, err = base64.RawURLEncoding.DecodeString(result)
	assert.NoError(t, err)
}

func TestRandomBase64String_DifferentLengths(t *testing.T) {
	tests := []uint32{1, 8, 16, 32, 64, 128}
	for _, n := range tests {
		result, err := RandomBase64String(n)
		assert.NoError(t, err)
		assert.NotEmpty(t, result)

		// Decode and verify raw bytes length matches requested n
		decoded, err := base64.RawURLEncoding.DecodeString(result)
		assert.NoError(t, err)
		assert.Len(t, decoded, int(n))
	}
}

func TestRandomBase64String_ProducesUniqueValues(t *testing.T) {
	// Generate multiple random strings and verify they're all different
	// (probability of collision is astronomically low)
	results := make(map[string]bool)
	for i := 0; i < 100; i++ {
		result, err := RandomBase64String(16)
		assert.NoError(t, err)
		results[result] = true
	}
	assert.Len(t, results, 100, "Expected 100 unique random strings")
}

func TestRandomBase64String_ZeroLength(t *testing.T) {
	result, err := RandomBase64String(0)
	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestRandomBase64String_SmallSize(t *testing.T) {
	result, err := RandomBase64String(1)
	assert.NoError(t, err)
	assert.NotEmpty(t, result)

	decoded, err := base64.RawURLEncoding.DecodeString(result)
	assert.NoError(t, err)
	assert.Len(t, decoded, 1)
}

func TestRandomBase64String_LargeSize(t *testing.T) {
	result, err := RandomBase64String(256)
	assert.NoError(t, err)
	assert.NotEmpty(t, result)

	decoded, err := base64.RawURLEncoding.DecodeString(result)
	assert.NoError(t, err)
	assert.Len(t, decoded, 256)
}
