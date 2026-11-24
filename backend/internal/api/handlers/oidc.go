package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// oidcVerifierCookie holds the PKCE code verifier between the login redirect and
// the callback. Being HttpOnly and per-browser, it also binds the callback to the
// browser that started the flow — mitigating login CSRF.
const oidcVerifierCookie = "oidc_verifier"

// setOIDCVerifierCookie stores the PKCE verifier for the redirect round-trip.
func setOIDCVerifierCookie(c echo.Context, verifier string) {
	c.SetCookie(&http.Cookie{
		Name:     oidcVerifierCookie,
		Value:    verifier,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Scheme() == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300, // the redirect round-trip takes seconds; expire fast
	})
}

// clearOIDCVerifierCookie removes the PKCE verifier cookie after the exchange.
func clearOIDCVerifierCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     oidcVerifierCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Scheme() == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// oidcReturnToCookie holds the page the browser was on when the operator chose
// the SSO login, so the callback can land there instead of on the patient list.
// A link to a patient or an ECG opened without a session would otherwise be
// lost at the identity provider, which is exactly when such a link is used: it
// arrives from somewhere else (an email, the HIS, a printed report).
const oidcReturnToCookie = "oidc_return_to"

// maxReturnToLen bounds what a login request can make the server store in a
// cookie. The application's own links are a path and two identifiers.
const maxReturnToLen = 1024

// safeReturnPath returns raw when it is a path on this server that the login
// may redirect to, and "" otherwise. The value comes from a query string anyone
// can write, so anything that could leave the origin is refused: a scheme, a
// host, a scheme-relative "//host", and backslashes, which browsers read as
// slashes ("/\host" is "//host" to them). url.Parse refuses control characters,
// which would otherwise let a value split the Location header. Paths under
// /api/ are refused too: the application never produces them, and a login
// should not end on an endpoint.
func safeReturnPath(raw string) string {
	if raw == "" || len(raw) > maxReturnToLen {
		return ""
	}
	if raw[0] != '/' || strings.HasPrefix(raw, "//") || strings.ContainsRune(raw, '\\') {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return ""
	}
	if u.Path == "/api" || strings.HasPrefix(u.Path, "/api/") {
		return ""
	}
	return raw
}

// setOIDCReturnToCookie remembers where to land after the callback. The value is
// query-escaped because a path may carry bytes a cookie value cannot (a
// semicolon, a double quote, non-ASCII), and net/http drops those silently
// rather than failing.
func setOIDCReturnToCookie(c echo.Context, path string) {
	c.SetCookie(&http.Cookie{
		Name:     oidcReturnToCookie,
		Value:    url.QueryEscape(path),
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Scheme() == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300, // same lifetime as the PKCE verifier it travels with
	})
}

// clearOIDCReturnToCookie removes the remembered destination.
func clearOIDCReturnToCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     oidcReturnToCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Scheme() == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// oidcLandingPath is where the callback sends the browser: the remembered page
// when there is one, the root otherwise. The cookie is ours and HttpOnly, but it
// is checked again because a cookie can still be planted by a sibling subdomain.
func oidcLandingPath(c echo.Context) string {
	ck, err := c.Cookie(oidcReturnToCookie)
	if err != nil || ck.Value == "" {
		return "/"
	}
	raw, err := url.QueryUnescape(ck.Value)
	if err != nil {
		return "/"
	}
	if p := safeReturnPath(raw); p != "" {
		return p
	}
	return "/"
}

// startOIDCAuth generates the signed state and a PKCE verifier, stores the verifier
// in a cookie, and returns the provider authorization URL to redirect to.
func startOIDCAuth(c echo.Context, flow auth.OIDCFlow) (string, error) {
	state, err := flow.GenerateSignedState()
	if err != nil {
		return "", echo.NewHTTPError(http.StatusInternalServerError, "failed to generate state")
	}
	verifier, err := auth.GeneratePKCEVerifier()
	if err != nil {
		return "", echo.NewHTTPError(http.StatusInternalServerError, "failed to generate PKCE verifier")
	}
	setOIDCVerifierCookie(c, verifier)
	return flow.AuthCodeURL(state, auth.PKCEChallenge(verifier)), nil
}

