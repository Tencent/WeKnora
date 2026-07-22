package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type batchFolderWorkerRepo struct {
	interfaces.KnowledgeRepository
	rows map[string]*types.Knowledge
}

func (r *batchFolderWorkerRepo) GetKnowledgeBatch(
	_ context.Context, tenantID uint64, ids []string,
) ([]*types.Knowledge, error) {
	out := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		if row := r.rows[id]; row != nil && row.TenantID == tenantID {
			out = append(out, row)
		}
	}
	return out, nil
}

type batchFolderWorkerTenantRepo struct{ interfaces.TenantRepository }

func (batchFolderWorkerTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: 1}, nil
}

type batchFolderWorkerScope struct {
	interfaces.KnowledgeFolderService
	ids []string
}

func (s *batchFolderWorkerScope) ResolveKnowledgeScope(
	context.Context, string, []string,
) (*types.FolderKnowledgeScope, error) {
	return &types.FolderKnowledgeScope{KnowledgeIDs: append([]string(nil), s.ids...)}, nil
}

func batchFolderDeleteTask(t *testing.T, payload any) *asynq.Task {
	t.Helper()
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return asynq.NewTask(types.TypeKnowledgeListDelete, data)
}

func TestProcessKnowledgeListDeleteBatchFolderUsesLiveScopeAndExplicitSnapshot(t *testing.T) {
	repo := &batchFolderWorkerRepo{rows: map[string]*types.Knowledge{
		"explicit":  {ID: "explicit", TenantID: 1, KnowledgeBaseID: "kb-1"},
		"moved-in":  {ID: "moved-in", TenantID: 1, KnowledgeBaseID: "kb-1"},
		"moved-out": {ID: "moved-out", TenantID: 1, KnowledgeBaseID: "kb-1"},
	}}
	scope := &batchFolderWorkerScope{ids: []string{"moved-in"}}
	var deleted []string
	svc := &knowledgeService{
		repo: repo, tenantRepo: batchFolderWorkerTenantRepo{}, folderService: scope,
		deleteKnowledgeListHook: func(_ context.Context, ids []string) error {
			deleted = append([]string(nil), ids...)
			return nil
		},
		finalizeFoldersHook: func(context.Context, string, []string) error { return nil },
	}
	payload := types.KnowledgeListDeletePayload{
		TenantID: 1, KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"explicit"}, FolderIDs: []string{"folder"},
	}
	require.NoError(t, svc.ProcessKnowledgeListDelete(context.Background(), batchFolderDeleteTask(t, payload)))
	require.ElementsMatch(t, []string{"explicit", "moved-in"}, deleted)
	require.NotContains(t, deleted, "moved-out", "a document moved out after enqueue must not be deleted")
}

func TestProcessKnowledgeListDeleteBatchFolderRetryIsFinalizeOnly(t *testing.T) {
	repo := &batchFolderWorkerRepo{rows: map[string]*types.Knowledge{
		"doc": {ID: "doc", TenantID: 1, KnowledgeBaseID: "kb-1"},
	}}
	scope := &batchFolderWorkerScope{ids: []string{"doc"}}
	deleteCalls, finalizeCalls := 0, 0
	transient := errors.New("transient finalize failure")
	svc := &knowledgeService{
		repo: repo, tenantRepo: batchFolderWorkerTenantRepo{}, folderService: scope,
		deleteKnowledgeListHook: func(_ context.Context, ids []string) error {
			deleteCalls++
			for _, id := range ids {
				delete(repo.rows, id)
			}
			return nil
		},
		finalizeFoldersHook: func(context.Context, string, []string) error {
			finalizeCalls++
			if finalizeCalls == 1 {
				return transient
			}
			return nil
		},
	}
	task := batchFolderDeleteTask(t, types.KnowledgeListDeletePayload{TenantID: 1, KnowledgeBaseID: "kb-1", FolderIDs: []string{"folder"}})
	require.ErrorIs(t, svc.ProcessKnowledgeListDelete(context.Background(), task), transient)
	require.NoError(t, svc.ProcessKnowledgeListDelete(context.Background(), task))
	require.Equal(t, 1, deleteCalls, "retry must not rerun external deletion after rows are gone")
	require.Equal(t, 2, finalizeCalls)
}

func TestProcessKnowledgeListDeleteLegacyPayloadCompatibility(t *testing.T) {
	repo := &batchFolderWorkerRepo{rows: map[string]*types.Knowledge{
		"legacy": {ID: "legacy", TenantID: 1, KnowledgeBaseID: "kb-legacy"},
	}}
	var deleted []string
	svc := &knowledgeService{
		repo: repo, tenantRepo: batchFolderWorkerTenantRepo{},
		deleteKnowledgeListHook: func(_ context.Context, ids []string) error {
			deleted = append([]string(nil), ids...)
			return nil
		},
	}
	task := asynq.NewTask(types.TypeKnowledgeListDelete, []byte(`{"tenant_id":1,"knowledge_ids":["legacy"]}`))
	require.NoError(t, svc.ProcessKnowledgeListDelete(context.Background(), task))
	require.Equal(t, []string{"legacy"}, deleted)
}

func TestProcessKnowledgeListDeleteBatchFolderKnowledgeFailureSkipsFinalize(t *testing.T) {
	repo := &batchFolderWorkerRepo{rows: map[string]*types.Knowledge{
		"doc": {ID: "doc", TenantID: 1, KnowledgeBaseID: "kb-1"},
	}}
	scope := &batchFolderWorkerScope{ids: []string{"doc"}}
	deleteErr := errors.New("delete pipeline failed")
	finalizeCalls := 0
	svc := &knowledgeService{
		repo: repo, tenantRepo: batchFolderWorkerTenantRepo{}, folderService: scope,
		deleteKnowledgeListHook: func(context.Context, []string) error { return deleteErr },
		finalizeFoldersHook: func(context.Context, string, []string) error {
			finalizeCalls++
			return nil
		},
	}
	task := batchFolderDeleteTask(t, types.KnowledgeListDeletePayload{TenantID: 1, KnowledgeBaseID: "kb-1", FolderIDs: []string{"folder"}})
	require.ErrorIs(t, svc.ProcessKnowledgeListDelete(context.Background(), task), deleteErr)
	require.Zero(t, finalizeCalls)
}
