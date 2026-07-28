package database

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	mysqlDriver "github.com/go-sql-driver/mysql"
)

type MySQLConfig struct {
	GormDSN         string
	MigrationURL    string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func BuildMySQLConfig(env func(string) string) (MySQLConfig, error) {
	host := strings.TrimSpace(env("DB_HOST"))
	user := strings.TrimSpace(env("DB_USER"))
	databaseName := strings.TrimSpace(env("DB_NAME"))
	port := strings.TrimSpace(env("DB_PORT"))
	if port == "" {
		port = "3306"
	}
	if host == "" || user == "" || databaseName == "" {
		return MySQLConfig{}, fmt.Errorf("DB_HOST, DB_USER, and DB_NAME are required for MySQL")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return MySQLConfig{}, fmt.Errorf("invalid DB_PORT %q", port)
	}

	address := net.JoinHostPort(host, port)
	driverConfig := mysqlDriver.NewConfig()
	driverConfig.User = user
	driverConfig.Passwd = env("DB_PASSWORD")
	driverConfig.Net = "tcp"
	driverConfig.Addr = address
	driverConfig.DBName = databaseName
	driverConfig.ParseTime = true
	driverConfig.Loc = time.UTC
	driverConfig.Collation = "utf8mb4_0900_ai_ci"
	driverConfig.Params = map[string]string{"charset": "utf8mb4"}
	driverConfig.Timeout = envDuration(env, "DB_CONNECT_TIMEOUT", 10*time.Second)
	driverConfig.ReadTimeout = envDuration(env, "DB_READ_TIMEOUT", 30*time.Second)
	driverConfig.WriteTimeout = envDuration(env, "DB_WRITE_TIMEOUT", 30*time.Second)

	maxOpen := envInt(env, "DB_MAX_OPEN_CONNS", 50)
	maxIdle := envInt(env, "DB_MAX_IDLE_CONNS", 10)
	if maxOpen < 0 || maxIdle < 0 || (maxOpen > 0 && maxIdle > maxOpen) {
		return MySQLConfig{}, fmt.Errorf("invalid MySQL connection pool limits: open=%d idle=%d", maxOpen, maxIdle)
	}

	query := url.Values{}
	query.Set("charset", "utf8mb4")
	query.Set("collation", "utf8mb4_0900_ai_ci")
	query.Set("parseTime", "true")
	query.Set("loc", "UTC")
	query.Set("multiStatements", "true")
	migrationURL := fmt.Sprintf("mysql://%s:%s@tcp(%s)/%s?%s",
		url.QueryEscape(user),
		url.QueryEscape(env("DB_PASSWORD")),
		address,
		databaseName,
		query.Encode(),
	)

	return MySQLConfig{
		GormDSN:         driverConfig.FormatDSN(),
		MigrationURL:    migrationURL,
		MaxOpenConns:    maxOpen,
		MaxIdleConns:    maxIdle,
		ConnMaxLifetime: envDuration(env, "DB_CONN_MAX_LIFETIME", 10*time.Minute),
		ConnMaxIdleTime: envDuration(env, "DB_CONN_MAX_IDLE_TIME", 5*time.Minute),
	}, nil
}

func envDuration(env func(string) string, name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(env(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func envInt(env func(string) string, name string, fallback int) int {
	raw := strings.TrimSpace(env(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func CheckMySQLVersion(version string) error {
	normalized := strings.TrimSpace(version)
	if normalized == "" || strings.Contains(strings.ToLower(normalized), "mariadb") {
		return fmt.Errorf("unsupported MySQL version %q; MySQL 8.0.16+ is required", version)
	}
	prefix := normalized
	if index := strings.IndexAny(prefix, "-+ "); index >= 0 {
		prefix = prefix[:index]
	}
	parts := strings.Split(prefix, ".")
	if len(parts) < 2 {
		return fmt.Errorf("invalid MySQL version %q", version)
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	patch := 0
	if len(parts) > 2 {
		patch, _ = strconv.Atoi(parts[2])
	}
	if majorErr != nil || minorErr != nil ||
		(major < 8) ||
		(major == 8 && minor == 0 && patch < 16) {
		return fmt.Errorf("unsupported MySQL version %q; MySQL 8.0.16+ is required", version)
	}
	return nil
}

func ParseRetrieverDrivers(raw string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func ValidateMySQLRetrieverConfig(dbDriver, rawRetrievers string) error {
	if dbDriver != "mysql" {
		return nil
	}
	retrievers := ParseRetrieverDrivers(rawRetrievers)
	if len(retrievers) == 0 {
		return fmt.Errorf("DB_DRIVER=mysql requires an external vector RETRIEVE_DRIVER")
	}
	mapping := types.GetRetrieverEngineMapping()
	for _, name := range retrievers {
		if name == "postgres" || name == "sqlite" || name == "mysql" {
			return fmt.Errorf("DB_DRIVER=mysql is incompatible with RETRIEVE_DRIVER=%s", name)
		}
		if _, exists := mapping[name]; !exists {
			return fmt.Errorf("unknown RETRIEVE_DRIVER %q", name)
		}
	}
	for _, name := range retrievers {
		for _, capability := range mapping[name] {
			if capability.RetrieverType == types.VectorRetrieverType {
				return nil
			}
		}
	}
	valid := make([]string, 0, len(mapping))
	for name, capabilities := range mapping {
		if name == "postgres" || name == "sqlite" {
			continue
		}
		for _, capability := range capabilities {
			if capability.RetrieverType == types.VectorRetrieverType {
				valid = append(valid, name)
				break
			}
		}
	}
	slices.Sort(valid)
	return fmt.Errorf("MySQL requires a vector-capable external retriever; valid options: %s", strings.Join(valid, ", "))
}