// finishOIDCAuth validates the state, consumes the PKCE verifier cookie (proving
// the callback belongs to the browser that started the flow), and exchanges the
// code for an ECG Hub JWT.
func finishOIDCAuth(c echo.Context, flow auth.OIDCFlow) (string, error) {
	state := c.QueryParam("state")
	if state == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, "missing state")
	}
	if err := flow.VerifyState(state); err != nil {
		slog.Warn("oidc callback: invalid state", "error", err)
		return "", echo.NewHTTPError(http.StatusBadRequest, "invalid state")
	}
	verifierCookie, err := c.Cookie(oidcVerifierCookie)
	if err != nil || verifierCookie.Value == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, "missing PKCE verifier — please restart the login")
	}
	clearOIDCVerifierCookie(c)
	code := c.QueryParam("code")
	if code == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, "missing authorization code")
	}
	jwtToken, err := flow.ExchangeAndIssue(c.Request().Context(), code, verifierCookie.Value)
	if err != nil {
		slog.Error("oidc callback: exchange failed", "error", err)
		return "", echo.NewHTTPError(http.StatusUnauthorized, "authentication failed")
	}
	return jwtToken, nil
}

// ─── Dynamic OIDC handlers (config from DB) ──────────────────────────────────

type oidcDBParams struct {
	IssuerURL     string   `json:"issuer_url"`
	InternalURL   string   `json:"internal_url"`
	ClientID      string   `json:"client_id"`
	ClientSecret  string   `json:"client_secret"`
	RedirectURL   string   `json:"redirect_url"`
	TLS           bool     `json:"tls"`
	Scopes        []string `json:"scopes"`
	UsernameClaim string   `json:"username_claim"`
	GroupsClaim   string   `json:"groups_claim"`
}

func oidcFlowFromDB(c echo.Context, staticFlow auth.OIDCFlow, repo *repository.AuthConfigRepository, encKey, jwtSecret string, userStore auth.UserStore) (auth.OIDCFlow, error) {
	if staticFlow != nil {
		return staticFlow, nil
	}
	if repo == nil {
		return nil, echo.NewHTTPError(http.StatusServiceUnavailable, "OIDC not configured")
	}
	dbCfg, _ := repo.Get("oidc")
	if dbCfg == nil {
		return nil, echo.NewHTTPError(http.StatusServiceUnavailable, "OIDC not configured")
	}
	decrypted, err := auth.DecryptString(dbCfg.ConfigEncrypted, encKey)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "failed to decrypt OIDC config")
	}
	var params oidcDBParams
	if err := json.Unmarshal([]byte(decrypted), &params); err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "failed to parse OIDC config")
	}
	flow, err := auth.NewOIDCProviderFromParams(c.Request().Context(), auth.OIDCParams{
		IssuerURL:     params.IssuerURL,
		InternalURL:   params.InternalURL,
		ClientID:      params.ClientID,
		ClientSecret:  params.ClientSecret,
		RedirectURL:   params.RedirectURL,
		TLS:           params.TLS,
		Scopes:        params.Scopes,
		UsernameClaim: params.UsernameClaim,
		GroupsClaim:   params.GroupsClaim,
		JWTSecret:     jwtSecret,
	}, userStore)
	if err != nil {
		slog.Error("oidc: failed to build flow from DB", "error", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "OIDC initialization failed")
	}
	return flow, nil
}

// OIDCLoginHandlerDynamic redirects to OIDC login. Builds the flow from DB config if needed.
func OIDCLoginHandlerDynamic(staticFlow auth.OIDCFlow, repo *repository.AuthConfigRepository, encKey, jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		flow, err := oidcFlowFromDB(c, staticFlow, repo, encKey, jwtSecret, nil)
		if err != nil {
			return err
		}
		authURL, err := startOIDCAuth(c, flow)
		if err != nil {
			return err
		}
		// A login started without a destination clears any left by an abandoned
		// one, which would otherwise send this login to that older page.
		if p := safeReturnPath(c.QueryParam("return_to")); p != "" {
			setOIDCReturnToCookie(c, p)
		} else {
			clearOIDCReturnToCookie(c)
		}
		return c.Redirect(http.StatusFound, authURL)
	}
}

// OIDCCallbackHandlerDynamic handles the OIDC callback. Builds the flow from DB config if needed.
func OIDCCallbackHandlerDynamic(staticFlow auth.OIDCFlow, repo *repository.AuthConfigRepository, encKey, jwtSecret string, userRepo auth.UserStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		flow, err := oidcFlowFromDB(c, staticFlow, repo, encKey, jwtSecret, userRepo)
		if err != nil {
			return err
		}
		jwtToken, err := finishOIDCAuth(c, flow)
		if err != nil {
			return err
		}
		setJWTCookie(c, jwtToken)
		landing := oidcLandingPath(c)
		clearOIDCReturnToCookie(c)
		return c.Redirect(http.StatusFound, landing)
	}
}
