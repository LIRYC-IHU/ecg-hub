package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

// validLDAPConfig returns a Config with all required LDAP fields populated.
func validLDAPConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Auth.LDAP.Host = "ldap.example.com"
	cfg.Auth.LDAP.Port = 389
	cfg.Auth.LDAP.UserSearchDN = "ou=users,dc=example,dc=com"
	cfg.Auth.LDAP.UserFilter = "(uid=%s)"
	cfg.LDAPBindDN = "cn=admin,dc=example,dc=com"
	cfg.LDAPBindPassword = "bindpass"
	cfg.JWTSecret = "test-jwt-secret-for-unit-tests"
	return cfg
}

func TestNewLDAPProvider_MissingHost(t *testing.T) {
	cfg := validLDAPConfig()
	cfg.Auth.LDAP.Host = ""
	_, err := NewLDAPProvider(cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing LDAP host, got nil")
	}
	if !strings.Contains(err.Error(), "host") {
		t.Errorf("error should mention host, got: %v", err)
	}
}

func TestNewLDAPProvider_MissingUserSearchDN(t *testing.T) {
	cfg := validLDAPConfig()
	cfg.Auth.LDAP.UserSearchDN = ""
	_, err := NewLDAPProvider(cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing user_search_dn, got nil")
	}
}

func TestNewLDAPProvider_InvalidUserFilter(t *testing.T) {
	cfg := validLDAPConfig()
	cfg.Auth.LDAP.UserFilter = "(objectClass=person)" // no format placeholder
	_, err := NewLDAPProvider(cfg, nil)
	if err == nil {
		t.Fatal("expected error for user_filter missing placeholder, got nil")
	}
	if !strings.Contains(err.Error(), "user_filter") {
		t.Errorf("error should mention user_filter, got: %v", err)
	}
}

func TestNewLDAPProvider_MissingJWTSecret(t *testing.T) {
	cfg := validLDAPConfig()
	cfg.JWTSecret = ""
	_, err := NewLDAPProvider(cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing JWT_SECRET, got nil")
	}
}

func TestLDAPProvider_ValidateToken_Valid(t *testing.T) {
	cfg := validLDAPConfig()
	provider, err := NewLDAPProvider(cfg, nil)
	if err != nil {
		t.Fatalf("NewLDAPProvider: %v", err)
	}

	// Issue a token using the internal helper.
	rawToken, err := provider.issueToken("testuser", "reader")
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}

	claims, err := provider.ValidateToken(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.Sub != "testuser" {
		t.Errorf("Sub: want testuser, got %q", claims.Sub)
	}
	if claims.Role != "reader" {
		t.Errorf("Role: want reader, got %q", claims.Role)
	}
}

func TestLDAPProvider_ValidateToken_AdminRole(t *testing.T) {
	cfg := validLDAPConfig()
	provider, err := NewLDAPProvider(cfg, nil)
	if err != nil {
		t.Fatalf("NewLDAPProvider: %v", err)
	}

	rawToken, err := provider.issueToken("adminuser", "admin")
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}

	claims, err := provider.ValidateToken(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.Role != "admin" {
		t.Errorf("Role: want admin, got %q", claims.Role)
	}
}

func TestLDAPProvider_ValidateToken_InvalidSignature(t *testing.T) {
	cfg := validLDAPConfig()
	provider, err := NewLDAPProvider(cfg, nil)
	if err != nil {
		t.Fatalf("NewLDAPProvider: %v", err)
	}

	rawToken, err := provider.issueToken("testuser", "reader")
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}

	// Tamper with the token signature.
	parts := strings.Split(rawToken, ".")
	if len(parts) == 3 {
		parts[2] = "invalidsignature"
		rawToken = strings.Join(parts, ".")
	}

	_, err = provider.ValidateToken(context.Background(), rawToken)
	if err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
}

func TestLDAPProvider_ValidateToken_WrongSecret(t *testing.T) {
	cfg := validLDAPConfig()
	provider, err := NewLDAPProvider(cfg, nil)
	if err != nil {
		t.Fatalf("NewLDAPProvider: %v", err)
	}

	rawToken, err := provider.issueToken("testuser", "reader")
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}

	// Validate with a different secret.
	cfg2 := validLDAPConfig()
	cfg2.JWTSecret = "different-secret"
	provider2, _ := NewLDAPProvider(cfg2, nil)

	_, err = provider2.ValidateToken(context.Background(), rawToken)
	if err == nil {
		t.Fatal("expected error when validating with wrong secret, got nil")
	}
}

func TestLDAPProvider_ValidateToken_MalformedToken(t *testing.T) {
	cfg := validLDAPConfig()
	provider, err := NewLDAPProvider(cfg, nil)
	if err != nil {
		t.Fatalf("NewLDAPProvider: %v", err)
	}

	_, err = provider.ValidateToken(context.Background(), "not.a.jwt")
	if err == nil {
		t.Fatal("expected error for malformed token, got nil")
	}
}

func TestLDAPProvider_ValidateToken_EmptyRole(t *testing.T) {
	cfg := validLDAPConfig()
	provider, err := NewLDAPProvider(cfg, nil)
	if err != nil {
		t.Fatalf("NewLDAPProvider: %v", err)
	}

	// issueToken with an empty role should produce a JWT that ValidateToken rejects.
	rawToken, err := provider.issueToken("testuser", "")
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}

	_, err = provider.ValidateToken(context.Background(), rawToken)
	if err == nil {
		t.Fatal("expected error for empty role, got nil")
	}
	if !strings.Contains(err.Error(), "missing role") {
		t.Errorf("error should mention missing role, got: %v", err)
	}
}

func TestLDAPProvider_ValidateToken_CustomRole(t *testing.T) {
	cfg := validLDAPConfig()
	provider, err := NewLDAPProvider(cfg, nil)
	if err != nil {
		t.Fatalf("NewLDAPProvider: %v", err)
	}

	// Custom role names (e.g. from DB) should now be accepted.
	rawToken, err := provider.issueToken("testuser", "nurse")
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}

	claims, err := provider.ValidateToken(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.Role != "nurse" {
		t.Errorf("Role: want nurse, got %q", claims.Role)
	}
}

func TestExtractRole_Admin(t *testing.T) {
	role := extractRole([]string{"other", "admin", "reader"}, "admin")
	if role != "admin" {
		t.Errorf("want admin, got %q", role)
	}
}

func TestExtractRole_Reader(t *testing.T) {
	role := extractRole([]string{"other", "reader"}, "admin")
	if role != "other" {
		t.Errorf("want other (first non-system role), got %q", role)
	}
}

func TestExtractRole_None(t *testing.T) {
	role := extractRole([]string{"offline_access", "uma_authorization"}, "admin")
	if role != "" {
		t.Errorf("want empty, got %q", role)
	}
}
