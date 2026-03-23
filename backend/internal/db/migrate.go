package db

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// RunMigrations applies all pending goose migrations from the embedded SQL files.
// It is idempotent: if all migrations are already applied, it returns nil.
// The SQL files are embedded into the binary — no filesystem access at runtime.
func RunMigrations(sqlDB *sql.DB) error {
	goose.SetBaseFS(migrationFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("db: set goose dialect: %w", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		return fmt.Errorf("db: run migrations: %w", err)
	}
	return nil
}
