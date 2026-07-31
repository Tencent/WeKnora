package database

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	gomysql "github.com/go-sql-driver/mysql"
)

var mysqlMainDatabaseEnvKeys = []string{
	"DB_DRIVER", "DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD",
	"DB_USE_TLS", "DB_TLS_SERVER_NAME", "DB_TLS_CA", "DB_TLS_CERT", "DB_TLS_KEY",
	"DB_TLS_INSECURE_SKIP_VERIFY", "DB_CONNECT_TIMEOUT", "DB_READ_TIMEOUT", "DB_WRITE_TIMEOUT",
}

var mysqlRetrieverEnvKeys = []string{
	"MYSQL_USE_TLS", "MYSQL_TLS_SERVER_NAME", "MYSQL_TLS_CA", "MYSQL_TLS_CERT", "MYSQL_TLS_KEY",
	"MYSQL_TLS_INSECURE_SKIP_VERIFY", "MYSQL_CONNECT_TIMEOUT", "MYSQL_READ_TIMEOUT", "MYSQL_WRITE_TIMEOUT",
}

func setMySQLMainDatabaseEnv(t *testing.T) {
	t.Helper()
	for _, key := range mysqlMainDatabaseEnvKeys {
		t.Setenv(key, "")
	}
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", "2001:db8::10")
	t.Setenv("DB_PORT", "3307")
	t.Setenv("DB_NAME", "We Knora")
	t.Setenv("DB_USER", "user.name")
	t.Setenv("DB_PASSWORD", "p@ss/word:#1")
}

func writeTestCAPEM(t *testing.T, serial int64) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: "WeKnora MySQL test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test CA: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write test CA: %v", err)
	}
	return path
}

func TestMySQLMainDatabaseConfigFromEnvDefaults(t *testing.T) {
	setMySQLMainDatabaseEnv(t)

	got, err := MySQLMainDatabaseConfigFromEnv()
	if err != nil {
		t.Fatalf("MySQLMainDatabaseConfigFromEnv() error = %v", err)
	}
	app, err := gomysql.ParseDSN(got.ApplicationDSN)
	if err != nil {
		t.Fatalf("parse application DSN: %v", err)
	}
	migration, err := gomysql.ParseDSN(got.MigrationDSN)
	if err != nil {
		t.Fatalf("parse migration DSN: %v", err)
	}
	for name, cfg := range map[string]*gomysql.Config{"application": app, "migration": migration} {
		if cfg.User != "user.name" || cfg.Passwd != "p@ss/word:#1" || cfg.Addr != "[2001:db8::10]:3307" || cfg.DBName != "We Knora" {
			t.Errorf("%s credentials/address/database were not preserved: %#v", name, cfg)
		}
		if cfg.Timeout != 10*time.Second || cfg.ReadTimeout != 0 || cfg.WriteTimeout != 0 {
			t.Errorf("%s timeouts = connect:%s read:%s write:%s", name, cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout)
		}
		if cfg.TLS != nil || cfg.TLSConfig != "" {
			t.Errorf("%s unexpectedly enables TLS", name)
		}
	}
	if app.MultiStatements {
		t.Fatal("application DSN must not enable multiStatements")
	}
	if !migration.MultiStatements {
		t.Fatal("migration DSN must enable multiStatements")
	}
	if got.TLSConfigName != "" {
		t.Fatalf("TLSConfigName = %q with TLS disabled", got.TLSConfigName)
	}
}

func TestMySQLMainDatabaseConfigFromEnvTLSAndTimeouts(t *testing.T) {
	setMySQLMainDatabaseEnv(t)
	t.Setenv("DB_USE_TLS", "true")
	t.Setenv("DB_TLS_CA", writeTestCAPEM(t, 1))
	t.Setenv("DB_TLS_SERVER_NAME", "mysql.internal.example")
	t.Setenv("DB_CONNECT_TIMEOUT", "7s")
	t.Setenv("DB_READ_TIMEOUT", "31s")
	t.Setenv("DB_WRITE_TIMEOUT", "29s")

	got, err := MySQLMainDatabaseConfigFromEnv()
	if err != nil {
		t.Fatalf("MySQLMainDatabaseConfigFromEnv() error = %v", err)
	}
	if got.TLSConfigName == "" {
		t.Fatal("TLS config was not registered")
	}
	for name, dsn := range map[string]string{"application": got.ApplicationDSN, "migration": got.MigrationDSN} {
		cfg, err := gomysql.ParseDSN(dsn)
		if err != nil {
			t.Fatalf("parse %s DSN: %v", name, err)
		}
		if cfg.TLSConfig != got.TLSConfigName || cfg.TLS == nil {
			t.Fatalf("%s TLS config = %q/%v, want %q/non-nil", name, cfg.TLSConfig, cfg.TLS, got.TLSConfigName)
		}
		if cfg.TLS.ServerName != "mysql.internal.example" || cfg.TLS.InsecureSkipVerify || cfg.TLS.MinVersion != 0x0303 {
			t.Errorf("%s TLS policy = server:%q insecure:%v min:%#x", name, cfg.TLS.ServerName, cfg.TLS.InsecureSkipVerify, cfg.TLS.MinVersion)
		}
		if cfg.Timeout != 7*time.Second || cfg.ReadTimeout != 31*time.Second || cfg.WriteTimeout != 29*time.Second {
			t.Errorf("%s timeouts = connect:%s read:%s write:%s", name, cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout)
		}
	}
}

