package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	ldap "github.com/go-ldap/ldap/v3"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// ldapDBConfig holds LDAP settings decrypted from DB.
type ldapDBConfig struct {
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	TLS           bool     `json:"tls"`
	BaseDN        string   `json:"base_dn"`
	UserSearchDN  string   `json:"user_search_dn"`
	UserFilter    string   `json:"user_filter"`
	BindDN        string   `json:"bind_dn"`
	BindPassword  string   `json:"bind_password"`
	AdminGroupDN  string   `json:"admin_group_dn"`
	WriterGroupDN string   `json:"writer_group_dn"`
	AdminUsers    []string `json:"admin_users"`
	AdminRoleName string   `json:"admin_role_name"`
	// UsernameAttribute is the LDAP attribute used as the canonical ECG Hub
	// username (external_id), e.g. "uid", "sAMAccountName", "cn" or "mail".
	// When empty, the login name typed by the user is used.
	UsernameAttribute string `json:"username_attribute"`
	// UUIDAttribute is the immutable LDAP attribute that uniquely identifies the
	// user regardless of renames — "objectGUID" (Active Directory, binary) or
	// "entryUUID" (OpenLDAP). Defaults to "objectGUID" when empty.
	UUIDAttribute string `json:"uuid_attribute"`
	JWTSecret     string `json:"jwt_secret"`
}

// defaultLDAPUUIDAttribute is used when no UUID attribute is configured.
const defaultLDAPUUIDAttribute = "objectGUID"

// formatLDAPUUID renders an LDAP UUID attribute value as a stable string. A 16-byte
// value is treated as an Active Directory objectGUID and formatted in its canonical
// mixed-endian GUID form; anything else (e.g. OpenLDAP entryUUID) is returned as-is.
func formatLDAPUUID(raw []byte) string {
	if len(raw) == 16 {
		return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
			raw[3], raw[2], raw[1], raw[0], raw[5], raw[4], raw[7], raw[6],
			raw[8], raw[9], raw[10], raw[11], raw[12], raw[13], raw[14], raw[15])
	}
	return string(raw)
}

