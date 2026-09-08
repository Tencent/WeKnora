package repository

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newModelUsageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.ModelUsage{}))
	return db
}

// newModelUsageFKTestDB creates both evaluation_runs and model_usage from the
// real SQLite migrations (so the foreign key and ON DELETE SET NULL are real)
// and enables foreign-key enforcement, which the migration runner does not.
func newModelUsageFKTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000&_foreign_keys=on"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	evalDDL, err := os.ReadFile("../../../migrations/sqlite/000013_evaluation_runs.up.sql")
	require.NoError(t, err)
	usageDDL, err := os.ReadFile("../../../migrations/sqlite/000014_model_usage.up.sql")
	require.NoError(t, err)
	pricingDDL, err := os.ReadFile("../../../migrations/sqlite/000015_model_pricing.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(evalDDL)).Error)
	require.NoError(t, db.Exec(string(usageDDL)).Error)
	require.NoError(t, db.Exec(string(pricingDDL)).Error)
	return db
}

// newModelUsageTenantCheckDB auto-migrates both tables WITHOUT a foreign key so
// the repository's own tenant-consistency check (not the FK) is what rejects a
// cross-tenant or unknown evaluation run reference.
func newModelUsageTenantCheckDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.ModelUsage{}, &types.EvaluationRun{}))
	return db
}

func insertEvaluationRun(t *testing.T, db *gorm.DB, runID string, tenantID uint64) {
	t.Helper()
	require.NoError(t, db.Exec(
		"INSERT INTO evaluation_runs "+
			"(id, task_id, tenant_id, dataset_id, embedding_model_id, chat_model_id, status, config_snapshot) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		runID, "task-"+runID, tenantID, "dataset", "embed", "chat", 0, "{}",
	).Error)
}

func testModelUsage(tenantID uint64, modelID string) *types.ModelUsage {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &types.ModelUsage{
		TenantID: tenantID, ModelTenantID: 10000,
		ModelID: modelID, ModelName: "model-" + modelID,
		ModelType: string(types.ModelTypeKnowledgeQA), ModelSource: string(types.ModelSourceOpenAI),
		ResolvedProvider: "openai", CallType: types.CallTypeChat,
		Purpose: "test", Status: types.UsageStatusSuccess,
		TokenProvenance: types.TokenProvenanceProviderReported,
		LatencyMS:       int64Ptr(1200),
		StartedAt:       &now, CreatedAt: now,
	}
}

func int64Ptr(v int64) *int64 { return &v }
func intPtr(v int) *int       { return &v }

func TestModelUsageRepositoryCreateAndRead(t *testing.T) {
	db := newModelUsageTestDB(t)
	ctx := context.Background()
	repo := NewModelUsageRepository(db)

	usage := testModelUsage(1, "chat-safe")
	usage.LogicalRequests = 1
	usage.InputTokens = intPtr(100)
	usage.OutputTokens = intPtr(50)
	usage.TotalTokens = intPtr(150)

	require.NoError(t, repo.Create(ctx, usage))
	require.NotEmpty(t, usage.ID)

	got, err := repo.GetByID(ctx, 1, usage.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, usage.ID, got.ID)
	require.Equal(t, uint64(1), got.TenantID)
	require.Equal(t, uint64(10000), got.ModelTenantID)
	require.Equal(t, "chat-safe", got.ModelID)
	require.Equal(t, "openai", got.ResolvedProvider)
	require.Nil(t, got.ResolvedModelName, "legacy/unknown effective identity must stay NULL")
	require.Equal(t, types.CallTypeChat, got.CallType)
	require.Equal(t, types.TokenProvenanceProviderReported, got.TokenProvenance)
	require.Equal(t, int64(1200), *got.LatencyMS)
	require.Equal(t, 1, got.LogicalRequests)
	require.Equal(t, 100, *got.InputTokens)
	require.Equal(t, 50, *got.OutputTokens)
	require.Equal(t, 150, *got.TotalTokens)
}

