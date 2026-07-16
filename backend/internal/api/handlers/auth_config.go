package handlers

// maskedSecret is the placeholder displayed for secrets in GET responses.
// Shared across the admin config services (auth/module/connector); the former
// REST auth-provider handlers were removed with the gRPC migration.
const maskedSecret = "••••••"

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

// SaveOIDCRequest is the config payload parsed by AuthAdminService.SaveOIDC/TestOIDC.
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

// SaveLDAPRequest is the config payload parsed by AuthAdminService.SaveLDAP/TestLDAP.
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
