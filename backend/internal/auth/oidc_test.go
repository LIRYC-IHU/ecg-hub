package auth

import "testing"

// TestPKCEChallenge verifies the S256 challenge against the RFC 7636 Appendix B
// test vector, proving the code_challenge sent to the IdP is computed correctly.
func TestPKCEChallenge(t *testing.T) {
	const (
		verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	)
	if got := PKCEChallenge(verifier); got != challenge {
		t.Errorf("PKCEChallenge = %q, want %q (RFC 7636 vector)", got, challenge)
	}
}

// TestGeneratePKCEVerifier checks the verifier is long enough (RFC 7636 requires
// 43–128 chars) and unique across calls.
func TestGeneratePKCEVerifier(t *testing.T) {
	v1, err := GeneratePKCEVerifier()
	if err != nil {
		t.Fatalf("GeneratePKCEVerifier: %v", err)
	}
	if len(v1) < 43 {
		t.Errorf("verifier length = %d, want >= 43", len(v1))
	}
	if v2, _ := GeneratePKCEVerifier(); v1 == v2 {
		t.Error("consecutive verifiers should differ")
	}
}
