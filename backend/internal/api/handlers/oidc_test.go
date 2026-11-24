package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// fakeOIDCFlow accepts any state and code, so the tests below exercise only
// what the handlers do around the identity provider.
type fakeOIDCFlow struct{}

func (fakeOIDCFlow) GenerateSignedState() (string, error) { return "state.sig", nil }
func (fakeOIDCFlow) VerifyState(string) error             { return nil }
func (fakeOIDCFlow) AuthCodeURL(state, _ string) string {
	return "https://idp.example/auth?state=" + state
}
func (fakeOIDCFlow) ExchangeAndIssue(context.Context, string, string) (string, error) {
	return "signed.jwt.token", nil
}

func TestSafeReturnPath(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"/", "/"},
		{"/?patient=P123&ecg=0b9e", "/?patient=P123&ecg=0b9e"},
		{"/quarantine", "/quarantine"},
		{"/?patient=DUPONT%20Jean", "/?patient=DUPONT%20Jean"},

		{"", ""},
		{"quarantine", ""},
		{"https://evil.example/", ""},
		{"//evil.example/", ""},
		{"/\\evil.example/", ""},
		{"\\\\evil.example/", ""},
		{"/%5Cevil.example/", "/%5Cevil.example/"}, // an escaped backslash stays a path segment
		{"javascript:alert(1)", ""},
		{"/x\r\nSet-Cookie: jwt=forged", ""},
		{"/api/v1/auth/logout", ""},
		{"/api", ""},
		{"/%61pi/v1/auth/logout", ""},
		{"/" + strings.Repeat("a", maxReturnToLen), ""},
	} {
		if got := safeReturnPath(tc.in); got != tc.want {
			t.Errorf("safeReturnPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func oidcLogin(t *testing.T, returnTo string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api/v1/auth/oidc/login"
	if returnTo != "" {
		target += "?return_to=" + url.QueryEscape(returnTo)
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, target, nil), rec)
	if err := OIDCLoginHandlerDynamic(fakeOIDCFlow{}, nil, "", "")(c); err != nil {
		t.Fatalf("login handler: %v", err)
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status: want 302, got %d", rec.Code)
	}
	return rec
}

func responseCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

func TestOIDCLogin_RemembersTheLinkThatWasOpened(t *testing.T) {
	for _, link := range []string{
		"/?patient=DUPONT%2001&ecg=0b9e",
		// Bytes net/http would strip from a raw cookie value.
		`/?patient=P;1"é&ecg=0b9e`,
	} {
		rec := oidcLogin(t, link)

		ck := responseCookie(rec, oidcReturnToCookie)
		if ck == nil || ck.MaxAge <= 0 {
			t.Fatalf("%q: want a live %s cookie, got %+v", link, oidcReturnToCookie, ck)
		}
		if !ck.HttpOnly {
			t.Errorf("%q: the return-to cookie must be HttpOnly", link)
		}
		if got, _ := url.QueryUnescape(ck.Value); got != link {
			t.Errorf("cookie holds %q, want %q", got, link)
		}
	}
}

func TestOIDCLogin_ForgetsAForeignDestination(t *testing.T) {
	for _, dest := range []string{"", "https://evil.example/", "//evil.example/"} {
		rec := oidcLogin(t, dest)
		ck := responseCookie(rec, oidcReturnToCookie)
		if ck == nil || ck.MaxAge >= 0 {
			t.Errorf("return_to=%q: want the cookie cleared, got %+v", dest, ck)
		}
	}
}

func oidcCallback(t *testing.T, returnToCookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?state=state.sig&code=abc", nil)
	req.AddCookie(&http.Cookie{Name: oidcVerifierCookie, Value: "verifier"})
	if returnToCookie != "" {
		req.AddCookie(&http.Cookie{Name: oidcReturnToCookie, Value: returnToCookie})
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	if err := OIDCCallbackHandlerDynamic(fakeOIDCFlow{}, nil, "", "", nil)(c); err != nil {
		t.Fatalf("callback handler: %v", err)
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status: want 302, got %d", rec.Code)
	}
	return rec
}

func TestOIDCCallback_LandsOnTheRememberedLink(t *testing.T) {
	link := "/?patient=DUPONT%2001&ecg=0b9e"
	rec := oidcCallback(t, url.QueryEscape(link))

	if got := rec.Header().Get("Location"); got != link {
		t.Errorf("Location: want %q, got %q", link, got)
	}
	if ck := responseCookie(rec, "jwt"); ck == nil || ck.Value == "" {
		t.Error("the session cookie must still be set")
	}
	if ck := responseCookie(rec, oidcReturnToCookie); ck == nil || ck.MaxAge >= 0 {
		t.Errorf("the return-to cookie must be cleared once used, got %+v", ck)
	}
}

func TestOIDCCallback_FallsBackToTheRoot(t *testing.T) {
	for name, cookie := range map[string]string{
		"no cookie":       "",
		"foreign host":    url.QueryEscape("https://evil.example/"),
		"scheme-relative": url.QueryEscape("//evil.example/"),
		"bad escaping":    "%zz",
	} {
		rec := oidcCallback(t, cookie)
		if got := rec.Header().Get("Location"); got != "/" {
			t.Errorf("%s: Location: want \"/\", got %q", name, got)
		}
	}
}
