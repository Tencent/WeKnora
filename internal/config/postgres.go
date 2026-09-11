package config

import (
	"fmt"
	"os"
)

// PostgresSSLMode returns the mode shared by pgx (GORM) and lib/pq (migrations).
// Only their common modes are accepted; lib/pq v1.10.9 rejects allow/prefer.
func PostgresSSLMode() (string, error) {
	mode := os.Getenv("DB_SSLMODE")
	if mode == "" {
		mode = "disable"
	}
	switch mode {
	case "disable", "require", "verify-ca", "verify-full":
		return mode, nil
	default:
		return "", fmt.Errorf("invalid DB_SSLMODE %q: expected disable, require, verify-ca, or verify-full", mode)
	}
}
