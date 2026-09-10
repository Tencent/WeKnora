package database_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var durableTaskTableNames = []string{"task_dead_letters", "task_pending_ops"}

var durableTaskIndexNames = []string{
	"idx_task_dead_letters_scope",
	"idx_task_dead_letters_task_type",
	"idx_task_dead_letters_tenant",
	"idx_task_pending_ops_scope",
	"idx_task_pending_ops_tenant",
}

func TestSQLiteMigrationsCreateDurableTaskTables(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	t.Chdir(repositoryRoot)

	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	require.NoError(t, runSQLiteMigrations(dbPath))

	db := openSQLiteTestDB(t, dbPath)
	version, dirty := sqliteMigrationState(t, db)
	t.Logf("fresh migration state: version=%d dirty=%t", version, dirty)
	assert.Equal(t, 2, version)
	assert.False(t, dirty)

	tables := sqliteDurableTaskTables(t, db)
	t.Logf("fresh sqlite durable-task tables: %v", tables)
	assert.Equal(t, durableTaskTableNames, tables)
	indexes := sqliteDurableTaskIndexes(t, db)
	t.Logf("fresh sqlite durable-task indexes: %v", indexes)
	assert.Equal(t, durableTaskIndexNames, indexes)

	pendingErr, deadLetterErr := exerciseTaskRepositories(db)
	t.Logf("TaskPendingOpsRepository.Enqueue error: %v", pendingErr)
	t.Logf("TaskDeadLetterRepository.Insert error: %v", deadLetterErr)
	assert.NoError(t, pendingErr)
	assert.NoError(t, deadLetterErr)
}

func TestSQLiteMigrationsUpgradeV1CreatesDurableTaskTablesWithoutReplayingInit(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	legacyMigrationRoot := copySQLiteMigrations(
		t,
		repositoryRoot,
		"000000_init.down.sql",
		"000000_init.up.sql",
		"000001_remove_wiki_log.down.sql",
		"000001_remove_wiki_log.up.sql",
	)
	t.Chdir(legacyMigrationRoot)

	dbPath := filepath.Join(t.TempDir(), "upgrade.db")
	require.NoError(t, runSQLiteMigrations(dbPath))

	db := openSQLiteTestDB(t, dbPath)
	versionBefore, dirtyBefore := sqliteMigrationState(t, db)
	tablesBefore := sqliteDurableTaskTables(t, db)
	require.Equal(t, 1, versionBefore)
	require.False(t, dirtyBefore)
	require.Empty(t, tablesBefore)
	require.NoError(t, db.Exec(
		"INSERT INTO tenants (id, name, business) VALUES (?, ?, ?)",
		4242,
		"upgrade-sentinel",
		"migration-test",
	).Error)

	upgradeMigrationRoot := copySQLiteMigrations(t, repositoryRoot)
	initPath := filepath.Join(upgradeMigrationRoot, "migrations", "sqlite", "000000_init.up.sql")
	initSQL, err := os.ReadFile(initPath)
	require.NoError(t, err)
	initSQL = append(initSQL, []byte("\nCREATE TABLE sqlite_init_edit_probe (id INTEGER PRIMARY KEY);\n")...)
	require.NoError(t, os.WriteFile(initPath, initSQL, 0o600))

	t.Chdir(upgradeMigrationRoot)
	require.NoError(t, runSQLiteMigrations(dbPath))

	versionAfter, dirtyAfter := sqliteMigrationState(t, db)
	tablesAfter := sqliteDurableTaskTables(t, db)
	indexesAfter := sqliteDurableTaskIndexes(t, db)
	var probeCount int64
	require.NoError(t, db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_init_edit_probe'",
	).Scan(&probeCount).Error)
	var sentinelName string
	require.NoError(t, db.Raw(
		"SELECT name FROM tenants WHERE id = ?",
		4242,
	).Scan(&sentinelName).Error)

	t.Logf(
		"SQLite v1->v2 upgrade: version=%d->%d dirty=%t->%t tables=%v->%v indexes=%v probe_count=%d sentinel=%q",
		versionBefore,
		versionAfter,
		dirtyBefore,
		dirtyAfter,
		tablesBefore,
		tablesAfter,
		indexesAfter,
		probeCount,
		sentinelName,
	)
	assert.Equal(t, 2, versionAfter)
	assert.False(t, dirtyAfter)
	assert.Equal(t, durableTaskTableNames, tablesAfter)
	assert.Equal(t, durableTaskIndexNames, indexesAfter)
	assert.Zero(t, probeCount, "an edited version-0 migration must not replay after version 1 is recorded")
	assert.Equal(t, "upgrade-sentinel", sentinelName)

	pendingErr, deadLetterErr := exerciseTaskRepositories(db)
	t.Logf("upgrade TaskPendingOpsRepository.Enqueue error: %v", pendingErr)
	t.Logf("upgrade TaskDeadLetterRepository.Insert error: %v", deadLetterErr)
	assert.NoError(t, pendingErr)
	assert.NoError(t, deadLetterErr)
}

