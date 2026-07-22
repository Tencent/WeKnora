package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/database/sqlitemigrations"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	migrateiofs "github.com/golang-migrate/migrate/v4/source/iofs"
)

var (
	migrationStateMu        sync.RWMutex
	currentMigrationVersion uint
	currentMigrationDirty   bool
	migrationVersionSet     bool
	currentMigrationError   string
)

// CachedMigrationVersion returns the migration version captured at startup.
// Returns (version, dirty, ok). ok is false if the version was never captured.
//
// Note: when migrations fail mid-way, the cache may still be populated via a
// best-effort m.Version() call inside RunMigrationsWithOptions so the system
// info endpoint can surface the partial state. Check CachedMigrationError() to
// distinguish a clean version reading from a recorded-after-failure one.
func CachedMigrationVersion() (uint, bool, bool) {
	migrationStateMu.RLock()
	defer migrationStateMu.RUnlock()
	return currentMigrationVersion, currentMigrationDirty, migrationVersionSet
}

// CachedMigrationError returns the error message captured when the most recent
// migration attempt failed at startup. Empty string means migrations either
// succeeded or were never run.
func CachedMigrationError() string {
	migrationStateMu.RLock()
	defer migrationStateMu.RUnlock()
	return currentMigrationError
}

// setMigrationState records the latest known migration state. Unlike the old
// sync.Once-based setter, this is intentionally idempotent-overwrite so the
// failure path (which runs after Up() errored) can replace the pre-migration
// snapshot taken from the initial m.Version() call.
func setMigrationState(version uint, dirty bool, errMsg string, versionKnown bool) {
	migrationStateMu.Lock()
	defer migrationStateMu.Unlock()
	if versionKnown {
		currentMigrationVersion = version
		currentMigrationDirty = dirty
		migrationVersionSet = true
	}
	currentMigrationError = errMsg
}

// captureMigrationFailure best-effort queries m for the current version so the
// system info endpoint can show "N (failed)" instead of vanishing the row, and
// stores the human-readable error message. Always returns the original error.
func captureMigrationFailure(m *migrate.Migrate, err error) error {
	versionKnown := false
	var ver uint
	var dirty bool
	if m != nil {
		v, d, vErr := m.Version()
		if vErr == nil {
			versionKnown = true
			ver, dirty = v, d
		}
	}
	setMigrationState(ver, dirty, err.Error(), versionKnown)
	return err
}

// RunMigrations executes all pending database migrations
// This should be called during application startup
func RunMigrations(dsn string) error {
	return RunMigrationsWithOptions(dsn, MigrationOptions{AutoRecoverDirty: false})
}

// MigrationOptions configures migration behavior
type MigrationOptions struct {
	// AutoRecoverDirty when true, automatically attempts to recover from dirty state
	// by forcing to the previous version and retrying the migration
	AutoRecoverDirty bool

	// SQLiteDBPath is the raw filesystem path to the SQLite database file.
	// When set, the migrator opens the DB directly via sql.Open instead of
	// parsing a URL-based DSN, which avoids breakage when the path contains
	// spaces (e.g. macOS "Application Support").
	SQLiteDBPath string

	// SQLiteMigrationsPath overrides the directory containing SQLite migration
	// files. When empty, the runner checks executable-relative release paths
	// before falling back to the current working directory.
	SQLiteMigrationsPath string
}

func sqliteMigrationDirectoryCandidates(executablePath string, workingDirectory string) []string {
	candidates := make([]string, 0, 5)
	seen := make(map[string]struct{}, 5)
	appendCandidate := func(candidate string) {
		if candidate == "" {
			return
		}
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}
	appendExecutableCandidates := func(path string) {
		if path == "" {
			return
		}
		executableDirectory := filepath.Dir(path)
		appendCandidate(filepath.Join(executableDirectory, "migrations", "sqlite"))
		appendCandidate(filepath.Join(
			executableDirectory,
			"..",
			"Resources",
			"migrations",
			"sqlite",
		))
	}

	if executablePath != "" {
		if resolvedExecutablePath, err := filepath.EvalSymlinks(executablePath); err == nil {
			appendExecutableCandidates(resolvedExecutablePath)
		}
		appendExecutableCandidates(executablePath)
	}
	if workingDirectory != "" {
		appendCandidate(filepath.Join(workingDirectory, "migrations", "sqlite"))
	}
	return candidates
}

func validateSQLiteMigrationsDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("sqlite migrations directory is empty")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve sqlite migrations directory %q: %w", path, err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return "", fmt.Errorf("stat sqlite migrations directory %q: %w", absolutePath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("sqlite migrations path %q is not a directory", absolutePath)
	}
	if _, err := sqlitemigrations.ValidateDirectory(
		absolutePath,
		sqlitemigrations.RequiredVersion,
	); err != nil {
		return "", fmt.Errorf(
			"validate sqlite migrations directory %q: %w",
			absolutePath,
			err,
		)
	}
	return filepath.Clean(absolutePath), nil
}

func resolveSQLiteMigrationsDirectory(
	explicitPath string,
	executablePath string,
	workingDirectory string,
) (string, error) {
	if strings.TrimSpace(explicitPath) != "" {
		return validateSQLiteMigrationsDirectory(explicitPath)
	}

	candidates := sqliteMigrationDirectoryCandidates(executablePath, workingDirectory)
	checked := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		checked = append(checked, candidate)
		_, statErr := os.Stat(candidate)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return "", fmt.Errorf(
				"stat sqlite migrations candidate %q: %w",
				candidate,
				statErr,
			)
		}
		path, err := validateSQLiteMigrationsDirectory(candidate)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf(
		"sqlite migrations directory not found; checked: %s",
		strings.Join(checked, ", "),
	)
}

func defaultSQLiteMigrationsDirectory(explicitPath string) (string, error) {
	executablePath, executableErr := os.Executable()
	if executableErr != nil {
		executablePath = ""
	}
	workingDirectory, workingDirectoryErr := os.Getwd()
	if workingDirectoryErr != nil {
		workingDirectory = ""
	}
	path, err := resolveSQLiteMigrationsDirectory(
		explicitPath,
		executablePath,
		workingDirectory,
	)
	if err != nil {
		return "", errors.Join(executableErr, workingDirectoryErr, err)
	}
	return path, nil
}

