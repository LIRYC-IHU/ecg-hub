package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// AuthAdminServiceHandler implements apiv1connect.AuthAdminServiceHandler — the
// gRPC/Connect replacement for the admin auth-provider REST endpoints of "étape
// 11". Every RPC requires admin.auth_config (enforced in the router). Provider
// configs travel as an opaque JSON string; the handler parses them into the
// shared SaveOIDCRequest/SaveLDAPRequest structs and reuses the existing
// mask/encrypt logic.
type AuthAdminServiceHandler struct {
	Repo   *repository.AuthConfigRepository
	EncKey string
	DB     *gorm.DB
}

func (h *AuthAdminServiceHandler) ListProviders(_ context.Context, _ *apiv1.ListAuthProvidersRequest) (*apiv1.ListAuthProvidersResponse, error) {
	configs, err := h.Repo.ListAll()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to list auth providers"))
	}
	out := make([]*apiv1.AuthProvider, 0, len(configs))
	for _, cfg := range configs {
		decrypted, err := auth.DecryptString(cfg.ConfigEncrypted, h.EncKey)
		if err != nil {
			slog.Warn("auth_admin: failed to decrypt config", "id", cfg.ID, "error", err)
			continue
		}
		var masked any
		switch cfg.ProviderType {
		case "oidc":
			var oidcCfg OIDCConfig
			if err := json.Unmarshal([]byte(decrypted), &oidcCfg); err != nil {
				slog.Warn("auth_admin: failed to unmarshal oidc config", "id", cfg.ID, "error", err)
				continue
			}
			oidcCfg.ClientSecret = maskedSecret
			masked = oidcCfg
		case "ldap":
			var ldapCfg LDAPConfig
			if err := json.Unmarshal([]byte(decrypted), &ldapCfg); err != nil {
				slog.Warn("auth_admin: failed to unmarshal ldap config", "id", cfg.ID, "error", err)
				continue
			}
			ldapCfg.BindPassword = maskedSecret
			masked = ldapCfg
		default:
			continue
		}
		configJSON, err := json.Marshal(masked)
		if err != nil {
			continue
		}
		out = append(out, &apiv1.AuthProvider{
			Id:           cfg.ID,
			ProviderType: cfg.ProviderType,
			Active:       cfg.Active,
			ConfigJson:   string(configJSON),
		})
	}
	return &apiv1.ListAuthProvidersResponse{Providers: out}, nil
}

func (h *AuthAdminServiceHandler) SaveOIDC(ctx context.Context, req *apiv1.SaveOIDCConfigRequest) (*apiv1.SaveAuthConfigResponse, error) {
	var body SaveOIDCRequest
	if err := json.Unmarshal([]byte(req.ConfigJson), &body); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid config payload"))
	}
	if body.IssuerURL == "" || body.ClientID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("issuer_url and client_id are required"))
	}

	// Masked or empty client_secret: preserve the existing stored value.
	if body.ClientSecret == maskedSecret || body.ClientSecret == "" {
		if existing, err := h.Repo.Get("oidc"); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read existing config"))
		} else if existing != nil {
			if decrypted, err := auth.DecryptString(existing.ConfigEncrypted, h.EncKey); err == nil {
				var existingCfg OIDCConfig
				if err := json.Unmarshal([]byte(decrypted), &existingCfg); err == nil {
					body.ClientSecret = existingCfg.ClientSecret
				}
			}
		}
	}

	oidcCfg := OIDCConfig{
		IssuerURL:     body.IssuerURL,
		InternalURL:   body.InternalURL,
		ClientID:      body.ClientID,
		ClientSecret:  body.ClientSecret,
		RedirectURL:   body.RedirectURL,
		LogoutURL:     body.LogoutURL,
		TLS:           body.TLS,
		Scopes:        body.Scopes,
		UsernameClaim: body.UsernameClaim,
		GroupsClaim:   body.GroupsClaim,
	}
	if err := h.upsertAuthConfig("oidc", oidcCfg, body.Active); err != nil {
		return nil, err
	}
	slog.Info("auth_admin: OIDC config saved, restart required to apply changes")
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "auth_config_saved", "oidc",
		map[string]any{"provider": "oidc"})
	return &apiv1.SaveAuthConfigResponse{Message: "OIDC configuration saved", RestartRequired: true}, nil
}

