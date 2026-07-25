package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

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
		url, err := startOIDCAuth(c, flow)
		if err != nil {
			return err
		}
		return c.Redirect(http.StatusFound, url)
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
		return c.Redirect(http.StatusFound, "/")
	}
}
