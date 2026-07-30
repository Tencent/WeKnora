package database

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMySQLDBMaxOpenConns    = 20
	defaultDBMaxIdleConns         = 10
	defaultDBConnMaxLifetime      = 10 * time.Minute
	defaultMySQLDBConnMaxIdleTime = 5 * time.Minute
)

// ConnectionPoolConfig bounds the main database pool for each application
// replica. Operators should size MaxOpenConns so the sum across replicas stays
// below the database server's connection budget.
type ConnectionPoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// MainDBConnectionPoolConfigFromEnv reads and validates the main database pool
// configuration. MySQL gets bounded production defaults. PostgreSQL keeps its
// pre-MySQL behavior unless the operator explicitly configures these values:
// unlimited open connections, ten idle connections, a ten-minute lifetime,
// and no idle timeout.
func MainDBConnectionPoolConfigFromEnv(driver string) (ConnectionPoolConfig, error) {
	maxOpenDefault := 0
	maxIdleTimeDefault := time.Duration(0)
	if strings.EqualFold(strings.TrimSpace(driver), "mysql") {
		maxOpenDefault = defaultMySQLDBMaxOpenConns
		maxIdleTimeDefault = defaultMySQLDBConnMaxIdleTime
	}

	maxOpen, err := nonNegativeIntEnv("DB_MAX_OPEN_CONNS", maxOpenDefault)
	if err != nil {
		return ConnectionPoolConfig{}, err
	}
	maxIdle, err := nonNegativeIntEnv("DB_MAX_IDLE_CONNS", defaultDBMaxIdleConns)
	if err != nil {
		return ConnectionPoolConfig{}, err
	}
	if maxOpen > 0 && maxIdle > maxOpen {
		return ConnectionPoolConfig{}, fmt.Errorf(
			"DB_MAX_IDLE_CONNS (%d) must not exceed DB_MAX_OPEN_CONNS (%d)",
			maxIdle,
			maxOpen,
		)
	}
	maxLifetime, err := positiveDurationEnv("DB_CONN_MAX_LIFETIME", defaultDBConnMaxLifetime)
	if err != nil {
		return ConnectionPoolConfig{}, err
	}
	maxIdleTime, err := nonNegativeDurationEnv("DB_CONN_MAX_IDLE_TIME", maxIdleTimeDefault)
	if err != nil {
		return ConnectionPoolConfig{}, err
	}
	return ConnectionPoolConfig{
		MaxOpenConns:    maxOpen,
		MaxIdleConns:    maxIdle,
		ConnMaxLifetime: maxLifetime,
		ConnMaxIdleTime: maxIdleTime,
	}, nil
}

// ApplyConnectionPoolConfig applies a previously validated pool configuration.
func ApplyConnectionPoolConfig(db *sql.DB, cfg ConnectionPoolConfig) {
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
}

// CloseOnStartupError closes a database that cannot safely be returned to the
// dependency container. The original error is always retained.
func CloseOnStartupError(db *sql.DB, startupErr error) error {
	if startupErr == nil || db == nil {
		return startupErr
	}
	if closeErr := db.Close(); closeErr != nil {
		return errors.Join(startupErr, fmt.Errorf("close database after startup failure: %w", closeErr))
	}
	return startupErr
}

func nonNegativeIntEnv(name string, defaultValue int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer, got %q", name, raw)
	}
	return value, nil
}

func positiveDurationEnv(name string, defaultValue time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration, got %q", name, raw)
	}
	return value, nil
}

func nonNegativeDurationEnv(name string, defaultValue time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative Go duration, got %q", name, raw)
	}
	return value, nil
}
