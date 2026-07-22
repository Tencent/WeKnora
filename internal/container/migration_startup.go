package container

import (
	"fmt"
	"strconv"
	"strings"
)

func parseAutoRecoverDirty(value string, present bool) (bool, error) {
	if !present || strings.TrimSpace(value) == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("invalid AUTO_RECOVER_DIRTY value %q: %w", value, err)
	}
	return parsed, nil
}

func runStartupMigrations(
	autoMigrateValue string,
	autoRecoverValue string,
	autoRecoverPresent bool,
	driver string,
	run func(bool) error,
	postMigration func(),
) (bool, error) {
	if autoMigrateValue == "false" {
		return false, nil
	}

	autoRecover, err := parseAutoRecoverDirty(autoRecoverValue, autoRecoverPresent)
	if err != nil {
		return false, err
	}
	if err := run(autoRecover); err != nil {
		return false, fmt.Errorf("%s database migration failed: %w", driver, err)
	}
	if postMigration != nil {
		postMigration()
	}
	return true, nil
}