func TestMySQLMainDatabaseConfigFromEnvRejectsUnsafeOrInvalidOptions(t *testing.T) {
	validCA := writeTestCAPEM(t, 2)
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "invalid TLS bool", env: map[string]string{"DB_USE_TLS": "sometimes"}, want: "DB_USE_TLS"},
		{name: "TLS option while disabled", env: map[string]string{"DB_TLS_CA": validCA}, want: "DB_USE_TLS"},
		{name: "insecure while disabled", env: map[string]string{"DB_TLS_INSECURE_SKIP_VERIFY": "true"}, want: "DB_USE_TLS"},
		{name: "invalid insecure bool", env: map[string]string{"DB_USE_TLS": "true", "DB_TLS_INSECURE_SKIP_VERIFY": "sometimes"}, want: "DB_TLS_INSECURE_SKIP_VERIFY"},
		{name: "missing CA", env: map[string]string{"DB_USE_TLS": "true", "DB_TLS_CA": filepath.Join(t.TempDir(), "missing.pem")}, want: "DB_TLS_CA"},
		{name: "invalid CA PEM", env: map[string]string{"DB_USE_TLS": "true", "DB_TLS_CA": filepath.Join(t.TempDir(), "invalid.pem")}, want: "DB_TLS_CA"},
		{name: "client cert without key", env: map[string]string{"DB_USE_TLS": "true", "DB_TLS_CERT": validCA}, want: "DB_TLS_CERT"},
		{name: "invalid connect timeout", env: map[string]string{"DB_CONNECT_TIMEOUT": "soon"}, want: "DB_CONNECT_TIMEOUT"},
		{name: "negative read timeout", env: map[string]string{"DB_READ_TIMEOUT": "-1s"}, want: "DB_READ_TIMEOUT"},
	}
	invalidPEM := tests[5].env["DB_TLS_CA"]
	if err := os.WriteFile(invalidPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setMySQLMainDatabaseEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			_, err := MySQLMainDatabaseConfigFromEnv()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestMySQLMainDatabaseTLSRegistrationIsContentAddressedAndConcurrentSafe(t *testing.T) {
	setMySQLMainDatabaseEnv(t)
	t.Setenv("DB_USE_TLS", "true")
	t.Setenv("DB_TLS_CA", writeTestCAPEM(t, 3))

	const workers = 16
	names := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg, err := MySQLMainDatabaseConfigFromEnv()
			if err != nil {
				errs <- err
				return
			}
			names <- cfg.TLSConfigName
		}()
	}
	wg.Wait()
	close(names)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent config build failed: %v", err)
	}
	var first string
	for name := range names {
		if first == "" {
			first = name
		}
		if name == "" || name != first {
			t.Fatalf("TLS config names are not stable: first=%q got=%q", first, name)
		}
	}

	t.Setenv("DB_TLS_CA", writeTestCAPEM(t, 4))
	different, err := MySQLMainDatabaseConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if different.TLSConfigName == first {
		t.Fatal("different CA content reused the same TLS registry name")
	}
}

