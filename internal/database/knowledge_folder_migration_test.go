package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/database/sqlitemigrations"
	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	migrateiofs "github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func knowledgeFolderMigrationRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func readKnowledgeFolderMigration(t *testing.T, dialect string, name string) string {
	t.Helper()
	root := knowledgeFolderMigrationRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "migrations", dialect, name))
	require.NoError(t, err)
	return string(content)
}

func newSQLiteKnowledgeFolderMigrator(
	t *testing.T,
	dbPath string,
	migrationsDirectory string,
) *migrate.Migrate {
	t.Helper()
	sourceDriver, err := migrateiofs.New(os.DirFS(migrationsDirectory), ".")
	require.NoError(t, err)

	sqlDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		_ = sourceDriver.Close()
		require.NoError(t, err)
	}

	driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
	if err != nil {
		_ = sourceDriver.Close()
		_ = sqlDB.Close()
		require.NoError(t, err)
	}
	migrator, err := migrate.NewWithInstance(
		"iofs",
		sourceDriver,
		"sqlite3",
		driver,
	)
	if err != nil {
		_ = sourceDriver.Close()
		_ = driver.Close()
		require.NoError(t, err)
	}
	return migrator
}

func closeSQLiteKnowledgeFolderMigrator(t *testing.T, migrator *migrate.Migrate) {
	t.Helper()
	sourceErr, databaseErr := migrator.Close()
	require.NoError(t, sourceErr)
	require.NoError(t, databaseErr)
}

func assertSQLiteKnowledgeFolderMigrationVersion(
	t *testing.T,
	db *sql.DB,
	expectedVersion int64,
) {
	t.Helper()
	var version int64
	var dirty bool
	err := db.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).
		Scan(&version, &dirty)
	require.NoError(t, err)
	assert.Equal(t, expectedVersion, version)
	assert.False(t, dirty)
}

func assertSQLiteKnowledgeFolderDefaults(t *testing.T, db *sql.DB) {
	t.Helper()
	var folderID string
	var folderVersion uint64
	var folderIndexedVersion uint64
	err := db.QueryRow(`
		SELECT folder_id, folder_version, folder_indexed_version
		FROM knowledges
		WHERE id = 'existing-knowledge'
	`).Scan(&folderID, &folderVersion, &folderIndexedVersion)
	require.NoError(t, err)
	assert.Equal(t, "", folderID)
	assert.Equal(t, uint64(1), folderVersion)
	assert.Equal(t, uint64(0), folderIndexedVersion)
}

func copyCurrentSQLiteMigrations(t *testing.T, directory string) {
	t.Helper()
	copySQLiteMigrations(
		t,
		filepath.Join(knowledgeFolderMigrationRoot(t), "migrations", "sqlite"),
		directory,
	)
}

func copySQLiteMigrations(t *testing.T, sourceDirectory string, targetDirectory string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(targetDirectory, 0o755))
	entries, err := os.ReadDir(sourceDirectory)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(sourceDirectory, entry.Name()))
		require.NoError(t, readErr)
		require.NoError(t, os.WriteFile(
			filepath.Join(targetDirectory, entry.Name()),
			content,
			0o644,
		))
	}
}

