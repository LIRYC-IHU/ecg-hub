package handlers

import (
	"errors"
	"regexp"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"gorm.io/gorm"
)

// digitRegex checks for at least one digit in the password.
var digitRegex = regexp.MustCompile(`[0-9]`)

// Setup sentinel errors, shared by the gRPC SetupService handler so it can map
// them to the right Connect codes (InvalidArgument / FailedPrecondition).
var (
	errSetupUsernameTooShort = errors.New("username must be at least 3 characters")
	errSetupPasswordTooShort = errors.New("password must be at least 8 characters")
	errSetupPasswordNoDigit  = errors.New("password must contain at least 1 digit")
	errSetupAlreadyInit      = errors.New("system is already initialized")
)

// validateLocalUsername and validateLocalPassword hold the credential rules for
// local accounts, shared by the setup wizard, admin user creation and the
// self-service password change so all three agree.
func validateLocalUsername(username string) error {
	if len(username) < 3 {
		return errSetupUsernameTooShort
	}
	return nil
}

func validateLocalPassword(password string) error {
	if len(password) < 8 {
		return errSetupPasswordTooShort
	}
	if !digitRegex.MatchString(password) {
		return errSetupPasswordNoDigit
	}
	return nil
}

// createFirstAdmin validates the credentials and atomically creates the first
// local admin account (advisory-lock guarded so two concurrent calls can't both
// pass the "no identity yet" check). Returns errSetup* sentinels for the caller
// to translate to a transport-specific status. Shared by the gRPC handler.
func createFirstAdmin(db *gorm.DB, username, password string) (*models.LocalUser, error) {
	if err := validateLocalUsername(username); err != nil {
		return nil, err
	}
	if err := validateLocalPassword(password); err != nil {
		return nil, err
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}

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
			return errSetupAlreadyInit
		}
		u := &models.LocalUser{
			Username:     username,
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
		return nil, txErr
	}

	// Write audit log entry for system initialization.
	auditRepo := repository.NewAuditRepository(db)
	_ = auditRepo.Insert(&models.AuditLog{
		UserID:     user.ID,
		Action:     "system_initialized",
		ResourceID: user.ID,
	})

	return user, nil
}

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

// GetStatus and Initialize are now served over gRPC/Connect by
// SetupServiceHandler (setup_service.go); countIdentities and createFirstAdmin
// above are shared with it.
