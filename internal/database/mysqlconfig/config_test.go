package mysqlconfig

import (
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

func testEnv(vals map[string]string) func(string) string {
	return func(key string) string { return vals[key] }
}

func baseEnv() map[string]string {
	return map[string]string{
		"DB_HOST":     "127.0.0.1",
		"DB_PORT":     "3306",
		"DB_USER":     "weknora",
		"DB_PASSWORD": "secret",
		"DB_NAME":     "weknora_db",
	}
}

func mustParseMySQLDSN(t *testing.T, dsn string) *mysqlDriver.Config {
	t.Helper()
	config, err := mysqlDriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse MySQL DSN: %v", err)
	}
	return config
}

func TestBuildDSN_BasicShape(t *testing.T) {
	gormDSN, migrateDSN, pool, err := BuildDSN(testEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"weknora",
		"secret",
		"tcp(127.0.0.1:3306)",
		"weknora_db",
		"charset=utf8mb4",
		"parseTime=true",
	} {
		if !strings.Contains(gormDSN, want) {
			t.Errorf("gormDSN missing %q; got: %s", want, gormDSN)
		}
	}
	applicationConfig := mustParseMySQLDSN(t, gormDSN)
	migrationConfig := mustParseMySQLDSN(t, migrateDSN)
	if applicationConfig.MultiStatements {
		t.Errorf("application DSN must keep multiStatements disabled; got: %s", gormDSN)
	}
	if !migrationConfig.MultiStatements {
		t.Errorf("migration DSN must enable multiStatements; got: %s", migrateDSN)
	}
	if migrationConfig.Addr != applicationConfig.Addr ||
		migrationConfig.User != applicationConfig.User ||
		migrationConfig.DBName != applicationConfig.DBName ||
		migrationConfig.Loc != applicationConfig.Loc {
		t.Fatal("deriving the migration DSN changed application connection settings")
	}
	if pool.MaxOpenConns != 50 || pool.MaxIdleConns != 10 {
		t.Errorf("pool defaults wrong: %+v", pool)
	}
}

func TestBuildDSN_CollationIsPinned(t *testing.T) {
	gormDSN, migrateDSN, _, err := BuildDSN(testEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gormDSN, "collation=utf8mb4_0900_ai_ci") {
		t.Errorf("gormDSN must pin collation; got: %s", gormDSN)
	}
	if !strings.Contains(migrateDSN, "collation=utf8mb4_0900_ai_ci") {
		t.Errorf("migrateDSN must pin collation; got: %s", migrateDSN)
	}
}