func TestResolveSQLiteMigrationsDirectory(t *testing.T) {
	t.Run("executable relative takes precedence", func(t *testing.T) {
		root := t.TempDir()
		executableDirectory := filepath.Join(root, "Program Files", "WeKnora")
		require.NoError(t, os.MkdirAll(executableDirectory, 0o755))
		executablePath := filepath.Join(executableDirectory, "WeKnora Lite.exe")
		require.NoError(t, os.WriteFile(executablePath, nil, 0o755))
		expected := filepath.Join(executableDirectory, "migrations", "sqlite")
		copyCurrentSQLiteMigrations(t, expected)

		workingDirectory := filepath.Join(root, "unrelated")
		copyCurrentSQLiteMigrations(
			t,
			filepath.Join(workingDirectory, "migrations", "sqlite"),
		)
		actual, err := resolveSQLiteMigrationsDirectory(
			"",
			executablePath,
			workingDirectory,
		)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(expected), actual)
	})

	t.Run("mac resources", func(t *testing.T) {
		root := t.TempDir()
		executablePath := filepath.Join(
			root,
			"WeKnora Lite.app",
			"Contents",
			"MacOS",
			"WeKnora Lite",
		)
		require.NoError(t, os.MkdirAll(filepath.Dir(executablePath), 0o755))
		require.NoError(t, os.WriteFile(executablePath, nil, 0o755))
		expected := filepath.Join(
			root,
			"WeKnora Lite.app",
			"Contents",
			"Resources",
			"migrations",
			"sqlite",
		)
		copyCurrentSQLiteMigrations(t, expected)

		actual, err := resolveSQLiteMigrationsDirectory("", executablePath, "")
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(expected), actual)
	})

	t.Run("working directory fallback", func(t *testing.T) {
		root := t.TempDir()
		executablePath := filepath.Join(root, "bin", "weknora")
		require.NoError(t, os.MkdirAll(filepath.Dir(executablePath), 0o755))
		require.NoError(t, os.WriteFile(executablePath, nil, 0o755))
		workingDirectory := filepath.Join(root, "workspace")
		expected := filepath.Join(workingDirectory, "migrations", "sqlite")
		copyCurrentSQLiteMigrations(t, expected)

		actual, err := resolveSQLiteMigrationsDirectory(
			"",
			executablePath,
			workingDirectory,
		)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(expected), actual)
	})

	t.Run("incomplete executable directory does not fall back", func(t *testing.T) {
		root := t.TempDir()
		executableDirectory := filepath.Join(root, "bundle")
		executablePath := filepath.Join(executableDirectory, "weknora")
		require.NoError(t, os.MkdirAll(
			filepath.Join(executableDirectory, "migrations", "sqlite"),
			0o755,
		))
		require.NoError(t, os.WriteFile(executablePath, nil, 0o755))
		workingDirectory := filepath.Join(root, "workspace")
		copyCurrentSQLiteMigrations(
			t,
			filepath.Join(workingDirectory, "migrations", "sqlite"),
		)

		_, err := resolveSQLiteMigrationsDirectory(
			"",
			executablePath,
			workingDirectory,
		)
		require.Error(t, err)
	})

	for _, tt := range []struct {
		name   string
		damage func(*testing.T, string)
	}{
		{
			name: "version gap does not fall back",
			damage: func(t *testing.T, directory string) {
				require.NoError(t, os.Remove(filepath.Join(
					directory,
					"000001_knowledge_folders.up.sql",
				)))
				require.NoError(t, os.Remove(filepath.Join(
					directory,
					"000001_knowledge_folders.down.sql",
				)))
				require.NoError(t, os.WriteFile(
					filepath.Join(directory, "000002_future.up.sql"),
					[]byte("-- up"),
					0o644,
				))
				require.NoError(t, os.WriteFile(
					filepath.Join(directory, "000002_future.down.sql"),
					[]byte("-- down"),
					0o644,
				))
			},
		},
		{
			name: "empty migration does not fall back",
			damage: func(t *testing.T, directory string) {
				require.NoError(t, os.WriteFile(
					filepath.Join(directory, "000001_knowledge_folders.up.sql"),
					nil,
					0o644,
				))
			},
		},
		{
			name: "mismatched names do not fall back",
			damage: func(t *testing.T, directory string) {
				require.NoError(t, os.Rename(
					filepath.Join(directory, "000001_knowledge_folders.down.sql"),
					filepath.Join(directory, "000001_other.down.sql"),
				))
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			executablePath := filepath.Join(root, "bundle", "weknora")
			require.NoError(t, os.MkdirAll(filepath.Dir(executablePath), 0o755))
			require.NoError(t, os.WriteFile(executablePath, nil, 0o755))
			highPriority := filepath.Join(filepath.Dir(executablePath), "migrations", "sqlite")
			copyCurrentSQLiteMigrations(t, highPriority)
			tt.damage(t, highPriority)

			workingDirectory := filepath.Join(root, "workspace")
			copyCurrentSQLiteMigrations(
				t,
				filepath.Join(workingDirectory, "migrations", "sqlite"),
			)
			_, err := resolveSQLiteMigrationsDirectory(
				"",
				executablePath,
				workingDirectory,
			)
			require.Error(t, err)
		})
	}

	t.Run("explicit path is strict", func(t *testing.T) {
		root := t.TempDir()
		explicitPath := filepath.Join(root, "configured", "migrations", "sqlite")
		copyCurrentSQLiteMigrations(t, explicitPath)
		actual, err := resolveSQLiteMigrationsDirectory(
			explicitPath,
			filepath.Join(root, "missing-executable"),
			filepath.Join(root, "missing-working-directory"),
		)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(explicitPath), actual)

		_, err = resolveSQLiteMigrationsDirectory(
			filepath.Join(root, "missing-explicit"),
			"",
			"",
		)
		require.Error(t, err)

		require.NoError(t, os.WriteFile(
			filepath.Join(explicitPath, "000001_knowledge_folders.up.sql"),
			nil,
			0o644,
		))
		workingDirectory := filepath.Join(root, "working")
		copyCurrentSQLiteMigrations(
			t,
			filepath.Join(workingDirectory, "migrations", "sqlite"),
		)
		_, err = resolveSQLiteMigrationsDirectory(
			explicitPath,
			"",
			workingDirectory,
		)
		require.Error(t, err)
	})

	t.Run("missing directory", func(t *testing.T) {
		root := t.TempDir()
		_, err := resolveSQLiteMigrationsDirectory(
			"",
			filepath.Join(root, "bin", "weknora"),
			filepath.Join(root, "working"),
		)
		require.Error(t, err)
	})

	t.Run("symlink executable uses resolved location", func(t *testing.T) {
		root := t.TempDir()
		realExecutable := filepath.Join(root, "real", "WeKnora Lite")
		require.NoError(t, os.MkdirAll(filepath.Dir(realExecutable), 0o755))
		require.NoError(t, os.WriteFile(realExecutable, nil, 0o755))
		expected := filepath.Join(filepath.Dir(realExecutable), "migrations", "sqlite")
		copyCurrentSQLiteMigrations(t, expected)

		symlink := filepath.Join(root, "link", "WeKnora Lite")
		require.NoError(t, os.MkdirAll(filepath.Dir(symlink), 0o755))
		if err := os.Symlink(realExecutable, symlink); err != nil {
			t.Skipf("symlink is unavailable: %v", err)
		}

		actual, err := resolveSQLiteMigrationsDirectory("", symlink, "")
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(expected), actual)
	})
}

