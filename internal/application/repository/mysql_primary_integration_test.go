package repository

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestMySQLPrimaryRepositoryQueries exercises SQL paths that historically used
// PostgreSQL-only syntax. It runs only when MYSQL_TEST_SQL_DSN points at a
// schema migrated with migrations/mysql.
func TestMySQLPrimaryRepositoryQueries(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_SQL_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_SQL_DSN is not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{NowFunc: func() time.Time { return time.Now().UTC() }})
	require.NoError(t, err)
	ctx := context.Background()
	const tenantID uint64 = 1679001
	const kbID = "mysql-integration-kb"

	t.Cleanup(func() {
		db.Exec("DELETE FROM system_settings WHERE `key` = ?", "mysql.integration")
		db.Exec("DELETE FROM custom_agents WHERE tenant_id = ?", tenantID)
		db.Exec("DELETE FROM wiki_pages WHERE tenant_id = ?", tenantID)
		db.Exec("DELETE FROM task_pending_ops WHERE tenant_id = ?", tenantID)
		db.Exec("DELETE FROM messages WHERE session_id IN (SELECT id FROM sessions WHERE tenant_id = ?)", tenantID)
		db.Exec("DELETE FROM sessions WHERE tenant_id = ?", tenantID)
		db.Exec("DELETE FROM knowledges WHERE tenant_id = ?", tenantID)
		db.Exec("DELETE FROM tenants WHERE id = ?", tenantID)
	})

	tenantRepo := &tenantRepository{db: db}
	require.NoError(t, tenantRepo.CreateTenant(ctx, &types.Tenant{
		ID: tenantID, Name: "Independent MySQL", Business: "integration",
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{}},
	}))

	sessionRepo := &sessionRepository{db: db}
	session, err := sessionRepo.Create(ctx, &types.Session{
		TenantID: tenantID, UserID: "mysql-user", Title: "MySQL Search",
	})
	require.NoError(t, err)
	messageRepo := &messageRepository{db: db}
	_, err = messageRepo.CreateMessage(ctx, &types.Message{
		ID: "mysql-independent-message", SessionID: session.ID, RequestID: "request",
		Role: "user", Content: "CaseInsensitive Needle", KnowledgeReferences: types.References{},
	})
	require.NoError(t, err)
	messages, err := messageRepo.SearchMessagesByKeyword(ctx, tenantID, "needle", nil, 5)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	knowledgeRepo := &knowledgeRepository{db: db}
	require.NoError(t, knowledgeRepo.CreateKnowledge(ctx, &types.Knowledge{
		ID: "mysql-independent-knowledge", TenantID: tenantID, KnowledgeBaseID: kbID,
		Type: "file", Title: "document", Source: "manual",
		Metadata: types.JSON("{\"external.id\":\"1679\"}"),
	}))
	knowledge, err := knowledgeRepo.FindByMetadataKey(ctx, tenantID, kbID, "external.id", "1679")
	require.NoError(t, err)
	require.NotNil(t, knowledge)

	taskRepo := &taskPendingOpsRepository{db: db}
	require.NoError(t, taskRepo.Enqueue(ctx, &types.TaskPendingOp{
		TenantID: tenantID, TaskType: "mysql", Scope: "knowledge", ScopeID: "1679",
		Op: "rebuild", DedupKey: "independent", EnqueuedAt: time.Now().UTC(),
	}))
	claimed, err := taskRepo.ClaimBatch(ctx, "mysql", "knowledge", "1679", 1, time.Now().UTC().Add(-time.Hour))
	require.NoError(t, err)
	require.NotEmpty(t, claimed)
	count, err := taskRepo.IncrFailCount(ctx, claimed[0].ID)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	settingRepo := &systemSettingRepository{db: db}
	require.NoError(t, settingRepo.Upsert(ctx, &types.SystemSetting{
		Key: "mysql.integration", Value: types.JSON("true"), ValueType: "bool",
		Category: "test", Description: "MySQL reserved-key coverage",
	}))
	setting, err := settingRepo.Get(ctx, "mysql.integration")
	require.NoError(t, err)
	require.NotNil(t, setting)
	deleted, err := settingRepo.Delete(ctx, "mysql.integration")
	require.NoError(t, err)
	require.True(t, deleted)

	wikiRepo := &wikiPageRepository{db: db}
	require.NoError(t, wikiRepo.Create(ctx, &types.WikiPage{
		ID: "mysql-wiki-page", TenantID: tenantID, KnowledgeBaseID: kbID,
		Slug: "mysql-wiki", Title: "MySQL Wiki", PageType: "summary",
		Status: "published", Content: "Portable database search",
	}))
	pages, err := wikiRepo.Search(ctx, kbID, "mysql.*wiki", 10)
	require.NoError(t, err)
	require.Len(t, pages, 1)

	// Nested model references must use MySQL JSON path syntax too. This path
	// is used when deciding whether a model can be safely deleted.
	require.NoError(t, db.Exec(
		"INSERT INTO custom_agents (id, name, tenant_id, config) VALUES (?, ?, ?, ?)",
		"mysql-nested-model-agent", "Nested model", tenantID,
		`{"question_suggestions":{"follow_ups":{"model_id":"nested-mysql-model"}}}`,
	).Error)
	agentRepo := &customAgentRepository{db: db}
	modelRefs, err := agentRepo.CountByModelID(ctx, tenantID, "nested-mysql-model")
	require.NoError(t, err)
	require.EqualValues(t, 1, modelRefs)
}

