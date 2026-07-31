package database

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

var (
	mysqlTLSRegistryMu sync.Mutex
	mysqlTLSRegistry   = make(map[string]struct{})
)

// MySQLMainDatabaseConfig contains the two go-sql-driver DSNs needed by the
// primary database. They are cloned from one configuration so application and
// migration connections cannot silently diverge. Only migrations enable
// multiStatements.
type MySQLMainDatabaseConfig struct {
	ApplicationDSN        string
	MigrationDSN          string
	MigrationURL          string
	TLSConfigName         string
	TLSInsecureSkipVerify bool
}

// MySQLClientConfig is a single application-style MySQL connection. It is
// used by the environment-backed MySQL retriever, whose trust policy is kept
// independent from the primary database.
type MySQLClientConfig struct {
	DSN                   string
	TLSConfigName         string
	TLSInsecureSkipVerify bool
}

type mysqlConnectionOptions struct {
	envPrefix             string
	useTLS                bool
	tlsServerName         string
	tlsCAPath             string
	tlsCertPath           string
	tlsKeyPath            string
	tlsInsecureSkipVerify bool
	connectTimeout        time.Duration
	readTimeout           time.Duration
	writeTimeout          time.Duration
}

// MySQLMainDatabaseConfigFromEnv loads the primary MySQL connection policy.
// Retriever MySQL settings deliberately do not use this loader because the
// main database and an external vector store may have different trust roots.
func MySQLMainDatabaseConfigFromEnv() (MySQLMainDatabaseConfig, error) {
	dbPort := strings.TrimSpace(os.Getenv("DB_PORT"))
	if dbPort == "" {
		dbPort = "3306"
	}
	opts, err := mysqlConnectionOptionsFromEnv("DB_")
	if err != nil {
		return MySQLMainDatabaseConfig{}, err
	}
	return buildMySQLMainDatabaseConfig(
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		net.JoinHostPort(strings.TrimSpace(os.Getenv("DB_HOST")), dbPort),
		os.Getenv("DB_NAME"),
		opts,
	)
}

// MySQLRetrieverConfigFromEnv builds the environment-backed retriever DSN.
// MYSQL_* transport settings are intentionally separate from DB_* settings so
// a PostgreSQL main database can safely use a MySQL retriever at another
// endpoint. Deployments that reuse one MySQL endpoint should mirror both sets.
func MySQLRetrieverConfigFromEnv(user, password, addr, database string) (MySQLClientConfig, error) {
	opts, err := mysqlConnectionOptionsFromEnv("MYSQL_")
	if err != nil {
		return MySQLClientConfig{}, err
	}
	config, err := buildMySQLMainDatabaseConfig(user, password, addr, database, opts)
	if err != nil {
		return MySQLClientConfig{}, err
	}
	return MySQLClientConfig{
		DSN:                   config.ApplicationDSN,
		TLSConfigName:         config.TLSConfigName,
		TLSInsecureSkipVerify: config.TLSInsecureSkipVerify,
	}, nil
}

func mysqlConnectionOptionsFromEnv(prefix string) (mysqlConnectionOptions, error) {
	envName := func(suffix string) string { return prefix + suffix }
	useTLS, err := parseMySQLBoolEnv(envName("USE_TLS"), false)
	if err != nil {
		return mysqlConnectionOptions{}, err
	}
	insecure, err := parseMySQLBoolEnv(envName("TLS_INSECURE_SKIP_VERIFY"), false)
	if err != nil {
		return mysqlConnectionOptions{}, err
	}
	connectTimeout, err := parseMySQLDurationEnv(envName("CONNECT_TIMEOUT"), 10*time.Second)
	if err != nil {
		return mysqlConnectionOptions{}, err
	}
	readTimeout, err := parseMySQLDurationEnv(envName("READ_TIMEOUT"), 0)
	if err != nil {
		return mysqlConnectionOptions{}, err
	}
	writeTimeout, err := parseMySQLDurationEnv(envName("WRITE_TIMEOUT"), 0)
	if err != nil {
		return mysqlConnectionOptions{}, err
	}

	opts := mysqlConnectionOptions{
		envPrefix:             prefix,
		useTLS:                useTLS,
		tlsServerName:         strings.TrimSpace(os.Getenv(envName("TLS_SERVER_NAME"))),
		tlsCAPath:             strings.TrimSpace(os.Getenv(envName("TLS_CA"))),
		tlsCertPath:           strings.TrimSpace(os.Getenv(envName("TLS_CERT"))),
		tlsKeyPath:            strings.TrimSpace(os.Getenv(envName("TLS_KEY"))),
		tlsInsecureSkipVerify: insecure,
		connectTimeout:        connectTimeout,
		readTimeout:           readTimeout,
		writeTimeout:          writeTimeout,
	}
	if !opts.useTLS && (opts.tlsServerName != "" || opts.tlsCAPath != "" ||
		opts.tlsCertPath != "" || opts.tlsKeyPath != "" || opts.tlsInsecureSkipVerify) {
		return mysqlConnectionOptions{}, fmt.Errorf(
			"%s must be true before %s, %s, %s, %s, or %s can be used",
			envName("USE_TLS"), envName("TLS_SERVER_NAME"), envName("TLS_CA"),
			envName("TLS_CERT"), envName("TLS_KEY"), envName("TLS_INSECURE_SKIP_VERIFY"),
		)
	}
	if (opts.tlsCertPath == "") != (opts.tlsKeyPath == "") {
		return mysqlConnectionOptions{}, fmt.Errorf(
			"%s and %s must be configured together", envName("TLS_CERT"), envName("TLS_KEY"),
		)
	}
	return opts, nil
}

