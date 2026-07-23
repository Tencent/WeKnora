package container

import (
	"strings"
	"testing"
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

func TestBuildMySQLDSN_BasicShape(t *testing.T) {
	gormDSN, migrateDSN, pool, err := buildMySQLDSN(testEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"weknora", "secret", "tcp(127.0.0.1:3306)", "weknora_db", "charset=utf8mb4", "parseTime=true"} {
		if !strings.Contains(gormDSN, want) {
			t.Errorf("gormDSN missing %q; got: %s", want, gormDSN)
		}
	}
	if !strings.HasPrefix(migrateDSN, "mysql://") {
		t.Errorf("migrateDSN must start with mysql://; got: %s", migrateDSN)
	}
	if !strings.Contains(migrateDSN, "multiStatements=true") {
		t.Errorf("migrateDSN must include multiStatements=true; got: %s", migrateDSN)
	}
	if pool.MaxOpenConns != 50 || pool.MaxIdleConns != 10 {
		t.Errorf("pool defaults wrong: %+v", pool)
	}
}

func TestBuildMySQLDSN_CollationIsPinned(t *testing.T) {
	gormDSN, migrateDSN, _, err := buildMySQLDSN(testEnv(baseEnv()))
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

func TestBuildMySQLDSN_IPv6AddressWrapped(t *testing.T) {
	env := baseEnv()
	env["DB_HOST"] = "::1"
	gormDSN, _, _, err := buildMySQLDSN(testEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// IPv6 host must be wrapped in [...]
	if !strings.Contains(gormDSN, "tcp([::1]:3306)") {
		t.Errorf("IPv6 host must be bracketed; got: %s", gormDSN)
	}
}

func TestBuildMySQLDSN_LocIsUTC(t *testing.T) {
	_, migrateDSN, _, err := buildMySQLDSN(testEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// migrateDSN explicitly sets loc=UTC in its query params.
	if !strings.Contains(migrateDSN, "loc=UTC") {
		t.Errorf("migrateDSN must include loc=UTC; got: %s", migrateDSN)
	}
	// gormDSN sets cfg.Loc = time.UTC directly; go-sql-driver omits the
	// loc param from the DSN string when Loc is time.UTC (it's the default).
	// The important check is that cfg.Loc is not nil (which would render
	// as loc=Local). We verify this indirectly by confirming no loc=Local.
	gormDSN, _, _, _ := buildMySQLDSN(testEnv(baseEnv()))
	if strings.Contains(gormDSN, "loc=Local") {
		t.Errorf("gormDSN must not default to loc=Local; got: %s", gormDSN)
	}
}

func TestBuildMySQLDSN_EmptyHostErrors(t *testing.T) {
	env := baseEnv()
	env["DB_HOST"] = ""
	_, _, _, err := buildMySQLDSN(testEnv(env))
	if err == nil {
		t.Fatal("expected error for empty DB_HOST")
	}
}

func TestBuildMySQLDSN_DefaultPortWhenUnset(t *testing.T) {
	env := baseEnv()
	delete(env, "DB_PORT")
	gormDSN, _, _, err := buildMySQLDSN(testEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gormDSN, "tcp(127.0.0.1:3306)") {
		t.Errorf("gormDSN must default to port 3306; got: %s", gormDSN)
	}
}

func TestBuildMySQLDSN_PasswordWithSpecialCharsIsURLEncodedInMigrateDSN(t *testing.T) {
	env := baseEnv()
	env["DB_PASSWORD"] = "p@ss/w:ord"
	gormDSN, migrateDSN, _, err := buildMySQLDSN(testEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gormDSN, "p@ss/w:ord") {
		t.Errorf("gormDSN should carry raw password; got: %s", gormDSN)
	}
	if strings.Contains(migrateDSN, "p@ss/w:ord") {
		t.Errorf("migrateDSN must URL-encode password; got: %s", migrateDSN)
	}
	if !strings.Contains(migrateDSN, "p%40ss%2Fw%3Aord") {
		t.Errorf("migrateDSN must contain URL-encoded password; got: %s", migrateDSN)
	}
}

func TestBuildMySQLDSN_MaxIdleExceedsMaxOpenErrors(t *testing.T) {
	env := baseEnv()
	env["DB_MAX_OPEN_CONNS"] = "5"
	env["DB_MAX_IDLE_CONNS"] = "10"
	_, _, _, err := buildMySQLDSN(testEnv(env))
	if err == nil {
		t.Fatal("expected error when maxIdle > maxOpen")
	}
}
