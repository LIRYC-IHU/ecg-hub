package db

import (
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	appmodels "github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// RunMigrations applies all schema changes using GORM AutoMigrate.
// It is idempotent: safe to call on every startup.
// AutoMigrate only adds — it never drops columns or tables.
func RunMigrations(db *gorm.DB) error {
	// drop all tables and recreate them from scratch, to ensure the schema is exactly as defined in the models.

	// Disable FK checks during drop+recreate (PostgreSQL syntax).
	db.Exec("SET session_replication_role = 'replica'")
	defer db.Exec("SET session_replication_role = 'origin'")
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
	// for _, m := range models {
	// 	err := db.Migrator().DropTable(m)
	// 	if err != nil {
	// 		return fmt.Errorf("db: drop table %s: %w", m, err)
	// 	}
	// 	fmt.Printf("Dropped table for %T\n", m)
	// }
	// Breaking change: export_jobs.format (string) → formats (jsonb string array).
	// Drop the orphaned column when present; AutoMigrate will then create the new "formats" column.
	if db.Migrator().HasColumn(&appmodels.ExportJob{}, "format") {
		if err := db.Migrator().DropColumn(&appmodels.ExportJob{}, "format"); err != nil {
			slog.Warn("db: drop export_jobs.format failed", "error", err)
		}
	}

	// Widen user_id columns from varchar(36) to text so any external ID length is accepted.
	// AutoMigrate doesn't alter existing column types, so we do it explicitly.
	// Only run when the table already exists (skipped on fresh installs).
	if db.Migrator().HasTable("audit_logs") {
		if err := db.Exec(`ALTER TABLE audit_logs ALTER COLUMN user_id TYPE text`).Error; err != nil {
			slog.Warn("db: migrate user_id column type", "table", "audit_logs", "error", err)
		}
	}
	// (export_jobs.user_id is migrated to uuid by migrateUserIdentity below —
	// the historical widen-to-text statement was removed so it cannot undo it.)

	// Rename patients.nip → patients.nda (NIP was incorrect, NDA = Numéro de Dossier Administratif).
	if db.Migrator().HasColumn(&appmodels.Patient{}, "nip") && !db.Migrator().HasColumn(&appmodels.Patient{}, "nda") {
		if err := db.Exec(`ALTER TABLE patients RENAME COLUMN nip TO nda`).Error; err != nil {
			slog.Warn("db: rename patients.nip to nda failed", "error", err)
		}
	}

	// Drop hl7_mappings.label — removed from model (was used to store sample values, serves no purpose).
	if db.Migrator().HasColumn(&appmodels.HL7Mapping{}, "label") {
		if err := db.Migrator().DropColumn(&appmodels.HL7Mapping{}, "label"); err != nil {
			slog.Warn("db: drop hl7_mappings.label failed", "error", err)
		}
	}

	// Widen varchar(36) columns to correct types (uuid/text) on existing installs.
	// AutoMigrate does not alter column types, so we do it explicitly.
	if db.Migrator().HasTable("ecg_hub_users") {
		for _, stmt := range []string{
			`ALTER TABLE ecg_hub_users ALTER COLUMN external_id TYPE text`,
			`ALTER TABLE ecg_hub_users ALTER COLUMN role_id TYPE uuid USING role_id::uuid`,
		} {
			if err := db.Exec(stmt).Error; err != nil {
				slog.Warn("db: widen ecg_hub_users column", "stmt", stmt, "error", err)
			}
		}
	}
	if db.Migrator().HasTable("ecgs") {
		if err := db.Exec(`ALTER TABLE ecgs ALTER COLUMN patient_id TYPE text`).Error; err != nil {
			slog.Warn("db: widen ecgs.patient_id", "error", err)
		}
	}

	// Backfill quarantine_entries.category for rows created before the "unidentified"
	// review workflow existed. AutoMigrate adds the column with DEFAULT 'error', but
	// this guards rows that predate the column (NULL/empty) on any edge case.
	if db.Migrator().HasColumn(&appmodels.QuarantineEntry{}, "category") {
		if err := db.Exec(`UPDATE quarantine_entries SET category = 'error' WHERE category IS NULL OR category = ''`).Error; err != nil {
			slog.Warn("db: backfill quarantine_entries.category", "error", err)
		}
	}
	if db.Migrator().HasTable("connector_jobs") {
		if err := db.Exec(`ALTER TABLE connector_jobs ALTER COLUMN ecg_id TYPE uuid USING ecg_id::uuid`).Error; err != nil {
			slog.Warn("db: widen connector_jobs.ecg_id", "error", err)
		}
		// Files that failed ingestion are now proxied too: such jobs reference a
		// quarantine entry instead of an ECG, so ecg_id must accept NULL.
		if err := db.Exec(`ALTER TABLE connector_jobs ALTER COLUMN ecg_id DROP NOT NULL`).Error; err != nil {
			slog.Warn("db: drop NOT NULL on connector_jobs.ecg_id", "error", err)
		}
	}
	if db.Migrator().HasTable("export_job_ecgs") {
		for _, stmt := range []string{
			`ALTER TABLE export_job_ecgs ALTER COLUMN export_job_id TYPE uuid USING export_job_id::uuid`,
			`ALTER TABLE export_job_ecgs ALTER COLUMN ecg_id TYPE uuid USING ecg_id::uuid`,
		} {
			if err := db.Exec(stmt).Error; err != nil {
				slog.Warn("db: widen export_job_ecgs column", "stmt", stmt, "error", err)
			}
		}
	}

	// Drop stale FK constraints from previous migration attempts — only when the table exists.
	if db.Migrator().HasTable("audit_logs") {
		if err := db.Exec(`ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS fk_ecg_hub_users_audit_log`).Error; err != nil {
			slog.Warn("db: drop stale fk constraint", "table", "audit_logs", "error", err)
		}
	}
	if db.Migrator().HasTable("export_jobs") {
		if err := db.Exec(`ALTER TABLE export_jobs DROP CONSTRAINT IF EXISTS fk_ecg_hub_users_export_job`).Error; err != nil {
			slog.Warn("db: drop stale fk constraint", "table", "export_jobs", "error", err)
		}
	}

	// Detect (before AutoMigrate creates it) whether ecgs.viewed_at is a brand-new
	// column on an existing table. If so, we backfill it once below so pre-existing
	// ECGs are treated as already seen — only genuinely new arrivals show as "new".
	backfillViewedAt := db.Migrator().HasTable("ecgs") &&
		!db.Migrator().HasColumn(&appmodels.ECG{}, "viewed_at")

	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			slog.Warn("db: auto migrate", "model", fmt.Sprintf("%T", m), "error", err)
		}
	}

	// One-time backfill: mark all pre-existing ECGs as viewed so the "new ECG"
	// indicator doesn't light up every patient on first deploy. Runs only on the
	// migration that introduces the column (guarded above), never on later restarts.
	if backfillViewedAt {
		if err := db.Exec(`UPDATE ecgs SET viewed_at = ingested_at WHERE viewed_at IS NULL`).Error; err != nil {
			slog.Warn("db: backfill ecgs.viewed_at", "error", err)
		} else {
			slog.Info("db: backfilled ecgs.viewed_at for pre-existing ECGs")
		}
	}

	// Foreign keys that AutoMigrate cannot generate: GORM inverts the
	// UserPin→Patient relation (see models/user_pin.go), and PatientTag declares
	// no Patient navigation field. Both reference the business key
	// patients.patient_id. Idempotent: skipped when the constraint exists.
	// Fails (warn only) if orphan rows exist — clean those manually first.
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
	} {
		if db.Migrator().HasTable(fk.table) && !db.Migrator().HasConstraint(fk.table, fk.name) {
			if err := db.Exec(fk.ddl).Error; err != nil {
				slog.Warn("db: add foreign key", "constraint", fk.name, "error", err)
			} else {
				slog.Info("db: added foreign key", "constraint", fk.name)
			}
		}
	}

	// ecg_hub_users.external_id must be unique: role resolution (GetCurrentRole)
	// and login upsert look up by external_id alone — duplicate rows would make
	// the effective role non-deterministic. The index was originally created
	// non-unique; rebuild it as unique when needed (fails with a warn if
	// duplicate external_ids already exist — deduplicate manually first).
	var extIDUnique bool
	db.Raw(`SELECT COALESCE((SELECT x.indisunique FROM pg_class c
		JOIN pg_index x ON x.indexrelid = c.oid
		WHERE c.relname = 'idx_ecg_hub_users_external_id'), false)`).Scan(&extIDUnique)
	if !extIDUnique {
		if err := db.Exec(`DROP INDEX IF EXISTS idx_ecg_hub_users_external_id`).Error; err != nil {
			slog.Warn("db: drop non-unique external_id index", "error", err)
		}
		if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_ecg_hub_users_external_id ON ecg_hub_users(external_id)`).Error; err != nil {
			slog.Warn("db: create unique external_id index", "error", err)
		} else {
			slog.Info("db: rebuilt idx_ecg_hub_users_external_id as unique")
		}
	}

	// init default role
	if err := iniRole(db); err != nil {
		return err
	}

	// Unified identity: per-user resources keyed on ecg_hub_users.id (uuid)
	// with ON DELETE CASCADE. Runs after iniRole so role names resolve on
	// fresh installs. Idempotent.
	migrateUserIdentity(db)

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

// migrateUserIdentity converts the per-user resource tables from a free-text
// user_id (the reusable JWT subject — username) to the stable internal
// ecg_hub_users.id uuid, with ON DELETE CASCADE foreign keys.
//
// Why: with a text user_id, deleting an account left orphan webhooks / API
// keys / pins, and a future account reusing the same username inherited them
// (including live API keys). Keying on the immutable uuid closes both holes.
//
// Steps (idempotent — guarded by the current column type):
//  1. Seed ecg_hub_users rows for local users (their logins now upsert too,
//     but existing sessions need the row immediately).
//  2. Map user_id values from external_id → uuid.
//  3. Delete rows whose owner is unknown (orphans — unreachable anyway, and
//     exactly what a reused username must never inherit).
//  4. Convert the column to uuid and add the CASCADE foreign key.
func migrateUserIdentity(db *gorm.DB) {
	// 1. Local users → ecg_hub_users (provider 'local').
	if err := db.Exec(`
		INSERT INTO ecg_hub_users (external_id, provider, role_id, last_login)
		SELECT lu.username, 'local', r.id, now()
		FROM local_users lu
		JOIN roles r ON r.name = lu.role
		WHERE NOT EXISTS (SELECT 1 FROM ecg_hub_users e WHERE e.external_id = lu.username)
	`).Error; err != nil {
		slog.Warn("db: identity: seed local users", "error", err)
	}

	const uuidPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
	tables := []struct{ table, fk string }{
		{"user_webhooks", "fk_user_webhooks_user"},
		{"api_keys", "fk_api_keys_user"},
		{"user_pins", "fk_user_pins_user"},
		{"export_jobs", "fk_export_jobs_user"},
	}
	for _, t := range tables {
		if !db.Migrator().HasTable(t.table) {
			continue
		}

		// Idempotence guard: skip mapping/conversion when user_id is already uuid.
		var colType string
		db.Raw(`SELECT data_type FROM information_schema.columns
			WHERE table_name = ? AND column_name = 'user_id'`, t.table).Scan(&colType)
		if colType != "uuid" {
			// 2. Map username → internal uuid.
			if err := db.Exec(`UPDATE `+t.table+` t SET user_id = e.id::text
				FROM ecg_hub_users e WHERE t.user_id = e.external_id`).Error; err != nil {
				slog.Warn("db: identity: map user_id", "table", t.table, "error", err)
				continue
			}
			// 3. Remove orphans (owner unknown — must not survive a username reuse).
			res := db.Exec(`DELETE FROM `+t.table+` WHERE user_id !~ ?`, uuidPattern)
			if res.Error != nil {
				slog.Warn("db: identity: delete orphans", "table", t.table, "error", res.Error)
				continue
			}
			if res.RowsAffected > 0 {
				slog.Info("db: identity: removed orphan rows", "table", t.table, "rows", res.RowsAffected)
			}
			// 4a. Convert the column type.
			if err := db.Exec(`ALTER TABLE ` + t.table + ` ALTER COLUMN user_id TYPE uuid USING user_id::uuid`).Error; err != nil {
				slog.Warn("db: identity: convert user_id to uuid", "table", t.table, "error", err)
				continue
			}
			slog.Info("db: identity: user_id migrated to uuid", "table", t.table)
		}

		// 4b. CASCADE foreign key (also covers fresh installs where the column
		// is created as uuid directly by AutoMigrate).
		if !db.Migrator().HasConstraint(t.table, t.fk) {
			if err := db.Exec(`ALTER TABLE ` + t.table + ` ADD CONSTRAINT ` + t.fk +
				` FOREIGN KEY (user_id) REFERENCES ecg_hub_users(id) ON DELETE CASCADE`).Error; err != nil {
				slog.Warn("db: identity: add foreign key", "constraint", t.fk, "error", err)
			} else {
				slog.Info("db: identity: added foreign key", "constraint", t.fk)
			}
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
			perms: []string{
				"patient.read",
				"ecg.read", "ecg.write", "ecg.download", "ecg.delete", "ecg.force_hl7",
				"hl7.config", "hl7.bulk_retry",
				"tag.create", "tag.delete", "tag.apply",
				"quarantine.read", "quarantine.delete", "quarantine.assign",
				"admin.audit", "admin.system", "admin.users", "admin.roles", "admin.branding", "admin.auth_config",
				"swagger.read",
				"webhook.manage", "apikey.manage",
			},
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
