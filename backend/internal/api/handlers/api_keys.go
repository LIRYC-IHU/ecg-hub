package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// apiKeyTokenPrefix namespaces every generated key so it is recognisable in
// logs and so a future auth middleware can fast-reject non-keys.
const apiKeyTokenPrefix = "ecghub_"

// apiKeyMaxNameLen bounds the user-supplied key name.
const apiKeyMaxNameLen = 100

// generateAPIKey returns a new plaintext key, its display prefix, and the
// SHA-256 hash to persist. The plaintext is never stored.
func generateAPIKey() (plaintext, prefix, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	plaintext = apiKeyTokenPrefix + token

	sum := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(sum[:])

	// Display prefix: namespace + first 6 chars of the token (non-secret).
	prefix = apiKeyTokenPrefix + token[:6]
	return plaintext, prefix, hash, nil
}

// ListAPIKeysHandler handles GET /api/v1/api-keys.
// Returns the authenticated user's keys — never the plaintext or the hash.
func ListAPIKeysHandler(repo *repository.APIKeyRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Get(mw.CtxKeyUserID).(string)
		keys, err := repo.ListByUser(userID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", "failed to list API keys"))
		}
		if keys == nil {
			keys = []models.APIKey{}
		}
		return c.JSON(http.StatusOK, map[string]any{"data": keys})
	}
}

// CreateAPIKeyHandler handles POST /api/v1/api-keys.
// Body: { "name": "my integration" }
// Returns the created key metadata plus the plaintext `key` — shown exactly once.
func CreateAPIKeyHandler(repo *repository.APIKeyRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Get(mw.CtxKeyUserID).(string)

		var body struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid body"))
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "name is required"))
		}
		if len(name) > apiKeyMaxNameLen {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "name is too long"))
		}

		plaintext, prefix, hash, err := generateAPIKey()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", "failed to generate key"))
		}

		key := models.APIKey{
			UserID:  userID,
			Name:    name,
			Prefix:  prefix,
			KeyHash: hash,
		}
		if err := repo.Create(&key); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", "failed to store API key"))
		}

		// The plaintext key is included only in this creation response.
		return c.JSON(http.StatusCreated, map[string]any{
			"data": map[string]any{
				"id":         key.ID,
				"name":       key.Name,
				"prefix":     key.Prefix,
				"created_at": key.CreatedAt,
				"key":        plaintext,
			},
		})
	}
}

// DeleteAPIKeyHandler handles DELETE /api/v1/api-keys/:id.
// Only deletes a key owned by the authenticated user.
func DeleteAPIKeyHandler(repo *repository.APIKeyRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Get(mw.CtxKeyUserID).(string)
		id := c.Param("id")
		if id == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "id is required"))
		}
		deleted, err := repo.DeleteByUserAndID(userID, id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", "failed to delete API key"))
		}
		if !deleted {
			return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "API key not found"))
		}
		return c.NoContent(http.StatusNoContent)
	}
}
