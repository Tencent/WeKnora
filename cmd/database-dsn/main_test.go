package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/database"
)

func TestMigrationDSNUsesSharedMySQLConfig(t *testing.T) {
	values := map[string]string{"DB_DRIVER": "mysql", "DB_HOST": "::1", "DB_USER": "user+name", "DB_PASSWORD": "a@b:'$()#%+", "DB_NAME": "test"}
	lookup := func(k string) string { return values[k] }
	_, want, err := database.MySQLDSNs(lookup)
	if err != nil {
		t.Fatal(err)
	}
	got, err := migrationDSN(lookup)
	if err != nil || got != want {
		t.Fatalf("shared MySQL configuration mismatch: %v", err)
	}
}

func TestMigrationDSNPostgresCredentialRoundTrip(t *testing.T) {
	password := "a@b:'$()#%+ space"
	values := map[string]string{"DB_PASSWORD": password, "DB_HOST": "::1"}
	got, err := migrationDSN(func(k string) string { return values[k] })
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := u.User.Password()
	if actual != password || u.Host != "[::1]:5432" || u.Query().Get("sslmode") != "disable" {
		t.Fatal("Postgres settings did not round trip")
	}
}

func TestMigrationDSNInvalidConfigDoesNotExposePassword(t *testing.T) {
	values := map[string]string{"DB_DRIVER": "mysql", "DB_PASSWORD": "secret-never-print", "DB_PORT": "invalid"}
	_, err := migrationDSN(func(k string) string { return values[k] })
	if err == nil || strings.Contains(err.Error(), values["DB_PASSWORD"]) {
		t.Fatal("invalid settings must fail without credentials")
	}
}