func TestRetrieverDSNBuilderDoesNotReadMainDatabaseTLSOptions(t *testing.T) {
	t.Setenv("DB_USE_TLS", "true")
	t.Setenv("DB_TLS_CA", filepath.Join(t.TempDir(), "missing.pem"))
	cfg, err := gomysql.ParseDSN(BuildMySQLApplicationDSN("u", "p", "mysql:3306", "retriever"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS != nil || cfg.TLSConfig != "" {
		t.Fatal("retriever DSN unexpectedly inherited main database TLS settings")
	}
}

func TestMySQLRetrieverConfigFromEnvUsesIndependentTLSPolicy(t *testing.T) {
	for _, key := range mysqlRetrieverEnvKeys {
		t.Setenv(key, "")
	}
	// A broken primary-database CA must not affect an independently configured
	// retriever; each endpoint has its own trust policy.
	t.Setenv("DB_USE_TLS", "true")
	t.Setenv("DB_TLS_CA", filepath.Join(t.TempDir(), "missing-main-ca.pem"))
	t.Setenv("MYSQL_USE_TLS", "true")
	t.Setenv("MYSQL_TLS_CA", writeTestCAPEM(t, 5))
	t.Setenv("MYSQL_TLS_SERVER_NAME", "retriever.mysql.example")
	t.Setenv("MYSQL_CONNECT_TIMEOUT", "8s")
	t.Setenv("MYSQL_READ_TIMEOUT", "20s")
	t.Setenv("MYSQL_WRITE_TIMEOUT", "21s")

	got, err := MySQLRetrieverConfigFromEnv(
		"retriever", "secret", "mysql.example:3306", "vectors",
	)
	if err != nil {
		t.Fatalf("MySQLRetrieverConfigFromEnv() error = %v", err)
	}
	cfg, err := gomysql.ParseDSN(got.DSN)
	if err != nil {
		t.Fatalf("parse retriever DSN: %v", err)
	}
	if cfg.TLS == nil || cfg.TLSConfig != got.TLSConfigName {
		t.Fatalf("retriever TLS = %#v/%q, want registered %q", cfg.TLS, cfg.TLSConfig, got.TLSConfigName)
	}
	if cfg.TLS.ServerName != "retriever.mysql.example" || cfg.TLS.InsecureSkipVerify {
		t.Fatalf("retriever TLS policy = server:%q insecure:%v", cfg.TLS.ServerName, cfg.TLS.InsecureSkipVerify)
	}
	if cfg.Timeout != 8*time.Second || cfg.ReadTimeout != 20*time.Second || cfg.WriteTimeout != 21*time.Second {
		t.Fatalf("retriever timeouts = connect:%s read:%s write:%s", cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout)
	}
}

func TestMySQLRetrieverConfigFromEnvFailsClosed(t *testing.T) {
	for _, key := range mysqlRetrieverEnvKeys {
		t.Setenv(key, "")
	}
	t.Setenv("MYSQL_TLS_CA", writeTestCAPEM(t, 6))
	if _, err := MySQLRetrieverConfigFromEnv("u", "p", "mysql:3306", "vectors"); err == nil ||
		!strings.Contains(err.Error(), "MYSQL_USE_TLS") {
		t.Fatalf("MySQLRetrieverConfigFromEnv() error = %v, want MYSQL_USE_TLS guidance", err)
	}
}

func TestBuildMySQLApplicationDSNEnforcesSessionContract(t *testing.T) {
	dsn := BuildMySQLApplicationDSN(
		"user:name",
		"p@ss/word:1",
		"mysql:3306",
		"WeKnora",
	)
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if !cfg.ParseTime || cfg.Loc.String() != "UTC" {
		t.Fatalf("time parsing config = parseTime:%v loc:%v, want true/UTC", cfg.ParseTime, cfg.Loc)
	}
	if got := cfg.Params["time_zone"]; got != "'"+MySQLSessionTimeZone+"'" {
		t.Fatalf("time_zone = %q", got)
	}
	if got := cfg.Params["sql_mode"]; got != "'"+MySQLSessionSQLMode+"'" {
		t.Fatalf("sql_mode = %q", got)
	}
}

func TestBuildMySQLMigrationDSNEnforcesSessionContract(t *testing.T) {
	dsn := BuildMySQLMigrationDSN(
		"weknora",
		"p@ss word#1",
		"mysql:3306",
		"WeKnora",
	)
	if !strings.HasPrefix(dsn, "mysql://") {
		t.Fatalf("migration DSN = %q", dsn)
	}
	cfg, err := gomysql.ParseDSN(strings.TrimPrefix(dsn, "mysql://"))
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if !cfg.MultiStatements || !cfg.ParseTime || cfg.Loc.String() != "UTC" {
		t.Fatalf(
			"migration config = multi:%v parseTime:%v loc:%v",
			cfg.MultiStatements,
			cfg.ParseTime,
			cfg.Loc,
		)
	}
	if got := cfg.Params["time_zone"]; got != "'"+MySQLSessionTimeZone+"'" {
		t.Fatalf("time_zone = %q", got)
	}
	if got := cfg.Params["sql_mode"]; got != "'"+MySQLSessionSQLMode+"'" {
		t.Fatalf("sql_mode = %q", got)
	}
}

func TestValidateMySQLVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		comment string
		wantErr bool
	}{
		{name: "minimum", version: "8.0.16", comment: "MySQL Community Server - GPL"},
		{name: "current LTS", version: "8.4.0", comment: "MySQL Community Server - GPL"},
		{name: "checks not enforced", version: "8.0.15", comment: "MySQL Community Server - GPL", wantErr: true},
		{name: "mysql 5.7", version: "5.7.44", comment: "MySQL Community Server - GPL", wantErr: true},
		{name: "mariadb", version: "10.11.6-MariaDB", comment: "mariadb.org binary distribution", wantErr: true},
		{name: "tidb", version: "8.0.11-TiDB-v8.5.0", comment: "TiDB Server", wantErr: true},
		{name: "percona", version: "8.0.36-28", comment: "Percona Server (GPL)"},
		{name: "invalid", version: "unknown", comment: "unknown", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMySQLVersion(tt.version, tt.comment)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateMySQLVersion() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateMySQLSession(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		comment  string
		timeZone string
		sqlMode  string
		wantErr  string
	}{
		{
			name:     "valid",
			version:  "8.4.0",
			comment:  "MySQL Community Server - GPL",
			timeZone: MySQLSessionTimeZone,
			sqlMode:  MySQLSessionSQLMode,
		},
		{
			name:     "non UTC session",
			version:  "8.4.0",
			comment:  "MySQL Community Server - GPL",
			timeZone: "SYSTEM",
			sqlMode:  MySQLSessionSQLMode,
			wantErr:  "time_zone",
		},
		{
			name:     "non strict session",
			version:  "8.4.0",
			comment:  "MySQL Community Server - GPL",
			timeZone: MySQLSessionTimeZone,
			sqlMode:  "NO_ENGINE_SUBSTITUTION",
			wantErr:  "strict mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New() error = %v", err)
			}
			defer sqlDB.Close()
			mock.ExpectQuery(regexp.QuoteMeta(
				"SELECT VERSION(), @@version_comment, @@SESSION.time_zone, @@SESSION.sql_mode",
			)).WillReturnRows(sqlmock.NewRows(
				[]string{"version", "version_comment", "time_zone", "sql_mode"},
			).AddRow(tt.version, tt.comment, tt.timeZone, tt.sqlMode))

			err = ValidateMySQLSession(context.Background(), sqlDB)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("ValidateMySQLSession() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("ValidateMySQLSession() error = %v, want substring %q", err, tt.wantErr)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet SQL expectations: %v", err)
			}
		})
	}
}

