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
	// drop all tables and recreate them from scratch, to ensure the schema is exactly as defined in the models.

	// Disable FK checks during drop+recreate (PostgreSQL syntax).
	db.Exec("SET session_replication_role = 'replica'")
	defer db.Exec("SET session_replication_role = 'origin'")
	models := []any{
		&models.Patient{},
		&models.ECG{},
		&models.AuditLog{},
		&models.QuarantineEntry{},
		&repository.RoleRecord{},
		&repository.RolePermRecord{},
		&repository.UserRecord{},
		&models.ExportJob{},
		&models.NihonKohdenTransfer{},
		&models.ConnectorJob{},
		&models.ECGBuffer{},
	}
	for _, m := range models {
		err := db.Migrator().DropTable(m)
		if err != nil {
			return fmt.Errorf("db: drop table %s: %w", m, err)
		}
		fmt.Printf("Dropped table for %T\n", m)
	}
	for _, m := range models {
		err := db.AutoMigrate(m)
		if err != nil {
			return fmt.Errorf("db: auto migrate %s: %w", m, err)
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
			desc: "Accès complet — bypass toutes les permissions",
			perms: []string{
				"patient.read", "ecg.read", "ecg.download",
				"ecg.delete", "ecg.force_hl7", "ecg.write",
				"quarantine.read", "quarantine.delete",
				"admin.audit", "admin.system", "admin.users",
				"ecg.delete", "ecg.download", "ecg.write", "ecg.read",
				"ecg.force_hl7", "patient.read", "quarantine.delete", "quarantine.read"},
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
				"quarantine.read", "quarantine.delete",
			},
		},
	}
	roles := []repository.RoleRecord{}
	result := db.Find(&roles)
	if result.Error != nil {
		return fmt.Errorf("query roles: %w", result.Error)

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
