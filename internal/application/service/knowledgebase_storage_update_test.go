package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type storageUpdateKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb    types.KnowledgeBase
	saves int
}

func (r *storageUpdateKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	kb := r.kb
	return &kb, nil
}

func (r *storageUpdateKBRepo) UpdateKnowledgeBase(_ context.Context, kb *types.KnowledgeBase) error {
	r.kb = *kb
	r.saves++
	return nil
}

type storageUpdateKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	t     *testing.T
	total int64
	err   error
	calls int
}

func (r *storageUpdateKnowledgeRepo) CountKnowledgeByKnowledgeBaseID(
	_ context.Context, tenantID uint64, id string,
) (int64, error) {
	require.Equal(r.t, uint64(1), tenantID)
	require.Equal(r.t, "kb-1", id)
	r.calls++
	return r.total, r.err
}

type storageUpdateResolver struct {
	interfaces.StorageBackendResolver
	err   error
	calls int
}

func (r *storageUpdateResolver) ResolveBackend(
	_ context.Context, tenant *types.Tenant, id, _ string,
) (*types.StorageBackend, error) {
	r.calls++
	return &types.StorageBackend{ID: id, TenantID: tenant.ID, Provider: "s3"}, r.err
}

func TestUpdateKnowledgeBaseStorageBinding(t *testing.T) {
	countErr := errors.New("cannot count documents")
	for _, tc := range []struct {
		name         string
		oldID        string
		nextID       string
		callerTenant uint64
		total        int64
		countErr     error
		resolveErr   error
		wantHTTP     int
		wantCounts   int
		wantResolves int
	}{
		{name: "empty KB", oldID: "old", nextID: "new", callerTenant: 1, wantCounts: 1, wantResolves: 1},
		{name: "initial binding", nextID: "new", callerTenant: 1, wantCounts: 1, wantResolves: 1},
		{
			name: "existing files", oldID: "old", nextID: "new", callerTenant: 1,
			total: 1, wantHTTP: http.StatusBadRequest, wantCounts: 1,
		},
		{
			name: "legacy files", nextID: "new", callerTenant: 1,
			total: 1, wantHTTP: http.StatusBadRequest, wantCounts: 1,
		},
		{
			name: "count failure", oldID: "old", nextID: "new", callerTenant: 1,
			countErr: countErr, wantCounts: 1,
		},
		{
			name: "same binding with files", oldID: "old", nextID: " old ", callerTenant: 1, total: 1,
		},
		{
			name: "share admin echo", oldID: "old", nextID: "old", callerTenant: 2, total: 1,
		},
		{
			name: "share admin rebind", oldID: "old", nextID: "new", callerTenant: 2,
			wantHTTP: http.StatusForbidden,
		},
		{
			name: "unavailable backend", oldID: "old", nextID: "new", callerTenant: 1,
			resolveErr: errors.New("not found"), wantHTTP: http.StatusBadRequest, wantCounts: 1, wantResolves: 1,
		},
		{name: "empty ID", oldID: "old", callerTenant: 1, wantHTTP: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := types.KnowledgeBase{ID: "kb-1", TenantID: 1, Name: "before"}
			stored.SetStorageProvider("local")
			if tc.oldID != "" {
				stored.StorageBackendID = &tc.oldID
			}
			repo := &storageUpdateKBRepo{kb: stored}
			knowledge := &storageUpdateKnowledgeRepo{t: t, total: tc.total, err: tc.countErr}
			resolver := &storageUpdateResolver{err: tc.resolveErr}
			svc := &knowledgeBaseService{repo: repo, kgRepo: knowledge, storageResolver: resolver}
			ctx := types.WithCaller(context.Background(), types.Caller{TenantID: tc.callerTenant})
			ctx = types.WithExecutionTenant(ctx, 1)
			ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: tc.callerTenant})
			updated, err := svc.UpdateKnowledgeBase(ctx, stored.ID, "after", "", &types.KnowledgeBaseConfig{
				StorageBackendID: &tc.nextID,
			}, nil)
			if tc.countErr != nil || tc.wantHTTP != 0 {
				require.Error(t, err)
				require.Nil(t, updated)
				require.Zero(t, repo.saves)
				require.Equal(t, stored, repo.kb, "a rejected update must not change persisted configuration")
				if tc.countErr != nil {
					require.ErrorIs(t, err, tc.countErr)
				} else {
					appErr, ok := apperrors.IsAppError(err)
					require.True(t, ok)
					require.Equal(t, tc.wantHTTP, appErr.HTTPCode)
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, repo.saves)
				require.Equal(t, "after", updated.Name)
				if tc.wantResolves > 0 {
					require.Equal(t, tc.nextID, *updated.StorageBackendID)
					require.Equal(t, "s3", updated.GetStorageProvider())
				} else {
					require.Equal(t, stored.StorageBackendID, updated.StorageBackendID)
					require.Equal(t, "local", updated.GetStorageProvider())
				}
			}
			require.Equal(t, tc.wantCounts, knowledge.calls)
			require.Equal(t, tc.wantResolves, resolver.calls)
		})
	}
}