func TestSQLiteKnowledgeFolderMigration_RunnerRejectsIncompleteSource(t *testing.T) {
	migrationsDirectory := filepath.Join(t.TempDir(), "migrations", "sqlite")
	copyCurrentSQLiteMigrations(t, migrationsDirectory)
	inventory, err := sqlitemigrations.ValidateDirectory(
		migrationsDirectory,
		sqlitemigrations.RequiredVersion,
	)
	require.NoError(t, err)
	missing := inventory.Files[len(inventory.Files)-1]
	require.NoError(t, os.Remove(filepath.Join(migrationsDirectory, missing)))

	dbPath := filepath.Join(t.TempDir(), "incomplete-source.db")
	err = RunMigrationsWithOptions("sqlite3://"+dbPath, MigrationOptions{
		SQLiteDBPath:         dbPath,
		SQLiteMigrationsPath: migrationsDirectory,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version "+missing[:6])
}

func TestSQLiteKnowledgeFolderMigration_RunnerRejectsDirtyDatabase(t *testing.T) {
	sourceMigrationsDirectory := filepath.Join(
		knowledgeFolderMigrationRoot(t),
		"migrations",
		"sqlite",
	)
	migrationsDirectory := filepath.Join(t.TempDir(), "migrations", "sqlite")
	copySQLiteMigrations(t, sourceMigrationsDirectory, migrationsDirectory)

	dbPath := filepath.Join(t.TempDir(), "dirty.db")
	migrator := newSQLiteKnowledgeFolderMigrator(t, dbPath, migrationsDirectory)
	require.NoError(t, migrator.Steps(1))
	closeSQLiteKnowledgeFolderMigrator(t, migrator)

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE schema_migrations SET dirty = 1`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	err = RunMigrationsWithOptions("sqlite3://"+dbPath, MigrationOptions{
		AutoRecoverDirty:     false,
		SQLiteDBPath:         dbPath,
		SQLiteMigrationsPath: migrationsDirectory,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dirty state")
	assertDirtyVersionZeroRemediation(t, err.Error())
	version, dirty, ok := CachedMigrationVersion()
	assert.True(t, ok)
	assert.Equal(t, uint(0), version)
	assert.True(t, dirty)
	assert.NotEmpty(t, CachedMigrationError())

	db, err = sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()
	var databaseVersion int64
	var databaseDirty bool
	err = db.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).
		Scan(&databaseVersion, &databaseDirty)
	require.NoError(t, err)
	assert.Equal(t, int64(0), databaseVersion)
	assert.True(t, databaseDirty)
	var folderTableCount int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'knowledge_folders'
	`).Scan(&folderTableCount)
	require.NoError(t, err)
	assert.Zero(t, folderTableCount)
}

func TestSQLiteKnowledgeFolderMigration_RunnerUpFailureUsesVersionZeroRemediation(t *testing.T) {
	sourceMigrationsDirectory := filepath.Join(
		knowledgeFolderMigrationRoot(t),
		"migrations",
		"sqlite",
	)
	migrationsDirectory := filepath.Join(t.TempDir(), "migrations", "sqlite")
	copySQLiteMigrations(t, sourceMigrationsDirectory, migrationsDirectory)
	require.NoError(t, os.WriteFile(
		filepath.Join(migrationsDirectory, "000000_init.up.sql"),
		[]byte("INVALID MIGRATION SQL"),
		0o644,
	))

	dbPath := filepath.Join(t.TempDir(), "up-failure.db")
	err := RunMigrationsWithOptions("sqlite3://"+dbPath, MigrationOptions{
		AutoRecoverDirty:     false,
		SQLiteDBPath:         dbPath,
		SQLiteMigrationsPath: migrationsDirectory,
	})

	require.Error(t, err)
	assertDirtyVersionZeroRemediation(t, err.Error())
	db, openErr := sql.Open("sqlite3", dbPath)
	require.NoError(t, openErr)
	defer db.Close()
	var version int64
	var dirty bool
	queryErr := db.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).
		Scan(&version, &dirty)
	require.NoError(t, queryErr)
	assert.Equal(t, int64(0), version)
	assert.True(t, dirty)
}

