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
		INSERT INTO knowledge_bases (
			id, name, tenant_id, embedding_model_id, summary_model_id,
			image_processing_config
		) VALUES (
			'mysql-kb', 'MySQL Knowledge Base', 9001, 'embedding-model',
			'summary-model', JSON_OBJECT('model_id', 'mysql-model')
		)
	`).Error)
	require.NoError(t, tx.Exec(`
		INSERT INTO users (id, username, email, password_hash, tenant_id)
		VALUES ('mysql-user', 'MySQL.Tester', 'mysql.tester@example.com', 'hash', 9001)
	`).Error)
	require.NoError(t, tx.Exec(`
		INSERT INTO organizations (id, name, description, owner_id, searchable)
		VALUES (
			'mysql-organization', 'MySQL Organization', 'Production workspace',
			'mysql-user', TRUE
		)
	`).Error)
	require.NoError(t, tx.Exec(`
		INSERT INTO custom_agents (id, name, tenant_id, config)
		VALUES (
			'mysql-agent', 'MySQL Agent', 9001,
			JSON_OBJECT(
				'question_suggestions',
				JSON_OBJECT('follow_ups', JSON_OBJECT('model_id', 'mysql-model'))
			)
		)
	`).Error)
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

	userRepo := &userRepository{db: tx}
	users, err := userRepo.SearchUsers(ctx, "mysql.tester", 10)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "mysql-user", users[0].ID)

	organizationRepo := &organizationRepository{db: tx}
	organizations, err := organizationRepo.ListSearchable(ctx, "mysql organization", 10)
	require.NoError(t, err)
	require.Len(t, organizations, 1)
	assert.Equal(t, "mysql-organization", organizations[0].ID)

	sessionRepo := &sessionRepository{db: tx}
	sessions, total, err := sessionRepo.QueryPaged(ctx, &types.SessionListQuery{
		TenantID: 9001,
		Keyword:  "mysql session",
		Page:     1,
		PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, sessions, 1)
	assert.Equal(t, "mysql-session", sessions[0].ID)

	knowledgeBaseRepo := &knowledgeBaseRepository{db: tx}
	usageCount, err := knowledgeBaseRepo.CountByModelID(ctx, 9001, "mysql-model")
	require.NoError(t, err)
	assert.EqualValues(t, 1, usageCount)

	customAgentRepo := &customAgentRepository{db: tx}
	usageCount, err = customAgentRepo.CountByModelID(ctx, 9001, "mysql-model")
	require.NoError(t, err)
	assert.EqualValues(t, 1, usageCount)

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

	pages, total, err = wikiRepo.List(ctx, &types.WikiPageListRequest{
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

	require.NoError(t, tx.Exec(`
		INSERT INTO sync_logs (id, data_source_id, tenant_id, status, started_at)
		VALUES
			('mysql-old-log', 'mysql-source', 9001, 'success', UTC_TIMESTAMP(6) - INTERVAL 60 DAY),
			('mysql-new-log', 'mysql-source', 9001, 'success', UTC_TIMESTAMP(6))
	`).Error)
	syncLogRepo := &SyncLogRepository{db: tx}
	require.NoError(t, syncLogRepo.CleanupOldLogs(ctx, 30))
	var remainingLogs int64
	require.NoError(t, tx.Model(&types.SyncLog{}).
		Where("id IN ?", []string{"mysql-old-log", "mysql-new-log"}).
		Count(&remainingLogs).Error)
	assert.EqualValues(t, 1, remainingLogs)
}

func sortedWikiSlugs(pages []*types.WikiPage) []string {
	slugs := make([]string, 0, len(pages))
	for _, page := range pages {
		slugs = append(slugs, page.Slug)
	}
	sort.Strings(slugs)
	return slugs
}
