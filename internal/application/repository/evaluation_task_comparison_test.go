package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEvaluationTaskRepositoryGetTasksByIDsIsOneTenantScopedQuery(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		tenantID uint64
		taskID   string
	}{
		{tenantID: 71, taskID: "task-a"},
		{tenantID: 71, taskID: "task-b"},
		{tenantID: 72, taskID: "task-hidden"},
	} {
		task := newListTask(fixture.tenantID, fixture.taskID, now, types.EvaluationStatueSuccess)
		task.LeaseExpiresAt = nil
		task.Metric = types.JSON(`{"retrieval_metrics":{"precision":0.5}}`)
		require.NoError(t, db.Create(task).Error)
	}

	tasks, err := repo.GetTasksByIDs(context.Background(), 71, []string{
		"task-b", "task-a", "task-hidden", "task-missing",
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"task-a", "task-b"}, mapKeys(tasks))
	require.JSONEq(t, `{"retrieval_metrics":{"precision":0.5}}`, tasks["task-a"].Metric.ToString())
}

func mapKeys(values map[string]*types.EvaluationTaskEntity) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
