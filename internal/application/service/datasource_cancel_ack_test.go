package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// cancelAckDSRepo scripts the data-source lookup: either the row is gone or it
// still exists and the knowledge-base lookup decides the outcome.
type cancelAckDSRepo struct {
	interfaces.DataSourceRepository
	ds  *types.DataSource
	err error
}

func (r cancelAckDSRepo) FindByID(context.Context, string) (*types.DataSource, error) {
	return r.ds, r.err
}

// cancelAckLogRepo fails the canceled-state write until the test clears err.
type cancelAckLogRepo struct {
	*processSyncSyncLogRepo
	err error
}

func (r *cancelAckLogRepo) Update(ctx context.Context, log *types.SyncLog) error {
	if r.err != nil {
		return r.err
	}
	return r.processSyncSyncLogRepo.Update(ctx, log)
}

type cancelAckKBService struct {
	interfaces.KnowledgeBaseService
	err error
}

func (s cancelAckKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, s.err
}

// A deleted data source or knowledge base cancels the running sync log. If
// writing that terminal state fails, the task must retry instead of
// acknowledging a round whose log still says "running".
func TestProcessSyncCanceledStatePersistenceRetries(t *testing.T) {
	writeErr := errors.New("database is read-only")
	for _, tc := range []struct {
		name    string
		ds      *types.DataSource
		dsErr   error
		kbErr   error
		message string
	}{
		{
			name:    "deleted data source",
			dsErr:   fmt.Errorf("lookup: %w", datasource.ErrDataSourceNotFound),
			message: "data source has been deleted",
		},
		{
			name: "deleted knowledge base",
			ds: &types.DataSource{
				ID: "ds-1", TenantID: 1, KnowledgeBaseID: "kb-1",
			},
			kbErr:   fmt.Errorf("lookup: %w", apprepo.ErrKnowledgeBaseNotFound),
			message: "knowledge base has been deleted",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logRepo := &cancelAckLogRepo{
				processSyncSyncLogRepo: &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{
					"log-1": {
						ID: "log-1", DataSourceID: "ds-1", TenantID: 1,
						Status: types.SyncLogStatusRunning,
					},
				}},
				err: writeErr,
			}
			svc := &DataSourceService{
				dsRepo:      cancelAckDSRepo{ds: tc.ds, err: tc.dsErr},
				syncLogRepo: logRepo,
				kbService:   cancelAckKBService{err: tc.kbErr},
			}
			payload, err := json.Marshal(types.DataSourceSyncPayload{
				DataSourceID: "ds-1", TenantID: 1, SyncLogID: "log-1",
			})
			require.NoError(t, err)
			task := asynq.NewTask(types.TypeDataSourceSync, payload)

			require.ErrorIs(t, svc.ProcessSync(context.Background(), task), writeErr)
			require.Equal(t, types.SyncLogStatusCanceled, logRepo.logs["log-1"].Status,
				"the cancellation is decided before the write fails")

			logRepo.err = nil
			require.NoError(t, svc.ProcessSync(context.Background(), task))

			stored := logRepo.logs["log-1"]
			require.Equal(t, types.SyncLogStatusCanceled, stored.Status)
			require.Equal(t, tc.message, stored.ErrorMessage)
			require.NotNil(t, stored.FinishedAt)
		})
	}
}
