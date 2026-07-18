package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestMySQLBusinessRepositoryCompatibility exercises SQL paths that cannot be
// covered by the SQLite unit suite. It is opt-in so normal contributors do not
// need a local MySQL service:
//
//	WEKNORA_TEST_MYSQL_DSN='root@tcp(127.0.0.1:3306)/weknora?parseTime=true' \
//	  go test ./internal/application/repository -run TestMySQLBusinessRepositoryCompatibility
func TestMySQLBusinessRepositoryCompatibility(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_MYSQL_DSN is not configured")
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{NowFunc: func() time.Time { return time.Now().UTC() }})
	require.NoError(t, err)
	ctx := context.Background()

	suffix := uuid.NewString()
	tenantID := uint64(time.Now().UnixNano()%1_000_000_000) + 8_000_000_000
	kbID := suffix
	knowledgeID := uuid.NewString()
	chunkID := uuid.NewString()
	wikiID := uuid.NewString()
	settingKey := "mysql.integration." + suffix
	taskType := "mysql-integration-" + suffix

	cleanup := func() {
		db.Exec("DELETE FROM task_pending_ops WHERE task_type = ?", taskType)
		db.Exec("DELETE FROM system_settings WHERE `key` = ?", settingKey)
		db.Exec("DELETE FROM wiki_pages WHERE id = ?", wikiID)
		db.Exec("DELETE FROM chunks WHERE id = ?", chunkID)
		db.Exec("DELETE FROM knowledges WHERE id = ?", knowledgeID)
		db.Exec("DELETE FROM knowledge_bases WHERE id = ?", kbID)
		db.Exec("DELETE FROM tenants WHERE id = ?", tenantID)
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
		knowledgeID, tenantID, kbID, "file", "MySQL knowledge", "manual", `{"external_id":"mysql-42","external.id":"literal-dot"}`,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO chunks (id, tenant_id, knowledge_base_id, knowledge_id, content, chunk_index, start_at, end_at, chunk_type, status, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CAST(? AS JSON))",
		chunkID, tenantID, kbID, knowledgeID, "original answer", 0, 0, 15, types.ChunkTypeFAQ, types.ChunkStatusIndexed,
		`{"standard_question":"What is MySQL?","similar_questions":["Define MySQL"],"answers":["A database"]}`,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO wiki_pages (id, tenant_id, knowledge_base_id, slug, title, page_type, status, content, summary, category_path, depth, wiki_path, source_refs) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CAST(? AS JSON), ?, ?, CAST(? AS JSON))",
		wikiID, tenantID, kbID, "entity/mysql", "Alpha MySQL", types.WikiPageTypeEntity,
		types.WikiPageStatusPublished, "MySQL content", "database summary", `["Databases","SQL"]`,
		2, "entity/Databases/SQL/Alpha MySQL", `["source-exact","source-legacy|Document"]`,
	).Error)

	knowledge, err := (&knowledgeRepository{db: db}).FindByMetadataKey(
		ctx, tenantID, kbID, "external_id", "mysql-42",
	)
	require.NoError(t, err)
	require.NotNil(t, knowledge)
	assert.Equal(t, knowledgeID, knowledge.ID)
	literalDotKey, err := (&knowledgeRepository{db: db}).FindByMetadataKey(
		ctx, tenantID, kbID, "external.id", "literal-dot",
	)
	require.NoError(t, err)
	require.NotNil(t, literalDotKey)
	assert.Equal(t, knowledgeID, literalDotKey.ID)

	chunkRepo := &chunkRepository{db: db}
	duplicate, err := chunkRepo.FindFAQChunkWithDuplicateQuestion(
		ctx, tenantID, kbID, "not-the-test-chunk", []string{"Define MySQL"},
	)
	require.NoError(t, err)
	require.NotNil(t, duplicate)
	assert.Equal(t, chunkID, duplicate.ID)
	require.NoError(t, chunkRepo.UpdateChunks(ctx, []*types.Chunk{{
		ID: chunkID, Content: "updated answer", IsEnabled: true, Flags: 3, Status: int(types.ChunkStatusIndexed),
	}}))
	var updatedChunk types.Chunk
	require.NoError(t, db.First(&updatedChunk, "id = ?", chunkID).Error)
	assert.Equal(t, "updated answer", updatedChunk.Content)
	assert.True(t, updatedChunk.IsEnabled)

	wikiRepo := &wikiPageRepository{db: db}
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
	assert.Equal(t, wikiID, searched[0].ID)

	settingRepo := &systemSettingRepository{db: db}
	setting := &types.SystemSetting{
		Key: settingKey, Value: types.JSON(`1`), ValueType: "int", Category: "test", Description: "mysql integration",
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

	taskRepo := &taskPendingOpsRepository{db: db}
	for i, dedupKey := range []string{"doc-a", "doc-b"} {
		require.NoError(t, taskRepo.Enqueue(ctx, &types.TaskPendingOp{
			TenantID: tenantID, TaskType: taskType, Scope: "knowledge_base", ScopeID: kbID,
			Op: "ingest", DedupKey: dedupKey, Payload: []byte(fmt.Sprintf(`{"index":%d}`, i)),
		}))
	}
	claimed, err := taskRepo.ClaimBatch(ctx, taskType, "knowledge_base", kbID, 1, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	failCount, err := taskRepo.IncrFailCount(ctx, claimed[0].ID)
	require.NoError(t, err)
	assert.Equal(t, 1, failCount)
}
