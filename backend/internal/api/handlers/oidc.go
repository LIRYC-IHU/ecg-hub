package handlers

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
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