func TestSQLiteMigrationsV2DownAndUpPreserveExistingData(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	t.Chdir(repositoryRoot)

	dbPath := filepath.Join(t.TempDir(), "down-up.db")
	require.NoError(t, runSQLiteMigrations(dbPath))
	db := openSQLiteTestDB(t, dbPath)
	require.NoError(t, db.Exec(
		"INSERT INTO tenants (id, name, business) VALUES (?, ?, ?)",
		4242,
		"down-up-sentinel",
		"migration-test",
	).Error)

	stepSQLiteMigrations(t, repositoryRoot, dbPath, -1)
	versionAfterDown, dirtyAfterDown := sqliteMigrationState(t, db)
	assert.Equal(t, 1, versionAfterDown)
	assert.False(t, dirtyAfterDown)
	assert.Empty(t, sqliteDurableTaskTables(t, db))
	assert.Empty(t, sqliteDurableTaskIndexes(t, db))
	assert.Equal(t, "down-up-sentinel", sqliteTenantName(t, db, 4242))

	stepSQLiteMigrations(t, repositoryRoot, dbPath, 1)
	versionAfterUp, dirtyAfterUp := sqliteMigrationState(t, db)
	assert.Equal(t, 2, versionAfterUp)
	assert.False(t, dirtyAfterUp)
	assert.Equal(t, durableTaskTableNames, sqliteDurableTaskTables(t, db))
	assert.Equal(t, durableTaskIndexNames, sqliteDurableTaskIndexes(t, db))
	assert.Equal(t, "down-up-sentinel", sqliteTenantName(t, db, 4242))

	pendingErr, deadLetterErr := exerciseTaskRepositories(db)
	assert.NoError(t, pendingErr)
	assert.NoError(t, deadLetterErr)
	t.Logf(
		"SQLite migration down/up state: down=%d/%t up=%d/%t, unrelated tenant preserved",
		versionAfterDown,
		dirtyAfterDown,
		versionAfterUp,
		dirtyAfterUp,
	)
}

