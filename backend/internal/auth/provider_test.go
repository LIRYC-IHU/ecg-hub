package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

func TestNew_UnknownProvider(t *testing.T) {
	cfg := &config.Config{}
	cfg.Auth.Providers = []string{"oracle"}
	_, err := New(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "oracle") {
		t.Errorf("error should mention unknown provider name, got: %v", err)
	}
}

func TestNew_LDAPProvider_MissingFields(t *testing.T) {
	cfg := &config.Config{}
	cfg.Auth.Providers = []string{"ldap"}
	// All LDAP fields empty → should fail
	_, err := New(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing LDAP config, got nil")
	}
}

func TestNew_OIDCProvider_MissingIssuerURL(t *testing.T) {
	cfg := &config.Config{}
	cfg.Auth.Providers = []string{"oidc"}
	cfg.Auth.OIDC.IssuerURL = ""
	cfg.OIDCClientID = "client-id"
	_, err := New(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing OIDC issuer URL, got nil")
	}
	if !strings.Contains(err.Error(), "issuer_url") {
		t.Errorf("error should mention issuer_url, got: %v", err)
	}
}

func TestNew_OIDCProvider_MissingClientID(t *testing.T) {
	cfg := &config.Config{}
	cfg.Auth.Providers = []string{"oidc"}
	cfg.Auth.OIDC.IssuerURL = "https://keycloak.example.com/realms/chu"
	cfg.Auth.OIDC.RedirectURL = "http://localhost/api/v1/auth/oidc/callback"
	cfg.OIDCClientID = "" // check fires before network call, safe as unit test
	_, err := New(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing OIDC client ID, got nil")
	}
	if !strings.Contains(err.Error(), "OIDC_CLIENT_ID") {
		t.Errorf("error should mention OIDC_CLIENT_ID, got: %v", err)
	}
}
