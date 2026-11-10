package db

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// openSchema opens the test database on a schema of its own, dropped at the
// end, so the migration runs on a known starting point.
func openSchema(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	schema := fmt.Sprintf("tenancy_%d", time.Now().UnixNano())
	admin, err := Open(&config.Config{DatabaseURL: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := admin.Exec(`CREATE SCHEMA ` + schema).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := Open(&config.Config{DatabaseURL: dsn + sep + "search_path=" + schema + ",public"})
	if err != nil {
		t.Fatalf("Open schema: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
		admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if sqlDB, err := admin.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return db
}

func mustExec(t *testing.T, db *gorm.DB, sql string, args ...any) {
	t.Helper()
	if err := db.Exec(sql, args...).Error; err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func insertECG(db *gorm.DB, tenant, patientID, hash string) error {
	return db.Exec(`INSERT INTO ecgs (tenant_id, patient_id, vendor, file_path, original_filename, content_hash, ingested_at)
		VALUES (?, ?, 'test', '/x', 'x.xml', ?, now())`, tenant, patientID, hash).Error
}

func count(t *testing.T, db *gorm.DB, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Raw(sql, args...).Scan(&n).Error; err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// Keys are per tenant, an ECG cannot hang off another tenant's patient, and
// removing a tenant removes its rows only.
func TestTenancySchema_KeysAndRemoval(t *testing.T) {
	db := openSchema(t)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	def := models.DefaultTenantID
	var other string
	if err := db.Raw(`INSERT INTO tenants (slug, name) VALUES ('cabinet-b', 'Cabinet B') RETURNING id`).Scan(&other).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	mustExec(t, db, `INSERT INTO patients (tenant_id, patient_id) VALUES (?, 'P1')`, def)
	mustExec(t, db, `INSERT INTO patients (tenant_id, patient_id) VALUES (?, 'P1')`, other)
	if err := db.Exec(`INSERT INTO patients (tenant_id, patient_id) VALUES (?, 'P1')`, def).Error; err == nil {
		t.Fatal("same patient_id twice in one tenant was accepted")
	}

	if err := insertECG(db, def, "P1", "h1"); err != nil {
		t.Fatalf("ECG in default tenant: %v", err)
	}
	if err := insertECG(db, other, "P1", "h1"); err != nil {
		t.Fatalf("same file in another tenant was refused: %v", err)
	}
	if err := insertECG(db, def, "P1", "h1"); err == nil {
		t.Fatal("same file twice in one tenant was accepted")
	}
	mustExec(t, db, `INSERT INTO patients (tenant_id, patient_id) VALUES (?, 'ONLY-DEFAULT')`, def)
	if err := insertECG(db, other, "ONLY-DEFAULT", "h2"); err == nil {
		t.Fatal("ECG attached to a patient of another tenant was accepted")
	}

	// Rows written without a tenant land in the default one.
	mustExec(t, db, `INSERT INTO patients (patient_id) VALUES ('NO-TENANT')`)
	if n := count(t, db, `SELECT count(*) FROM patients WHERE patient_id = 'NO-TENANT' AND tenant_id = ?`, def); n != 1 {
		t.Fatalf("row without tenant: got %d in default tenant, want 1", n)
	}

	mustExec(t, db, `DELETE FROM tenants WHERE id = ?`, other)
	if n := count(t, db, `SELECT count(*) FROM patients WHERE tenant_id = ?`, other); n != 0 {
		t.Fatalf("patients left after tenant removal: %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM ecgs WHERE tenant_id = ?`, other); n != 0 {
		t.Fatalf("ECGs left after tenant removal: %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM ecgs WHERE tenant_id = ?`, def); n != 1 {
		t.Fatalf("default tenant ECGs after removal of another tenant: got %d, want 1", n)
	}

	// A patient with ECGs still cannot be deleted.
	if err := db.Exec(`DELETE FROM patients WHERE tenant_id = ? AND patient_id = 'P1'`, def).Error; err == nil {
		t.Fatal("patient with ECGs was deleted")
	}
}

// A database created before tenancy keeps its rows, which move to the default
// tenant, and loses its deployment-wide keys.
func TestTenancySchema_UpgradesLegacyDatabase(t *testing.T) {
	db := openSchema(t)
	mustExec(t, db, `CREATE TABLE patients (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
		created_at timestamptz, updated_at timestamptz,
		patient_id text NOT NULL,
		CONSTRAINT uni_patients_patient_id UNIQUE (patient_id))`)
	mustExec(t, db, `CREATE TABLE ecgs (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
		patient_id text NOT NULL, vendor text NOT NULL,
		file_path text NOT NULL, original_filename text NOT NULL,
		content_hash varchar(64),
		ingested_at timestamptz NOT NULL DEFAULT now(),
		CONSTRAINT fk_patients_ecgs FOREIGN KEY (patient_id) REFERENCES patients(patient_id) ON UPDATE CASCADE ON DELETE RESTRICT)`)
	mustExec(t, db, `CREATE UNIQUE INDEX idx_ecgs_content_hash ON ecgs (content_hash)`)
	mustExec(t, db, `INSERT INTO patients (patient_id) VALUES ('LEGACY')`)
	mustExec(t, db, `INSERT INTO ecgs (patient_id, vendor, file_path, original_filename, content_hash)
		VALUES ('LEGACY', 'test', '/x', 'x.xml', 'h1')`)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	// Twice: the second start must find nothing left to do.
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations, second start: %v", err)
	}

	def := models.DefaultTenantID
	if n := count(t, db, `SELECT count(*) FROM ecgs WHERE patient_id = 'LEGACY' AND tenant_id = ?`, def); n != 1 {
		t.Fatalf("legacy ECG in default tenant: got %d, want 1", n)
	}
	for _, name := range []string{"uni_patients_patient_id", "fk_patients_ecgs"} {
		if n := count(t, db, `SELECT count(*) FROM pg_constraint WHERE conname = ? AND connamespace = current_schema()::regnamespace`, name); n != 0 {
			t.Errorf("legacy constraint %s still present", name)
		}
	}
	if n := count(t, db, `SELECT count(*) FROM pg_constraint WHERE conname = 'fk_ecgs_patient' AND connamespace = current_schema()::regnamespace`); n != 1 {
		t.Error("composite foreign key fk_ecgs_patient missing")
	}
	var other string
	if err := db.Raw(`INSERT INTO tenants (slug, name) VALUES ('cabinet-b', 'Cabinet B') RETURNING id`).Scan(&other).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	mustExec(t, db, `INSERT INTO patients (tenant_id, patient_id) VALUES (?, 'LEGACY')`, other)
	if err := insertECG(db, other, "LEGACY", "h1"); err != nil {
		t.Fatalf("legacy hash still unique across tenants: %v", err)
	}
}

// A tenant is dated however it is inserted, and a tenant left undated by an
// earlier version gets a date on the next start.
func TestTenancySchema_TenantsAreDated(t *testing.T) {
	db := openSchema(t)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	mustExec(t, db, `INSERT INTO tenants (slug, name) VALUES ('by-sql', 'Inserted by SQL')`)
	if n := count(t, db, `SELECT count(*) FROM tenants WHERE created_at IS NULL OR created_at < '1900-01-01'`); n != 0 {
		t.Fatalf("%d tenant(s) without a creation time after a plain insert", n)
	}
	mustExec(t, db, `UPDATE tenants SET created_at = NULL`)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations, second start: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM tenants WHERE created_at IS NULL`); n != 0 {
		t.Fatalf("%d undated tenant(s) left after a restart", n)
	}
}
