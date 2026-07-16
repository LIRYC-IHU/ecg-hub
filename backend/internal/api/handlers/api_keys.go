package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// apiKeyTokenPrefix namespaces every generated key so it is recognisable in
// logs and so a future auth middleware can fast-reject non-keys.
const apiKeyTokenPrefix = "ecghub_"

// apiKeyMaxNameLen bounds the user-supplied key name.
const apiKeyMaxNameLen = 100

// generateAPIKey returns a new plaintext key, its display prefix, and the
// SHA-256 hash to persist. The plaintext is never stored.
func generateAPIKey() (plaintext, prefix, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	plaintext = apiKeyTokenPrefix + token

	sum := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(sum[:])

	// Display prefix: namespace + first 6 chars of the token (non-secret).
	prefix = apiKeyTokenPrefix + token[:6]
	return plaintext, prefix, hash, nil
}