func TestSQLiteKnowledgeFolderMigration_RunnerRecoversDirtyVersionZeroWhenExplicit(t *testing.T) {
	sourceMigrationsDirectory := filepath.Join(
		knowledgeFolderMigrationRoot(t),
		"migrations",
		"sqlite",
	)
	migrationsDirectory := filepath.Join(t.TempDir(), "migrations", "sqlite")
	copySQLiteMigrations(t, sourceMigrationsDirectory, migrationsDirectory)

	dbPath := filepath.Join(t.TempDir(), "explicit-recovery.db")
	migrator := newSQLiteKnowledgeFolderMigrator(t, dbPath, migrationsDirectory)
	require.NoError(t, migrator.Force(0))
	closeSQLiteKnowledgeFolderMigrator(t, migrator)

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE schema_migrations SET version = 0, dirty = 1`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	err = RunMigrationsWithOptions("sqlite3://"+dbPath, MigrationOptions{
		AutoRecoverDirty:     true,
		SQLiteDBPath:         dbPath,
		SQLiteMigrationsPath: migrationsDirectory,
	})
	require.NoError(t, err)

	db, err = sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()
	assertSQLiteKnowledgeFolderMigrationVersion(t, db, 2)
	var migratedTableCount int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table'
		  AND name IN (
		      'knowledges',
		      'knowledge_folders',
		      'knowledge_folder_index_pending'
		  )
	`).Scan(&migratedTableCount)
	require.NoError(t, err)
	assert.Equal(t, 3, migratedTableCount)
	assert.Empty(t, CachedMigrationError())
}

