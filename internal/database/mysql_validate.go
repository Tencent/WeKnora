package database

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var mysqlVersionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func ValidateMySQLServer(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("mysql sql.DB is nil")
	}

	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return fmt.Errorf("query mysql version: %w", err)
	}
	if err := ValidateMySQLVersionString(version); err != nil {
		return err
	}

	var timeZone string
	if err := db.QueryRowContext(ctx, "SELECT @@session.time_zone").Scan(&timeZone); err != nil {
		return fmt.Errorf("query mysql session time_zone: %w", err)
	}
	if timeZone != "+00:00" && !strings.EqualFold(timeZone, "UTC") {
		return fmt.Errorf("mysql session time_zone must be UTC/+00:00, got %q", timeZone)
	}

	var sqlMode string
	if err := db.QueryRowContext(ctx, "SELECT @@session.sql_mode").Scan(&sqlMode); err != nil {
		return fmt.Errorf("query mysql session sql_mode: %w", err)
	}
	if !strings.Contains(strings.ToUpper(sqlMode), "STRICT") {
		return fmt.Errorf("mysql sql_mode must include STRICT mode")
	}

	return nil
}

func ValidateMySQLVersionString(version string) error {
	lower := strings.ToLower(version)
	if strings.Contains(lower, "mariadb") {
		return fmt.Errorf("MariaDB is not declared supported for DB_DRIVER=mysql")
	}

	major, minor, patch, ok := parseMySQLVersion(version)
	if !ok {
		return fmt.Errorf("could not parse MySQL version %q", version)
	}
	if major > 8 {
		return nil
	}
	if major == 8 {
		if minor > 0 || (minor == 0 && patch >= 16) {
			return nil
		}
	}
	return fmt.Errorf("MySQL 8.0.16 or newer is required, got %q", version)
}

func parseMySQLVersion(version string) (int, int, int, bool) {
	matches := mysqlVersionPattern.FindStringSubmatch(version)
	if len(matches) != 4 {
		return 0, 0, 0, false
	}
	major, majorErr := strconv.Atoi(matches[1])
	minor, minorErr := strconv.Atoi(matches[2])
	patch, patchErr := strconv.Atoi(matches[3])
	if majorErr != nil || minorErr != nil || patchErr != nil {
		return 0, 0, 0, false
	}
	return major, minor, patch, true
}
