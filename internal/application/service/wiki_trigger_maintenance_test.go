package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiTriggerMaintenance_ReenqueuesMissingTrigger(t *testing.T) {
	db := setupHousekeepingDB(t)
	pendingRepo := repository.NewTaskPendingOpsRepository(db)
	insertWikiPendingOp(t, db, "kb-missing", "kid-1")
	insertWikiPendingOp(t, db, "kb-queued", "kid-2")
	enqueuer := &fakeTaskEnqueuer{}

	runner := NewWikiTriggerMaintenanceRunner(pendingRepo, fakeTaskInspector{
		wikiQueued: map[string]bool{"kb-queued": true},
	}, enqueuer)
	runner.sweep(context.Background())

	require.Len(t, enqueuer.tasks, 1)
	assert.Equal(t, "wiki:ingest", enqueuer.tasks[0].Type())
}

func TestWikiTriggerMaintenance_ProbeErrorDoesNotEnqueue(t *testing.T) {
	db := setupHousekeepingDB(t)
	pendingRepo := repository.NewTaskPendingOpsRepository(db)
	insertWikiPendingOp(t, db, "kb-unknown", "kid-1")
	enqueuer := &fakeTaskEnqueuer{}

	runner := NewWikiTriggerMaintenanceRunner(pendingRepo, fakeTaskInspector{
		wikiErr: errors.New("redis down"),
	}, enqueuer)
	runner.sweep(context.Background())

	assert.Empty(t, enqueuer.tasks)
}
