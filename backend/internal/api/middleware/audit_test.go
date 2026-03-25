package middleware

import (
	"context"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

func TestWriteAuditLog_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skipf("set TEST_DATABASE_URL env var to run integration tests")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}

	// AutoMigrate for the test (not used in prod — prod uses goose migrations)
	if err := db.AutoMigrate(&models.AuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	details := map[string]any{"query": "doe", "results": 3}
	if err := WriteAuditLog(context.Background(), db, "testuser", "patient_search", "test-resource-1", details); err != nil {
		t.Fatalf("WriteAuditLog: %v", err)
	}

	var log models.AuditLog
	if err := db.Where("user_id = ? AND action = ?", "testuser", "patient_search").
		Order("created_at DESC").First(&log).Error; err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if log.UserID != "testuser" {
		t.Errorf("UserID: want testuser, got %q", log.UserID)
	}
	if log.Action != "patient_search" {
		t.Errorf("Action: want patient_search, got %q", log.Action)
	}
	if log.ResourceID != "test-resource-1" {
		t.Errorf("ResourceID: want test-resource-1, got %q", log.ResourceID)
	}
}