func TestSQLiteMigrationsSupportTaskRepositoryCRUD(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	t.Chdir(repositoryRoot)

	dbPath := filepath.Join(t.TempDir(), "repository-crud.db")
	require.NoError(t, runSQLiteMigrations(dbPath))
	db := openSQLiteTestDB(t, dbPath)
	ctx := context.Background()

	pendingRepo := repository.NewTaskPendingOpsRepository(db)
	firstPending := &types.TaskPendingOp{
		TenantID: 1,
		TaskType: types.TypeWikiIngest,
		Scope:    types.TaskScopeKnowledgeBase,
		ScopeID:  "kb-repository-crud",
		Op:       "ingest",
		DedupKey: "knowledge-1",
		Payload:  json.RawMessage(`{"knowledge_id":"knowledge-1"}`),
	}
	secondPending := &types.TaskPendingOp{
		TenantID: 1,
		TaskType: types.TypeWikiIngest,
		Scope:    types.TaskScopeKnowledgeBase,
		ScopeID:  "kb-repository-crud",
		Op:       "retract",
		DedupKey: "knowledge-2",
		Payload:  json.RawMessage(`{"knowledge_id":"knowledge-2"}`),
	}
	require.NoError(t, pendingRepo.Enqueue(ctx, firstPending))
	require.NoError(t, pendingRepo.Enqueue(ctx, secondPending))
	require.Positive(t, firstPending.ID)
	require.Positive(t, secondPending.ID)

	pendingCount, err := pendingRepo.PendingCount(
		ctx,
		types.TypeWikiIngest,
		types.TaskScopeKnowledgeBase,
		"kb-repository-crud",
	)
	require.NoError(t, err)
	assert.Equal(t, int64(2), pendingCount)
	peeked, err := pendingRepo.PeekBatch(
		ctx,
		types.TypeWikiIngest,
		types.TaskScopeKnowledgeBase,
		"kb-repository-crud",
		1,
	)
	require.NoError(t, err)
	require.Len(t, peeked, 1)
	assert.Equal(t, firstPending.ID, peeked[0].ID)

	claimed, err := pendingRepo.ClaimBatch(
		ctx,
		types.TypeWikiIngest,
		types.TaskScopeKnowledgeBase,
		"kb-repository-crud",
		1,
		time.Now().Add(-time.Minute),
	)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, firstPending.ID, claimed[0].ID)
	assert.NotNil(t, claimed[0].ClaimedAt)
	failCount, err := pendingRepo.IncrFailCount(ctx, firstPending.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, failCount)
	require.NoError(t, pendingRepo.ReleaseByIDs(ctx, []int64{firstPending.ID}))

	reclaimed, err := pendingRepo.ClaimBatch(
		ctx,
		types.TypeWikiIngest,
		types.TaskScopeKnowledgeBase,
		"kb-repository-crud",
		1,
		time.Now().Add(-time.Minute),
	)
	require.NoError(t, err)
	require.Len(t, reclaimed, 1)
	assert.Equal(t, firstPending.ID, reclaimed[0].ID)
	assert.Equal(t, 1, reclaimed[0].FailCount)
	require.NoError(t, pendingRepo.DeleteByIDs(ctx, []int64{firstPending.ID, secondPending.ID}))
	pendingCount, err = pendingRepo.PendingCount(
		ctx,
		types.TypeWikiIngest,
		types.TaskScopeKnowledgeBase,
		"kb-repository-crud",
	)
	require.NoError(t, err)
	assert.Zero(t, pendingCount)

	deadLetterRepo := repository.NewTaskDeadLetterRepository(db)
	deadLetters := []*types.TaskDeadLetter{
		{
			TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
			ScopeID: "kb-repository-crud", RelatedID: "knowledge-1",
			Payload: json.RawMessage(`{"knowledge_id":"knowledge-1"}`), LastError: "first failure", FailCount: 3,
		},
		{
			TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
			ScopeID: "kb-repository-crud", RelatedID: "knowledge-2",
			Payload: json.RawMessage(`{"knowledge_id":"knowledge-2"}`), LastError: "second failure", FailCount: 4,
		},
		{
			TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
			ScopeID: "kb-repository-crud", RelatedID: "knowledge-3",
			Payload: json.RawMessage(`{"knowledge_id":"knowledge-3"}`), LastError: "third failure", FailCount: 5,
		},
	}
	for _, deadLetter := range deadLetters {
		require.NoError(t, deadLetterRepo.Insert(ctx, deadLetter))
		require.Positive(t, deadLetter.ID)
	}

	firstPage, cursor, err := deadLetterRepo.ListByScope(
		ctx,
		types.TaskScopeKnowledgeBase,
		"kb-repository-crud",
		"",
		2,
	)
	require.NoError(t, err)
	require.Len(t, firstPage, 2)
	require.NotEmpty(t, cursor)
	assert.Greater(t, firstPage[0].ID, firstPage[1].ID)
	secondPage, nextCursor, err := deadLetterRepo.ListByScope(
		ctx,
		types.TaskScopeKnowledgeBase,
		"kb-repository-crud",
		cursor,
		2,
	)
	require.NoError(t, err)
	require.Len(t, secondPage, 1)
	assert.Empty(t, nextCursor)
	assert.Less(t, secondPage[0].ID, firstPage[1].ID)

	byTaskType, _, err := deadLetterRepo.ListByTaskType(ctx, types.TypeWikiIngest, "", 10)
	require.NoError(t, err)
	require.Len(t, byTaskType, 3)
	require.NoError(t, deadLetterRepo.DeleteByID(ctx, firstPage[0].ID))
	remainingDeadLetters, _, err := deadLetterRepo.ListByScope(
		ctx,
		types.TaskScopeKnowledgeBase,
		"kb-repository-crud",
		"",
		10,
	)
	require.NoError(t, err)
	assert.Len(t, remainingDeadLetters, 2)
}

