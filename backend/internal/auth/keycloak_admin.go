package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// KeycloakAdminClient interacts with the Keycloak Admin REST API (FR: role management).
// It uses client_credentials to obtain a short-lived admin token before each operation.
type KeycloakAdminClient struct {
	baseURL      string // e.g., "http://keycloak:8080"
	realm        string // e.g., "ecg_hub"
	clientID     string
	clientSecret string
	httpClient   *http.Client
}

// NewKeycloakAdminClient constructs a client from the OIDC issuer URL.
// issuerURL format: "https://keycloak/realms/{realm}" — realm and base URL are extracted from it.
// When skipTLS is true, certificate verification is skipped (dev only).
func NewKeycloakAdminClient(issuerURL, clientID, clientSecret string, skipTLS bool) (*KeycloakAdminClient, error) {
	parsed, err := url.Parse(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("keycloak_admin: parse issuer URL: %w", err)
	}
	// issuerURL path is /realms/{realm}; split off the realm.
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 || segments[len(segments)-2] != "realms" {
		return nil, fmt.Errorf("keycloak_admin: issuer URL must contain /realms/{realm}, got %q", parsed.Path)
	}
	realm := segments[len(segments)-1]
	// Base URL is everything before /realms
	realmIdx := strings.LastIndex(parsed.Path, "/realms/")
	baseURL := parsed.Scheme + "://" + parsed.Host + parsed.Path[:realmIdx]

	transport := http.DefaultTransport
	if skipTLS {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // dev/self-signed only
		}
	}

	return &KeycloakAdminClient{
		baseURL:      baseURL,
		realm:        realm,
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 10 * time.Second, Transport: transport},
	}, nil
}

// KeycloakUser represents a user entry from the Keycloak Admin API.
type KeycloakUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Enabled  bool   `json:"enabled"`
	// ECGHubRole is populated by ListUsersWithRoles, not from Keycloak directly.
	ECGHubRole string `json:"ecg_hub_role,omitempty"`
}

// keycloakRole is the Keycloak role representation.
type keycloakRole struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ecgHubRoles are the valid ECG Hub realm roles in Keycloak.
var ecgHubRoles = []string{"admin", "writer", "reader"}

// ListUsersWithRoles returns all users in the realm with their current ECG Hub role resolved.
func (c *KeycloakAdminClient) ListUsersWithRoles(ctx context.Context) ([]KeycloakUser, error) {
	token, err := c.adminToken(ctx)
	if err != nil {
		return nil, err
	}

	// List all users.
	usersURL := fmt.Sprintf("%s/admin/realms/%s/users?max=500", c.baseURL, c.realm)
	body, err := c.get(ctx, usersURL, token)
	if err != nil {
		return nil, fmt.Errorf("keycloak_admin: list users: %w", err)
	}

	var users []KeycloakUser
	if err := json.Unmarshal(body, &users); err != nil {
		return nil, fmt.Errorf("keycloak_admin: decode users: %w", err)
	}

	// Resolve ECG Hub role for each user.
	for i := range users {
		role, err := c.userECGHubRole(ctx, users[i].ID, token)
		if err != nil {
			// Non-fatal: if role lookup fails, leave role empty.
			continue
		}
		users[i].ECGHubRole = role
	}

	return users, nil
}

