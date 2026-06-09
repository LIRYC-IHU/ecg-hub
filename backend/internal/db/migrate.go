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
		&appmodels.LocalUser{},
		&appmodels.AuthProviderConfig{},
		&appmodels.ModuleConfig{},
		&appmodels.ModuleSettings{},
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
	if db.Migrator().HasTable("export_jobs") {
		if err := db.Exec(`ALTER TABLE export_jobs ALTER COLUMN user_id TYPE text`).Error; err != nil {
			slog.Warn("db: migrate user_id column type", "table", "export_jobs", "error", err)
		}
	}

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

	// init default role
	if err := iniRole(db); err != nil {
		return err
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
			perms: []string{
				"patient.read",
				"ecg.read", "ecg.write", "ecg.download", "ecg.delete", "ecg.force_hl7",
				"hl7.config", "hl7.bulk_retry",
				"tag.create", "tag.delete", "tag.apply",
				"quarantine.read", "quarantine.delete", "quarantine.assign",
				"admin.audit", "admin.system", "admin.users", "admin.roles", "admin.branding", "admin.auth_config",
				"swagger.read",
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