func runSQLiteMigrations(dbPath string) error {
	return database.RunMigrationsWithOptions(
		"sqlite3://"+dbPath,
		database.MigrationOptions{SQLiteDBPath: dbPath},
	)
}

func openSQLiteTestDB(t *testing.T, dbPath string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func stepSQLiteMigrations(t *testing.T, repositoryRoot, dbPath string, steps int) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
	require.NoError(t, err)
	migrator, err := migrate.NewWithDatabaseInstance(
		"file://"+filepath.Join(repositoryRoot, "migrations", "sqlite"),
		"sqlite3",
		driver,
	)
	require.NoError(t, err)

	migrationErr := migrator.Steps(steps)
	sourceCloseErr, databaseCloseErr := migrator.Close()
	require.NoError(t, migrationErr)
	require.NoError(t, sourceCloseErr)
	require.NoError(t, databaseCloseErr)
}

func sqliteMigrationState(t *testing.T, db *gorm.DB) (int, bool) {
	t.Helper()
	var state struct {
		Version int
		Dirty   bool
	}
	require.NoError(t, db.Raw(
		"SELECT version, dirty FROM schema_migrations LIMIT 1",
	).Scan(&state).Error)
	return state.Version, state.Dirty
}

func sqliteTenantName(t *testing.T, db *gorm.DB, tenantID int64) string {
	t.Helper()
	var name string
	require.NoError(t, db.Raw(
		"SELECT name FROM tenants WHERE id = ?",
		tenantID,
	).Scan(&name).Error)
	return name
}

func sqliteDurableTaskTables(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var tables []string
	require.NoError(t, db.Raw(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name IN (?, ?) ORDER BY name",
		durableTaskTableNames[0],
		durableTaskTableNames[1],
	).Scan(&tables).Error)
	return tables
}

func sqliteDurableTaskIndexes(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var indexes []string
	require.NoError(t, db.Raw(
		`SELECT name FROM sqlite_master
		 WHERE type = 'index'
		   AND name IN (?, ?, ?, ?, ?)
		 ORDER BY name`,
		durableTaskIndexNames[0],
		durableTaskIndexNames[1],
		durableTaskIndexNames[2],
		durableTaskIndexNames[3],
		durableTaskIndexNames[4],
	).Scan(&indexes).Error)
	return indexes
}

func exerciseTaskRepositories(db *gorm.DB) (error, error) {
	ctx := context.Background()
	pendingErr := repository.NewTaskPendingOpsRepository(db).Enqueue(ctx, &types.TaskPendingOp{
		TenantID: 1,
		TaskType: types.TypeWikiIngest,
		Scope:    types.TaskScopeKnowledgeBase,
		ScopeID:  "kb-migration-probe",
		Op:       "ingest",
		DedupKey: "knowledge-migration-probe",
		Payload:  json.RawMessage(`{}`),
	})
	deadLetterErr := repository.NewTaskDeadLetterRepository(db).Insert(ctx, &types.TaskDeadLetter{
		TenantID:  1,
		TaskType:  types.TypeWikiIngest,
		Scope:     types.TaskScopeKnowledgeBase,
		ScopeID:   "kb-migration-probe",
		RelatedID: "knowledge-migration-probe",
		Payload:   json.RawMessage(`{}`),
		LastError: "migration probe",
		FailCount: 1,
	})
	return pendingErr, deadLetterErr
}

func copySQLiteMigrations(t *testing.T, repositoryRoot string, names ...string) string {
	t.Helper()
	temporaryRoot := t.TempDir()
	sourceDir := filepath.Join(repositoryRoot, "migrations", "sqlite")
	targetDir := filepath.Join(temporaryRoot, "migrations", "sqlite")
	require.NoError(t, os.MkdirAll(targetDir, 0o700))
	includedNames := make(map[string]struct{}, len(names))
	for _, name := range names {
		includedNames[name] = struct{}{}
	}

	entries, err := os.ReadDir(sourceDir)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if len(includedNames) > 0 {
			if _, ok := includedNames[entry.Name()]; !ok {
				continue
			}
		}
		contents, readErr := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		require.NoError(t, readErr)
		require.NoError(t, os.WriteFile(filepath.Join(targetDir, entry.Name()), contents, 0o600))
	}
	return temporaryRoot
}

func testRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}