func TestMySQLTaskQueueConcurrentClaims(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_SQL_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_SQL_DSN is not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{NowFunc: func() time.Time { return time.Now().UTC() }})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)

	ctx := context.Background()
	const tenantID uint64 = 1679002
	const scopeID = "concurrent"
	db.Exec("DELETE FROM task_pending_ops WHERE tenant_id = ?", tenantID)
	t.Cleanup(func() { db.Exec("DELETE FROM task_pending_ops WHERE tenant_id = ?", tenantID) })

	repo := &taskPendingOpsRepository{db: db}
	for i := 0; i < 4; i++ {
		require.NoError(t, repo.Enqueue(ctx, &types.TaskPendingOp{
			TenantID: tenantID, TaskType: "mysql-concurrency", Scope: "knowledge", ScopeID: scopeID,
			Op: "rebuild", DedupKey: fmt.Sprintf("key-%d", i), EnqueuedAt: time.Now().UTC(),
		}))
	}

	start := make(chan struct{})
	results := make(chan []*types.TaskPendingOp, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := repo.ClaimBatch(ctx, "mysql-concurrency", "knowledge", scopeID, 2, time.Now().UTC().Add(-time.Hour))
			results <- claimed
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	seen := make(map[int64]bool)
	for batch := range results {
		for _, op := range batch {
			require.False(t, seen[op.ID], "task %d claimed twice", op.ID)
			seen[op.ID] = true
		}
	}
	require.Len(t, seen, 4)
}

func TestMySQLTaskQueueConcurrentFailCount(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_SQL_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_SQL_DSN is not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(16)

	ctx := context.Background()
	const tenantID uint64 = 1679003
	db.Exec("DELETE FROM task_pending_ops WHERE tenant_id = ?", tenantID)
	t.Cleanup(func() { db.Exec("DELETE FROM task_pending_ops WHERE tenant_id = ?", tenantID) })
	repo := &taskPendingOpsRepository{db: db}
	require.NoError(t, repo.Enqueue(ctx, &types.TaskPendingOp{
		TenantID: tenantID, TaskType: "mysql-fail-count", Scope: "knowledge", ScopeID: "atomic",
		Op: "rebuild", DedupKey: "atomic", EnqueuedAt: time.Now().UTC(),
	}))
	var op types.TaskPendingOp
	require.NoError(t, db.Where("tenant_id = ?", tenantID).First(&op).Error)

	const increments = 12
	var wg sync.WaitGroup
	errs := make(chan error, increments)
	for i := 0; i < increments; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.IncrFailCount(ctx, op.ID)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, db.First(&op, op.ID).Error)
	require.Equal(t, increments, op.FailCount)
}
