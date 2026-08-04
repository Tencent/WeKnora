package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

var requiredTables = []string{
	"tenants",
	"knowledge_bases",
	"knowledges",
	"chunks",
	"chunk_revisions",
	"wiki_pages",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "mysql migration check failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("mysql migration check passed")
}

func run() error {
	migrationDSN := strings.TrimSpace(os.Getenv("WEKNORA_MYSQL_TEST_DSN"))
	if migrationDSN == "" {
		return fmt.Errorf("WEKNORA_MYSQL_TEST_DSN is required")
	}
	if !strings.HasPrefix(migrationDSN, "mysql://") {
		return fmt.Errorf("WEKNORA_MYSQL_TEST_DSN must use mysql:// scheme for golang-migrate")
	}

	driverDSN := strings.TrimPrefix(migrationDSN, "mysql://")
	cfg, err := mysql.ParseDSN(driverDSN)
	if err != nil {
		return fmt.Errorf("parse MySQL DSN: %w", err)
	}
	if !strings.Contains(strings.ToLower(cfg.DBName), "test") {
		return fmt.Errorf("refusing to run against non-test database %q", cfg.DBName)
	}

	db, err := sql.Open("mysql", driverDSN)
	if err != nil {
		return fmt.Errorf("open MySQL: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping MySQL: %w", err)
	}
	if err := validateMySQLServer(ctx, db); err != nil {
		return err
	}

	m, err := migrate.New("file://migrations/mysql", migrationDSN)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run MySQL migrations up: %w", err)
	}
	if err := assertRequiredTables(ctx, db, true); err != nil {
		return err
	}

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run MySQL migrations down: %w", err)
	}
	if err := assertRequiredTables(ctx, db, false); err != nil {
		return err
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("rerun MySQL migrations up: %w", err)
	}
	return assertRequiredTables(ctx, db, true)
}

func assertRequiredTables(ctx context.Context, db *sql.DB, wantExists bool) error {
	for _, table := range requiredTables {
		var exists int
		err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.tables
WHERE table_schema = DATABASE() AND table_name = ?`, table).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check table %s: %w", table, err)
		}
		if wantExists && exists != 1 {
			return fmt.Errorf("required table %s was not created", table)
		}
		if !wantExists && exists != 0 {
			return fmt.Errorf("required table %s still exists after down migration", table)
		}
	}

	return nil
}

func validateMySQLServer(ctx context.Context, db *sql.DB) error {
	var version, timeZone, sqlMode string
	if err := db.QueryRowContext(ctx, "SELECT VERSION(), @@session.time_zone, @@session.sql_mode").Scan(&version, &timeZone, &sqlMode); err != nil {
		return fmt.Errorf("query MySQL server settings: %w", err)
	}
	if strings.Contains(strings.ToLower(version), "mariadb") {
		return fmt.Errorf("MariaDB is not supported for this migration check: %s", version)
	}
	if !strings.HasPrefix(version, "8.0.") {
		return fmt.Errorf("MySQL 8.0.16 or newer is required, got %s", version)
	}
	patch := 0
	if _, err := fmt.Sscanf(version, "8.0.%d", &patch); err != nil {
		return fmt.Errorf("parse MySQL version %q: %w", version, err)
	}
	if patch < 16 {
		return fmt.Errorf("MySQL 8.0.16 or newer is required, got %s", version)
	}
	if timeZone != "+00:00" && strings.ToUpper(timeZone) != "UTC" {
		return fmt.Errorf("MySQL session time_zone must be UTC/+00:00, got %s", timeZone)
	}
	if strings.TrimSpace(sqlMode) == "" {
		return fmt.Errorf("MySQL sql_mode must not be empty")
	}
	return nil
}
