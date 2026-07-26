package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type folderDeleteTenantRepo struct {
	interfaces.TenantRepository
}

func (folderDeleteTenantRepo) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

func TestProcessKnowledgeListDeleteFinalizesFolder(t *testing.T) {
	folderService := &recordingKnowledgeFolderService{}
	svc := &knowledgeService{
		tenantRepo:    folderDeleteTenantRepo{},
		folderService: folderService,
	}
	payload, err := json.Marshal(types.KnowledgeListDeletePayload{
		TenantID: 1,
		FolderDeleteTarget: &types.FolderDeleteTarget{
			KnowledgeBaseID: "kb-1",
			FolderID:        "folder-1",
		},
	})
	require.NoError(t, err)

	err = svc.ProcessKnowledgeListDelete(
		context.Background(),
		asynq.NewTask(types.TypeKnowledgeListDelete, payload),
	)

	require.NoError(t, err)
	require.Equal(t, []types.FolderDeleteTarget{{
		KnowledgeBaseID: "kb-1",
		FolderID:        "folder-1",
	}}, folderService.recursiveTargets)
}
