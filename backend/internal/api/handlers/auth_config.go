package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// maskedSecret is the placeholder displayed for secrets in GET responses.
const maskedSecret = "••••••"

// --- OIDC config JSON shape ---

// OIDCConfig is the decrypted JSON stored for an OIDC provider.
type OIDCConfig struct {
	IssuerURL     string   `json:"issuer_url"`
	InternalURL   string   `json:"internal_url,omitempty"`
	ClientID      string   `json:"client_id"`
	ClientSecret  string   `json:"client_secret"`
	RedirectURL   string   `json:"redirect_url"`
	LogoutURL     string   `json:"logout_url,omitempty"`
	TLS           bool     `json:"tls"`
	Scopes        []string `json:"scopes,omitempty"`
	UsernameClaim string   `json:"username_claim,omitempty"`
	GroupsClaim   string   `json:"groups_claim,omitempty"`
}

// --- LDAP config JSON shape ---

// LDAPConfig is the decrypted JSON stored for an LDAP provider.
type LDAPConfig struct {
	Host              string   `json:"host"`
	Port              int      `json:"port"`
	TLS               bool     `json:"tls"`
	BaseDN            string   `json:"base_dn"`
	UserSearchDN      string   `json:"user_search_dn"`
	UserFilter        string   `json:"user_filter"`
	BindDN            string   `json:"bind_dn"`
	BindPassword      string   `json:"bind_password"`
	AdminGroupDN      string   `json:"admin_group_dn"`
	WriterGroupDN     string   `json:"writer_group_dn"`
	AdminUsers        []string `json:"admin_users"`
	UsernameAttribute string   `json:"username_attribute,omitempty"`
	UUIDAttribute     string   `json:"uuid_attribute,omitempty"`
}

// --- Request bodies ---

// SaveOIDCRequest is the body for PUT /admin/auth/oidc.
type SaveOIDCRequest struct {
	IssuerURL     string   `json:"issuer_url"`
	InternalURL   string   `json:"internal_url"`
	ClientID      string   `json:"client_id"`
	ClientSecret  string   `json:"client_secret"`
	RedirectURL   string   `json:"redirect_url"`
	LogoutURL     string   `json:"logout_url"`
	TLS           bool     `json:"tls"`
	Scopes        []string `json:"scopes"`
	UsernameClaim string   `json:"username_claim"`
	GroupsClaim   string   `json:"groups_claim"`
	Active        bool     `json:"active"`
}

// SaveLDAPRequest is the body for PUT /admin/auth/ldap.
type SaveLDAPRequest struct {
	Host              string   `json:"host"`
	Port              int      `json:"port"`
	TLS               bool     `json:"tls"`
	BaseDN            string   `json:"base_dn"`
	UserSearchDN      string   `json:"user_search_dn"`
	UserFilter        string   `json:"user_filter"`
	BindDN            string   `json:"bind_dn"`
	BindPassword      string   `json:"bind_password"`
	AdminGroupDN      string   `json:"admin_group_dn"`
	WriterGroupDN     string   `json:"writer_group_dn"`
	AdminUsers        []string `json:"admin_users"`
	UsernameAttribute string   `json:"username_attribute"`
	UUIDAttribute     string   `json:"uuid_attribute"`
	Active            bool     `json:"active"`
}

// --- Handlers ---

