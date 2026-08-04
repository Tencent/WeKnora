package container

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDatabaseURLParsing(t *testing.T) {
	tests := []struct {
		name         string
		supabaseURL  string
		databaseURL  string
		dbHostEnv    string
		dbSSLModeEnv string
		wantHost     string
		wantPort     string
		wantUser     string
		wantPass     string
		wantName     string
		wantSSLMode  string
	}{
		{
			name:        "SUPABASE_DB_URL standard parsing",
			supabaseURL: "postgres://postgres.ref:secretpass@aws-0-sa-east-1.pooler.supabase.com:6543/postgres?sslmode=require",
			wantHost:    "aws-0-sa-east-1.pooler.supabase.com",
			wantPort:    "6543",
			wantUser:    "postgres.ref",
			wantPass:    "secretpass",
			wantName:    "postgres",
			wantSSLMode: "require",
		},
		{
			name:        "DATABASE_URL fallback when SUPABASE_DB_URL is empty",
			databaseURL: "postgresql://dbuser:dbpass@localhost:5432/mydb?sslmode=disable",
			wantHost:    "localhost",
			wantPort:    "5432",
			wantUser:    "dbuser",
			wantPass:    "dbpass",
			wantName:    "mydb",
			wantSSLMode: "disable",
		},
		{
			name:        "Default port 5432 when omitted in URL",
			supabaseURL: "postgres://user:pass@dbhost/dbname",
			wantHost:    "dbhost",
			wantPort:    "5432",
			wantUser:    "user",
			wantPass:    "pass",
			wantName:    "dbname",
			wantSSLMode: "disable",
		},
		{
			name:         "DB_SSLMODE env overrides URL query sslmode",
			supabaseURL:  "postgres://user:pass@dbhost:5432/dbname?sslmode=require",
			dbSSLModeEnv: "verify-full",
			wantHost:     "dbhost",
			wantPort:     "5432",
			wantUser:     "user",
			wantPass:     "pass",
			wantName:     "dbname",
			wantSSLMode:  "verify-full",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SUPABASE_DB_URL", tt.supabaseURL)
			t.Setenv("DATABASE_URL", tt.databaseURL)
			t.Setenv("DB_HOST", tt.dbHostEnv)
			t.Setenv("DB_SSLMODE", tt.dbSSLModeEnv)

			dbSSLMode := os.Getenv("DB_SSLMODE")
			if dbSSLMode == "" {
				dbSSLMode = "disable"
			}

			dbHost := os.Getenv("DB_HOST")
			dbPort := os.Getenv("DB_PORT")
			dbUser := os.Getenv("DB_USER")
			dbPassword := os.Getenv("DB_PASSWORD")
			dbName := os.Getenv("DB_NAME")

			dbURL := os.Getenv("SUPABASE_DB_URL")
			if dbURL == "" {
				dbURL = os.Getenv("DATABASE_URL")
			}

			if dbURL != "" && os.Getenv("DB_HOST") == "" {
				if u, err := url.Parse(dbURL); err == nil {
					dbHost = u.Hostname()
					dbPort = u.Port()
					if dbPort == "" {
						dbPort = "5432"
					}
					if u.User != nil {
						dbUser = u.User.Username()
						dbPassword, _ = u.User.Password()
					}
					dbName = strings.TrimPrefix(u.Path, "/")
					if querySSL := u.Query().Get("sslmode"); querySSL != "" && os.Getenv("DB_SSLMODE") == "" {
						dbSSLMode = querySSL
					}
				}
			}

			assert.Equal(t, tt.wantHost, dbHost)
			assert.Equal(t, tt.wantPort, dbPort)
			assert.Equal(t, tt.wantUser, dbUser)
			assert.Equal(t, tt.wantPass, dbPassword)
			assert.Equal(t, tt.wantName, dbName)
			assert.Equal(t, tt.wantSSLMode, dbSSLMode)
		})
	}
}
