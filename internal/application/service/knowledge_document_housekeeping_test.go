package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestHousekeepingRecognizesDurableDocumentBacklog(t *testing.T) {
	db := setupHousekeepingDB(t)
	insertKnowledge(t, db, "doc-backlog", types.ParseStatusProcessing, time.Now().Add(-3*time.Hour))
	require.NoError(t, db.Create(&types.TaskPendingOp{
		TenantID: 1, TaskType: types.TypeDocumentProcess, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: "kb-1", Op: "process", DedupKey: "doc-backlog", Payload: []byte("{}"),
	}).Error)
	svc := newHousekeepingSvcWithInspector(db, &queuePassCountingInspector{})
	got, err := svc.QueuedWork(context.Background(), []string{"doc-backlog", "absent"})
	require.NoError(t, err)
	require.True(t, got["doc-backlog"])
	require.False(t, got["absent"])
	var candidates []types.Knowledge
	require.NoError(t, db.Where("id=?", "doc-backlog").Find(&candidates).Error)
	orphans, skipped, wiki := svc.filterOutQueued(context.Background(), candidates)
	require.Empty(t, orphans)
	require.Equal(t, 1, skipped)
	require.Empty(t, wiki, "document work must not generate Wiki ingest triggers")
}
