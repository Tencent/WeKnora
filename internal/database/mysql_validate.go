package database

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var mysqlServerVersion = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

func ValidateMySQLVersionString(version string) error {
	parts := mysqlServerVersion.FindStringSubmatch(version)
	if len(parts) != 4 || strings.Contains(strings.ToLower(version), "mariadb") {
		return fmt.Errorf("unsupported MySQL server version %q; use MySQL 8.0.17+ or 8.4", version)
	}
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	patch, _ := strconv.Atoi(parts[3])
	if major == 8 && (minor == 4 || (minor == 0 && patch >= 17)) {
		return nil
	}
	return fmt.Errorf("unsupported MySQL server version %q; use MySQL 8.0.17+ or 8.4", version)
}

func ValidateMySQLServer(ctx context.Context, db *sql.DB) error {
	var version, zone, mode string
	if err := db.QueryRowContext(ctx, "SELECT VERSION(), @@session.time_zone, @@session.sql_mode").Scan(&version, &zone, &mode); err != nil {
		return fmt.Errorf("inspect MySQL server: %w", err)
	}
	if err := ValidateMySQLVersionString(version); err != nil {
		return err
	}
	if zone != "+00:00" && !strings.EqualFold(zone, "UTC") {
		return fmt.Errorf("MySQL session time_zone must be UTC, got %q", zone)
	}
	for _, flag := range strings.Split(strings.ToUpper(mode), ",") {
		if flag == "STRICT_TRANS_TABLES" || flag == "STRICT_ALL_TABLES" {
			return nil
		}
	}
	return fmt.Errorf("MySQL sql_mode must include STRICT_TRANS_TABLES or STRICT_ALL_TABLES")
}