func (h *AuthAdminServiceHandler) SaveLDAP(ctx context.Context, req *apiv1.SaveLDAPConfigRequest) (*apiv1.SaveAuthConfigResponse, error) {
	var body SaveLDAPRequest
	if err := json.Unmarshal([]byte(req.ConfigJson), &body); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid config payload"))
	}
	if body.Host == "" || body.Port == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("host and port are required"))
	}
	body.Host = strings.TrimPrefix(body.Host, "ldap://")

	// Masked or empty bind_password: preserve the existing stored value.
	if body.BindPassword == maskedSecret || body.BindPassword == "" {
		if existing, err := h.Repo.Get("ldap"); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read existing config"))
		} else if existing != nil {
			if decrypted, err := auth.DecryptString(existing.ConfigEncrypted, h.EncKey); err == nil {
				var existingCfg LDAPConfig
				if err := json.Unmarshal([]byte(decrypted), &existingCfg); err == nil {
					body.BindPassword = existingCfg.BindPassword
				}
			}
		}
	}

	ldapCfg := LDAPConfig{
		Host:              body.Host,
		Port:              body.Port,
		TLS:               body.TLS,
		BaseDN:            body.BaseDN,
		UserSearchDN:      body.UserSearchDN,
		UserFilter:        body.UserFilter,
		BindDN:            body.BindDN,
		BindPassword:      body.BindPassword,
		AdminGroupDN:      body.AdminGroupDN,
		WriterGroupDN:     body.WriterGroupDN,
		AdminUsers:        body.AdminUsers,
		UsernameAttribute: body.UsernameAttribute,
		UUIDAttribute:     body.UUIDAttribute,
	}
	if err := h.upsertAuthConfig("ldap", ldapCfg, body.Active); err != nil {
		return nil, err
	}
	slog.Info("auth_admin: LDAP config saved, restart required to apply changes")
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "auth_config_saved", "ldap",
		map[string]any{"provider": "ldap"})
	return &apiv1.SaveAuthConfigResponse{Message: "LDAP configuration saved", RestartRequired: true}, nil
}

// upsertAuthConfig marshals, encrypts and upserts an auth provider config.
func (h *AuthAdminServiceHandler) upsertAuthConfig(providerType string, cfg any, active bool) error {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("failed to serialize config"))
	}
	encrypted, err := auth.EncryptString(string(configJSON), h.EncKey)
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("failed to encrypt config"))
	}
	record := &models.AuthProviderConfig{ProviderType: providerType, ConfigEncrypted: encrypted, Active: active}
	if err := h.Repo.Upsert(record); err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("failed to save auth config"))
	}
	return nil
}

func (h *AuthAdminServiceHandler) DeleteProvider(ctx context.Context, req *apiv1.DeleteAuthProviderRequest) (*apiv1.DeleteAuthProviderResponse, error) {
	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}
	if err := h.Repo.Delete(req.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to delete auth provider config"))
	}
	slog.Info("auth_admin: provider config deleted", "id", req.Id)
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "auth_config_deleted", req.Id, nil)
	return &apiv1.DeleteAuthProviderResponse{Message: "auth provider config deleted", RestartRequired: true}, nil
}

func (h *AuthAdminServiceHandler) TestOIDC(_ context.Context, req *apiv1.TestOIDCRequest) (*apiv1.TestAuthResponse, error) {
	var body SaveOIDCRequest
	if err := json.Unmarshal([]byte(req.ConfigJson), &body); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid config payload"))
	}
	if body.IssuerURL == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("issuer_url is required"))
	}

	wellKnownURL := body.IssuerURL + "/.well-known/openid-configuration"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(wellKnownURL)
	if err != nil {
		return &apiv1.TestAuthResponse{Success: false, Error: fmt.Sprintf("failed to reach issuer: %s", err.Error())}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &apiv1.TestAuthResponse{Success: false, Error: fmt.Sprintf("issuer returned HTTP %d", resp.StatusCode)}, nil
	}
	return &apiv1.TestAuthResponse{Success: true}, nil
}

func (h *AuthAdminServiceHandler) TestLDAP(_ context.Context, req *apiv1.TestLDAPRequest) (*apiv1.TestAuthResponse, error) {
	var body SaveLDAPRequest
	if err := json.Unmarshal([]byte(req.ConfigJson), &body); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid config payload"))
	}
	if body.Host == "" || body.Port == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("host and port are required"))
	}
	addr := net.JoinHostPort(body.Host, fmt.Sprintf("%d", body.Port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	latency := time.Since(start).Round(time.Millisecond).String()
	if err != nil {
		return &apiv1.TestAuthResponse{Success: false, Host: addr, Latency: latency, Error: err.Error()}, nil
	}
	conn.Close()
	return &apiv1.TestAuthResponse{Success: true, Host: addr, Latency: latency}, nil
}
