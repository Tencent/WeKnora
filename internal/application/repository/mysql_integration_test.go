package repository

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestMySQLRepositoryDialect runs only when MYSQL_TEST_DSN points at a
// migrated disposable MySQL database. It deliberately exercises repository
// methods whose SQL differs across PostgreSQL, MySQL, and SQLite.
func TestMySQLRepositoryDialect(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{NowFunc: func() time.Time { return time.Now().UTC() }})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)

	ctx := context.Background()
	const tenantID = uint64(981418)
	const kbID = "mysql-it-kb"
	t.Cleanup(func() {
		for _, table := range []string{"tenant_api_keys", "sessions", "knowledges", "task_pending_ops", "wiki_pages", "sync_logs", "chunks"} {
			db.Exec("DELETE FROM "+table+" WHERE tenant_id = ?", tenantID)
		}
		db.Exec("DELETE FROM messages WHERE id LIKE 'mysql-it-%'")
		db.Exec("DELETE FROM tenants WHERE id = ?", tenantID)
		db.Exec("DELETE FROM system_settings WHERE `key` LIKE 'mysql.it.%'")
	})

	t.Run("tenant and tenant API key CRUD", func(t *testing.T) {
		tenantRepo := &tenantRepository{db: db}
		tenant := &types.Tenant{ID: tenantID, Name: "MySQL Tenant", Business: "integration", RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{}}}
		require.NoError(t, tenantRepo.CreateTenant(ctx, tenant))
		gotTenant, err := tenantRepo.GetTenantByID(ctx, tenantID)
		require.NoError(t, err)
		require.Equal(t, tenant.Name, gotTenant.Name)

		keyRepo := &tenantAPIKeyRepository{db: db}
		key := &types.TenantAPIKey{TenantID: tenantID, Name: "integration", KeyHash: "mysql-it-key-hash", APIKey: "wk-test", KnowledgeBaseIDs: types.StringArray{kbID}, Capabilities: types.StringArray{"retrieve"}}
		require.NoError(t, keyRepo.CreateAPIKey(ctx, key))
		gotKey, err := keyRepo.GetAPIKeyByHash(ctx, key.KeyHash)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{kbID}, gotKey.KnowledgeBaseIDs)
		require.Equal(t, types.StringArray{"retrieve"}, gotKey.Capabilities)
	})

	t.Run("session message and knowledge JSON", func(t *testing.T) {
		sessionRepo := &sessionRepository{db: db}
		session, err := sessionRepo.Create(ctx, &types.Session{TenantID: tenantID, UserID: "mysql-user", Title: "Case Search Session"})
		require.NoError(t, err)

		messageRepo := &messageRepository{db: db}
		message := &types.Message{ID: "mysql-it-message", SessionID: session.ID, RequestID: "mysql-it-request", Content: "MixedCase MySQL Content", Role: "user", KnowledgeReferences: types.References{}, AgentSteps: types.AgentSteps{}, MentionedItems: types.MentionedItems{}, Images: types.MessageImages{}, Attachments: types.MessageAttachments{}}
		_, err = messageRepo.CreateMessage(ctx, message)
		require.NoError(t, err)
		messages, err := messageRepo.SearchMessagesByKeyword(ctx, tenantID, "mixedcase", nil, 10)
		require.NoError(t, err)
		require.Len(t, messages, 1)

		knowledgeRepo := &knowledgeRepository{db: db}
		knowledge := &types.Knowledge{ID: "mysql-it-knowledge", TenantID: tenantID, KnowledgeBaseID: kbID, Type: "file", Title: "MySQL knowledge", Source: "manual", ParseStatus: "completed", EnableStatus: "enabled", Metadata: types.JSON(`{"source_id":"source-1418","nested":{"enabled":true}}`), LastFAQImportResult: types.JSON(`{}`)}
		require.NoError(t, knowledgeRepo.CreateKnowledge(ctx, knowledge))
		gotKnowledge, err := knowledgeRepo.FindByMetadataKey(ctx, tenantID, kbID, "source_id", "source-1418")
		require.NoError(t, err)
		require.NotNil(t, gotKnowledge)
		require.Equal(t, knowledge.ID, gotKnowledge.ID)
	})

	t.Run("chunk JSON and batch update", func(t *testing.T) {
		repo := &chunkRepository{db: db}
		chunks := []*types.Chunk{
			{ID: "mysql-it-chunk-1", TenantID: tenantID, KnowledgeBaseID: kbID, KnowledgeID: "mysql-it-doc", Content: "before one", ChunkType: types.ChunkTypeFAQ, IsEnabled: true, Flags: 1, Status: int(types.ChunkStatusIndexed), Metadata: types.JSON(`{"standard_question":"Hello","similar_questions":["Hi"]}`)},
			{ID: "mysql-it-chunk-2", TenantID: tenantID, KnowledgeBaseID: kbID, KnowledgeID: "mysql-it-doc", Content: "before two", ChunkType: types.ChunkTypeFAQ, IsEnabled: true, Flags: 1, Status: int(types.ChunkStatusIndexed), Metadata: types.JSON(`{"standard_question":"Other","generated_questions":["Generated"]}`)},
		}
		require.NoError(t, repo.CreateChunks(ctx, chunks))

		duplicate, err := repo.FindFAQChunkWithDuplicateQuestion(ctx, tenantID, kbID, "missing", []string{"Hi"})
		require.NoError(t, err)
		require.NotNil(t, duplicate)
		require.Equal(t, chunks[0].ID, duplicate.ID)

		chunks[0].Content, chunks[0].IsEnabled, chunks[0].Flags = "after one", false, 3
		chunks[1].Content, chunks[1].Status = "after two", int(types.ChunkStatusDefault)
		require.NoError(t, repo.UpdateChunks(ctx, chunks))
		got, err := repo.GetChunkByID(ctx, tenantID, chunks[0].ID)
		require.NoError(t, err)
		require.Equal(t, "after one", got.Content)
		require.False(t, got.IsEnabled)
		require.Equal(t, types.ChunkFlags(3), got.Flags)
	})

	t.Run("datasource cutoff", func(t *testing.T) {
		repo := &SyncLogRepository{db: db}
		oldLog := &types.SyncLog{ID: "mysql-it-log-old", DataSourceID: "mysql-it-source", TenantID: tenantID, Status: types.SyncLogStatusSuccess, StartedAt: time.Now().UTC().AddDate(0, 0, -40), Result: types.JSON(`{}`)}
		newLog := &types.SyncLog{ID: "mysql-it-log-new", DataSourceID: "mysql-it-source", TenantID: tenantID, Status: types.SyncLogStatusSuccess, StartedAt: time.Now().UTC(), Result: types.JSON(`{}`)}
		require.NoError(t, repo.Create(ctx, oldLog))
		require.NoError(t, repo.Create(ctx, newLog))
		require.NoError(t, repo.CleanupOldLogs(ctx, 30))
		var count int64
		require.NoError(t, db.Model(&types.SyncLog{}).Where("id = ?", oldLog.ID).Count(&count).Error)
		require.Zero(t, count)
		require.NoError(t, db.Model(&types.SyncLog{}).Where("id = ?", newLog.ID).Count(&count).Error)
		require.EqualValues(t, 1, count)
	})

	t.Run("system setting reserved key upsert", func(t *testing.T) {
		repo := &systemSettingRepository{db: db}
		setting := &types.SystemSetting{Key: "mysql.it.limit", Value: types.JSON(`42`), ValueType: "int", Category: "tests"}
		require.NoError(t, repo.Upsert(ctx, setting))
		setting.Value = types.JSON(`43`)
		require.NoError(t, repo.Upsert(ctx, setting))
		got, err := repo.Get(ctx, setting.Key)
		require.NoError(t, err)
		require.Equal(t, "43", string(got.Value))
	})

	t.Run("wiki source refs and case-insensitive search", func(t *testing.T) {
		repo := &wikiPageRepository{db: db}
		page := &types.WikiPage{ID: "mysql-it-page", TenantID: tenantID, KnowledgeBaseID: kbID, Slug: "mysql-page", Title: "MySQL Integration", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished, Content: "Repository dialect behavior", SourceRefs: types.StringArray{"source-1418|Document"}, Aliases: types.StringArray{}, CategoryPath: types.StringArray{}, ChunkRefs: types.StringArray{}, InLinks: types.StringArray{}, OutLinks: types.StringArray{}, PageMetadata: types.JSON(`{}`), Version: 1}
		require.NoError(t, repo.Create(ctx, page))
		pages, err := repo.ListBySourceRef(ctx, kbID, "source-1418")
		require.NoError(t, err)
		require.Len(t, pages, 1)
		pages, err = repo.Search(ctx, kbID, "mysql", 10)
		require.NoError(t, err)
		require.Len(t, pages, 1)
	})

	t.Run("concurrent task claims and fail count", func(t *testing.T) {
		repo := &taskPendingOpsRepository{db: db}
		for _, key := range []string{"doc-a", "doc-a", "doc-b", "doc-b"} {
			require.NoError(t, repo.Enqueue(ctx, &types.TaskPendingOp{TenantID: tenantID, TaskType: "mysql-it", Scope: "knowledge_base", ScopeID: kbID, Op: "ingest", DedupKey: key}))
		}

		start := make(chan struct{})
		results := make(chan []*types.TaskPendingOp, 2)
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ops, claimErr := repo.ClaimBatch(ctx, "mysql-it", "knowledge_base", kbID, 1, time.Now().UTC().Add(-time.Hour))
				results <- ops
				errs <- claimErr
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		close(errs)
		for claimErr := range errs {
			require.NoError(t, claimErr)
		}
		claimedKeys := map[string]bool{}
		var firstID int64
		for ops := range results {
			if len(ops) == 0 {
				continue
			}
			require.Len(t, ops, 2)
			require.False(t, claimedKeys[ops[0].DedupKey], "a dedup key was claimed by two workers")
			claimedKeys[ops[0].DedupKey] = true
			if firstID == 0 {
				firstID = ops[0].ID
			}
		}
		if len(claimedKeys) < 2 {
			ops, claimErr := repo.ClaimBatch(ctx, "mysql-it", "knowledge_base", kbID, 1, time.Now().UTC().Add(-time.Hour))
			require.NoError(t, claimErr)
			require.Len(t, ops, 2)
			require.False(t, claimedKeys[ops[0].DedupKey], "a dedup key was claimed twice")
			claimedKeys[ops[0].DedupKey] = true
			if firstID == 0 {
				firstID = ops[0].ID
			}
		}
		require.Equal(t, map[string]bool{"doc-a": true, "doc-b": true}, claimedKeys)
		count, err := repo.IncrFailCount(ctx, firstID)
		require.NoError(t, err)
		require.Equal(t, 1, count)
	})
}