// RunMigrationsWithOptions executes all pending database migrations with custom options
func RunMigrationsWithOptions(dsn string, opts MigrationOptions) (resultErr error) {
	ctx := context.Background()

	logger.Infof(ctx, "Starting database migration...")

	var m *migrate.Migrate
	if strings.HasPrefix(dsn, "sqlite3://") {
		migrationsDirectory, err := defaultSQLiteMigrationsDirectory(opts.SQLiteMigrationsPath)
		if err != nil {
			wrapped := fmt.Errorf("failed to locate sqlite migrations: %w", err)
			logger.Errorf(ctx, "%v", wrapped)
			setMigrationState(0, false, wrapped.Error(), false)
			return wrapped
		}
		sourceDriver, err := migrateiofs.New(os.DirFS(migrationsDirectory), ".")
		if err != nil {
			wrapped := fmt.Errorf(
				"failed to open sqlite migrations from %s: %w",
				migrationsDirectory,
				err,
			)
			logger.Errorf(ctx, "%v", wrapped)
			setMigrationState(0, false, wrapped.Error(), false)
			return wrapped
		}
		logger.Infof(ctx, "Using SQLite migrations from %s", migrationsDirectory)

		if opts.SQLiteDBPath == "" {
			m, err = migrate.NewWithSourceInstance("iofs", sourceDriver, dsn)
			if err != nil {
				_ = sourceDriver.Close()
				logger.Errorf(ctx, "Failed to create migrate instance: %v", err)
				wrapped := fmt.Errorf("failed to create migrate instance: %w", err)
				setMigrationState(0, false, wrapped.Error(), false)
				return wrapped
			}
		} else {
			sqlDB, err := sql.Open("sqlite3", opts.SQLiteDBPath)
			if err != nil {
				_ = sourceDriver.Close()
				logger.Errorf(ctx, "Failed to open sqlite db for migration: %v", err)
				wrapped := fmt.Errorf("failed to open sqlite db for migration: %w", err)
				setMigrationState(0, false, wrapped.Error(), false)
				return wrapped
			}
			driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
			if err != nil {
				_ = sourceDriver.Close()
				_ = sqlDB.Close()
				logger.Errorf(ctx, "Failed to create sqlite3 migrate driver: %v", err)
				wrapped := fmt.Errorf("failed to create sqlite3 migrate driver: %w", err)
				setMigrationState(0, false, wrapped.Error(), false)
				return wrapped
			}
			m, err = migrate.NewWithInstance("iofs", sourceDriver, "sqlite3", driver)
			if err != nil {
				_ = sourceDriver.Close()
				_ = driver.Close()
				logger.Errorf(ctx, "Failed to create migrate instance: %v", err)
				wrapped := fmt.Errorf("failed to create migrate instance: %w", err)
				setMigrationState(0, false, wrapped.Error(), false)
				return wrapped
			}
		}
	} else {
		var err error
		m, err = newValidatedPostgresMigrator(
			postgresMigrationsDirectory,
			dsn,
			migrate.New,
		)
		if err != nil {
			logger.Errorf(ctx, "Failed to create migrate instance: %v", err)
			wrapped := fmt.Errorf("failed to create migrate instance: %w", err)
			setMigrationState(0, false, wrapped.Error(), false)
			return wrapped
		}
	}
	migrationClosed := false
	defer func() {
		if migrationClosed {
			return
		}
		sourceErr, databaseErr := m.Close()
		if closeErr := errors.Join(sourceErr, databaseErr); closeErr != nil {
			wrapped := fmt.Errorf("close migration resources: %w", closeErr)
			if resultErr != nil {
				resultErr = errors.Join(resultErr, wrapped)
				return
			}
			resultErr = wrapped
			setMigrationState(0, false, resultErr.Error(), false)
		}
	}()

	// Check current version and dirty state before migration
	oldVersion, oldDirty, versionErr := m.Version()
	if versionErr != nil && versionErr != migrate.ErrNilVersion {
		logger.Errorf(ctx, "Failed to get migration version: %v", versionErr)
		return captureMigrationFailure(m, fmt.Errorf("failed to get migration version: %w", versionErr))
	}

	if versionErr == migrate.ErrNilVersion {
		// A fresh database may have no history, but this is not a successful
		// state: Up and the final formal-version check are still mandatory.
		logger.Infof(ctx, "Database has no migration history, will start from version 0")
	} else {
		logger.Infof(ctx, "Current migration version: %d, dirty: %v", oldVersion, oldDirty)
	}

	// If database is in dirty state, try to recover or return error
	if oldDirty {
		logger.Warnf(ctx, "Database is in dirty state at version %d", oldVersion)
		if opts.AutoRecoverDirty {
			logger.Infof(ctx, "AutoRecoverDirty is enabled, attempting recovery...")
			recoveredVersion, err := recoverDirtyMigrationWithRemediation(ctx, m, oldVersion)
			if err != nil {
				return captureMigrationFailure(m, err)
			}
			oldVersion = recoveredVersion
		} else {
			return captureMigrationFailure(
				m,
				withDirtyMigrationRemediation(
					oldVersion,
					fmt.Errorf("database is in dirty state at version %d", oldVersion),
				),
			)
		}
	}

	// Run all pending migrations
	logger.Infof(ctx, "Running pending migrations...")
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		logger.Errorf(ctx, "Migration failed: %v", err)
		// Check if error is due to dirty state (in case it became dirty during migration)
		currentVersion, currentDirty, versionCheckErr := m.Version()
		if versionCheckErr == nil && currentDirty {
			logger.Warnf(ctx, "Migration caused dirty state at version %d", currentVersion)
			if opts.AutoRecoverDirty {
				logger.Infof(ctx, "Attempting to recover from dirty state...")
				// Try to recover and retry
				if _, recoverErr := recoverDirtyMigrationWithRemediation(
					ctx,
					m,
					currentVersion,
				); recoverErr != nil {
					return captureMigrationFailure(m, recoverErr)
				}
				// Retry migration after recovery
				logger.Infof(ctx, "Retrying migration after recovery...")
				if retryErr := m.Up(); retryErr != nil && retryErr != migrate.ErrNoChange {
					logger.Errorf(ctx, "Migration failed after recovery attempt: %v", retryErr)
					return captureMigrationFailure(m, fmt.Errorf("migration failed after recovery attempt: %w", retryErr))
				}
			} else {
				return captureMigrationFailure(
					m,
					withDirtyMigrationRemediation(
						currentVersion,
						fmt.Errorf(
							"migration failed and database is now dirty at version %d: %w",
							currentVersion,
							err,
						),
					),
				)
			}
		} else {
			return captureMigrationFailure(m, fmt.Errorf("failed to run migrations: %w", err))
		}
	}

	version, dirty, err := m.Version()
	if finalErr := validateFinalMigrationState(version, dirty, err); finalErr != nil {
		return captureMigrationFailure(m, finalErr)
	}

	sourceErr, databaseErr := m.Close()
	migrationClosed = true
	if closeErr := errors.Join(sourceErr, databaseErr); closeErr != nil {
		wrapped := fmt.Errorf("close migration resources: %w", closeErr)
		setMigrationState(version, dirty, wrapped.Error(), true)
		return wrapped
	}

	setMigrationState(version, dirty, "", true)

	if oldVersion != version {
		logger.Infof(ctx, "Database migrated from version %d to %d", oldVersion, version)
	} else {
		logger.Infof(ctx, "Database is up to date (version: %d)", version)
	}

	return nil
}

