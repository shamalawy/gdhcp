package users

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// generateAPIToken creates a new raw API token in the amendment A14 format
// ("gdhcp_<random 32 bytes base64url>") and returns it alongside the SHA-256
// hex digest that CreateAPIToken persists (the raw value itself is never
// stored — bcrypt can't support the deterministic lookup ValidateAPIToken
// needs, and SHA-256 hex enables a direct unique-index lookup instead).
func generateAPIToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("users: generating token entropy: %w", err)
	}
	raw = "gdhcp_" + base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])
	return raw, hash, nil
}