// LoginWithLDAPFromDB reads the LDAP config from DB, authenticates the user, and issues a JWT.
// jwtSecret is the application JWT signing key (from JWT_SECRET env var).
// userStore (may be nil) registers the login in ecg_hub_users so LDAP users get
// the same stable internal identity as local/OIDC users.
func LoginWithLDAPFromDB(ctx context.Context, username, password, jwtSecret string, repo *repository.AuthConfigRepository, encKey string, userStore UserStore) (string, error) {
	// Defence in depth: reject an empty password before any bind. Some LDAP servers
	// treat a bind with an empty password as a successful "unauthenticated bind",
	// which would let an existing DN authenticate without a real password. The HTTP
	// login handler already rejects empty passwords; this guards other callers too.
	if password == "" {
		return "", fmt.Errorf("auth: ldap: empty password")
	}
	dbCfg, err := repo.Get("ldap")
	if err != nil || dbCfg == nil {
		slog.Debug("ldap: not configured", "error", err)
		return "", fmt.Errorf("auth: ldap: no DB config found")
	}

	decrypted, err := DecryptString(dbCfg.ConfigEncrypted, encKey)
	if err != nil {
		slog.Warn("ldap: decrypt config failed", "error", err)
		return "", fmt.Errorf("auth: ldap: decrypt config: %w", err)
	}

	var cfg ldapDBConfig
	if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
		slog.Warn("ldap: parse config failed", "error", err)
		return "", fmt.Errorf("auth: ldap: parse config: %w", err)
	}

	if cfg.Host == "" {
		slog.Warn("ldap: host not configured")
		return "", fmt.Errorf("auth: ldap: host not configured")
	}
	scheme := "ldap"
	port := cfg.Port
	if cfg.TLS {
		scheme = "ldaps"
		if port == 0 {
			port = 636
		}
	} else if port == 0 {
		port = 389
	}

	dialURL := fmt.Sprintf("%s://%s:%d", scheme, cfg.Host, port)
	l, err := ldap.DialURL(dialURL)
	if err != nil {
		slog.Warn("ldap: dial failed", "url", dialURL, "error", err)
		return "", fmt.Errorf("auth: ldap: dial: %w", err)
	}
	defer l.Close()

	// Bind as service account.
	if cfg.BindDN != "" {
		if err := l.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			slog.Warn("ldap: service bind failed", "error", err)
			return "", fmt.Errorf("auth: ldap: service bind: %w", err)
		}
	}

	// Build the search filter. When no explicit filter is set, derive it from the
	// username attribute (Keycloak-style): (<usernameAttribute>=<username>). This
	// is why setting "sAMAccountName" as the username attribute is enough for AD —
	// no need to also craft a custom filter. user_filter stays an advanced override
	// (use %s for the typed username, e.g. "(&(objectClass=user)(sAMAccountName=%s))").
	userFilter := cfg.UserFilter
	if userFilter == "" {
		attr := cfg.UsernameAttribute
		if attr == "" {
			attr = "uid"
		}
		userFilter = "(" + attr + "=%s)"
	}
	filter := fmt.Sprintf(userFilter, ldap.EscapeFilter(username))
	searchBase := cfg.UserSearchDN
	if searchBase == "" {
		searchBase = cfg.BaseDN
	}

	uuidAttr := cfg.UUIDAttribute
	if uuidAttr == "" {
		uuidAttr = defaultLDAPUUIDAttribute
	}

	// Request memberOf (for group-based roles), the username attribute and the
	// immutable UUID attribute.
	attrs := []string{"dn", "memberOf", uuidAttr}
	if cfg.UsernameAttribute != "" {
		attrs = append(attrs, cfg.UsernameAttribute)
	}

	searchReq := ldap.NewSearchRequest(
		searchBase,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		1, 0, false,
		filter,
		attrs,
		nil,
	)
	result, err := l.Search(searchReq)
	if err != nil {
		slog.Warn("ldap: search failed", "error", err)
		return "", fmt.Errorf("auth: ldap: search: %w", err)
	}
	if len(result.Entries) == 0 {
		slog.Debug("ldap: user not found", "login", username)
		return "", fmt.Errorf("auth: ldap: user not found")
	}
	entry := result.Entries[0]
	userDN := entry.DN

	// Bind as user to verify password.
	if err := l.Bind(userDN, password); err != nil {
		slog.Debug("ldap: password bind failed", "error", err)
		return "", fmt.Errorf("auth: ldap: invalid credentials")
	}

	// Resolve the canonical username from the configured attribute (fallback to the
	// login name typed by the user).
	effectiveUsername := username
	if cfg.UsernameAttribute != "" {
		if v := entry.GetAttributeValue(cfg.UsernameAttribute); v != "" {
			effectiveUsername = v
		}
	}

	// Resolve the immutable UUID (objectGUID / entryUUID). Read raw to handle the
	// binary AD objectGUID, then format to a stable string.
	ldapUUID := formatLDAPUUID(entry.GetRawAttributeValue(uuidAttr))

	memberOf := entry.GetAttributeValues("memberOf")

	// Determine role.
	adminRoleName := cfg.AdminRoleName
	if adminRoleName == "" {
		adminRoleName = "admin"
	}

	role := "reader"
	for _, u := range cfg.AdminUsers {
		if u == effectiveUsername || u == username {
			role = adminRoleName
			break
		}
	}
	if role == "reader" && cfg.AdminGroupDN != "" {
		for _, v := range memberOf {
			if v == cfg.AdminGroupDN {
				role = adminRoleName
				break
			}
		}
	}
	if role == "reader" && cfg.WriterGroupDN != "" {
		for _, v := range memberOf {
			if v == cfg.WriterGroupDN {
				role = "writer"
				break
			}
		}
	}

	// Register the login in ecg_hub_users (unified identity). A role assigned
	// from the admin UI takes priority over the LDAP-group-derived role.
	if userStore != nil {
		if dbRole, err := userStore.UpsertLogin(ctx, ldapUUID, effectiveUsername, "ldap", []string{role}); err == nil && dbRole != "" {
			role = dbRole
		}
	}

	// Issue JWT using the provided application secret.
	return IssueAppToken(effectiveUsername, role, []byte(jwtSecret))
}