func validateFinalMigrationState(version uint, dirty bool, err error) error {
	if errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("migration completed without a recorded version: %w", err)
	}
	if err != nil {
		return fmt.Errorf("failed to get final migration version: %w", err)
	}
	if dirty {
		return fmt.Errorf("database remains in dirty state at version %d", version)
	}
	return nil
}

func dirtyMigrationRemediation(dirtyVersion uint) string {
	if dirtyVersion == 0 {
		return "Manual remediation for dirty migration version 0:\n" +
			"To re-run version 0, inspect and clean up or roll back all partially applied objects, " +
			"restore the migration state to no-version with golang-migrate Force(-1) or the project equivalent, " +
			"then restart so version 0 runs from the beginning.\n" +
			"Only if you have manually verified that version 0 is fully applied, use Force(0) to mark version 0 complete, " +
			"then restart for later migrations. Force(0) skips re-running version 0."
	}

	previousVersion := dirtyVersion - 1
	return fmt.Sprintf(
		"Manual remediation for dirty migration version %d: inspect and clean up or roll back partial results, "+
			"restore the previous successful version %d with Force(%d) or the project equivalent, "+
			"then restart so version %d is re-run.",
		dirtyVersion,
		previousVersion,
		previousVersion,
		dirtyVersion,
	)
}

func withDirtyMigrationRemediation(dirtyVersion uint, err error) error {
	return fmt.Errorf("%w\n%s", err, dirtyMigrationRemediation(dirtyVersion))
}

type dirtyMigrationRecovery interface {
	Force(version int) error
	Version() (version uint, dirty bool, err error)
}

