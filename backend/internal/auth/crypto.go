package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// deriveKey derives a 32-byte key from the input using SHA-256.
// This ensures any length input produces a valid AES-256 key.
func deriveKey(key string) []byte {
	h := sha256.Sum256([]byte(key))
	return h[:]
}

// EncryptString encrypts plaintext using AES-256-GCM and returns a base64-encoded ciphertext.
// The key is derived via SHA-256 to ensure it is exactly 32 bytes.
func EncryptString(plaintext, key string) (string, error) {
	derivedKey := deriveKey(key)

	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return "", fmt.Errorf("crypto: new cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: new gcm: %w", err)
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: generate nonce: %w", err)
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptString decrypts a base64-encoded ciphertext using AES-256-GCM.
// The key is derived via SHA-256 to ensure it is exactly 32 bytes.
func DecryptString(ciphertext, key string) (string, error) {
	derivedKey := deriveKey(key)

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("crypto: base64 decode: %w", err)
	}

	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return "", fmt.Errorf("crypto: new cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: new gcm: %w", err)
	}

	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("crypto: ciphertext too short")
	}

	nonce, encrypted := data[:nonceSize], data[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: decrypt: %w", err)
	}

	return string(plaintext), nil
}
