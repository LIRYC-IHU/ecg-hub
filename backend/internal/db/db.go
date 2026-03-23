package db

import (
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

const (
	defaultMaxOpenConns    = 10
	defaultMaxIdleConns    = 5
	defaultConnMaxLifetime = 30 * time.Minute
)

// Open creates and validates a GORM database connection using the provided config.
// It configures the connection pool and verifies the database is reachable via Ping.
// The GORM built-in logger is silenced — use slog for application-level logging.
func Open(cfg *config.Config) (*gorm.DB, error) {
	gormDB, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), // use slog; GORM logger disabled
	})
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("db: get sql.DB: %w", err)
	}

	maxOpen := cfg.Database.MaxOpenConns
	if maxOpen == 0 {
		maxOpen = defaultMaxOpenConns
	}
	maxIdle := cfg.Database.MaxIdleConns
	if maxIdle == 0 {
		maxIdle = defaultMaxIdleConns
	}

	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(defaultConnMaxLifetime)

	// Verify the database is actually reachable.
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	return gormDB, nil
}