func TestModelUsageRepositoryPreservesNullCounters(t *testing.T) {
	db := newModelUsageTestDB(t)
	ctx := context.Background()
	repo := NewModelUsageRepository(db)

	usage := testModelUsage(1, "chat-null")
	usage.TokenProvenance = types.TokenProvenanceUnreported
	usage.LogicalRequests = 1
	// Input tokens deliberately left nil: provider did not report them.
	usage.OutputTokens = intPtr(20)
	usage.TotalTokens = intPtr(20)

	require.NoError(t, repo.Create(ctx, usage))

	got, err := repo.GetByID(ctx, 1, usage.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Nil(t, got.InputTokens, "unreported input tokens must round-trip as NULL, not 0")
	require.Nil(t, got.CacheReadTokens)
	require.NotNil(t, got.OutputTokens)
	require.Equal(t, 20, *got.OutputTokens)
}

func TestModelUsageRepositoryTenantIsolation(t *testing.T) {
	db := newModelUsageTestDB(t)
	ctx := context.Background()
	repo := NewModelUsageRepository(db)

	usage := testModelUsage(7, "chat-tenant")
	require.NoError(t, repo.Create(ctx, usage))

	other, err := repo.GetByID(ctx, 8, usage.ID)
	require.NoError(t, err)
	require.Nil(t, other, "a usage row must not be readable under another tenant")

	same, err := repo.GetByID(ctx, 7, usage.ID)
	require.NoError(t, err)
	require.NotNil(t, same)
}

func TestModelUsageRepositoryNullableEvaluationRunID(t *testing.T) {
	db := newModelUsageTestDB(t)
	ctx := context.Background()
	repo := NewModelUsageRepository(db)

	usage := testModelUsage(9, "chat-null-run")
	usage.EvaluationRunID = nil
	require.NoError(t, repo.Create(ctx, usage))

	got, err := repo.GetByID(ctx, 9, usage.ID)
	require.NoError(t, err)
	require.Nil(t, got.EvaluationRunID)
}

func TestModelUsageRepositoryFKSetNullOnEvaluationRunDelete(t *testing.T) {
	db := newModelUsageFKTestDB(t)
	ctx := context.Background()
	repo := NewModelUsageRepository(db)

	runID := uuid.NewString()
	insertEvaluationRun(t, db, runID, 42)

	usage := testModelUsage(42, "embed-fk")
	usage.CallType = types.CallTypeEmbedding
	usage.ModelType = string(types.ModelTypeEmbedding)
	usage.EvaluationRunID = &runID
	require.NoError(t, repo.Create(ctx, usage))

	require.NoError(t, db.Exec("DELETE FROM evaluation_runs WHERE id = ?", runID).Error)

	got, err := repo.GetByID(ctx, 42, usage.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Nil(t, got.EvaluationRunID, "ON DELETE SET NULL must null the reference when the run is deleted")
}

func TestModelUsageRepositoryRejectsCrossTenantEvaluationRun(t *testing.T) {
	db := newModelUsageTenantCheckDB(t)
	ctx := context.Background()
	repo := NewModelUsageRepository(db)

	runID := uuid.NewString()
	insertEvaluationRun(t, db, runID, 42)

	usage := testModelUsage(43, "chat-cross") // different tenant
	usage.EvaluationRunID = &runID
	require.Error(t, repo.Create(ctx, usage), "tenant A usage must not reference tenant B run")

	// Same tenant must succeed.
	ok := testModelUsage(42, "chat-ok")
	ok.EvaluationRunID = &runID
	require.NoError(t, repo.Create(ctx, ok))
}

func TestModelUsageRepositoryRejectsUnknownEvaluationRun(t *testing.T) {
	db := newModelUsageTenantCheckDB(t)
	ctx := context.Background()
	repo := NewModelUsageRepository(db)

	runID := uuid.NewString()
	usage := testModelUsage(42, "chat-unknown")
	usage.EvaluationRunID = &runID
	require.Error(t, repo.Create(ctx, usage), "a non-existent evaluation run must be rejected")
}

func TestModelUsageRepositoryRejectsInvalidRow(t *testing.T) {
	db := newModelUsageTestDB(t)
	ctx := context.Background()
	repo := NewModelUsageRepository(db)

	usage := testModelUsage(1, "chat-invalid")
	usage.ModelTenantID = 0
	require.Error(t, repo.Create(ctx, usage))

	neg := -1
	usage2 := testModelUsage(1, "chat-neg")
	usage2.InputTokens = &neg
	require.Error(t, repo.Create(ctx, usage2))
}

// TestModelUsageRepositoryConcurrentCreateGoroutineSafety is a goroutine-safety
// smoke test, NOT a real multi-connection concurrency benchmark: the test DB
// pins MaxOpenConns=1, so SQLite serializes every write through a single
// connection. It only proves that concurrent Create calls through the
// repository are data-race free and produce distinct primary keys.
func TestModelUsageRepositoryConcurrentCreateGoroutineSafety(t *testing.T) {
	db := newModelUsageTestDB(t)
	ctx := context.Background()
	repo := NewModelUsageRepository(db)

	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	ids := make(chan string, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			usage := testModelUsage(11, "chat-concurrent")
			if err := repo.Create(ctx, usage); err != nil {
				errs <- err
				return
			}
			ids <- usage.ID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		require.NoError(t, err)
	}
	seen := map[string]bool{}
	count := 0
	for id := range ids {
		count++
		require.False(t, seen[id], "concurrent inserts must generate distinct primary keys")
		seen[id] = true
		got, err := repo.GetByID(ctx, 11, id)
		require.NoError(t, err)
		require.NotNil(t, got)
	}
	require.Equal(t, workers, count)
}

func TestModelUsageMigrationParity(t *testing.T) {
	pg, err := os.ReadFile("../../../migrations/versioned/000092_model_usage.up.sql")
	require.NoError(t, err)
	sq, err := os.ReadFile("../../../migrations/sqlite/000014_model_usage.up.sql")
	require.NoError(t, err)
	pgPricing, err := os.ReadFile("../../../migrations/versioned/000093_model_pricing.up.sql")
	require.NoError(t, err)
	sqPricing, err := os.ReadFile("../../../migrations/sqlite/000015_model_pricing.up.sql")
	require.NoError(t, err)

	// Shared column contract (both dialects).
	requiredColumns := []string{
		"tenant_id", "model_tenant_id", "evaluation_run_id", "model_id", "model_name",
		"model_type", "model_source", "resolved_provider", "call_type", "purpose",
		"status", "token_provenance", "latency_ms", "started_at", "created_at",
		"input_tokens", "output_tokens", "total_tokens", "prompt_cache_status",
		"cache_read_tokens", "cache_write_tokens", "cache_miss_tokens",
		"logical_requests", "embedding_inputs", "cache_hits", "cache_misses",
		"provider_requests", "provider_inputs", "cache_read_errors", "cache_write_errors",
		"embedding_cache_status", "queries", "documents", "pairs", "provider_pairs",
	}
	require.Contains(t, string(pgPricing), "resolved_model_name")
	require.Contains(t, string(sqPricing), "resolved_model_name")
	for _, col := range requiredColumns {
		require.Contains(t, string(pg), col)
		require.Contains(t, string(sq), col)
	}

	// Structural markers present in both.
	for _, marker := range []string{
		"ON DELETE SET NULL",
		"tenant_id > 0",
		"model_tenant_id > 0",
		"token_provenance VARCHAR(32) NOT NULL",
	} {
		require.Contains(t, string(pg), marker)
		require.Contains(t, string(sq), marker)
	}

	// Index contract present in both.
	for _, idx := range []string{
		"idx_model_usage_tenant_created",
		"idx_model_usage_tenant_model_created",
		"idx_model_usage_tenant_evaluation_created",
		"idx_model_usage_evaluation_run",
	} {
		require.Contains(t, string(pg), idx)
		require.Contains(t, string(sq), idx)
	}

	// SQLite side is executed and introspected for real (PostgreSQL DDL above
	// is a static string check only — this test does not run PostgreSQL).
	evalDDL, err := os.ReadFile("../../../migrations/sqlite/000013_evaluation_runs.up.sql")
	require.NoError(t, err)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(evalDDL)).Error)
	require.NoError(t, db.Exec(string(sq)).Error)
	require.NoError(t, db.Exec(string(sqPricing)).Error)

	require.True(t, db.Migrator().HasTable("model_usage"))

	nullable := sqliteColumnNullability(t, db, "model_usage")
	// NOT NULL identity columns.
	for _, notNull := range []string{
		"tenant_id", "model_tenant_id", "model_id", "model_name", "model_type",
		"model_source", "resolved_provider", "call_type", "status", "token_provenance",
	} {
		require.Falsef(t, nullable[notNull], "column %s must be NOT NULL", notNull)
	}
	// Nullable provider-reported / optional columns.
	for _, nullCol := range []string{
		"evaluation_run_id", "resolved_model_name", "latency_ms", "started_at", "input_tokens", "output_tokens",
		"total_tokens", "prompt_cache_status", "cache_read_tokens", "cache_write_tokens",
		"cache_miss_tokens", "embedding_cache_status",
	} {
		require.Truef(t, nullable[nullCol], "column %s must be nullable", nullCol)
	}

	// Foreign key with ON DELETE SET NULL.
	fkOnDelete := sqliteFKOnDelete(t, db, "model_usage")
	require.Equal(t, "SET NULL", fkOnDelete)

	// Indexes actually created.
	indexes := sqliteIndexes(t, db, "model_usage")
	for _, idx := range []string{
		"idx_model_usage_tenant_created",
		"idx_model_usage_tenant_model_created",
		"idx_model_usage_tenant_evaluation_created",
		"idx_model_usage_evaluation_run",
	} {
		require.Truef(t, indexes[idx], "missing index %s", idx)
	}
}

func sqliteColumnNullability(t *testing.T, db *gorm.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Raw("PRAGMA table_info(" + table + ")").Rows()
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt interface{}
		require.NoError(t, rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk))
		out[name] = notNull == 0
	}
	require.NoError(t, rows.Err())
	return out
}

func sqliteFKOnDelete(t *testing.T, db *gorm.DB, table string) string {
	t.Helper()
	rows, err := db.Raw("PRAGMA foreign_key_list(" + table + ")").Rows()
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var onDelete string
	for rows.Next() {
		var id, seq int
		var tableName, from, to, onUpdate, onDeleteVal, match string
		require.NoError(t, rows.Scan(&id, &seq, &tableName, &from, &to, &onUpdate, &onDeleteVal, &match))
		if tableName == "evaluation_runs" {
			onDelete = onDeleteVal
		}
	}
	require.NoError(t, rows.Err())
	return onDelete
}

func sqliteIndexes(t *testing.T, db *gorm.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Raw("PRAGMA index_list(" + table + ")").Rows()
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var seq, unique int
		var name, origin, partial string
		require.NoError(t, rows.Scan(&seq, &name, &unique, &origin, &partial))
		out[name] = true
	}
	require.NoError(t, rows.Err())
	return out
}
