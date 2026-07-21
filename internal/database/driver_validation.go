package database

import (
	"fmt"
	"strings"
)

// ValidateDriverCombination keeps the business database and retrieval backend
// responsibilities separate.
func ValidateDriverCombination(dbDriver, retrieveDriver string) error {
	dbDriver = strings.ToLower(strings.TrimSpace(dbDriver))
	retrievers := ParseDriverList(retrieveDriver)

	for _, driver := range retrievers {
		if driver == "mysql" {
			return fmt.Errorf(
				"RETRIEVE_DRIVER=mysql is not supported: MySQL can only be used as the business primary database",
			)
		}
	}

	if dbDriver != "mysql" {
		return nil
	}
	if len(retrievers) == 0 {
		return fmt.Errorf(
			"RETRIEVE_DRIVER=qdrant is required when DB_DRIVER=mysql",
		)
	}
	if len(retrievers) != 1 || retrievers[0] != "qdrant" {
		return fmt.Errorf(
			"DB_DRIVER=mysql only supports RETRIEVE_DRIVER=qdrant; got %q",
			strings.Join(retrievers, ","),
		)
	}
	return nil
}

// ParseDriverList normalizes a comma-separated driver list and removes empty
// entries. Startup validation and registry construction use the same parser so
// mixed case and incidental whitespace cannot pass one layer but fail another.
func ParseDriverList(raw string) []string {
	parts := strings.Split(raw, ",")
	drivers := make([]string, 0, len(parts))
	for _, part := range parts {
		if driver := strings.ToLower(strings.TrimSpace(part)); driver != "" {
			drivers = append(drivers, driver)
		}
	}
	return drivers
}
