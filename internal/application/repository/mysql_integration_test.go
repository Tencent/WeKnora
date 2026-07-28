package repository

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mysqlgorm "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMySQLRepositoryQueries(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MYSQL_TEST_DSN")
	}

	db, err := gorm.Open(mysqlgorm.Open(dsn), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	require.NoError(t, err)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() {
		require.NoError(t, tx.Rollback().Error)
	})

	ctx := context.Background()
	require.NoError(t, tx.Exec(`
		INSERT INTO sessions (id, tenant_id, title)
		VALUES ('mysql-session', 9001, 'MySQL Session')
	`).Error)
	require.NoError(t, tx.Exec(`
		INSERT INTO messages (id, request_id, session_id, role, content)
		VALUES ('mysql-message', 'mysql-request', 'mysql-session', 'user', 'Hello MySQL')
	`).Error)
	require.NoError(t, tx.Exec(`
		INSERT INTO knowledges (
			id, tenant_id, knowledge_base_id, type, title, source, metadata
		) VALUES (
			'mysql-knowledge', 9001, 'mysql-kb', 'file', 'Metadata Test',
			'manual', JSON_OBJECT('source-resource-id', 'resource-42')
		)
	`).Error)

	knowledgeRepo := &knowledgeRepository{db: tx}
	knowledge, err := knowledgeRepo.FindByMetadataKey(
		ctx, 9001, "mysql-kb", "source-resource-id", "resource-42",
	)
	require.NoError(t, err)
	require.NotNil(t, knowledge)
	assert.Equal(t, "mysql-knowledge", knowledge.ID)

	messageRepo := &messageRepository{db: tx}
	messages, err := messageRepo.SearchMessagesByKeyword(ctx, 9001, "hello mysql", nil, 10)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "mysql-message", messages[0].ID)

	chunkRepo := &chunkRepository{db: tx}
	chunk := &types.Chunk{
		ID:              "mysql-chunk",
		TenantID:        9001,
		KnowledgeBaseID: "mysql-kb",
		KnowledgeID:     "mysql-knowledge",
		Content:         "before update",
		ChunkIndex:      0,
		StartAt:         0,
		EndAt:           13,
		IsEnabled:       false,
		ChunkType:       types.ChunkTypeText,
	}
	require.NoError(t, chunkRepo.CreateChunks(ctx, []*types.Chunk{chunk}))

	chunk.Content = "after update"
	chunk.IsEnabled = true
	chunk.Flags = types.ChunkFlagRecommended
	chunk.Status = int(types.ChunkStatusIndexed)
	require.NoError(t, chunkRepo.UpdateChunks(ctx, []*types.Chunk{chunk}))

	var storedChunk types.Chunk
	require.NoError(t, tx.Where("id = ?", chunk.ID).First(&storedChunk).Error)
	assert.Positive(t, storedChunk.SeqID)
	assert.Equal(t, "after update", storedChunk.Content)
	assert.True(t, storedChunk.IsEnabled)
	assert.Equal(t, types.ChunkFlagRecommended, storedChunk.Flags)
	assert.Equal(t, int(types.ChunkStatusIndexed), storedChunk.Status)

	require.NoError(t, tx.Exec(`
		INSERT INTO wiki_pages (
			id, tenant_id, knowledge_base_id, slug, title, page_type,
			content, category_path, source_refs, aliases, in_links, out_links
		) VALUES
			(
				'mysql-wiki-1', 9001, 'mysql-kb', 'alpha', 'Alpha Entity',
				'entity', 'Primary Alpha content', JSON_ARRAY('AI'),
				JSON_ARRAY('source-1'), JSON_ARRAY('A'), JSON_ARRAY(), JSON_ARRAY()
			),
			(
				'mysql-wiki-2', 9001, 'mysql-kb', 'beta', 'Beta Concept',
				'concept', 'References Alpha', JSON_ARRAY('AI'),
				JSON_ARRAY('source-1|Document'), JSON_ARRAY(), JSON_ARRAY(), JSON_ARRAY()
			)
	`).Error)

	wikiRepo := &wikiPageRepository{db: tx}
	pages, err := wikiRepo.ListBySourceRef(ctx, "mysql-kb", "source-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta"}, sortedWikiSlugs(pages))

	pages, total, err := wikiRepo.List(ctx, &types.WikiPageListRequest{
		KnowledgeBaseID: "mysql-kb",
		Query:           "alpha",
		CategoryPath:    types.StringArray{"AI"},
		Page:            1,
		PageSize:        10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Equal(t, []string{"alpha", "beta"}, sortedWikiSlugs(pages))

	similar, err := wikiRepo.FindSimilarPages(ctx, "mysql-kb", "Alpha", nil, 10)
	require.NoError(t, err)
	require.Len(t, similar, 1)
	assert.Equal(t, "alpha", similar[0].Slug)

	pages, err = wikiRepo.Search(ctx, "mysql-kb", "alpha", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta"}, sortedWikiSlugs(pages))

	orphans, err := wikiRepo.CountOrphans(ctx, "mysql-kb")
	require.NoError(t, err)
	assert.EqualValues(t, 2, orphans)

	taskRepo := &taskPendingOpsRepository{db: tx}
	op := &types.TaskPendingOp{
		TenantID: 9001,
		TaskType: "wiki:ingest",
		Scope:    types.TaskScopeKnowledgeBase,
		ScopeID:  "mysql-kb",
		Op:       "ingest",
		DedupKey: "mysql-knowledge",
	}
	require.NoError(t, taskRepo.Enqueue(ctx, op))
	claimed, err := taskRepo.ClaimBatch(
		ctx,
		op.TaskType,
		op.Scope,
		op.ScopeID,
		10,
		time.Now().UTC().Add(-time.Hour),
	)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, op.ID, claimed[0].ID)

	failCount, err := taskRepo.IncrFailCount(ctx, op.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, failCount)

	settingRepo := &systemSettingRepository{db: tx}
	require.NoError(t, settingRepo.Upsert(ctx, &types.SystemSetting{
		Key:         "mysql.integration",
		Value:       types.JSON(`true`),
		ValueType:   "bool",
		Category:    "test",
		Description: "MySQL integration test",
	}))
	setting, err := settingRepo.Get(ctx, "mysql.integration")
	require.NoError(t, err)
	require.NotNil(t, setting)
	assert.Equal(t, "mysql.integration", setting.Key)
}

func sortedWikiSlugs(pages []*types.WikiPage) []string {
	slugs := make([]string, 0, len(pages))
	for _, page := range pages {
		slugs = append(slugs, page.Slug)
	}
	sort.Strings(slugs)
	return slugs
}
