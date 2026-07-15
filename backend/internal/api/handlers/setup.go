package handlers

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"gorm.io/gorm"
)

// digitRegex checks for at least one digit in the password.
var digitRegex = regexp.MustCompile(`[0-9]`)

// countIdentities returns local users plus unified-identity rows (ecg_hub_users).
// The setup endpoint treats the system as initialised when either is non-zero, so
// an OIDC/LDAP-only deployment (no local user) still locks /setup as soon as any
// identity has logged in — closing the window where an unauthenticated caller
// could otherwise create a local admin.
func countIdentities(db *gorm.DB) (int64, error) {
	var local, hub int64
	if err := db.Model(&models.LocalUser{}).Count(&local).Error; err != nil {
		return 0, err
	}
	if err := db.Table("ecg_hub_users").Count(&hub).Error; err != nil {
		return 0, err
	}
	return local + hub, nil
}

// GetStatus is now served over gRPC/Connect by SetupServiceHandler
// (setup_service.go); countIdentities above is shared with it.

// SetupRequest is the JSON body for POST /api/v1/setup.
type SetupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// SetupHandler creates the first local admin user (system initialization).
// Public endpoint — no authentication required.
// Returns 409 if the system is already initialized.
//
//	@Summary		Initialize system
//	@Description	Creates the first local admin user. Returns 409 if already initialized.
//	@Tags			setup
//	@Accept			json
//	@Produce		json
//	@Param			body	body		SetupRequest	true	"Admin credentials"
//	@Success		201		{object}	map[string]string
//	@Failure		400		{object}	map[string]string
//	@Failure		409		{object}	map[string]string
//	@Router			/api/v1/setup [post]
func SetupHandler(repo *repository.LocalUserRepository, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req SetupRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid request body"))
		}

		// Validate username: min 3 characters.
		if len(req.Username) < 3 {
			return c.JSON(http.StatusBadRequest, mw.APIError("VALIDATION_ERROR", "username must be at least 3 characters"))
		}

		// Validate password: min 8 characters + at least 1 digit.
		if len(req.Password) < 8 {
			return c.JSON(http.StatusBadRequest, mw.APIError("VALIDATION_ERROR", "password must be at least 8 characters"))
		}
		if !digitRegex.MatchString(req.Password) {
			return c.JSON(http.StatusBadRequest, mw.APIError("VALIDATION_ERROR", "password must contain at least 1 digit"))
		}

		// Hash the password.
		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL_ERROR", "failed to hash password"))
		}

		// Atomic initialization: use advisory lock + count check inside a transaction
		// to prevent race conditions (two concurrent setup calls both passing the check).
		var user *models.LocalUser
		txErr := db.Transaction(func(tx *gorm.DB) error {
			// pg_advisory_xact_lock ensures only one setup can run at a time.
			if err := tx.Exec("SELECT pg_advisory_xact_lock(42)").Error; err != nil {
				return err
			}
			count, err := countIdentities(tx)
			if err != nil {
				return err
			}
			if count > 0 {
				return fmt.Errorf("ALREADY_INITIALIZED")
			}
			u := &models.LocalUser{
				Username:     req.Username,
				PasswordHash: hash,
				Role:         "admin",
				Active:       true,
			}
			if err := tx.Create(u).Error; err != nil {
				return err
			}
			user = u
			return nil
		})
		if txErr != nil {
			if txErr.Error() == "ALREADY_INITIALIZED" {
				return c.JSON(http.StatusConflict, mw.APIError("ALREADY_INITIALIZED", "system is already initialized"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL_ERROR", "failed to create admin user"))
		}

		// Write audit log entry for system initialization.
		auditRepo := repository.NewAuditRepository(db)
		_ = auditRepo.Insert(&models.AuditLog{
			UserID:     user.ID,
			Action:     "system_initialized",
			ResourceID: user.ID,
		})

		return c.JSON(http.StatusCreated, map[string]string{
			"id":       user.ID,
			"username": user.Username,
			"role":     user.Role,
		})
	}
}