func TestHasStrictMySQLMode(t *testing.T) {
	for _, mode := range []string{
		"STRICT_TRANS_TABLES",
		"ONLY_FULL_GROUP_BY,STRICT_ALL_TABLES",
		"traditional",
	} {
		if !hasStrictMySQLMode(mode) {
			t.Fatalf("hasStrictMySQLMode(%q) = false", mode)
		}
	}
	if hasStrictMySQLMode("ONLY_FULL_GROUP_BY,NO_ENGINE_SUBSTITUTION") {
		t.Fatal("non-strict sql_mode accepted")
	}
}

func TestMainDatabaseDeploymentDefaultsAreSafe(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", ".env.example"): {
			"AUTO_RECOVER_DIRTY=true",
			"# DB_MAX_OPEN_CONNS=20",
			"# DB_CONN_MAX_IDLE_TIME=5m",
		},
		filepath.Join("..", "..", "docker-compose.yml"): {
			"AUTO_RECOVER_DIRTY=${AUTO_RECOVER_DIRTY:-true}",
			"DB_MAX_OPEN_CONNS=${DB_MAX_OPEN_CONNS:-}",
			"DB_CONN_MAX_IDLE_TIME=${DB_CONN_MAX_IDLE_TIME:-}",
		},
		filepath.Join("..", "..", "docker-compose.mysql.yml"): {
			"--default-time-zone=+00:00",
			"AUTO_RECOVER_DIRTY=false",
			"DB_MAX_OPEN_CONNS=${DB_MAX_OPEN_CONNS:-20}",
			"DB_CONN_MAX_IDLE_TIME=${DB_CONN_MAX_IDLE_TIME:-5m}",
		},
		filepath.Join("..", "..", "helm", "values.yaml"): {
			`AUTO_RECOVER_DIRTY: "true"`,
			`maxOpenConns: "20"`,
			`connMaxIdleTime: "5m"`,
		},
		filepath.Join("..", "..", "helm", "templates", "app.yaml"): {
			"name: DB_MAX_OPEN_CONNS",
			"name: DB_CONN_MAX_IDLE_TIME",
			`ternary "false" .Values.app.env.AUTO_RECOVER_DIRTY`,
		},
	}
	for file, wants := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(body)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", file, want)
			}
		}
	}
}
