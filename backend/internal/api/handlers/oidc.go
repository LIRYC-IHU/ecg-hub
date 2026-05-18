package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// OIDCLoginHandler redirects the browser to the Keycloak authorization endpoint.
// The state is HMAC-signed so it can be verified in the callback without a cookie or session.
//
// GET /api/v1/auth/oidc/login
func OIDCLoginHandler(flow auth.OIDCFlow) echo.HandlerFunc {
	return func(c echo.Context) error {
		state, err := flow.GenerateSignedState()
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate state")
		}
		return c.Redirect(http.StatusFound, flow.AuthCodeURL(state))
	}
}

// OIDCCallbackHandler handles the redirect from Keycloak after user authentication.
// It verifies the signed state, exchanges the authorization code for an ECG Hub JWT,
// and redirects the browser to the frontend with the token in the query string.
//
// GET /api/v1/auth/oidc/callback?code=...&state=...
func OIDCCallbackHandler(flow auth.OIDCFlow) echo.HandlerFunc {
	return func(c echo.Context) error {
		state := c.QueryParam("state")
		if state == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing state")
		}
		if err := flow.VerifyState(state); err != nil {
			slog.Warn("oidc callback: invalid state", "error", err)
			return echo.NewHTTPError(http.StatusBadRequest, "invalid state")
		}

		code := c.QueryParam("code")
		if code == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing authorization code")
		}

		jwtToken, err := flow.ExchangeAndIssue(c.Request().Context(), code)
		if err != nil {
			slog.Error("oidc callback: exchange failed", "error", err)
			return echo.NewHTTPError(http.StatusUnauthorized, "authentication failed")
		}

		setJWTCookie(c, jwtToken)
		return c.Redirect(http.StatusFound, "/")
	}
}

// ─── Dynamic OIDC handlers (config from DB) ──────────────────────────────────

type oidcDBParams struct {
	IssuerURL     string `json:"issuer_url"`
	InternalURL   string `json:"internal_url"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	RedirectURL   string `json:"redirect_url"`
	TLS           bool   `json:"tls"`
	AdminRoleName string `json:"admin_role_name"`
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
		AdminRoleName: params.AdminRoleName,
		JWTSecret:     jwtSecret,
	}, userStore)
	if err != nil {
		slog.Error("oidc: failed to build flow from DB", "error", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "OIDC initialization failed: "+err.Error())
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
		state, err := flow.GenerateSignedState()
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate state")
		}
		return c.Redirect(http.StatusFound, flow.AuthCodeURL(state))
	}
}

// OIDCCallbackHandlerDynamic handles the OIDC callback. Builds the flow from DB config if needed.
func OIDCCallbackHandlerDynamic(staticFlow auth.OIDCFlow, repo *repository.AuthConfigRepository, encKey, jwtSecret string, userRepo auth.UserStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		flow, err := oidcFlowFromDB(c, staticFlow, repo, encKey, jwtSecret, userRepo)
		if err != nil {
			return err
		}

		state := c.QueryParam("state")
		if state == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing state")
		}
		if err := flow.VerifyState(state); err != nil {
			slog.Warn("oidc callback: invalid state", "error", err)
			return echo.NewHTTPError(http.StatusBadRequest, "invalid state")
		}

		code := c.QueryParam("code")
		if code == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing authorization code")
		}

		jwtToken, err := flow.ExchangeAndIssue(c.Request().Context(), code)
		if err != nil {
			slog.Error("oidc callback: exchange failed", "error", err)
			return echo.NewHTTPError(http.StatusUnauthorized, "authentication failed")
		}

		setJWTCookie(c, jwtToken)
		return c.Redirect(http.StatusFound, "/")
	}
}
