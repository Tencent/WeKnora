package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// statusOnlyDSRepo fails the test if pause/resume take the full-row Update
// path: a full write from the handler's stale snapshot rolls back the cursor,
// last_sync_at, result and error message an in-flight sync persisted after
// the snapshot was read.
type statusOnlyDSRepo struct {
	interfaces.DataSourceRepository
	ds          *types.DataSource
	updateCalls int
	statuses    []string
}

func (r *statusOnlyDSRepo) FindByID(context.Context, string) (*types.DataSource, error) {
	return r.ds, nil
}

func (r *statusOnlyDSRepo) Update(context.Context, *types.DataSource) error {
	r.updateCalls++
	return errors.New("pause/resume must not write the whole data source")
}

func (r *statusOnlyDSRepo) UpdateStatus(_ context.Context, _, status string) error {
	r.statuses = append(r.statuses, status)
	r.ds.Status = status
	return nil
}

func TestPauseAndResumeWriteOnlyTheStatus(t *testing.T) {
	repo := &statusOnlyDSRepo{ds: &types.DataSource{
		ID: "ds-1", TenantID: 1, KnowledgeBaseID: "kb-1",
		Status:         types.DataSourceStatusActive,
		LastSyncCursor: types.JSON(`{"offset":5}`),
	}}
	svc := &DataSourceService{
		dsRepo:    repo,
		scheduler: datasource.NewScheduler(repo, nil, nil),
	}
	ctx := context.Background()

	require.NoError(t, svc.PauseDataSource(ctx, "ds-1"))
	require.Equal(t, []string{types.DataSourceStatusPaused}, repo.statuses)
	require.Zero(t, repo.updateCalls, "a full-row update would roll back sync progress")

	require.NoError(t, svc.ResumeDataSource(ctx, "ds-1"))
	require.Equal(t, []string{types.DataSourceStatusPaused, types.DataSourceStatusActive}, repo.statuses)
	require.Zero(t, repo.updateCalls)
}
