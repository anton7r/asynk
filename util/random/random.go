package random

import (
	"crypto/rand"
	"encoding/base64"
)

// Create a cryptographically secure random byte slice of length n
func bytes(n uint32) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, err
	}

	return b, nil
}

// Url safe base64 encoder
func encodeBase64Url(src []byte) string {
	return base64.RawURLEncoding.EncodeToString(src)
}

func RandomBase64String(n uint32) (string, error) {
	b, err := bytes(n)
	if err != nil {
		return "", err
	}

	return encodeBase64Url(b), nil
}
