package container

import (
	"fmt"
	"strconv"
	"strings"
)

// CheckMySQLVersion enforces the MySQL 8.0.16+ hard requirement.
//
// WeKnora's MySQL mode uses CHECK constraints (enforced from 8.0.16),
// JSON expression defaults (8.0.13+), SKIP LOCKED (8.0.1+),
// JSON_LENGTH, and the utf8mb4_0900_ai_ci collation. The binding
// minimum is 8.0.16 because CHECK constraints are silently ignored
// before that version. MySQL 8.4.x and 9.x are also accepted.
//
// versionString is the value returned by `SELECT VERSION()` on a real
// MySQL server. Vanilla MySQL returns "8.0.36", "8.4.0", "9.0.1";
// distro builds append a suffix ("-0ubuntu0.20.04.1"); MariaDB returns
// "10.11.8-MariaDB" and is NOT supported.
//
// Returns nil for 8.0.16+, 8.4.x, 9.x; an error otherwise.
func CheckMySQLVersion(versionString string) error {
	v := strings.TrimSpace(versionString)
	if v == "" {
		return fmt.Errorf("MySQL version is empty; could not read SELECT VERSION(). WeKnora requires MySQL 8.0.16+")
	}

	// MariaDB advertises a high major version (10+) but is not binary-
	// compatible with MySQL 8. Reject it explicitly.
	if strings.Contains(strings.ToLower(v), "mariadb") {
		return fmt.Errorf("MariaDB is not supported (got %q); WeKnora requires MySQL 8.0.16+ — MariaDB's JSON, SKIP LOCKED, CHECK, and utf8mb4_0900_ai_ci semantics differ from MySQL 8", v)
	}

	// Strip any distro suffix: keep only the leading numeric version
	// ("8.0.36-0ubuntu0.20.04.1" -> "8.0.36").
	numericPrefix := v
	if dash := strings.IndexAny(v, "-+ "); dash >= 0 {
		numericPrefix = v[:dash]
	}

	parts := strings.Split(numericPrefix, ".")
	if len(parts) < 2 {
		return fmt.Errorf("could not parse MySQL version from %q: expected major.minor[.patch] (e.g. \"8.0.36\"); WeKnora requires MySQL 8.0.16+", v)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("could not parse MySQL major version from %q: %w; WeKnora requires MySQL 8.0.16+", v, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("could not parse MySQL minor version from %q: %w; WeKnora requires MySQL 8.0.16+", v, err)
	}
	patch := 0
	if len(parts) >= 3 {
		// Strip trailing non-digit suffix (e.g. "16rc1" → "16", "34-log" → "34")
		numericRun := parts[2]
		for i, r := range numericRun {
			if r < '0' || r > '9' {
				numericRun = numericRun[:i]
				break
			}
		}
		if numericRun != "" {
			patch, _ = strconv.Atoi(numericRun)
		}
	}

	// Accept MySQL 8.0.16+, 8.4.x, 9.x.
	if major >= 9 {
		return nil
	}
	if major == 8 && minor >= 4 {
		return nil
	}
	if major == 8 && minor == 0 && patch >= 16 {
		return nil
	}

	return fmt.Errorf("MySQL %d.%d.%d is not supported; WeKnora requires MySQL 8.0.16+ (CHECK constraints are enforced from 8.0.16; earlier 8.0.x silently ignores them). 8.4.x and 9.x are also accepted",
		major, minor, patch)
}
