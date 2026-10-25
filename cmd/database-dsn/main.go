// database-dsn supplies the migration CLI with the application's connection rules.
// Its output contains credentials; consume it through command substitution.
package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/database"
)

func migrationDSN(lookup func(string) string) (string, error) {
	driver := strings.ToLower(strings.TrimSpace(lookup("DB_DRIVER")))
	if driver == "mysql" {
		_, migration, err := database.MySQLDSNs(lookup)
		return migration, err
	}
	if driver != "" && driver != "postgres" {
		return "", fmt.Errorf("database-dsn supports DB_DRIVER=postgres or mysql")
	}
	value := func(key, fallback string) string {
		if v := lookup(key); v != "" {
			return v
		}
		return fallback
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(value("DB_USER", "postgres"), value("DB_PASSWORD", "postgres")),
		Host:     net.JoinHostPort(strings.Trim(value("DB_HOST", "localhost"), "[]"), value("DB_PORT", "5432")),
		Path:     "/" + value("DB_NAME", "WeKnora"),
		RawQuery: "sslmode=disable",
	}
	return u.String(), nil
}

func main() {
	dsn, err := migrationDSN(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(dsn)
}