// recoverFromDirtyState forces the previous migration version and verifies the
// exact intermediate state before the caller retries pending migrations.
func recoverFromDirtyState(
	ctx context.Context,
	m dirtyMigrationRecovery,
	dirtyVersion uint,
) (uint, error) {
	// Special case: if dirty at version 0 (init migration), we cannot go back further
	// The only option is to force to no version and retry from version 0.
	if dirtyVersion == 0 {
		logger.Warnf(ctx, "Database is in dirty state at version 0 (init migration). "+
			"This is the initial migration, cannot rollback further. "+
			"Will attempt to clear dirty flag and retry. "+
			"Note: This only works if the init migration uses IF NOT EXISTS clauses.")

		// Force to version -1 (no version) to allow re-running version 0
		// This effectively tells migrate that no migrations have been applied
		if err := m.Force(-1); err != nil {
			return 0, fmt.Errorf(
				"failed to force dirty migration version 0 to no-version: %w",
				err,
			)
		}

		recoveredVersion, recoveredDirty, recoveredErr := m.Version()
		if !errors.Is(recoveredErr, migrate.ErrNilVersion) {
			if recoveredErr != nil {
				return 0, fmt.Errorf(
					"read migration state after forcing dirty version 0 to no version: %w",
					recoveredErr,
				)
			}
			if recoveredDirty {
				return 0, fmt.Errorf(
					"database remains dirty at version %d after forcing dirty version 0 to no version",
					recoveredVersion,
				)
			}
			return 0, fmt.Errorf(
				"unexpected migration version %d after forcing dirty version 0 to no version",
				recoveredVersion,
			)
		}

		logger.Infof(ctx, "Cleared migration state, will retry from version 0")
		return 0, nil
	}

	forceVersion := int(dirtyVersion) - 1

	logger.Warnf(ctx, "Database is in dirty state at version %d, attempting auto-recovery by forcing to version %d",
		dirtyVersion, forceVersion)

	// Force to previous version to clear dirty state
	if err := m.Force(forceVersion); err != nil {
		return 0, fmt.Errorf("failed to force migration version during recovery: %w", err)
	}

	recoveredVersion, recoveredDirty, recoveredErr := m.Version()
	if recoveredErr != nil {
		return 0, fmt.Errorf(
			"read migration state after forcing dirty version %d to version %d: %w",
			dirtyVersion,
			forceVersion,
			recoveredErr,
		)
	}
	if recoveredDirty {
		return 0, fmt.Errorf(
			"database remains dirty at version %d after forcing dirty version %d to version %d",
			recoveredVersion,
			dirtyVersion,
			forceVersion,
		)
	}
	if recoveredVersion != uint(forceVersion) {
		return 0, fmt.Errorf(
			"unexpected migration version %d after forcing dirty version %d to version %d",
			recoveredVersion,
			dirtyVersion,
			forceVersion,
		)
	}

	logger.Infof(ctx, "Successfully forced migration to version %d, migration will be retried", forceVersion)
	return recoveredVersion, nil
}

func recoverDirtyMigrationWithRemediation(
	ctx context.Context,
	m dirtyMigrationRecovery,
	dirtyVersion uint,
) (uint, error) {
	recoveredVersion, err := recoverFromDirtyState(ctx, m, dirtyVersion)
	if err != nil {
		return 0, withDirtyMigrationRemediation(dirtyVersion, err)
	}
	return recoveredVersion, nil
}

// GetMigrationVersion returns the current migration version
func GetMigrationVersion() (uint, bool, error) {
	dbURL := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"),
	)

	m, err := newValidatedPostgresMigrator(
		postgresMigrationsDirectory,
		dbURL,
		migrate.New,
	)
	if err != nil {
		return 0, false, fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer m.Close()

	version, dirty, err := m.Version()
	if err != nil {
		return 0, false, err
	}

	return version, dirty, nil
}