// SetUserRole assigns the given ECG Hub role to the user, replacing any existing ECG Hub roles.
// role must be one of "reader", "writer", "admin".
func (c *KeycloakAdminClient) SetUserRole(ctx context.Context, userID, role string) error {
	if !isValidRole(role) {
		return fmt.Errorf("keycloak_admin: invalid role %q", role)
	}

	token, err := c.adminToken(ctx)
	if err != nil {
		return err
	}

	// Get all available realm roles to find IDs.
	allRoles, err := c.listRealmRoles(ctx, token)
	if err != nil {
		return err
	}

	rolesByName := make(map[string]keycloakRole, len(allRoles))
	for _, r := range allRoles {
		rolesByName[r.Name] = r
	}

	// Revoke all current ECG Hub roles.
	var toRevoke []keycloakRole
	for _, name := range ecgHubRoles {
		if r, ok := rolesByName[name]; ok {
			toRevoke = append(toRevoke, r)
		}
	}
	if len(toRevoke) > 0 {
		if err := c.deleteUserRoles(ctx, userID, toRevoke, token); err != nil {
			return fmt.Errorf("keycloak_admin: revoke roles: %w", err)
		}
	}

	// Assign the new role.
	target, ok := rolesByName[role]
	if !ok {
		return fmt.Errorf("keycloak_admin: role %q not found in realm — create it in Keycloak first", role)
	}
	if err := c.addUserRoles(ctx, userID, []keycloakRole{target}, token); err != nil {
		return fmt.Errorf("keycloak_admin: assign role: %w", err)
	}
	return nil
}

// adminToken obtains a short-lived access token via client_credentials grant.
func (c *KeycloakAdminClient) adminToken(ctx context.Context) (string, error) {
	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", c.baseURL, c.realm)
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("keycloak_admin: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("keycloak_admin: token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("keycloak_admin: token request failed (HTTP %d): %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil || tokenResp.AccessToken == "" {
		return "", fmt.Errorf("keycloak_admin: decode token response: %w", err)
	}
	return tokenResp.AccessToken, nil
}

// userECGHubRole returns the highest ECG Hub role assigned to the user ("admin" > "writer" > "reader" > "").
func (c *KeycloakAdminClient) userECGHubRole(ctx context.Context, userID, token string) (string, error) {
	rolesURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/role-mappings/realm", c.baseURL, c.realm, userID)
	body, err := c.get(ctx, rolesURL, token)
	if err != nil {
		return "", err
	}
	var roles []keycloakRole
	if err := json.Unmarshal(body, &roles); err != nil {
		return "", err
	}
	assigned := make(map[string]bool, len(roles))
	for _, r := range roles {
		assigned[r.Name] = true
	}
	for _, name := range ecgHubRoles { // checked in priority order: admin, writer, reader
		if assigned[name] {
			return name, nil
		}
	}
	return "", nil
}

// listRealmRoles returns all realm-level roles.
func (c *KeycloakAdminClient) listRealmRoles(ctx context.Context, token string) ([]keycloakRole, error) {
	rolesURL := fmt.Sprintf("%s/admin/realms/%s/roles", c.baseURL, c.realm)
	body, err := c.get(ctx, rolesURL, token)
	if err != nil {
		return nil, fmt.Errorf("keycloak_admin: list realm roles: %w", err)
	}
	var roles []keycloakRole
	if err := json.Unmarshal(body, &roles); err != nil {
		return nil, fmt.Errorf("keycloak_admin: decode realm roles: %w", err)
	}
	return roles, nil
}

// addUserRoles assigns roles to a user via POST role-mappings/realm.
func (c *KeycloakAdminClient) addUserRoles(ctx context.Context, userID string, roles []keycloakRole, token string) error {
	rolesURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/role-mappings/realm", c.baseURL, c.realm, userID)
	return c.jsonRequest(ctx, http.MethodPost, rolesURL, roles, token)
}

// deleteUserRoles removes roles from a user via DELETE role-mappings/realm.
func (c *KeycloakAdminClient) deleteUserRoles(ctx context.Context, userID string, roles []keycloakRole, token string) error {
	rolesURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/role-mappings/realm", c.baseURL, c.realm, userID)
	return c.jsonRequest(ctx, http.MethodDelete, rolesURL, roles, token)
}

func (c *KeycloakAdminClient) get(ctx context.Context, endpoint, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return body, nil
}

func (c *KeycloakAdminClient) jsonRequest(ctx context.Context, method, endpoint string, payload any, token string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return nil
}

func isValidRole(role string) bool {
	for _, r := range ecgHubRoles {
		if r == role {
			return true
		}
	}
	return false
}