func TestSQLiteKnowledgeFolderMigration_UpgradeAndRollback(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "knowledge-folder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE knowledges (
			id VARCHAR(36) PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id VARCHAR(36) NOT NULL,
			deleted_at DATETIME
		);
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id)
		VALUES ('existing-knowledge', 1, 'kb-1');
	`)
	require.NoError(t, err)

	up := readKnowledgeFolderMigration(t, "sqlite", "000001_knowledge_folders.up.sql")
	_, err = db.Exec(up)
	require.NoError(t, err)

	expectedColumns := map[string]string{
		"folder_id":              "''",
		"folder_version":         "1",
		"folder_indexed_version": "0",
	}
	schemaRows, err := db.Query(`PRAGMA table_info(knowledges)`)
	require.NoError(t, err)
	foundColumns := make(map[string]bool, len(expectedColumns))
	for schemaRows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		require.NoError(t, schemaRows.Scan(
			&cid,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		))
		expectedDefault, ok := expectedColumns[name]
		if !ok {
			continue
		}
		foundColumns[name] = true
		assert.Equal(t, 1, notNull, name)
		assert.Equal(t, expectedDefault, defaultValue.String, name)
	}
	require.NoError(t, schemaRows.Close())
	require.NoError(t, schemaRows.Err())
	assert.Len(t, foundColumns, len(expectedColumns))

	var folderID string
	var folderVersion uint64
	var folderIndexedVersion uint64
	err = db.QueryRow(`
		SELECT folder_id, folder_version, folder_indexed_version
		FROM knowledges
		WHERE id = 'existing-knowledge'
	`).Scan(&folderID, &folderVersion, &folderIndexedVersion)
	require.NoError(t, err)
	assert.Equal(t, "", folderID)
	assert.Equal(t, uint64(1), folderVersion)
	assert.Equal(t, uint64(0), folderIndexedVersion)

	for _, tt := range []struct {
		id    string
		depth int
	}{
		{id: "invalid-depth-zero", depth: 0},
		{id: "invalid-depth-33", depth: 33},
	} {
		_, err = db.Exec(`
			INSERT INTO knowledge_folders
				(id, tenant_id, knowledge_base_id, parent_id, name, path, depth)
			VALUES
				(?, 1, 'kb-1', '', ?, ?, ?)
		`,
			tt.id,
			"Invalid depth",
			"/invalid-depth/",
			tt.depth,
		)
		require.Error(t, err)
	}

	for _, tt := range []struct {
		id   string
		name string
		path string
	}{
		{id: "empty-name", name: "", path: "/empty-name/"},
		{id: "empty-path", name: "Empty path", path: ""},
	} {
		_, err = db.Exec(`
			INSERT INTO knowledge_folders
				(id, tenant_id, knowledge_base_id, parent_id, name, path, depth)
			VALUES
				(?, 1, 'kb-1', '', ?, ?, 1)
		`, tt.id, tt.name, tt.path)
		require.Error(t, err)
	}

	_, err = db.Exec(`
		INSERT INTO knowledge_folders
			(id, tenant_id, knowledge_base_id, parent_id, name, path, depth)
		VALUES
			('folder-1', 1, 'kb-1', '', 'Reports', '/folder-1/', 1)
	`)
	require.NoError(t, err)
	_, err = db.Exec(`
		INSERT INTO knowledge_folders
			(id, tenant_id, knowledge_base_id, parent_id, name, path, depth)
		VALUES
			('folder-duplicate', 1, 'kb-1', '', 'Reports', '/folder-duplicate/', 1)
	`)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE knowledge_folders SET deleted_at = CURRENT_TIMESTAMP WHERE id = 'folder-1'`)
	require.NoError(t, err)
	_, err = db.Exec(`
		INSERT INTO knowledge_folders
			(id, tenant_id, knowledge_base_id, parent_id, name, path, depth)
		VALUES
			('folder-replacement', 1, 'kb-1', '', 'Reports', '/folder-replacement/', 1)
	`)
	require.NoError(t, err)

	for _, indexName := range []string{
		"idx_knowledge_folders_live_sibling_name",
		"idx_knowledge_folders_parent",
		"idx_knowledge_folders_path",
		"idx_knowledges_folder",
		"idx_knowledges_folder_index_pending",
	} {
		var count int
		err = db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`,
			indexName,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, indexName)
	}

	down := readKnowledgeFolderMigration(t, "sqlite", "000001_knowledge_folders.down.sql")
	_, err = db.Exec(down)
	require.NoError(t, err)

	var folderTableCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'knowledge_folders'`,
	).Scan(&folderTableCount)
	require.NoError(t, err)
	assert.Zero(t, folderTableCount)

	rows, err := db.Query(`PRAGMA table_info(knowledges)`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		require.NoError(t, rows.Scan(
			&cid,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		))
		assert.NotContains(t, []string{"folder_id", "folder_version", "folder_indexed_version"}, name)
	}
	require.NoError(t, rows.Err())
}

