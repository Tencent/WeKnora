package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
)

const (
	// MySQLSessionTimeZone keeps MySQL DATETIME values aligned with the UTC
	// time.Time values written by GORM's NowFunc.
	MySQLSessionTimeZone = "+00:00"

	// MySQLSessionSQLMode makes data validation deterministic even when the
	// server-wide sql_mode has been relaxed by an operator.
	MySQLSessionSQLMode = "ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION"
)

var mysqlVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// BuildMySQLApplicationDSN builds the go-sql-driver DSN used by GORM.
// System-variable values must remain SQL literals because go-sql-driver emits
// them as SET statements whenever a new pooled connection is established.
func BuildMySQLApplicationDSN(user, password, addr, database string) string {
	cfg := gomysql.NewConfig()
	cfg.User = user
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = addr
	cfg.DBName = database
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Params = mysqlSessionParams()
	return cfg.FormatDSN()
}

// BuildMySQLMigrationDSN builds the URL accepted by golang-migrate's MySQL
// driver. It intentionally carries the same session contract as the GORM DSN.
func BuildMySQLMigrationDSN(user, password, addr, database string) string {
	query := url.Values{}
	query.Set("charset", "utf8mb4")
	query.Set("loc", "UTC")
	query.Set("multiStatements", "true")
	query.Set("parseTime", "true")
	for key, value := range mysqlSessionParams() {
		if key != "charset" {
			query.Set(key, value)
		}
	}
	return fmt.Sprintf(
		"mysql://%s@tcp(%s)/%s?%s",
		url.UserPassword(user, password).String(),
		addr,
		url.PathEscape(database),
		query.Encode(),
	)
}

func mysqlSessionParams() map[string]string {
	return map[string]string{
		"charset":   "utf8mb4",
		"time_zone": "'" + MySQLSessionTimeZone + "'",
		"sql_mode":  "'" + MySQLSessionSQLMode + "'",
	}
}

// ValidateMySQLSession verifies the production contract before migrations can
// make any schema changes. Known products with materially different SQL,
// locking, or FULLTEXT semantics are rejected. Percona Server follows the
// corresponding MySQL release and is accepted by the same version gate.
func ValidateMySQLSession(ctx context.Context, db *sql.DB) error {
	var version, versionComment, timeZone, sqlMode string
	if err := db.QueryRowContext(
		ctx,
		"SELECT VERSION(), @@version_comment, @@SESSION.time_zone, @@SESSION.sql_mode",
	).Scan(&version, &versionComment, &timeZone, &sqlMode); err != nil {
		return fmt.Errorf("query MySQL server capabilities: %w", err)
	}
	if err := validateMySQLVersion(version, versionComment); err != nil {
		return err
	}
	if timeZone != MySQLSessionTimeZone {
		return fmt.Errorf(
			"MySQL session time_zone is %q, want %q",
			timeZone,
			MySQLSessionTimeZone,
		)
	}
	if !hasStrictMySQLMode(sqlMode) {
		return fmt.Errorf(
			"MySQL strict mode is required, session sql_mode is %q",
			sqlMode,
		)
	}
	return nil
}

func validateMySQLVersion(version, versionComment string) error {
	product := strings.ToLower(version + " " + versionComment)
	for _, unsupported := range []string{"mariadb", "tidb", "oceanbase"} {
		if strings.Contains(product, unsupported) {
			return fmt.Errorf(
				"unsupported MySQL-compatible server %q; MySQL or Percona Server 8.0.16 or newer is required",
				strings.TrimSpace(version+" "+versionComment),
			)
		}
	}

	matches := mysqlVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if matches == nil {
		return fmt.Errorf(
			"cannot parse MySQL server version %q; MySQL or Percona Server 8.0.16 or newer is required",
			version,
		)
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch, _ := strconv.Atoi(matches[3])
	// MySQL 8.0.16 is the first release that enforces CHECK constraints.
	// Later MySQL migrations rely on them for authorization-related schema
	// invariants, so accepting 8.0.13-8.0.15 would silently weaken the schema.
	if major < 8 || (major == 8 && minor == 0 && patch < 16) {
		return fmt.Errorf(
			"unsupported MySQL server version %q; MySQL or Percona Server 8.0.16 or newer is required",
			version,
		)
	}
	return nil
}

func hasStrictMySQLMode(sqlMode string) bool {
	for _, mode := range strings.Split(sqlMode, ",") {
		switch strings.ToUpper(strings.TrimSpace(mode)) {
		case "STRICT_TRANS_TABLES", "STRICT_ALL_TABLES", "TRADITIONAL":
			return true
		}
	}
	return false
}
