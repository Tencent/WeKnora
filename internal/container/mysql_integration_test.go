package container

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMySQLDatabaseIntegration is opt-in because it requires a disposable
// MySQL database. It exercises startup migration and representative SQL paths
// that SQLite-based unit tests cannot validate.
//
//	WEKNORA_TEST_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/WeKnora?parseTime=true&loc=UTC' \
//	  go test ./internal/container -run TestMySQLDatabaseIntegration -count=1
func TestMySQLDatabaseIntegration(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_MYSQL_DSN is not configured")
	}
	parsed, err := mysqlDriver.ParseDSN(dsn)
	require.NoError(t, err)
	host, port, err := net.SplitHostPort(parsed.Addr)
	require.NoError(t, err)

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	projectRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	originalWorkingDirectory, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(projectRoot))
	t.Cleanup(func() { _ = os.Chdir(originalWorkingDirectory) })

	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", host)
	t.Setenv("DB_PORT", port)
	t.Setenv("DB_USER", parsed.User)
	t.Setenv("DB_PASSWORD", parsed.Passwd)
	t.Setenv("DB_NAME", parsed.DBName)
	t.Setenv("RETRIEVE_DRIVER", "qdrant")
	t.Setenv("AUTO_MIGRATE", "true")
	t.Setenv("AUTO_RECOVER_DIRTY", "false")
	t.Setenv("STORAGE_TYPE", "local")

	db, err := initDatabase(&config.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	var tableCount int64
	require.NoError(t, db.Raw(`
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name <> 'schema_migrations'
	`).Scan(&tableCount).Error)
	assert.Equal(t, int64(50), tableCount)

	ctx := context.Background()
	suffix := uuid.NewString()
	tenantID := uint64(time.Now().UnixNano()%1_000_000_000) + 8_000_000_000
	kbID := uuid.NewString()
	knowledgeID := uuid.NewString()
	chunkID := uuid.NewString()
	wikiID := uuid.NewString()
	settingKey := "mysql.integration." + suffix
	taskType := "mysql-integration-" + suffix

	cleanup := func() {
		db.Exec("DELETE FROM task_pending_ops WHERE task_type = ?", taskType)
		db.Exec("DELETE FROM task_dead_letters WHERE task_type = ?", taskType)
		db.Exec("DELETE FROM system_settings WHERE `key` = ?", settingKey)
		db.Unscoped().Exec("DELETE FROM wiki_pages WHERE id = ?", wikiID)
		db.Unscoped().Exec("DELETE FROM chunks WHERE id = ?", chunkID)
		db.Unscoped().Exec("DELETE FROM knowledges WHERE id = ?", knowledgeID)
		db.Unscoped().Exec("DELETE FROM knowledge_bases WHERE id = ?", kbID)
		db.Unscoped().Exec("DELETE FROM tenants WHERE id = ?", tenantID)
	}
	cleanup()
	t.Cleanup(cleanup)

	require.NoError(t, db.Exec(
		"INSERT INTO tenants (id, name, business) VALUES (?, ?, ?)",
		tenantID, "MySQL integration", "test",
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, name, tenant_id, embedding_model_id, summary_model_id) VALUES (?, ?, ?, ?, ?)",
		kbID, "MySQL integration", tenantID, "", "",
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, metadata) VALUES (?, ?, ?, ?, ?, ?, CAST(? AS JSON))",
		knowledgeID, tenantID, kbID, "file", "MySQL knowledge", "manual",
		`{"external_id":"mysql-42","source-resource-id":"resource-42"}`,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO chunks (id, tenant_id, knowledge_base_id, knowledge_id, content, chunk_index, start_at, end_at, chunk_type, status, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CAST(? AS JSON))",
		chunkID, tenantID, kbID, knowledgeID, "original answer", 0, 0, 15,
		types.ChunkTypeFAQ, types.ChunkStatusIndexed,
		`{"standard_question":"What is MySQL?","similar_questions":["Define MySQL"],"answers":["A database"]}`,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO wiki_pages (id, tenant_id, knowledge_base_id, slug, title, page_type, status, content, summary, category_path, depth, wiki_path, source_refs) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CAST(? AS JSON), ?, ?, CAST(? AS JSON))",
		wikiID, tenantID, kbID, "entity/mysql", "Alpha MySQL", types.WikiPageTypeEntity,
		types.WikiPageStatusPublished, "MySQL content", "database summary",
		`["Databases","SQL"]`, 2, "entity/Databases/SQL/Alpha MySQL",
		`["source-exact","source-legacy|Document"]`,
	).Error)

	knowledge, err := repository.NewKnowledgeRepository(db).
		FindByMetadataKey(ctx, tenantID, kbID, "external_id", "mysql-42")
	require.NoError(t, err)
	require.NotNil(t, knowledge)
	assert.Equal(t, knowledgeID, knowledge.ID)
	hyphenated, err := repository.NewKnowledgeRepository(db).
		FindByMetadataKey(ctx, tenantID, kbID, "source-resource-id", "resource-42")
	require.NoError(t, err)
	require.NotNil(t, hyphenated)
	assert.Equal(t, knowledgeID, hyphenated.ID)

	chunkRepo := repository.NewChunkRepository(db)
	duplicate, err := chunkRepo.FindFAQChunkWithDuplicateQuestion(
		ctx, tenantID, kbID, "not-the-test-chunk", []string{"Define MySQL"},
	)
	require.NoError(t, err)
	require.NotNil(t, duplicate)
	assert.Equal(t, chunkID, duplicate.ID)
	require.NoError(t, chunkRepo.UpdateChunks(ctx, []*types.Chunk{{
		ID: chunkID, Content: "updated answer", IsEnabled: true,
		Flags: 3, Status: int(types.ChunkStatusIndexed),
	}}))

	wikiRepo := repository.NewWikiPageRepository(db)
	depth := 2
	pages, total, err := wikiRepo.List(ctx, &types.WikiPageListRequest{
		KnowledgeBaseID: kbID,
		Query:           "Alpha",
		CategoryPath:    []string{"Databases", "SQL"},
		CategoryDepth:   &depth,
		Page:            1,
		PageSize:        10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, pages, 1)
	assert.Equal(t, wikiID, pages[0].ID)
	for _, sourceID := range []string{"source-exact", "source-legacy"} {
		matched, err := wikiRepo.ListBySourceRef(ctx, kbID, sourceID)
		require.NoError(t, err)
		require.Len(t, matched, 1)
		assert.Equal(t, wikiID, matched[0].ID)
	}
	searched, err := wikiRepo.Search(ctx, kbID, "Alpha", 10)
	require.NoError(t, err)
	require.Len(t, searched, 1)

	settingRepo := repository.NewSystemSettingRepository(db)
	setting := &types.SystemSetting{
		Key: settingKey, Value: types.JSON(`1`), ValueType: "int",
		Category: "test", Description: "mysql integration",
	}
	require.NoError(t, settingRepo.Upsert(ctx, setting))
	setting.Value = types.JSON(`2`)
	require.NoError(t, settingRepo.Upsert(ctx, setting))
	storedSetting, err := settingRepo.Get(ctx, settingKey)
	require.NoError(t, err)
	require.NotNil(t, storedSetting)
	value, err := storedSetting.AsInt()
	require.NoError(t, err)
	assert.Equal(t, int64(2), value)

	taskRepo := repository.NewTaskPendingOpsRepository(db)
	for _, dedupKey := range []string{"doc-a", "doc-b"} {
		require.NoError(t, taskRepo.Enqueue(ctx, &types.TaskPendingOp{
			TenantID: tenantID, TaskType: taskType,
			Scope: types.TaskScopeKnowledgeBase, ScopeID: kbID,
			Op: "ingest", DedupKey: dedupKey,
		}))
	}
	claimed, err := taskRepo.ClaimBatch(
		ctx, taskType, types.TaskScopeKnowledgeBase, kbID,
		1, time.Now().Add(-time.Hour),
	)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	failCount, err := taskRepo.IncrFailCount(ctx, claimed[0].ID)
	require.NoError(t, err)
	assert.Equal(t, 1, failCount)

	deadLetter := &types.TaskDeadLetter{
		TenantID: tenantID, TaskType: taskType,
		Scope: types.TaskScopeKnowledgeBase, ScopeID: kbID,
		RelatedID: knowledgeID, LastError: "integration failure", FailCount: 3,
	}
	require.NoError(t, repository.NewTaskDeadLetterRepository(db).Insert(ctx, deadLetter))
	assert.False(t, deadLetter.FailedAt.IsZero())
}
