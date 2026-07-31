package mysqlconfig

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

// PoolConfig holds validated MySQL connection-pool and timeout settings.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// BuildDSN constructs separate go-sql-driver DSNs for application queries and
// migrations from the same validated environment. Only the migration DSN
// enables multiStatements because the squashed MySQL baseline contains the
// complete schema in one multi-statement file.
func BuildDSN(env func(string) string) (
	applicationDSN string,
	migrationDSN string,
	pool PoolConfig,
	err error,
) {
	host := strings.TrimSpace(env("DB_HOST"))
	port := strings.TrimSpace(env("DB_PORT"))
	if port == "" {
		port = "3306"
	}
	user := strings.TrimSpace(env("DB_USER"))
	password := env("DB_PASSWORD")
	dbname := strings.TrimSpace(env("DB_NAME"))

	if host == "" {
		return "", "", PoolConfig{}, fmt.Errorf("DB_HOST is empty; cannot construct MySQL DSN")
	}
	if user == "" {
		return "", "", PoolConfig{}, fmt.Errorf("DB_USER is empty; cannot construct MySQL DSN")
	}
	if password == "" {
		return "", "", PoolConfig{}, fmt.Errorf("DB_PASSWORD is empty; cannot construct MySQL DSN")
	}
	if dbname == "" {
		return "", "", PoolConfig{}, fmt.Errorf("DB_NAME is empty; cannot construct MySQL DSN")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", "", PoolConfig{}, fmt.Errorf("DB_PORT must be an integer between 1 and 65535, got %q", port)
	}

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
		"time_zone": "'+00:00'",
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC

	cfg.Timeout, err = envDuration(env, "DB_CONNECT_TIMEOUT", 10*time.Second)
	if err != nil {
		return "", "", PoolConfig{}, err
	}
	cfg.ReadTimeout, err = envDuration(env, "DB_READ_TIMEOUT", 30*time.Second)
	if err != nil {
		return "", "", PoolConfig{}, err
	}
	cfg.WriteTimeout, err = envDuration(env, "DB_WRITE_TIMEOUT", 30*time.Second)
	if err != nil {
		return "", "", PoolConfig{}, err
	}

	tlsConfigName, err := configureTLS(env)
	if err != nil {
		return "", "", PoolConfig{}, err
	}
	if tlsConfigName != "" {
		cfg.TLSConfig = tlsConfigName
	}

	applicationDSN = cfg.FormatDSN()
	cfg.MultiStatements = true
	migrationDSN = cfg.FormatDSN()

	maxOpen, err := envInt(env, "DB_MAX_OPEN_CONNS", 50, false)
	if err != nil {
		return "", "", PoolConfig{}, err
	}
	maxIdle, err := envInt(env, "DB_MAX_IDLE_CONNS", 10, true)
	if err != nil {
		return "", "", PoolConfig{}, err
	}
	if maxIdle > maxOpen {
		return "", "", PoolConfig{}, fmt.Errorf(
			"DB_MAX_IDLE_CONNS (%d) must not exceed DB_MAX_OPEN_CONNS (%d)",
			maxIdle,
			maxOpen,
		)
	}
	connMaxLifetime, err := envDuration(env, "DB_CONN_MAX_LIFETIME", 10*time.Minute)
	if err != nil {
		return "", "", PoolConfig{}, err
	}
	connMaxIdleTime, err := envDuration(env, "DB_CONN_MAX_IDLE_TIME", 5*time.Minute)
	if err != nil {
		return "", "", PoolConfig{}, err
	}

	return applicationDSN, migrationDSN, PoolConfig{
		MaxOpenConns:    maxOpen,
		MaxIdleConns:    maxIdle,
		ConnMaxLifetime: connMaxLifetime,
		ConnMaxIdleTime: connMaxIdleTime,
	}, nil
}

