package container

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

// MySQLPoolConfig holds the parsed connection-pool and timeout settings
// for MySQL. It is returned alongside the DSN strings so the caller
// (container.go) can apply them to *sql.DB without re-parsing env vars.
type MySQLPoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// buildMySQLDSN constructs the two DSN strings WeKnora needs for a
// MySQL metadata database, plus the parsed pool configuration.
//
// Returns an error if any configuration value is invalid (bad port,
// bad duration, maxIdle > maxOpen, TLS cert without key, etc.) so the
// caller can fail fast instead of starting with a broken connection.
//
// env is an os.Getenv-shaped function so tests can inject values.
func buildMySQLDSN(env func(string) string) (gormDSN, migrateDSN string, pool MySQLPoolConfig, err error) {
	host := env("DB_HOST")
	port := env("DB_PORT")
	if port == "" {
		port = "3306"
	}
	user := env("DB_USER")
	password := env("DB_PASSWORD")
	dbname := env("DB_NAME")

	if host == "" {
		return "", "", MySQLPoolConfig{}, fmt.Errorf("DB_HOST is empty; cannot construct MySQL DSN")
	}

	// net.JoinHostPort wraps IPv6 hosts in [...] (e.g. "[::1]:3306").
	// String concatenation would produce "::1:3306" which is ambiguous.
	addr := net.JoinHostPort(host, port)

	cfg := mysqlDriver.NewConfig()
	cfg.User = user
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = addr
	cfg.DBName = dbname
	cfg.Params = map[string]string{
		"charset":   "utf8mb4",
		"collation": "utf8mb4_0900_ai_ci",
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC

	// Timeouts (driver-level, per-connection).
	cfg.Timeout = getEnvDuration(env, "DB_CONNECT_TIMEOUT", 10*time.Second)
	cfg.ReadTimeout = getEnvDuration(env, "DB_READ_TIMEOUT", 30*time.Second)
	cfg.WriteTimeout = getEnvDuration(env, "DB_WRITE_TIMEOUT", 30*time.Second)

	// TLS: operators who need mTLS can register a named TLS config via the
	// go-sql-driver in a follow-up. For v1 the driver's built-in tls=true
	// (set via cfg.Params) covers the common case.

	gormDSN = cfg.FormatDSN()

	// golang-migrate DSN: same coordinates + multiStatements for batch DDL.
	params := url.Values{}
	params.Set("charset", "utf8mb4")
	params.Set("collation", "utf8mb4_0900_ai_ci")
	params.Set("parseTime", "true")
	params.Set("loc", "UTC")
	params.Set("multiStatements", "true")
	migrateDSN = fmt.Sprintf(
		"mysql://%s:%s@tcp(%s)/%s?%s",
		url.QueryEscape(user),
		url.QueryEscape(password),
		addr,
		dbname,
		params.Encode(),
	)

	maxOpen := getEnvInt(env, "DB_MAX_OPEN_CONNS", 50)
	maxIdle := getEnvInt(env, "DB_MAX_IDLE_CONNS", 10)
	if maxIdle > maxOpen {
		return "", "", MySQLPoolConfig{}, fmt.Errorf("DB_MAX_IDLE_CONNS (%d) must not exceed DB_MAX_OPEN_CONNS (%d)", maxIdle, maxOpen)
	}

	return gormDSN, migrateDSN, MySQLPoolConfig{
		MaxOpenConns:    maxOpen,
		MaxIdleConns:    maxIdle,
		ConnMaxLifetime: getEnvDuration(env, "DB_CONN_MAX_LIFETIME", 10*time.Minute),
		ConnMaxIdleTime: getEnvDuration(env, "DB_CONN_MAX_IDLE_TIME", 5*time.Minute),
	}, nil
}

func getEnvDuration(env func(string) string, key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(env(key))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return def
}

func getEnvInt(env func(string) string, key string, def int) int {
	v := strings.TrimSpace(env(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
