package auth

import (
	"context"
	"encoding/json"
	"fmt"

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
	JWTSecret     string   `json:"jwt_secret"`
}

// LoginWithLDAPFromDB reads the LDAP config from DB, authenticates the user, and issues a JWT.
// jwtSecret is the application JWT signing key (from JWT_SECRET env var).
// userStore (may be nil) registers the login in ecg_hub_users so LDAP users get
// the same stable internal identity as local/OIDC users.
func LoginWithLDAPFromDB(ctx context.Context, username, password, jwtSecret string, repo *repository.AuthConfigRepository, encKey string, userStore UserStore) (string, error) {
	dbCfg, err := repo.Get("ldap")
	if err != nil || dbCfg == nil {
		return "", fmt.Errorf("auth: ldap: no DB config found")
	}

	decrypted, err := DecryptString(dbCfg.ConfigEncrypted, encKey)
	if err != nil {
		return "", fmt.Errorf("auth: ldap: decrypt config: %w", err)
	}

	var cfg ldapDBConfig
	if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
		return "", fmt.Errorf("auth: ldap: parse config: %w", err)
	}

	if cfg.Host == "" {
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

	l, err := ldap.DialURL(fmt.Sprintf("%s://%s:%d", scheme, cfg.Host, port))
	if err != nil {
		return "", fmt.Errorf("auth: ldap: dial: %w", err)
	}
	defer l.Close()

	// Bind as service account.
	if cfg.BindDN != "" {
		if err := l.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			return "", fmt.Errorf("auth: ldap: service bind: %w", err)
		}
	}

	// Search for user.
	userFilter := cfg.UserFilter
	if userFilter == "" {
		userFilter = "(uid=%s)"
	}
	filter := fmt.Sprintf(userFilter, ldap.EscapeFilter(username))
	searchBase := cfg.UserSearchDN
	if searchBase == "" {
		searchBase = cfg.BaseDN
	}

	searchReq := ldap.NewSearchRequest(
		searchBase,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		1, 0, false,
		filter,
		[]string{"dn", "memberOf"},
		nil,
	)
	result, err := l.Search(searchReq)
	if err != nil {
		return "", fmt.Errorf("auth: ldap: search: %w", err)
	}
	if len(result.Entries) == 0 {
		return "", fmt.Errorf("auth: ldap: user not found")
	}
	userDN := result.Entries[0].DN

	// Bind as user to verify password.
	if err := l.Bind(userDN, password); err != nil {
		return "", fmt.Errorf("auth: ldap: invalid credentials")
	}

	// Determine role.
	adminRoleName := cfg.AdminRoleName
	if adminRoleName == "" {
		adminRoleName = "admin"
	}

	role := "reader"
	for _, u := range cfg.AdminUsers {
		if u == username {
			role = adminRoleName
			break
		}
	}
	if role == "reader" && cfg.AdminGroupDN != "" {
		for _, v := range result.Entries[0].GetAttributeValues("memberOf") {
			if v == cfg.AdminGroupDN {
				role = adminRoleName
				break
			}
		}
	}
	if role == "reader" && cfg.WriterGroupDN != "" {
		for _, v := range result.Entries[0].GetAttributeValues("memberOf") {
			if v == cfg.WriterGroupDN {
				role = "writer"
				break
			}
		}
	}

	// Register the login in ecg_hub_users (unified identity). A role assigned
	// from the admin UI takes priority over the LDAP-group-derived role.
	if userStore != nil {
		if dbRole, err := userStore.UpsertLogin(ctx, username, "ldap", []string{role}); err == nil && dbRole != "" {
			role = dbRole
		}
	}

	// Issue JWT using the provided application secret.
	return IssueAppToken(username, role, []byte(jwtSecret))
}