func envDuration(
	env func(string) string,
	key string,
	defaultValue time.Duration,
) (time.Duration, error) {
	value := strings.TrimSpace(env(key))
	if value == "" {
		return defaultValue, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration, got %q: %w", key, value, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero, got %q", key, value)
	}
	return duration, nil
}

func envInt(
	env func(string) string,
	key string,
	defaultValue int,
	allowZero bool,
) (int, error) {
	value := strings.TrimSpace(env(key))
	if value == "" {
		return defaultValue, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q: %w", key, value, err)
	}
	if number < 0 || (!allowZero && number == 0) {
		requirement := "greater than zero"
		if allowZero {
			requirement = "zero or greater"
		}
		return 0, fmt.Errorf("%s must be %s, got %d", key, requirement, number)
	}
	return number, nil
}

func envBool(env func(string) string, key string, defaultValue bool) (bool, error) {
	value := strings.TrimSpace(env(key))
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false, got %q: %w", key, value, err)
	}
	return parsed, nil
}

func configureTLS(env func(string) string) (string, error) {
	useTLS, err := envBool(env, "DB_USE_TLS", false)
	if err != nil {
		return "", err
	}
	insecureSkipVerify, err := envBool(env, "DB_TLS_INSECURE_SKIP_VERIFY", false)
	if err != nil {
		return "", err
	}

	serverName := strings.TrimSpace(env("DB_TLS_SERVER_NAME"))
	caFile := strings.TrimSpace(env("DB_TLS_CA"))
	certFile := strings.TrimSpace(env("DB_TLS_CERT"))
	keyFile := strings.TrimSpace(env("DB_TLS_KEY"))
	hasCustomSettings := serverName != "" ||
		caFile != "" ||
		certFile != "" ||
		keyFile != "" ||
		insecureSkipVerify
	if !useTLS {
		if hasCustomSettings {
			return "", fmt.Errorf("DB_USE_TLS must be true when DB_TLS_* settings are configured")
		}
		return "", nil
	}

	if (certFile == "") != (keyFile == "") {
		if certFile != "" {
			return "", fmt.Errorf("DB_TLS_CERT requires DB_TLS_KEY")
		}
		return "", fmt.Errorf("DB_TLS_KEY requires DB_TLS_CERT")
	}
	if !hasCustomSettings {
		return "true", nil
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         serverName,
		InsecureSkipVerify: insecureSkipVerify, //nolint:gosec // Explicit development-only operator setting.
	}

	fingerprint := sha256.New()
	_, _ = fingerprint.Write([]byte(serverName))
	_, _ = fingerprint.Write([]byte(strconv.FormatBool(insecureSkipVerify)))

	if caFile != "" {
		caPEM, readErr := os.ReadFile(caFile)
		if readErr != nil {
			return "", fmt.Errorf("read DB_TLS_CA %q: %w", caFile, readErr)
		}
		rootCAs, poolErr := x509.SystemCertPool()
		if poolErr != nil || rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}
		if !rootCAs.AppendCertsFromPEM(caPEM) {
			return "", fmt.Errorf("DB_TLS_CA %q does not contain a valid PEM certificate", caFile)
		}
		tlsConfig.RootCAs = rootCAs
		_, _ = fingerprint.Write(caPEM)
	}

	if certFile != "" {
		certificate, loadErr := tls.LoadX509KeyPair(certFile, keyFile)
		if loadErr != nil {
			return "", fmt.Errorf(
				"load DB_TLS_CERT %q and DB_TLS_KEY %q: %w",
				certFile,
				keyFile,
				loadErr,
			)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
		_, _ = fingerprint.Write([]byte(certFile))
		_, _ = fingerprint.Write([]byte(keyFile))
	}

	configName := fmt.Sprintf("weknora-%x", fingerprint.Sum(nil)[:8])
	if err := mysqlDriver.RegisterTLSConfig(configName, tlsConfig); err != nil {
		return "", fmt.Errorf("register MySQL TLS configuration: %w", err)
	}
	return configName, nil
}