func parseMySQLBoolEnv(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	if strings.EqualFold(raw, "true") {
		return true, nil
	}
	if strings.EqualFold(raw, "false") {
		return false, nil
	}
	return false, fmt.Errorf("%s must be true or false, got %q", name, raw)
}

func parseMySQLDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid Go duration: %w", name, err)
	}
	if duration < 0 {
		return 0, fmt.Errorf("%s must not be negative", name)
	}
	return duration, nil
}

func buildMySQLMainDatabaseConfig(
	user, password, addr, database string,
	opts mysqlConnectionOptions,
) (MySQLMainDatabaseConfig, error) {
	base := gomysql.NewConfig()
	base.User = user
	base.Passwd = password
	base.Net = "tcp"
	base.Addr = addr
	base.DBName = database
	base.ParseTime = true
	base.Loc = time.UTC
	base.Params = mysqlSessionParams()
	base.Timeout = opts.connectTimeout
	base.ReadTimeout = opts.readTimeout
	base.WriteTimeout = opts.writeTimeout

	tlsConfigName := ""
	if opts.useTLS {
		var err error
		tlsConfigName, err = registerMySQLTLSConfig(opts)
		if err != nil {
			return MySQLMainDatabaseConfig{}, err
		}
		base.TLSConfig = tlsConfigName
	}

	application := base.Clone()
	application.MultiStatements = false
	migration := base.Clone()
	migration.MultiStatements = true

	return MySQLMainDatabaseConfig{
		ApplicationDSN:        application.FormatDSN(),
		MigrationDSN:          migration.FormatDSN(),
		MigrationURL:          buildMySQLMigrationURL(migration),
		TLSConfigName:         tlsConfigName,
		TLSInsecureSkipVerify: opts.tlsInsecureSkipVerify,
	}, nil
}

func registerMySQLTLSConfig(opts mysqlConnectionOptions) (string, error) {
	envName := func(suffix string) string { return opts.envPrefix + suffix }
	var caPEM, certPEM, keyPEM []byte
	var roots *x509.CertPool
	if opts.tlsCAPath != "" {
		var err error
		caPEM, err = os.ReadFile(opts.tlsCAPath)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", envName("TLS_CA"), err)
		}
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(caPEM) {
			return "", fmt.Errorf("%s does not contain a valid PEM certificate", envName("TLS_CA"))
		}
	}

	certificates := make([]tls.Certificate, 0, 1)
	if opts.tlsCertPath != "" {
		var err error
		certPEM, err = os.ReadFile(opts.tlsCertPath)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", envName("TLS_CERT"), err)
		}
		keyPEM, err = os.ReadFile(opts.tlsKeyPath)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", envName("TLS_KEY"), err)
		}
		certificate, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return "", fmt.Errorf("load %s and %s: %w", envName("TLS_CERT"), envName("TLS_KEY"), err)
		}
		certificates = append(certificates, certificate)
	}

	hash := sha256.New()
	for _, value := range [][]byte{
		[]byte(opts.tlsServerName),
		[]byte(strconv.FormatBool(opts.tlsInsecureSkipVerify)),
		[]byte(strconv.Itoa(tls.VersionTLS12)),
		caPEM,
		certPEM,
		keyPEM,
	} {
		_, _ = fmt.Fprintf(hash, "%d:", len(value))
		_, _ = hash.Write(value)
	}
	name := "weknora-" + hex.EncodeToString(hash.Sum(nil))
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         opts.tlsServerName,
		InsecureSkipVerify: opts.tlsInsecureSkipVerify, // #nosec G402 -- explicit operator opt-in
		RootCAs:            roots,
		Certificates:       certificates,
	}

	mysqlTLSRegistryMu.Lock()
	defer mysqlTLSRegistryMu.Unlock()
	if _, exists := mysqlTLSRegistry[name]; exists {
		return name, nil
	}
	if err := gomysql.RegisterTLSConfig(name, tlsConfig); err != nil {
		return "", fmt.Errorf("register MySQL TLS configuration: %w", err)
	}
	mysqlTLSRegistry[name] = struct{}{}
	return name, nil
}

func buildMySQLMigrationURL(cfg *gomysql.Config) string {
	query := url.Values{}
	query.Set("loc", "UTC")
	query.Set("multiStatements", "true")
	query.Set("parseTime", "true")
	if cfg.Timeout > 0 {
		query.Set("timeout", cfg.Timeout.String())
	}
	if cfg.ReadTimeout > 0 {
		query.Set("readTimeout", cfg.ReadTimeout.String())
	}
	if cfg.WriteTimeout > 0 {
		query.Set("writeTimeout", cfg.WriteTimeout.String())
	}
	if cfg.TLSConfig != "" {
		query.Set("tls", cfg.TLSConfig)
	}
	for key, value := range cfg.Params {
		query.Set(key, value)
	}
	return fmt.Sprintf(
		"mysql://%s@tcp(%s)/%s?%s",
		url.UserPassword(cfg.User, cfg.Passwd).String(),
		cfg.Addr,
		url.PathEscape(cfg.DBName),
		query.Encode(),
	)
}

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
