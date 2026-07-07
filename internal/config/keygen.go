package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// GenerateKey returns a random key like "<prefix>-9f3ec2a1..." (32 hex chars).
func GenerateKey(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return prefix + "-" + hex.EncodeToString(b)
}
