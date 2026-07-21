package database

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

var (
	mysqlTLSMu         sync.Mutex
	mysqlTLSRegistered = make(map[string]struct{})
)

// MySQLSettings contains connection and pool settings for the business
// primary database. Retrieval remains configured independently.
type MySQLSettings struct {
	DSN             string
	MigrationDSN    string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// LoadMySQLSettingsFromEnv builds MySQL settings from the shared DB_*
// environment variables without concatenating credentials into a raw DSN.
func LoadMySQLSettingsFromEnv() (*MySQLSettings, error) {
	host := envOrDefault("DB_HOST", "127.0.0.1")
	port := envOrDefault("DB_PORT", "3306")
	dbName := strings.TrimSpace(os.Getenv("DB_NAME"))
	if dbName == "" {
		return nil, fmt.Errorf("DB_NAME is required when DB_DRIVER=mysql")
	}
	dbUser := strings.TrimSpace(os.Getenv("DB_USER"))
	if dbUser == "" {
		return nil, fmt.Errorf("DB_USER is required when DB_DRIVER=mysql")
	}

	cfg := mysqlDriver.NewConfig()
	cfg.User = dbUser
	cfg.Passwd = os.Getenv("DB_PASSWORD")
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, port)
	cfg.DBName = dbName
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Collation = envOrDefault("DB_COLLATION", "utf8mb4_unicode_ci")
	cfg.Params = map[string]string{
		"charset":   envOrDefault("DB_CHARSET", "utf8mb4"),
		"time_zone": "'+00:00'",
	}
	cfg.Timeout = envDuration("DB_CONNECT_TIMEOUT", 10*time.Second)
	cfg.ReadTimeout = envDuration("DB_READ_TIMEOUT", 30*time.Second)
	cfg.WriteTimeout = envDuration("DB_WRITE_TIMEOUT", 30*time.Second)
	cfg.CheckConnLiveness = true
	cfg.RejectReadOnly = envBool("DB_REJECT_READ_ONLY", false)

	if envBool("DB_USE_TLS", false) {
		name, err := registerMySQLTLSConfig()
		if err != nil {
			return nil, err
		}
		cfg.TLSConfig = name
	}

	migrationCfg := cfg.Clone()
	migrationCfg.MultiStatements = true

	return &MySQLSettings{
		DSN: cfg.FormatDSN(),
		// Keep this as a native go-sql-driver/mysql DSN. The migration driver
		// can use it directly, which avoids re-parsing credentials through a
		// mysql:// URL and accidentally QueryUnescape-ing a literal '%' in a
		// password.
		MigrationDSN:    migrationCfg.FormatDSN(),
		MaxOpenConns:    envNonNegativeInt("DB_MAX_OPEN_CONNS", 50),
		MaxIdleConns:    envNonNegativeInt("DB_MAX_IDLE_CONNS", 10),
		ConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", 10*time.Minute),
		ConnMaxIdleTime: envDuration("DB_CONN_MAX_IDLE_TIME", 5*time.Minute),
	}, nil
}

func registerMySQLTLSConfig() (string, error) {
	tlsConfig, fingerprint, err := mysqlTLSConfigFromEnv()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("weknora-primary-%x", sha256.Sum256([]byte(fingerprint)))[:32]

	mysqlTLSMu.Lock()
	defer mysqlTLSMu.Unlock()
	if _, ok := mysqlTLSRegistered[name]; ok {
		return name, nil
	}
	if err := mysqlDriver.RegisterTLSConfig(name, tlsConfig); err != nil {
		return "", fmt.Errorf("register MySQL TLS config: %w", err)
	}
	mysqlTLSRegistered[name] = struct{}{}
	return name, nil
}

func mysqlTLSConfigFromEnv() (*tls.Config, string, error) {
	serverName := strings.TrimSpace(os.Getenv("DB_TLS_SERVER_NAME"))
	caPath := strings.TrimSpace(os.Getenv("DB_TLS_CA"))
	certPath := strings.TrimSpace(os.Getenv("DB_TLS_CERT"))
	keyPath := strings.TrimSpace(os.Getenv("DB_TLS_KEY"))
	insecure := envBool("DB_TLS_INSECURE_SKIP_VERIFY", false)

	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         serverName,
		InsecureSkipVerify: insecure,
	}

	if caPath != "" {
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return nil, "", fmt.Errorf("read DB_TLS_CA: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if ok := pool.AppendCertsFromPEM(pem); !ok {
			return nil, "", fmt.Errorf("DB_TLS_CA does not contain a valid certificate")
		}
		cfg.RootCAs = pool
	}

	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return nil, "", fmt.Errorf("DB_TLS_CERT and DB_TLS_KEY must be configured together")
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, "", fmt.Errorf("load MySQL client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	fingerprint := strings.Join([]string{
		serverName,
		caPath,
		certPath,
		keyPath,
		strconv.FormatBool(insecure),
	}, "\x00")
	return cfg, fingerprint, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func envNonNegativeInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}
