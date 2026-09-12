package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluationTaskRepositoryFiltersLabelsWithANDSemantics(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC)

	for _, taskID := range []string{"both", "baseline-only", "other-tenant"} {
		tenantID := uint64(81)
		if taskID == "other-tenant" {
			tenantID = 82
		}
		task := newListTask(tenantID, taskID, now, types.EvaluationStatueSuccess)
		task.DatasetID = "dataset-a"
		versionID := "version-a"
		task.DatasetVersionID = &versionID
		task.ExperimentSnapshot = types.JSON(`{"models":{"chat":{"id":"chat-a"}}}`)
		hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		task.DatasetContentSHA256 = &hash
		task.ExperimentSHA256 = &hash
		task.LeaseExpiresAt = nil
		require.NoError(t, db.Create(task).Error)
	}
	require.NoError(t, repo.ReplaceTaskLabels(ctx, 81, "both", []string{"baseline", "retrieval"}, now))
	require.NoError(t, repo.ReplaceTaskLabels(ctx, 81, "baseline-only", []string{"baseline"}, now))
	require.NoError(t, repo.ReplaceTaskLabels(ctx, 82, "other-tenant", []string{"baseline", "retrieval"}, now))

	tasks, err := repo.ListTasks(ctx, 81, types.EvaluationTaskListQuery{
		DatasetID:        "dataset-a",
		DatasetVersionID: "version-a",
		ModelID:          "chat-a",
		Labels:           []string{"baseline", "retrieval"},
		Limit:            10,
	})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "both", tasks[0].ID)
	labels, err := repo.ListTaskLabels(ctx, 81, []string{tasks[0].ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"baseline", "retrieval"}, labels[tasks[0].ID])
}

func TestEvaluationTaskRepositoryReplaceLabelsPreservesTaskVersionAndUpdatedAt(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	task := newListTask(83, "labels", now, types.EvaluationStatueSuccess)
	task.Version = 17
	task.UpdatedAt = now.Add(-time.Hour)
	task.LeaseExpiresAt = nil
	require.NoError(t, db.Create(task).Error)

	require.NoError(t, repo.ReplaceTaskLabels(ctx, task.TenantID, task.ID, []string{"b", "a"}, now))
	require.NoError(t, repo.ReplaceTaskLabels(ctx, task.TenantID, task.ID, []string{"c"}, now.Add(time.Minute)))

	labels, err := repo.ListTaskLabels(ctx, task.TenantID, []string{task.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"c"}, labels[task.ID])

	var persisted types.EvaluationTaskEntity
	require.NoError(t, db.Where("tenant_id = ? AND id = ?", task.TenantID, task.ID).First(&persisted).Error)
	assert.Equal(t, uint64(17), persisted.Version)
	assert.Equal(t, task.UpdatedAt, persisted.UpdatedAt)

	err = repo.ReplaceTaskLabels(ctx, 84, task.ID, []string{"hidden"}, now)
	require.ErrorIs(t, err, ErrEvaluationTaskNotFound)
}
