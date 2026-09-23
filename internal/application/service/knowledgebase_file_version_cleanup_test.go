package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type deletedKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r deletedKBRepo) GetKnowledgeBaseIncludingDeleted(context.Context, string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

type plainKBRepo struct {
	interfaces.KnowledgeBaseRepository
}

func TestKnowledgeBaseForVersionCleanupUsesDeletedRow(t *testing.T) {
	backend := "backend-1"
	svc := &knowledgeBaseService{repo: deletedKBRepo{kb: &types.KnowledgeBase{
		ID:                    "kb-1",
		TenantID:              7,
		StorageProviderConfig: &types.StorageProviderConfig{Provider: "cos"},
		StorageBackendID:      &backend,
	}}}
	got := svc.knowledgeBaseForVersionCleanup(context.Background(), "kb-1", 7, types.KBDeletePayload{
		StorageProvider: "local",
	})
	require.Equal(t, "cos", got.GetStorageProvider())
	require.Equal(t, "backend-1", *got.StorageBackendID)
}

func TestKnowledgeBaseForVersionCleanupFallsBackToSnapshot(t *testing.T) {
	backend := "backend-2"
	svc := &knowledgeBaseService{repo: plainKBRepo{}}
	got := svc.knowledgeBaseForVersionCleanup(context.Background(), "kb-1", 7, types.KBDeletePayload{
		StorageProvider:  "cos",
		StorageBackendID: &backend,
	})
	require.Equal(t, "cos", got.GetStorageProvider())
	require.Equal(t, "backend-2", *got.StorageBackendID)
}
