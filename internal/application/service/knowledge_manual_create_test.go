package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCreateKnowledgeFromManualWithoutParent(t *testing.T) {
	t.Parallel()

	repo := &createKnowledgeFileRepoStub{}
	queue := &createKnowledgeTaskEnqueuerStub{}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1}
	kb.StorageProviderConfig = &types.StorageProviderConfig{Provider: "local"}
	svc := &knowledgeService{
		repo: repo, kbService: &createKnowledgeFileKBServiceStub{kb: kb}, task: queue,
	}
	ctx := newCreateKnowledgeFileContext()

	draft, err := svc.CreateKnowledgeFromManual(ctx, "kb-1", &types.ManualKnowledgePayload{
		Title: "Draft guide", Content: "Draft body", Status: "draft",
	}, "")
	require.NoError(t, err)
	require.Empty(t, draft.ParentKnowledgeID)
	require.Equal(t, types.ManualKnowledgeStatusDraft, draft.ParseStatus)

	published, err := svc.CreateKnowledgeFromManual(ctx, "kb-1", &types.ManualKnowledgePayload{
		Title: "Published guide", Content: "Published body", Status: "publish",
	}, "")
	require.NoError(t, err)
	require.Empty(t, published.ParentKnowledgeID)
	require.Equal(t, "pending", published.ParseStatus)
	require.Equal(t, 2, repo.createCalls)
	require.Equal(t, 1, queue.calls)
}
