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
	for _, stmt := range []string{
		`ALTER TABLE audit_logs ALTER COLUMN user_id TYPE text`,
		`ALTER TABLE export_jobs ALTER COLUMN user_id TYPE text`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			slog.Warn("db: migrate user_id column type", "stmt", stmt, "error", err)
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

	// Drop the stale FK constraint on audit_logs if it still exists from a previous migration attempt.
	for _, stmt := range []string{
		`ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS fk_ecg_hub_users_audit_log`,
		`ALTER TABLE export_jobs DROP CONSTRAINT IF EXISTS fk_ecg_hub_users_export_job`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			slog.Warn("db: drop stale fk constraint", "stmt", stmt, "error", err)
		}
	}

	for _, m := range models {
		err := db.AutoMigrate(m)
		if err != nil {
			slog.Warn("db: auto migrate %s: %w", m, err)
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
				"quarantine.read", "quarantine.delete",
				"admin.audit", "admin.system", "admin.users", "admin.roles", "admin.auth_config",
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
				"quarantine.read", "quarantine.delete",
			},
		},
	}
	roles := []repository.RoleRecord{}
	result := db.Find(&roles)
	if result.Error != nil {
		return fmt.Errorf("query roles: %w", result.Error)

	}

	// Always sync permissions for seeded roles — ensures new permissions
	// added in code are granted on existing installations without a DB reset.
	for _, s := range seeds {
		var existing repository.RoleRecord
		if err := db.Where("name = ?", s.name).First(&existing).Error; err != nil {
			// Role doesn't exist — create it.
			fmt.Printf("Creating role %s\n", s.name)
			existing = repository.RoleRecord{Name: s.name, Description: s.desc}
			if err := db.Create(&existing).Error; err != nil {
				fmt.Printf("Error creating role %s: %v\n", s.name, err)
				continue
			}
		}
		// Ensure every permission in the seed exists (add missing ones, never remove).
		for _, perm := range s.perms {
			var count int64
			db.Model(&repository.RolePermRecord{}).Where("role_id = ? AND permission = ?", existing.ID, perm).Count(&count)
			if count == 0 {
				p := repository.RolePermRecord{RoleID: existing.ID, Permission: perm, Role: existing}
				if err := db.Create(&p).Error; err != nil {
					slog.Warn("db: add permission to role", "role", s.name, "perm", perm, "error", err)
				}
			}
		}
	}

	if len(roles) == 0 {
		for _, s := range seeds {
			fmt.Printf("Creating role %s\n", s.name)

			record := repository.RoleRecord{Name: s.name, Description: s.desc}
			if err := db.Create(&record).Error; err != nil {
				fmt.Printf("Error creating role %s: %v\n", s.name, err)
				continue
			}

			for _, perm := range s.perms {
				p := repository.RolePermRecord{RoleID: record.ID, Permission: perm, Role: record}
				if err := db.Create(&p).Error; err != nil {
					fmt.Printf("Error creating permission %s for role %s: %v\n", s.perms, s.name, err)
				}
			}
		}
	}
	return nil
}
