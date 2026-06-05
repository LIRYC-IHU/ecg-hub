package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateAPIKey(t *testing.T) {
	plaintext, prefix, hash, err := generateAPIKey()
	if err != nil {
		t.Fatalf("generateAPIKey: %v", err)
	}

	if !strings.HasPrefix(plaintext, apiKeyTokenPrefix) {
		t.Errorf("plaintext %q missing prefix %q", plaintext, apiKeyTokenPrefix)
	}
	if !strings.HasPrefix(plaintext, prefix) {
		t.Errorf("display prefix %q is not a prefix of the key %q", prefix, plaintext)
	}
	if !strings.HasPrefix(prefix, apiKeyTokenPrefix) {
		t.Errorf("display prefix %q missing namespace %q", prefix, apiKeyTokenPrefix)
	}

	// The stored hash must be the SHA-256 of the plaintext — never the plaintext itself.
	sum := sha256.Sum256([]byte(plaintext))
	if want := hex.EncodeToString(sum[:]); hash != want {
		t.Errorf("hash = %q, want %q", hash, want)
	}
	if strings.Contains(hash, plaintext) || hash == plaintext {
		t.Error("hash must not contain the plaintext key")
	}
}

func TestGenerateAPIKeyUnique(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		plaintext, _, hash, err := generateAPIKey()
		if err != nil {
			t.Fatalf("generateAPIKey: %v", err)
		}
		if seen[plaintext] {
			t.Fatal("duplicate plaintext key generated")
		}
		if seen[hash] {
			t.Fatal("duplicate hash generated")
		}
		seen[plaintext] = true
		seen[hash] = true
	}
}
