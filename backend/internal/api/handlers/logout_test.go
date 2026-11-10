package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// logoutFlow is an OIDC flow of which only the logout URL matters here.
type logoutFlow struct{ auth.OIDCFlow }

func (logoutFlow) LogoutURL(redirectURI string) string {
	return "https://idp.example/logout?post_logout_redirect_uri=" + redirectURI
}

// logoutUsers knows one user per provider.
type logoutUsers map[string]string // subject → provider

func (u logoutUsers) ResolveIdentity(_ context.Context, sub string) (string, string, error) {
	if _, ok := u[sub]; !ok {
		return "", "", nil
	}
	return "id-" + sub, "reader", nil
}

func (u logoutUsers) GetByID(_ context.Context, id string) (*repository.UserRecord, error) {
	for sub, provider := range u {
		if "id-"+sub == id {
			return &repository.UserRecord{ID: id, Provider: provider}, nil
		}
	}
	return nil, errors.New("not found")
}

// subjectProvider accepts a token that is the subject itself.
type subjectProvider struct{}

func (subjectProvider) ValidateToken(_ context.Context, token string) (*auth.Claims, error) {
	if token == "expired" {
		return nil, errors.New("expired")
	}
	return &auth.Claims{Sub: token, Role: "reader"}, nil
}

// Logging out ends the identity provider's session for someone who signed in
// through it, and only for them: otherwise the next SSO click signs them back
// in without asking, and a local account would be sent to a provider it never
// used. The cookie goes in every case.
func TestLogout_EndsTheOIDCSessionOnlyForOIDCUsers(t *testing.T) {
	users := logoutUsers{"dr-a": "oidc", "ops": "local"}
	h := LogoutHandler(subjectProvider{}, logoutFlow{}, nil, "", "", users)

	cases := []struct {
		name, token, wantLocation string
	}{
		{"oidc user", "dr-a", "https://idp.example/logout?post_logout_redirect_uri=http://example.com/"},
		{"local user", "ops", "/"},
		{"expired token", "expired", "/"},
		{"no session", "", "/"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/logout", nil)
		if c.token != "" {
			req.AddCookie(&http.Cookie{Name: "jwt", Value: c.token})
		}
		rec := httptest.NewRecorder()
		if err := h(echo.New().NewContext(req, rec)); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := rec.Header().Get("Location"); rec.Code != http.StatusFound || got != c.wantLocation {
			t.Errorf("%s: status %d, redirect %q, want %q", c.name, rec.Code, got, c.wantLocation)
		}
		cleared := false
		for _, ck := range rec.Result().Cookies() {
			if ck.Name == "jwt" && ck.MaxAge < 0 {
				cleared = true
			}
		}
		if !cleared {
			t.Errorf("%s: session cookie not cleared", c.name)
		}
	}
}
