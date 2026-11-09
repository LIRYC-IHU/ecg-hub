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
	if err := migrateTenants(db); err != nil {
		return err
	}
	dropGlobalKeys(db)

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
		&appmodels.HL7InboundMessage{},
		&appmodels.HL7ORUAttempt{},
		&appmodels.LocalUser{},
		&appmodels.AuthProviderConfig{},
		&appmodels.Device{},
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
	//
	// Patients are keyed on (tenant_id, patient_id). ecgs uses NO ACTION rather
	// than RESTRICT: a patient with ECGs still cannot be deleted, but deleting a
	// tenant removes its patients and ECGs in one statement, and RESTRICT would
	// check before the cascade reaches the ECGs.
	type fkFix struct{ table, name, ddl string }
	fks := []fkFix{
		{"ecgs", "fk_ecgs_patient",
			`ALTER TABLE ecgs ADD CONSTRAINT fk_ecgs_patient FOREIGN KEY (tenant_id, patient_id) REFERENCES patients(tenant_id, patient_id) ON UPDATE CASCADE ON DELETE NO ACTION`},
		{"user_pins", "fk_user_pins_tenant_patient",
			`ALTER TABLE user_pins ADD CONSTRAINT fk_user_pins_tenant_patient FOREIGN KEY (tenant_id, patient_id) REFERENCES patients(tenant_id, patient_id) ON UPDATE CASCADE ON DELETE CASCADE`},
		{"patient_tags", "fk_patient_tags_tenant_patient",
			`ALTER TABLE patient_tags ADD CONSTRAINT fk_patient_tags_tenant_patient FOREIGN KEY (tenant_id, patient_id) REFERENCES patients(tenant_id, patient_id) ON UPDATE CASCADE ON DELETE CASCADE`},
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
	}
	// Removing a tenant removes every row it owns. audit_logs is left out on
	// purpose: traceability outlives the tenant.
	for _, table := range tenantTables {
		name := "fk_" + table + "_tenant"
		fks = append(fks, fkFix{table, name, fmt.Sprintf(
			`ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE`, table, name)})
	}
	for _, fk := range fks {
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

	// Delivery history is read newest-first per webhook and pruned by age; both
	// queries are served by this composite index.
	if err := db.Exec(
		`CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_webhook_delivered_at ` +
			`ON webhook_deliveries (webhook_id, delivered_at DESC)`).Error; err != nil {
		slog.Warn("db: create webhook delivery index failed", "error", err)
	}

	// The upload spool. In s3 mode the rows whose file_path is still a local
	// path are the backlog, and the uploader scans for them on every tick. A
	// partial index keeps that scan proportional to the backlog instead of to
	// the table, which grows for the life of the installation.
	spoolIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_ecgs_upload_spool ON ecgs (ingested_at) WHERE file_path NOT LIKE 's3://%'`,
		`CREATE INDEX IF NOT EXISTS idx_quarantine_upload_spool ON quarantine_entries (received_at) WHERE file_path NOT LIKE 's3://%'`,
	}
	for _, ddl := range spoolIndexes {
		if err := db.Exec(ddl).Error; err != nil {
			slog.Warn("db: create upload spool index failed", "error", err)
		}
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

// tenantTables are the tables whose rows belong to a tenant and go with it.
// audit_logs carries tenant_id too, without the foreign key.
var tenantTables = []string{
	"patients", "ecgs", "quarantine_entries", "ecg_buffer", "tags",
	"patient_tags", "ecg_tags", "user_pins", "ecg_hub_users", "api_keys",
	"user_webhooks", "webhook_deliveries", "export_jobs", "export_job_ecgs",
	"connector_jobs", "hl7_attempts", "hl7_oru_attempts", "nihon_kohden_transfers",
}

// migrateTenants creates the tenants table and the default tenant, which must
// exist before any tenant_id column refers to it.
func migrateTenants(db *gorm.DB) error {
	if err := db.AutoMigrate(&appmodels.Tenant{}); err != nil {
		return fmt.Errorf("db: migrate tenants: %w", err)
	}
	if err := db.Exec(
		`INSERT INTO tenants (id, slug, name) VALUES (?, 'default', 'Default') ON CONFLICT (id) DO NOTHING`,
		appmodels.DefaultTenantID).Error; err != nil {
		return fmt.Errorf("db: create default tenant: %w", err)
	}
	return nil
}

// dropGlobalKeys removes the deployment-wide unique keys that became
// per-tenant: patients(patient_id) and ecgs(content_hash). It runs before
// AutoMigrate, which would otherwise try to drop the patients constraint
// itself and fail on the foreign keys that depend on it. CASCADE takes those
// foreign keys with it; the composite ones replace them below. A no-op on a
// fresh or already migrated database.
func dropGlobalKeys(db *gorm.DB) {
	for _, ddl := range []string{
		`DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT c.conname
    FROM pg_constraint c
    JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attname = 'patient_id'
    WHERE c.conrelid = to_regclass(quote_ident(current_schema()) || '.patients') AND c.contype = 'u' AND c.conkey = ARRAY[a.attnum]
  LOOP
    EXECUTE format('ALTER TABLE patients DROP CONSTRAINT %I CASCADE', r.conname);
  END LOOP;
END $$`,
		`DROP INDEX IF EXISTS idx_ecgs_content_hash`,
		`DROP INDEX IF EXISTS idx_quarantine_entries_content_hash`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			slog.Warn("db: drop global unique key", "error", err)
		}
	}
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
	// Seeding happens on the first start only: an empty roles table means a
	// brand-new install, anything else means an operator already owns the role
	// model. Without this an admin/reader/writer deleted on purpose — a site
	// running its own named roles instead — came back with every permission at
	// the next restart.
	//
	// Deleting *every* role therefore re-seeds on the next start. That is the
	// deliberate recovery path for an install that has locked itself out.
	var roleCount int64
	if err := db.Model(&repository.RoleRecord{}).Count(&roleCount).Error; err != nil {
		return fmt.Errorf("db: count roles: %w", err)
	}
	firstStart := roleCount == 0

	// Sync permissions idempotently on the roles that exist: a permission added
	// to the canonical list reaches them on the next start with no migration.
	// Never removes permissions set via the admin UI, and never re-creates a
	// role that was deleted.
	for _, s := range seeds {
		var existing repository.RoleRecord
		if err := db.Where("name = ?", s.name).First(&existing).Error; err != nil {
			if !firstStart {
				continue // deleted on purpose — leave it deleted
			}
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