// ListAuthProvidersHandler returns GET /admin/auth/providers.
// Returns all configured providers with decrypted config (secrets masked).
func ListAuthProvidersHandler(repo *repository.AuthConfigRepository, encKey string) echo.HandlerFunc {
	return func(c echo.Context) error {
		configs, err := repo.ListAll()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to list auth providers",
			})
		}

		type providerResponse struct {
			ID           string `json:"id"`
			ProviderType string `json:"provider_type"`
			Active       bool   `json:"active"`
			Config       any    `json:"config"`
		}

		var result []providerResponse
		for _, cfg := range configs {
			decrypted, err := auth.DecryptString(cfg.ConfigEncrypted, encKey)
			if err != nil {
				slog.Warn("auth_config: failed to decrypt config", "id", cfg.ID, "error", err)
				continue
			}

			var masked any
			switch cfg.ProviderType {
			case "oidc":
				var oidcCfg OIDCConfig
				if err := json.Unmarshal([]byte(decrypted), &oidcCfg); err != nil {
					slog.Warn("auth_config: failed to unmarshal oidc config", "id", cfg.ID, "error", err)
					continue
				}
				oidcCfg.ClientSecret = maskedSecret
				masked = oidcCfg
			case "ldap":
				var ldapCfg LDAPConfig
				if err := json.Unmarshal([]byte(decrypted), &ldapCfg); err != nil {
					slog.Warn("auth_config: failed to unmarshal ldap config", "id", cfg.ID, "error", err)
					continue
				}
				ldapCfg.BindPassword = maskedSecret
				masked = ldapCfg
			default:
				continue
			}

			result = append(result, providerResponse{
				ID:           cfg.ID,
				ProviderType: cfg.ProviderType,
				Active:       cfg.Active,
				Config:       masked,
			})
		}

		if result == nil {
			result = []providerResponse{}
		}

		return c.JSON(http.StatusOK, map[string]any{"data": result})
	}
}

// SaveOIDCConfigHandler handles PUT /admin/auth/oidc.
// Encrypts and stores OIDC configuration.
func SaveOIDCConfigHandler(repo *repository.AuthConfigRepository, encKey string, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req SaveOIDCRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": err.Error(),
			})
		}

		if req.IssuerURL == "" || req.ClientID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "issuer_url and client_id are required",
			})
		}

		// If client_secret is masked or empty, keep existing secret from DB.
		if req.ClientSecret == maskedSecret || req.ClientSecret == "" {
			existing, err := repo.Get("oidc")
			if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"code":    "DB_ERROR",
					"message": "failed to read existing config",
				})
			}
			if existing != nil {
				decrypted, err := auth.DecryptString(existing.ConfigEncrypted, encKey)
				if err == nil {
					var existingCfg OIDCConfig
					if err := json.Unmarshal([]byte(decrypted), &existingCfg); err == nil {
						req.ClientSecret = existingCfg.ClientSecret
					}
				}
			}
		}

		oidcCfg := OIDCConfig{
			IssuerURL:     req.IssuerURL,
			InternalURL:   req.InternalURL,
			ClientID:      req.ClientID,
			ClientSecret:  req.ClientSecret,
			RedirectURL:   req.RedirectURL,
			LogoutURL:     req.LogoutURL,
			TLS:           req.TLS,
			Scopes:        req.Scopes,
			UsernameClaim: req.UsernameClaim,
			GroupsClaim:   req.GroupsClaim,
		}

		configJSON, err := json.Marshal(oidcCfg)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "MARSHAL_ERROR",
				"message": "failed to serialize config",
			})
		}

		encrypted, err := auth.EncryptString(string(configJSON), encKey)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "ENCRYPT_ERROR",
				"message": "failed to encrypt config",
			})
		}

		record := &models.AuthProviderConfig{
			ProviderType:    "oidc",
			ConfigEncrypted: encrypted,
			Active:          req.Active,
		}

		if err := repo.Upsert(record); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to save auth config",
			})
		}

		slog.Info("auth_config: OIDC config saved, restart required to apply changes")
		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "auth_config_saved", "oidc",
			map[string]any{"provider": "oidc"})

		return c.JSON(http.StatusOK, map[string]any{
			"message":          "OIDC configuration saved",
			"restart_required": true,
		})
	}
}

