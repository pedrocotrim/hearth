package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

func New() (string, error) {
	return randomHex(32)
}

func NewID() (string, error) {
	return randomHex(16)
}

func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func Valid(raw string) bool {
	return hexOK(raw, 64)
}

func ValidID(id string) bool {
	return hexOK(id, 32)
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func hexOK(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
