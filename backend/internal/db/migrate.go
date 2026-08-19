package db

import (
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	appmodels "github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// RunMigrations creates the schema from the current models using GORM
// AutoMigrate. Idempotent: safe to call on every startup.
//
// Fresh-install only: the historical ALTER/rename/backfill statements were
// removed. There is no upgrade path from an older schema — recreate the DB
// (see createDATABAE.sh).
func RunMigrations(db *gorm.DB) error {
	models := []any{
		&appmodels.Patient{},
		&appmodels.ECG{},
		&appmodels.AuditLog{},
		&appmodels.QuarantineEntry{},
		&repository.RoleRecord{},
		&repository.RolePermRecord{},
		&repository.UserRecord{},
		&appmodels.ExportJob{},
		&appmodels.ExportJobECG{},
		&appmodels.NihonKohdenTransfer{},
		&appmodels.ConnectorJob{},
		&appmodels.ECGBuffer{},
		&appmodels.UserPin{},
		&appmodels.APIKey{},
		&appmodels.Tag{},
		&appmodels.PatientTag{},
		&appmodels.ECGTag{},
		&appmodels.HL7MappingPreset{},
		&appmodels.HL7Mapping{},
		&appmodels.HL7Settings{},
		&appmodels.HL7Attempt{},
		&appmodels.HL7ORUAttempt{},
		&appmodels.LocalUser{},
		&appmodels.AuthProviderConfig{},
		&appmodels.ModuleConfig{},
		&appmodels.ModuleSettings{},
		&appmodels.UserWebhook{},
		&appmodels.WebhookDelivery{},
	}

	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			slog.Warn("db: auto migrate", "model", fmt.Sprintf("%T", m), "error", err)
		}
	}

	// Foreign keys AutoMigrate cannot generate: GORM inverts the UserPin→Patient
	// relation (see models/user_pin.go), PatientTag declares no Patient
	// navigation field (both reference the business key patients.patient_id),
	// and the per-user tables key on ecg_hub_users.id with no navigation field.
	// Idempotent: skipped when the constraint already exists.
	type fkFix struct{ table, name, ddl string }
	for _, fk := range []fkFix{
		{"user_pins", "fk_user_pins_patient",
			`ALTER TABLE user_pins ADD CONSTRAINT fk_user_pins_patient FOREIGN KEY (patient_id) REFERENCES patients(patient_id) ON UPDATE CASCADE ON DELETE CASCADE`},
		{"patient_tags", "fk_patient_tags_patient",
			`ALTER TABLE patient_tags ADD CONSTRAINT fk_patient_tags_patient FOREIGN KEY (patient_id) REFERENCES patients(patient_id) ON UPDATE CASCADE ON DELETE CASCADE`},
		{"connector_jobs", "fk_connector_jobs_quarantine",
			`ALTER TABLE connector_jobs ADD CONSTRAINT fk_connector_jobs_quarantine FOREIGN KEY (quarantine_id) REFERENCES quarantine_entries(id) ON DELETE CASCADE`},
		{"webhook_deliveries", "fk_webhook_deliveries_webhook",
			`ALTER TABLE webhook_deliveries ADD CONSTRAINT fk_webhook_deliveries_webhook FOREIGN KEY (webhook_id) REFERENCES user_webhooks(id) ON DELETE CASCADE`},
		// Per-user resources: deleting an account must not leave orphans that a
		// later account reusing the username could inherit (live API keys!).
		{"user_webhooks", "fk_user_webhooks_user",
			`ALTER TABLE user_webhooks ADD CONSTRAINT fk_user_webhooks_user FOREIGN KEY (user_id) REFERENCES ecg_hub_users(id) ON DELETE CASCADE`},
		{"api_keys", "fk_api_keys_user",
			`ALTER TABLE api_keys ADD CONSTRAINT fk_api_keys_user FOREIGN KEY (user_id) REFERENCES ecg_hub_users(id) ON DELETE CASCADE`},
		{"user_pins", "fk_user_pins_user",
			`ALTER TABLE user_pins ADD CONSTRAINT fk_user_pins_user FOREIGN KEY (user_id) REFERENCES ecg_hub_users(id) ON DELETE CASCADE`},
		{"export_jobs", "fk_export_jobs_user",
			`ALTER TABLE export_jobs ADD CONSTRAINT fk_export_jobs_user FOREIGN KEY (user_id) REFERENCES ecg_hub_users(id) ON DELETE CASCADE`},
	} {
		if db.Migrator().HasTable(fk.table) && !db.Migrator().HasConstraint(fk.table, fk.name) {
			if err := db.Exec(fk.ddl).Error; err != nil {
				slog.Warn("db: add foreign key", "constraint", fk.name, "error", err)
			} else {
				slog.Info("db: added foreign key", "constraint", fk.name)
			}
		}
	}

	// init default role
	if err := iniRole(db); err != nil {
		return err
	}

	// Trigram indexes for substring search. Patient/ECG search uses ILIKE '%term%'
	// (leading wildcard), which a B-tree index cannot serve — every search is a
	// sequential scan. pg_trgm GIN indexes make these index-assisted. Idempotent;
	// warnings (not fatals) keep startup resilient if the DB role cannot CREATE
	// EXTENSION (search still works, just without the index).
	if err := db.Exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm`).Error; err != nil {
		slog.Warn("db: pg_trgm extension unavailable — patient search will fall back to sequential scans", "error", err)
	} else {
		trgmIndexes := []string{
			`CREATE INDEX IF NOT EXISTS idx_patients_last_name_trgm ON patients USING gin (last_name gin_trgm_ops)`,
			`CREATE INDEX IF NOT EXISTS idx_patients_first_name_trgm ON patients USING gin (first_name gin_trgm_ops)`,
			`CREATE INDEX IF NOT EXISTS idx_patients_patient_id_trgm ON patients USING gin (patient_id gin_trgm_ops)`,
			`CREATE INDEX IF NOT EXISTS idx_patients_nda_trgm ON patients USING gin (nda gin_trgm_ops)`,
			`CREATE INDEX IF NOT EXISTS idx_ecgs_patient_id_trgm ON ecgs USING gin (patient_id gin_trgm_ops)`,
			`CREATE INDEX IF NOT EXISTS idx_ecgs_original_filename_trgm ON ecgs USING gin (original_filename gin_trgm_ops)`,
		}
		for _, ddl := range trgmIndexes {
			if err := db.Exec(ddl).Error; err != nil {
				slog.Warn("db: create trigram index failed", "error", err)
			}
		}
	}

	return nil
}

// Create Role admin | reader | writer
// If no role exists, create it. If it already exists, do nothing. This is idempotent and safe to call on every startup.
func iniRole(db *gorm.DB) error {
	// create the default role for users
	type roleSeed struct {
		name  string
		desc  string
		perms []string
	}
	seeds := []roleSeed{
		{
			name: "admin",
			desc: "Accès complet — toutes les permissions",
			// Seeded from the canonical list rather than a copy of it: the copy
			// had drifted (ecg.upload and ecg.send_result were missing), which
			// the Roles screen faithfully showed as unchecked while the checker
			// let admin do them anyway. Reading the list here also means a new
			// permission reaches admin on the next start with no migration.
			perms: auth.AllPermissions,
		},
		{
			name:  "reader",
			desc:  "Lecture seule",
			perms: []string{"patient.read", "ecg.read", "ecg.download"},
		},
		{
			name: "writer",
			desc: "Lecture + écriture",
			perms: []string{
				"patient.read", "ecg.read", "ecg.download",
				"ecg.delete", "ecg.force_hl7", "ecg.write",
				"tag.create", "tag.apply",
				"quarantine.read", "quarantine.delete", "quarantine.assign",
			},
		},
	}
	// Upsert seeded roles and sync permissions idempotently.
	// Creates the role if missing, then adds any permissions not yet present.
	// Never removes permissions set via the admin UI.
	for _, s := range seeds {
		var existing repository.RoleRecord
		if err := db.Where("name = ?", s.name).First(&existing).Error; err != nil {
			slog.Info("db: creating seeded role", "role", s.name)
			existing = repository.RoleRecord{Name: s.name, Description: s.desc}
			if err := db.Create(&existing).Error; err != nil {
				slog.Warn("db: create seeded role", "role", s.name, "error", err)
				continue
			}
		}
		for _, perm := range s.perms {
			var count int64
			db.Model(&repository.RolePermRecord{}).Where("role_id = ? AND permission = ?", existing.ID, perm).Count(&count)
			if count == 0 {
				p := repository.RolePermRecord{RoleID: existing.ID, Permission: perm, Role: existing}
				if err := db.Create(&p).Error; err != nil {
					slog.Warn("db: add permission to seeded role", "role", s.name, "perm", perm, "error", err)
				}
			}
		}
	}
	return nil
}
