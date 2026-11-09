package db

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

const (
	defaultMaxOpenConns    = 10
	defaultMaxIdleConns    = 5
	defaultConnMaxLifetime = 30 * time.Minute
	// defaultConnMaxIdleTime closes connections idle beyond this, so the pool
	// never holds sockets a hospital firewall may have silently dropped.
	defaultConnMaxIdleTime = 5 * time.Minute
)

// Open creates and validates a GORM database connection using the provided config.
// It configures the connection pool and verifies the database is reachable via Ping.
// The GORM built-in logger is silenced — use slog for application-level logging.
func Open(cfg *config.Config) (*gorm.DB, error) {
	connCfg, err := pgx.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: parse url: %w", err)
	}
	sqlDB := stdlib.OpenDB(*connCfg,
		stdlib.OptionAfterConnect(applyTenant),
		stdlib.OptionResetSession(applyTenant))

	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), // use slog; GORM logger disabled
	})
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: open: %w", err)
	}
	if err := registerTenantCallbacks(gormDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: tenant callbacks: %w", err)
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
	sqlDB.SetConnMaxIdleTime(defaultConnMaxIdleTime)

	// Verify the database is actually reachable.
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	return gormDB, nil
}