func TestSQLiteKnowledgeFolderMigration_RunnerUpgradeIdempotentAndRollback(t *testing.T) {
	sourceMigrationsDirectory := filepath.Join(
		knowledgeFolderMigrationRoot(t),
		"migrations",
		"sqlite",
	)
	migrationsDirectory := filepath.Join(
		t.TempDir(),
		"release path with spaces",
		"migrations",
		"sqlite",
	)
	copySQLiteMigrations(t, sourceMigrationsDirectory, migrationsDirectory)
	originalWorkingDirectory, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(t.TempDir()))
	defer func() {
		require.NoError(t, os.Chdir(originalWorkingDirectory))
	}()

	freshDBPath := filepath.Join(t.TempDir(), "knowledge-folder-fresh-runner.db")
	freshDSN := "sqlite3://" + freshDBPath
	freshOptions := MigrationOptions{
		AutoRecoverDirty:     false,
		SQLiteDBPath:         freshDBPath,
		SQLiteMigrationsPath: migrationsDirectory,
	}
	require.NoError(t, RunMigrationsWithOptions(freshDSN, freshOptions))

	freshDB, err := sql.Open("sqlite3", freshDBPath)
	require.NoError(t, err)
	assertSQLiteKnowledgeFolderMigrationVersion(t, freshDB, 2)
	var freshFolderTableCount int
	err = freshDB.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'knowledge_folders'
	`).Scan(&freshFolderTableCount)
	require.NoError(t, err)
	assert.Equal(t, 1, freshFolderTableCount)
	var freshPendingTableCount int
	err = freshDB.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'knowledge_folder_index_pending'
	`).Scan(&freshPendingTableCount)
	require.NoError(t, err)
	assert.Equal(t, 1, freshPendingTableCount)
	freshColumns, err := freshDB.Query(`
		SELECT folder_id, folder_version, folder_indexed_version
		FROM knowledges
		LIMIT 0
	`)
	require.NoError(t, err)
	require.NoError(t, freshColumns.Close())
	for _, indexName := range []string{
		"idx_knowledge_folders_live_sibling_name",
		"idx_knowledge_folders_parent",
		"idx_knowledge_folders_path",
		"idx_knowledges_folder",
		"idx_knowledges_folder_index_pending",
	} {
		var count int
		err = freshDB.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`,
			indexName,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, indexName)
	}
	require.NoError(t, freshDB.Close())

	dbPath := filepath.Join(t.TempDir(), "knowledge-folder-runner.db")
	migrator := newSQLiteKnowledgeFolderMigrator(t, dbPath, migrationsDirectory)
	require.NoError(t, migrator.Steps(1))
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	assert.Equal(t, uint(0), version)
	assert.False(t, dirty)
	closeSQLiteKnowledgeFolderMigrator(t, migrator)

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(`
		INSERT INTO knowledges (
			id,
			tenant_id,
			knowledge_base_id,
			type,
			title,
			source
		) VALUES (
			'existing-knowledge',
			1,
			'kb-1',
			'file',
			'Existing knowledge',
			'existing.txt'
		)
	`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	dsn := "sqlite3://" + dbPath
	options := MigrationOptions{
		AutoRecoverDirty:     false,
		SQLiteDBPath:         dbPath,
		SQLiteMigrationsPath: migrationsDirectory,
	}
	require.NoError(t, RunMigrationsWithOptions(dsn, options))

	db, err = sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	assertSQLiteKnowledgeFolderMigrationVersion(t, db, 2)
	assertSQLiteKnowledgeFolderDefaults(t, db)
	var folderTableCount int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'knowledge_folders'
	`).Scan(&folderTableCount)
	require.NoError(t, err)
	assert.Equal(t, 1, folderTableCount)
	require.NoError(t, db.Close())

	require.NoError(t, RunMigrationsWithOptions(dsn, options))
	db, err = sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	assertSQLiteKnowledgeFolderMigrationVersion(t, db, 2)
	assertSQLiteKnowledgeFolderDefaults(t, db)
	require.NoError(t, db.Close())

	migrator = newSQLiteKnowledgeFolderMigrator(t, dbPath, migrationsDirectory)
	require.NoError(t, migrator.Steps(-2))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	assert.Equal(t, uint(0), version)
	assert.False(t, dirty)
	closeSQLiteKnowledgeFolderMigrator(t, migrator)

	db, err = sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	assertSQLiteKnowledgeFolderMigrationVersion(t, db, 0)
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'knowledge_folders'
	`).Scan(&folderTableCount)
	require.NoError(t, err)
	assert.Zero(t, folderTableCount)
	var pendingTableCount int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'knowledge_folder_index_pending'
	`).Scan(&pendingTableCount)
	require.NoError(t, err)
	assert.Zero(t, pendingTableCount)

	rows, err := db.Query(`PRAGMA table_info(knowledges)`)
	require.NoError(t, err)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		require.NoError(t, rows.Scan(
			&cid,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		))
		assert.NotContains(
			t,
			[]string{"folder_id", "folder_version", "folder_indexed_version"},
			name,
		)
	}
	require.NoError(t, rows.Close())
	require.NoError(t, rows.Err())

	var knowledgeCount int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM knowledges WHERE id = 'existing-knowledge'
	`).Scan(&knowledgeCount)
	require.NoError(t, err)
	assert.Equal(t, 1, knowledgeCount)
	require.NoError(t, db.Close())

	require.NoError(t, RunMigrationsWithOptions(dsn, options))
	db, err = sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	assertSQLiteKnowledgeFolderMigrationVersion(t, db, 2)
	assertSQLiteKnowledgeFolderDefaults(t, db)
	require.NoError(t, db.Close())
}

func TestPostgresKnowledgeFolderMigrationContract(t *testing.T) {
	up := strings.ToLower(readKnowledgeFolderMigration(
		t,
		"versioned",
		"000071_knowledge_folders.up.sql",
	))
	down := strings.ToLower(readKnowledgeFolderMigration(
		t,
		"versioned",
		"000071_knowledge_folders.down.sql",
	))

	for _, fragment := range []string{
		"create table if not exists knowledge_folders",
		"check (name <> '')",
		"check (path <> '')",
		"check (depth between 1 and 32)",
		"tenant_id, knowledge_base_id, parent_id, name",
		"where deleted_at is null",
		"folder_id varchar(36) not null default ''",
		"folder_version bigint not null default 1",
		"folder_indexed_version bigint not null default 0",
		"folder_indexed_version < folder_version",
		"path varchar_pattern_ops",
	} {
		assert.Contains(t, up, fragment)
	}
	for _, fragment := range []string{
		"drop column if exists folder_indexed_version",
		"drop column if exists folder_version",
		"drop column if exists folder_id",
		"drop table if exists knowledge_folders",
	} {
		assert.Contains(t, down, fragment)
	}
}

func TestSQLiteMigrationReleaseContract(t *testing.T) {
	root := knowledgeFolderMigrationRoot(t)
	workflowContent, err := os.ReadFile(filepath.Join(
		root,
		".github",
		"workflows",
		"release-lite.yml",
	))
	require.NoError(t, err)
	workflow := string(workflowContent)
	assert.Contains(t, workflow, "go run ./cmd/sqlite-migration-inventory")
	assert.Contains(t, workflow, "sqlite-migrations.expected")
	assert.Contains(t, workflow, "sqlite-migrations.actual")
	assert.Contains(t, workflow, "sqlite-migrations.app")
	assert.Contains(t, workflow, "sqlite-migrations.dmg")
	assert.Contains(t, workflow, "sqlite-migrations.linux")
	assert.Contains(t, workflow, "sqlite-migrations.nsis")
	assert.NotContains(t, workflow, "000001_knowledge_folders.up.sql")
	assert.NotContains(t, workflow, `eval "$(`)

	installerContent, err := os.ReadFile(filepath.Join(
		root,
		"cmd",
		"desktop",
		"build",
		"windows",
		"installer",
		"project.nsi",
	))
	require.NoError(t, err)
	installer := string(installerContent)
	assert.Contains(t, installer, `SetOutPath "$INSTDIR\migrations\sqlite"`)
	assert.Contains(t, installer, `migrations\sqlite\*.*`)
}
