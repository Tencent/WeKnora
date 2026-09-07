package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type finalizeFolderRepoStub struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r *finalizeFolderRepoStub) GetKnowledgeByID(_ context.Context, _ uint64, _ string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func TestFinalizeFolderUploadLeavesStoreOnlyAttachmentsSkipped(t *testing.T) {
	svc := &knowledgeService{repo: &finalizeFolderRepoStub{knowledge: &types.Knowledge{
		ID: "attachment", KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusSkipped,
	}}}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	started, skipped, err := svc.FinalizeFolderUpload(ctx, "kb-1", []string{"attachment"})
	require.NoError(t, err)
	require.Zero(t, started)
	require.Equal(t, 1, skipped)
}