func TestBuildDSN_TLSUsesVerifiedTransport(t *testing.T) {
	env := baseEnv()
	env["DB_USE_TLS"] = "true"

	gormDSN, migrateDSN, _, err := BuildDSN(testEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, dsn := range map[string]string{
		"application": gormDSN,
		"migration":   migrateDSN,
	} {
		if !strings.Contains(dsn, "tls=true") {
			t.Errorf("%s DSN must require verified TLS; got: %s", name, dsn)
		}
	}
}

func TestBuildDSN_TLSCustomVerificationSettings(t *testing.T) {
	env := baseEnv()
	env["DB_USE_TLS"] = "true"
	env["DB_TLS_SERVER_NAME"] = "mysql.internal.example"
	env["DB_TLS_INSECURE_SKIP_VERIFY"] = "true"

	gormDSN, migrateDSN, _, err := BuildDSN(testEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, dsn := range map[string]string{
		"application": gormDSN,
		"migration":   migrateDSN,
	} {
		if !strings.Contains(dsn, "tls=weknora-") {
			t.Errorf("%s DSN must reference the registered custom TLS config; got: %s", name, dsn)
		}
	}
}

func TestBuildDSN_InvalidTLSConfigurationErrors(t *testing.T) {
	tests := []struct {
		name        string
		values      map[string]string
		errContains string
	}{
		{
			name:        "invalid use tls boolean",
			values:      map[string]string{"DB_USE_TLS": "sometimes"},
			errContains: "DB_USE_TLS",
		},
		{
			name: "custom settings require tls",
			values: map[string]string{
				"DB_USE_TLS":         "false",
				"DB_TLS_SERVER_NAME": "mysql.example",
			},
			errContains: "DB_USE_TLS",
		},
		{
			name: "client certificate requires key",
			values: map[string]string{
				"DB_USE_TLS":  "true",
				"DB_TLS_CERT": "/tmp/client.pem",
			},
			errContains: "DB_TLS_CERT",
		},
		{
			name: "client key requires certificate",
			values: map[string]string{
				"DB_USE_TLS": "true",
				"DB_TLS_KEY": "/tmp/client-key.pem",
			},
			errContains: "DB_TLS_KEY",
		},
		{
			name: "missing ca file",
			values: map[string]string{
				"DB_USE_TLS": "true",
				"DB_TLS_CA":  "/definitely/missing/mysql-ca.pem",
			},
			errContains: "DB_TLS_CA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := baseEnv()
			for key, value := range tt.values {
				env[key] = value
			}

			_, _, _, err := BuildDSN(testEnv(env))
			if err == nil {
				t.Fatal("expected TLS configuration error")
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("error %q must mention %s", err, tt.errContains)
			}
		})
	}
}

func TestBuildDSN_IPv6AddressWrapped(t *testing.T) {
	env := baseEnv()
	env["DB_HOST"] = "::1"
	gormDSN, _, _, err := BuildDSN(testEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// IPv6 host must be wrapped in [...]
	if !strings.Contains(gormDSN, "tcp([::1]:3306)") {
		t.Errorf("IPv6 host must be bracketed; got: %s", gormDSN)
	}
}

func TestBuildDSN_LocIsUTC(t *testing.T) {
	applicationDSN, migrationDSN, _, err := BuildDSN(testEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, dsn := range map[string]string{
		"application": applicationDSN,
		"migration":   migrationDSN,
	} {
		if config := mustParseMySQLDSN(t, dsn); config.Loc != time.UTC {
			t.Errorf("%s DSN must decode timestamps in UTC; got location %s", name, config.Loc)
		}
	}
}

func TestBuildDSN_SessionTimeZoneIsUTC(t *testing.T) {
	gormDSN, migrateDSN, _, err := BuildDSN(testEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, dsn := range map[string]string{
		"application": gormDSN,
		"migration":   migrateDSN,
	} {
		if !strings.Contains(dsn, "time_zone=%27%2B00%3A00%27") {
			t.Errorf("%s DSN must set the MySQL session time_zone to UTC; got: %s", name, dsn)
		}
	}
}

func TestBuildDSN_EmptyHostErrors(t *testing.T) {
	env := baseEnv()
	env["DB_HOST"] = ""
	_, _, _, err := BuildDSN(testEnv(env))
	if err == nil {
		t.Fatal("expected error for empty DB_HOST")
	}
}

func TestBuildDSN_InvalidConfigurationErrors(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		value       string
		errContains string
	}{
		{name: "empty user", key: "DB_USER", value: "", errContains: "DB_USER"},
		{name: "empty password", key: "DB_PASSWORD", value: "", errContains: "DB_PASSWORD"},
		{name: "empty database", key: "DB_NAME", value: "", errContains: "DB_NAME"},
		{name: "non numeric port", key: "DB_PORT", value: "mysql", errContains: "DB_PORT"},
		{name: "port out of range", key: "DB_PORT", value: "65536", errContains: "DB_PORT"},
		{name: "invalid connect timeout", key: "DB_CONNECT_TIMEOUT", value: "soon", errContains: "DB_CONNECT_TIMEOUT"},
		{name: "zero read timeout", key: "DB_READ_TIMEOUT", value: "0s", errContains: "DB_READ_TIMEOUT"},
		{name: "negative write timeout", key: "DB_WRITE_TIMEOUT", value: "-1s", errContains: "DB_WRITE_TIMEOUT"},
		{name: "invalid max open", key: "DB_MAX_OPEN_CONNS", value: "many", errContains: "DB_MAX_OPEN_CONNS"},
		{name: "zero max open", key: "DB_MAX_OPEN_CONNS", value: "0", errContains: "DB_MAX_OPEN_CONNS"},
		{name: "negative max idle", key: "DB_MAX_IDLE_CONNS", value: "-1", errContains: "DB_MAX_IDLE_CONNS"},
		{name: "invalid max lifetime", key: "DB_CONN_MAX_LIFETIME", value: "later", errContains: "DB_CONN_MAX_LIFETIME"},
		{name: "zero max idle time", key: "DB_CONN_MAX_IDLE_TIME", value: "0", errContains: "DB_CONN_MAX_IDLE_TIME"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := baseEnv()
			env[tt.key] = tt.value

			_, _, _, err := BuildDSN(testEnv(env))
			if err == nil {
				t.Fatalf("expected an error for %s=%q", tt.key, tt.value)
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("error %q must name %s", err, tt.errContains)
			}
		})
	}
}

func TestBuildDSN_DefaultPortWhenUnset(t *testing.T) {
	env := baseEnv()
	delete(env, "DB_PORT")
	gormDSN, _, _, err := BuildDSN(testEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gormDSN, "tcp(127.0.0.1:3306)") {
		t.Errorf("gormDSN must default to port 3306; got: %s", gormDSN)
	}
}

func TestBuildDSN_PreservesPasswordWithSpecialCharacters(t *testing.T) {
	env := baseEnv()
	env["DB_PASSWORD"] = "p@ss/w:ord"
	gormDSN, migrateDSN, _, err := BuildDSN(testEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, dsn := range map[string]string{
		"application": gormDSN,
		"migration":   migrateDSN,
	} {
		if password := mustParseMySQLDSN(t, dsn).Passwd; password != env["DB_PASSWORD"] {
			t.Errorf("%s DSN password changed: got %q, want %q", name, password, env["DB_PASSWORD"])
		}
	}
}

func TestBuildDSN_MaxIdleExceedsMaxOpenErrors(t *testing.T) {
	env := baseEnv()
	env["DB_MAX_OPEN_CONNS"] = "5"
	env["DB_MAX_IDLE_CONNS"] = "10"
	_, _, _, err := BuildDSN(testEnv(env))
	if err == nil {
		t.Fatal("expected error when maxIdle > maxOpen")
	}
}