// SaveLDAPConfigHandler handles PUT /admin/auth/ldap.
// Encrypts and stores LDAP configuration.
func SaveLDAPConfigHandler(repo *repository.AuthConfigRepository, encKey string, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req SaveLDAPRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": err.Error(),
			})
		}

		if req.Host == "" || req.Port == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "host and port are required",
			})
		}
		req.Host = strings.TrimPrefix(req.Host, "ldap://")

		// If bind_password is masked or empty, keep existing from DB.
		if req.BindPassword == maskedSecret || req.BindPassword == "" {
			existing, err := repo.Get("ldap")
			if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"code":    "DB_ERROR",
					"message": "failed to read existing config",
				})
			}
			if existing != nil {
				decrypted, err := auth.DecryptString(existing.ConfigEncrypted, encKey)
				if err == nil {
					var existingCfg LDAPConfig
					if err := json.Unmarshal([]byte(decrypted), &existingCfg); err == nil {
						req.BindPassword = existingCfg.BindPassword
					}
				}
			}
		}

		ldapCfg := LDAPConfig{
			Host:              req.Host,
			Port:              req.Port,
			TLS:               req.TLS,
			BaseDN:            req.BaseDN,
			UserSearchDN:      req.UserSearchDN,
			UserFilter:        req.UserFilter,
			BindDN:            req.BindDN,
			BindPassword:      req.BindPassword,
			AdminGroupDN:      req.AdminGroupDN,
			WriterGroupDN:     req.WriterGroupDN,
			AdminUsers:        req.AdminUsers,
			UsernameAttribute: req.UsernameAttribute,
			UUIDAttribute:     req.UUIDAttribute,
		}

		configJSON, err := json.Marshal(ldapCfg)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "MARSHAL_ERROR",
				"message": "failed to serialize config",
			})
		}

		encrypted, err := auth.EncryptString(string(configJSON), encKey)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "ENCRYPT_ERROR",
				"message": "failed to encrypt config",
			})
		}

		record := &models.AuthProviderConfig{
			ProviderType:    "ldap",
			ConfigEncrypted: encrypted,
			Active:          req.Active,
		}

		if err := repo.Upsert(record); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to save auth config",
			})
		}

		slog.Info("auth_config: LDAP config saved, restart required to apply changes")
		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "auth_config_saved", "ldap",
			map[string]any{"provider": "ldap"})

		return c.JSON(http.StatusOK, map[string]any{
			"message":          "LDAP configuration saved",
			"restart_required": true,
		})
	}
}

// DeleteAuthProviderHandler handles DELETE /admin/auth/providers/:id.
func DeleteAuthProviderHandler(repo *repository.AuthConfigRepository, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if id == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "id is required",
			})
		}

		if err := repo.Delete(id); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to delete auth provider config",
			})
		}

		slog.Info("auth_config: provider config deleted", "id", id)
		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "auth_config_deleted", id, nil)

		return c.JSON(http.StatusOK, map[string]any{
			"message":          "auth provider config deleted",
			"restart_required": true,
		})
	}
}

// TestOIDCHandler handles POST /admin/auth/oidc/test.
// Tests connectivity to the OIDC issuer by fetching .well-known/openid-configuration.
func TestOIDCHandler(encKey string) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req SaveOIDCRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": err.Error(),
			})
		}

		if req.IssuerURL == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "issuer_url is required",
			})
		}

		// Fetch the OIDC discovery endpoint.
		wellKnownURL := req.IssuerURL + "/.well-known/openid-configuration"
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(wellKnownURL)
		if err != nil {
			return c.JSON(http.StatusOK, map[string]any{
				"success": false,
				"error":   fmt.Sprintf("failed to reach issuer: %s", err.Error()),
			})
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return c.JSON(http.StatusOK, map[string]any{
				"success": false,
				"error":   fmt.Sprintf("issuer returned HTTP %d", resp.StatusCode),
			})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"success": true,
		})
	}
}

// TestLDAPHandler handles POST /admin/auth/ldap/test.
// Tests TCP connectivity to the LDAP server.
func TestLDAPHandler(encKey string) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req SaveLDAPRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": err.Error(),
			})
		}

		if req.Host == "" || req.Port == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "host and port are required",
			})
		}

		addr := net.JoinHostPort(req.Host, fmt.Sprintf("%d", req.Port))
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		latency := time.Since(start)

		if err != nil {
			return c.JSON(http.StatusOK, map[string]any{
				"success": false,
				"host":    addr,
				"latency": latency.Round(time.Millisecond).String(),
				"error":   err.Error(),
			})
		}
		conn.Close()

		return c.JSON(http.StatusOK, map[string]any{
			"success": true,
			"host":    addr,
			"latency": latency.Round(time.Millisecond).String(),
		})
	}
}
