package db

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// RunMigrations applies all schema changes using GORM AutoMigrate.
// It is idempotent: safe to call on every startup.
// AutoMigrate only adds — it never drops columns or tables.
func RunMigrations(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&models.Patient{},
		&models.ECG{},
		&models.AuditLog{},
		&models.QuarantineEntry{},
		&models.ExportJob{},
		&repository.RoleRecord{},
		&repository.RolePermRecord{},
		&repository.UserRecord{},
	); err != nil {
		return fmt.Errorf("db: auto migrate: %w", err)
	}

	if err := applyConstraints(db); err != nil {
		return err
	}

	if err := seedRoles(db); err != nil {
		return fmt.Errorf("db: seed roles: %w", err)
	}

	return nil
}

// applyConstraints adds FKs, CHECK constraints, join tables, and privilege
// restrictions that GORM AutoMigrate cannot express via struct tags.
// All statements are guarded by IF NOT EXISTS or are idempotent by nature.
func applyConstraints(db *gorm.DB) error {
	stmts := []struct {
		name string
		sql  string
	}{
		{
			"export_job_ecgs join table",
			`CREATE TABLE IF NOT EXISTS export_job_ecgs (
				export_job_id VARCHAR(36) NOT NULL REFERENCES export_jobs(id) ON DELETE CASCADE,
				ecg_id        BIGINT      NOT NULL,
				PRIMARY KEY (export_job_id, ecg_id)
			)`,
		},
		{
			"fk ecgs.patient_id → patients.patient_id",
			`DO $$ BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM information_schema.table_constraints
					WHERE constraint_name = 'fk_ecgs_patient_id' AND table_name = 'ecgs'
				) THEN
					ALTER TABLE ecgs ADD CONSTRAINT fk_ecgs_patient_id
						FOREIGN KEY (patient_id) REFERENCES patients(patient_id);
				END IF;
			END $$`,
		},
		{
			"check ecgs.hl7_status",
			`DO $$ BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM information_schema.table_constraints
					WHERE constraint_name = 'chk_ecgs_hl7_status' AND table_name = 'ecgs'
				) THEN
					ALTER TABLE ecgs ADD CONSTRAINT chk_ecgs_hl7_status
						CHECK (hl7_status IN ('pending', 'success', 'hl7_exhausted'));
				END IF;
			END $$`,
		},
		{
			"fk role_permissions.role_id → roles.id CASCADE",
			`DO $$ BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM information_schema.table_constraints
					WHERE constraint_name = 'fk_role_permissions_role_id' AND table_name = 'role_permissions'
				) THEN
					ALTER TABLE role_permissions ADD CONSTRAINT fk_role_permissions_role_id
						FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE;
				END IF;
			END $$`,
		},
		{
			"fk ecg_hub_users.role_id → roles.id SET NULL",
			`DO $$ BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM information_schema.table_constraints
					WHERE constraint_name = 'fk_ecg_hub_users_role_id' AND table_name = 'ecg_hub_users'
				) THEN
					ALTER TABLE ecg_hub_users ADD CONSTRAINT fk_ecg_hub_users_role_id
						FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE SET NULL;
				END IF;
			END $$`,
		},
		{
			// Enforce audit log immutability at the DB privilege level (NFR-S6, RGPD).
			// REVOKE is idempotent: revoking a privilege not held produces no error.
			"revoke audit_logs update/delete",
			`REVOKE UPDATE, DELETE ON audit_logs FROM CURRENT_USER`,
		},
	}

	for _, s := range stmts {
		if err := db.Exec(s.sql).Error; err != nil {
			return fmt.Errorf("db: constraint %q: %w", s.name, err)
		}
	}
	return nil
}

// seedRoles inserts the built-in roles (admin, reader, writer) if they do not
// already exist. Uses ON CONFLICT DO NOTHING so it is always idempotent.
func seedRoles(db *gorm.DB) error {
	type roleSeed struct {
		name        string
		description string
		permissions []string
	}

	seeds := []roleSeed{
		{
			name:        "admin",
			description: "Accès complet — bypass toutes les permissions",
		},
		{
			name:        "reader",
			description: "Lecture seule",
			permissions: []string{"patient.read", "ecg.read", "ecg.download"},
		},
		{
			name:        "writer",
			description: "Lecture + écriture",
			permissions: []string{
				"patient.read", "ecg.read", "ecg.download",
				"ecg.delete", "ecg.force_hl7", "ecg.write",
				"quarantine.read", "quarantine.delete",
			},
		},
	}

	for _, s := range seeds {
		// Insert role if absent.
		if err := db.Exec(
			`INSERT INTO roles (name, description) VALUES (?, ?) ON CONFLICT (name) DO NOTHING`,
			s.name, s.description,
		).Error; err != nil {
			return fmt.Errorf("db: seed role %q: %w", s.name, err)
		}

		// Resolve the role ID we just ensured exists.
		var roleID uint
		if err := db.Raw(`SELECT id FROM roles WHERE name = ?`, s.name).Scan(&roleID).Error; err != nil {
			return fmt.Errorf("db: resolve role id %q: %w", s.name, err)
		}

		for _, perm := range s.permissions {
			if err := db.Exec(
				`INSERT INTO role_permissions (role_id, permission) VALUES (?, ?) ON CONFLICT DO NOTHING`,
				roleID, perm,
			).Error; err != nil {
				return fmt.Errorf("db: seed permission %q for role %q: %w", perm, s.name, err)
			}
		}
	}
	return nil
}
