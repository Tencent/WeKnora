package database

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// MySQLDSNs builds the application and golang-migrate connection strings from
// the same settings. Only the migration connection enables multiple statements.
// migrate decodes credentials once more than go-sql-driver, so its credentials
// must be escaped independently of the application's native driver DSN.
func MySQLDSNs(lookup func(string) string) (application, migration string, err error) {
	cfg := mysql.NewConfig()
	for key, dest := range map[string]*string{
		"DB_HOST": &cfg.Addr, "DB_USER": &cfg.User, "DB_NAME": &cfg.DBName,
	} {
		*dest = strings.TrimSpace(lookup(key))
		if *dest == "" {
			return "", "", fmt.Errorf("%s is required for DB_DRIVER=mysql", key)
		}
	}
	port := 3306
	if raw := strings.TrimSpace(lookup("DB_PORT")); raw != "" {
		port, err = strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return "", "", fmt.Errorf("DB_PORT must be an integer between 1 and 65535")
		}
	}
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(strings.Trim(cfg.Addr, "[]"), strconv.Itoa(port))
	cfg.Passwd = lookup("DB_PASSWORD")
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.ClientFoundRows = true
	cfg.Params = map[string]string{"charset": "utf8mb4", "time_zone": "'+00:00'"}
	for _, item := range []struct {
		key      string
		fallback time.Duration
		dest     *time.Duration
	}{
		{"DB_CONNECT_TIMEOUT", 5 * time.Second, &cfg.Timeout},
		{"DB_READ_TIMEOUT", 30 * time.Second, &cfg.ReadTimeout},
		{"DB_WRITE_TIMEOUT", 30 * time.Second, &cfg.WriteTimeout},
	} {
		*item.dest = item.fallback
		if raw := strings.TrimSpace(lookup(item.key)); raw != "" {
			*item.dest, err = time.ParseDuration(raw)
			if err != nil || *item.dest <= 0 {
				return "", "", fmt.Errorf("%s must be a positive duration, such as 5s", item.key)
			}
		}
	}
	if raw := strings.TrimSpace(lookup("DB_MYSQL_TLS")); raw != "" {
		enabled, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return "", "", fmt.Errorf("DB_MYSQL_TLS must be true or false")
		}
		if enabled {
			cfg.TLSConfig = "true"
		}
	}
	application = cfg.FormatDSN()
	cfg.User = url.QueryEscape(cfg.User)
	cfg.Passwd = url.QueryEscape(cfg.Passwd)
	cfg.MultiStatements = true
	return application, "mysql://" + cfg.FormatDSN(), nil
}

// ValidateMySQLRetrievalDrivers rejects engines that reuse the application's
// database connection. An empty list is valid: stores may be configured through
// the management API and loaded from vector_stores at runtime.
func ValidateMySQLRetrievalDrivers(raw string) error {
	for _, part := range strings.Split(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "postgres", "sqlite":
			return fmt.Errorf(
				"DB_DRIVER=mysql cannot reuse its connection for RETRIEVE_DRIVER=%s; "+
					"configure an external vector store",
				strings.TrimSpace(part))
		}
	}
	return nil
}
