package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMySQLPrimaryRepositoryQueries(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	ctx := context.Background()
	const tenantID uint64 = 1679001
	t.Cleanup(func() {
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
	session, err := sessionRepo.Create(ctx, &types.Session{TenantID: tenantID, UserID: "mysql-user", Title: "MySQL Search"})
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
		ID: "mysql-independent-knowledge", TenantID: tenantID, KnowledgeBaseID: "kb",
		Type: "file", Title: "document", Source: "manual", Metadata: types.JSON(`{"external_id":"1679"}`),
	}))
	knowledge, err := knowledgeRepo.FindByMetadataKey(ctx, tenantID, "kb", "external_id", "1679")
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
}
