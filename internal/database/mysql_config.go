package database

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

type Driver string

const (
	DriverPostgres Driver = "postgres"
	DriverSQLite   Driver = "sqlite"
	DriverMySQL    Driver = "mysql"
)

type EnvLookup func(string) string

type Config struct {
	Driver          Driver
	Host            string
	Port            int
	User            string
	Password        string
	Name            string
	UseTLS          bool
	TLSCA           string
	TLSCert         string
	TLSKey          string
	TLSServerName   string
	ConnectTimeout  time.Duration
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func LoadConfig(lookup EnvLookup) (Config, error) {
	if lookup == nil {
		return Config{}, fmt.Errorf("database env lookup is nil")
	}
	driver := strings.ToLower(strings.TrimSpace(lookup("DB_DRIVER")))
	if driver == "" {
		driver = string(DriverPostgres)
	}

	cfg := Config{
		Driver:          Driver(driver),
		Host:            strings.TrimSpace(lookup("DB_HOST")),
		User:            strings.TrimSpace(lookup("DB_USER")),
		Password:        lookup("DB_PASSWORD"),
		Name:            strings.TrimSpace(lookup("DB_NAME")),
		ConnectTimeout:  5 * time.Second,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    30 * time.Second,
		MaxOpenConns:    0,
		MaxIdleConns:    10,
		ConnMaxLifetime: 10 * time.Minute,
	}

	switch cfg.Driver {
	case DriverPostgres:
		cfg.Port = parsePortOrDefault(lookup("DB_PORT"), 5432)
	case DriverMySQL:
		cfg.Port = parsePortOrDefault(lookup("DB_PORT"), 3306)
	case DriverSQLite:
		return cfg, nil
	default:
		return Config{}, fmt.Errorf("unsupported DB_DRIVER %q", driver)
	}

	if cfg.Host == "" {
		return Config{}, fmt.Errorf("DB_HOST is required for DB_DRIVER=%s", cfg.Driver)
	}
	if cfg.User == "" {
		return Config{}, fmt.Errorf("DB_USER is required for DB_DRIVER=%s", cfg.Driver)
	}
	if cfg.Name == "" {
		return Config{}, fmt.Errorf("DB_NAME is required for DB_DRIVER=%s", cfg.Driver)
	}

	if v, err := parseOptionalDuration(lookup("DB_CONNECT_TIMEOUT")); err != nil {
		return Config{}, err
	} else if v > 0 {
		cfg.ConnectTimeout = v
	}
	if v, err := parseOptionalDuration(lookup("DB_READ_TIMEOUT")); err != nil {
		return Config{}, err
	} else if v > 0 {
		cfg.ReadTimeout = v
	}
	if v, err := parseOptionalDuration(lookup("DB_WRITE_TIMEOUT")); err != nil {
		return Config{}, err
	} else if v > 0 {
		cfg.WriteTimeout = v
	}

	return cfg, nil
}

func parsePortOrDefault(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return fallback
	}
	return port
}

func parseOptionalDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err == nil {
		return d, nil
	}
	seconds, secErr := strconv.Atoi(raw)
	if secErr != nil || seconds <= 0 {
		return 0, fmt.Errorf("invalid database timeout %q", raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

func (c Config) SafeSummary() string {
	return fmt.Sprintf("driver=%s host=%s port=%d user=%s dbname=%s",
		c.Driver, c.Host, c.Port, c.User, c.Name)
}

func MySQLApplicationDSN(c Config) (string, error) {
	return mysqlDSN(c, false)
}

func MySQLMigrationDSN(c Config) (string, error) {
	dsn, err := mysqlDSN(c, true)
	if err != nil {
		return "", err
	}
	return "mysql://" + dsn, nil
}

func mysqlDSN(c Config, multiStatements bool) (string, error) {
	if c.Driver != DriverMySQL {
		return "", fmt.Errorf("cannot build MySQL DSN for DB_DRIVER=%s", c.Driver)
	}
	if c.Host == "" || c.Port <= 0 || c.User == "" || c.Name == "" {
		return "", fmt.Errorf("incomplete MySQL database config: %s", c.SafeSummary())
	}

	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.DBName = c.Name
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Timeout = c.ConnectTimeout
	cfg.ReadTimeout = c.ReadTimeout
	cfg.WriteTimeout = c.WriteTimeout
	cfg.MultiStatements = multiStatements
	cfg.Params = map[string]string{
		"charset":   "utf8mb4",
		"loc":       "UTC",
		"time_zone": "'+00:00'",
	}
	if c.UseTLS {
		cfg.TLSConfig = "true"
	}
	return cfg.FormatDSN(), nil
}

var knownRetrieverDrivers = map[string]struct{}{
	"postgres":         {},
	"sqlite":           {},
	"elasticsearch_v7": {},
	"elasticsearch_v8": {},
	"opensearch":       {},
	"qdrant":           {},
	"milvus":           {},
	"tencent_vectordb": {},
	"weaviate":         {},
	"doris":            {},
}

var externalVectorRetrievers = map[string]struct{}{
	"elasticsearch_v7": {},
	"elasticsearch_v8": {},
	"opensearch":       {},
	"qdrant":           {},
	"milvus":           {},
	"tencent_vectordb": {},
	"weaviate":         {},
	"doris":            {},
}

func ParseRetrieveDrivers(raw string) ([]string, error) {
	seen := map[string]struct{}{}
	var drivers []string
	for _, part := range strings.Split(raw, ",") {
		driver := strings.ToLower(strings.TrimSpace(part))
		if driver == "" {
			continue
		}
		if _, ok := knownRetrieverDrivers[driver]; !ok {
			return nil, fmt.Errorf("unknown RETRIEVE_DRIVER %q", driver)
		}
		if _, ok := seen[driver]; ok {
			continue
		}
		seen[driver] = struct{}{}
		drivers = append(drivers, driver)
	}
	return drivers, nil
}

func ValidateDriverCombination(db Driver, retrievers []string) error {
	normalized := map[string]struct{}{}
	for _, raw := range retrievers {
		driver := strings.ToLower(strings.TrimSpace(raw))
		if driver == "" {
			continue
		}
		if _, ok := knownRetrieverDrivers[driver]; !ok {
			return fmt.Errorf("unknown RETRIEVE_DRIVER %q", driver)
		}
		normalized[driver] = struct{}{}
	}

	if db != DriverMySQL {
		return nil
	}
	if _, ok := normalized["postgres"]; ok {
		return fmt.Errorf("DB_DRIVER=mysql cannot use local RETRIEVE_DRIVER=postgres; configure one of: %s", externalRetrieverList())
	}
	if _, ok := normalized["sqlite"]; ok {
		return fmt.Errorf("DB_DRIVER=mysql cannot use local RETRIEVE_DRIVER=sqlite; configure one of: %s", externalRetrieverList())
	}
	for driver := range normalized {
		if _, ok := externalVectorRetrievers[driver]; ok {
			return nil
		}
	}
	return fmt.Errorf("DB_DRIVER=mysql requires at least one external vector retriever; configure one of: %s", externalRetrieverList())
}

func externalRetrieverList() string {
	keys := make([]string, 0, len(externalVectorRetrievers))
	for k := range externalVectorRetrievers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
