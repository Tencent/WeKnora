package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type knowledgeCrossKBMoveRepositoryStub struct {
	interfaces.KnowledgeRepository
	sourceKnowledgeBaseID string
	saved                 *types.Knowledge
	knowledge             *types.Knowledge
	genericSaved          []*types.Knowledge
	genericUpdates        int
	deletedTagRelations   int
	updateOrder           []string
}

func (r *knowledgeCrossKBMoveRepositoryStub) GetKnowledgeByID(
	_ context.Context,
	_ uint64,
	_ string,
) (*types.Knowledge, error) {
	copy := *r.knowledge
	return &copy, nil
}

func (r *knowledgeCrossKBMoveRepositoryStub) UpdateKnowledge(
	_ context.Context,
	knowledge *types.Knowledge,
) error {
	r.genericUpdates++
	r.updateOrder = append(r.updateOrder, "processing")
	copy := *knowledge
	r.genericSaved = append(r.genericSaved, &copy)
	return nil
}

func (r *knowledgeCrossKBMoveRepositoryStub) DeleteKnowledgeTagRelations(
	_ context.Context,
	_ string,
) error {
	r.deletedTagRelations++
	return nil
}

func (r *knowledgeCrossKBMoveRepositoryStub) UpdateKnowledgeForCrossKBMove(
	_ context.Context,
	knowledge *types.Knowledge,
	sourceKnowledgeBaseID string,
) error {
	r.updateOrder = append(r.updateOrder, "cross-kb")
	r.sourceKnowledgeBaseID = sourceKnowledgeBaseID
	copy := *knowledge
	r.saved = &copy
	return nil
}

type knowledgeCrossKBMoveChunkRepositoryStub struct {
	interfaces.ChunkRepository
	movedKnowledgeID string
	movedTargetKBID  string
}

type knowledgeCrossKBMoveUnsupportedRepositoryStub struct {
	interfaces.KnowledgeRepository
	knowledge      *types.Knowledge
	genericUpdates int
}

func (r *knowledgeCrossKBMoveUnsupportedRepositoryStub) GetKnowledgeByID(
	_ context.Context,
	_ uint64,
	_ string,
) (*types.Knowledge, error) {
	copy := *r.knowledge
	return &copy, nil
}

func (r *knowledgeCrossKBMoveUnsupportedRepositoryStub) UpdateKnowledge(
	_ context.Context,
	_ *types.Knowledge,
) error {
	r.genericUpdates++
	return nil
}

func (r *knowledgeCrossKBMoveChunkRepositoryStub) ListChunksByKnowledgeID(
	_ context.Context,
	_ uint64,
	_ string,
) ([]*types.Chunk, error) {
	return []*types.Chunk{}, nil
}

func (r *knowledgeCrossKBMoveChunkRepositoryStub) MoveChunksByKnowledgeID(
	_ context.Context,
	_ uint64,
	knowledgeID string,
	targetKBID string,
) error {
	r.movedKnowledgeID = knowledgeID
	r.movedTargetKBID = targetKBID
	return nil
}

type knowledgeCrossKBMoveTaskEnqueuerStub struct {
	calls int
	task  *asynq.Task
}

func (s *knowledgeCrossKBMoveTaskEnqueuerStub) Enqueue(
	task *asynq.Task,
	_ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	s.calls++
	s.task = task
	return &asynq.TaskInfo{ID: "task-1", Queue: types.QueueDefault}, nil
}

func knowledgeCrossKBMoveTestContext() context.Context {
	return context.WithValue(
		context.Background(),
		types.TenantIDContextKey,
		uint64(7),
	)
}

func knowledgeCrossKBMoveTestKnowledge() *types.Knowledge {
	return &types.Knowledge{
		ID:                   knowledgeFolderMoveTestKnowledgeA,
		TenantID:             7,
		KnowledgeBaseID:      "kb-source",
		Type:                 "file",
		ParseStatus:          types.ParseStatusCompleted,
		EnableStatus:         "enabled",
		FolderID:             knowledgeFolderMoveTestFolderA,
		FolderVersion:        9,
		FolderIndexedVersion: 8,
	}
}

func requireKnowledgeCrossKBMoveFolderReset(
	t *testing.T,
	repo *knowledgeCrossKBMoveRepositoryStub,
) {
	t.Helper()
	require.NotNil(t, repo.saved)
	assert.Equal(t, "kb-source", repo.sourceKnowledgeBaseID)
	assert.Equal(t, "kb-target", repo.saved.KnowledgeBaseID)
	assert.Equal(t, types.KnowledgeFolderRootID, repo.saved.FolderID)
	assert.Equal(t, uint64(1), repo.saved.FolderVersion)
	assert.Zero(t, repo.saved.FolderIndexedVersion)
	assert.Equal(t, 1, repo.deletedTagRelations)
}

func TestMoveKnowledgeReuseVectorsResetsTargetFolderState(t *testing.T) {
	repo := &knowledgeCrossKBMoveRepositoryStub{}
	chunkRepo := &knowledgeCrossKBMoveChunkRepositoryStub{}
	knowledgeSvc := &knowledgeService{
		repo:      repo,
		chunkRepo: chunkRepo,
	}
	knowledge := knowledgeCrossKBMoveTestKnowledge()

	err := knowledgeSvc.moveKnowledgeReuseVectors(
		knowledgeCrossKBMoveTestContext(),
		knowledge,
		&types.KnowledgeBase{ID: "kb-source", TenantID: 7},
		&types.KnowledgeBase{ID: "kb-target", TenantID: 7},
	)

	require.NoError(t, err)
	requireKnowledgeCrossKBMoveFolderReset(t, repo)
	assert.Zero(t, repo.genericUpdates)
	assert.Equal(t, types.ParseStatusCompleted, repo.saved.ParseStatus)
	assert.Equal(t, knowledge.ID, chunkRepo.movedKnowledgeID)
	assert.Equal(t, "kb-target", chunkRepo.movedTargetKBID)
}

func TestMoveKnowledgeReparseResetsTargetFolderState(t *testing.T) {
	repo := &knowledgeCrossKBMoveRepositoryStub{}
	taskEnqueuer := &knowledgeCrossKBMoveTaskEnqueuerStub{}
	knowledgeSvc := &knowledgeService{
		repo: repo,
		task: taskEnqueuer,
	}
	knowledge := knowledgeCrossKBMoveTestKnowledge()
	knowledge.ParseStatus = types.ManualKnowledgeStatusDraft
	knowledge.StorageSize = 0
	knowledge.FileName = "source.pdf"
	knowledge.FilePath = "tenant/source.pdf"

	err := knowledgeSvc.moveKnowledgeReparse(
		knowledgeCrossKBMoveTestContext(),
		knowledge,
		&types.KnowledgeBase{ID: "kb-source", TenantID: 7},
		&types.KnowledgeBase{
			ID:               "kb-target",
			TenantID:         7,
			EmbeddingModelID: "target-embedding",
		},
	)

	require.NoError(t, err)
	requireKnowledgeCrossKBMoveFolderReset(t, repo)
	assert.Zero(t, repo.genericUpdates)
	assert.Equal(t, types.ParseStatusPending, repo.saved.ParseStatus)
	assert.Equal(t, "target-embedding", repo.saved.EmbeddingModelID)
	require.Equal(t, 1, taskEnqueuer.calls)
	require.NotNil(t, taskEnqueuer.task)
	assert.Equal(t, types.TypeDocumentProcess, taskEnqueuer.task.Type())
	var payload types.DocumentProcessPayload
	require.NoError(t, json.Unmarshal(taskEnqueuer.task.Payload(), &payload))
	assert.Equal(t, uint64(7), payload.TenantID)
	assert.Equal(t, knowledge.ID, payload.KnowledgeID)
	assert.Equal(t, "kb-target", payload.KnowledgeBaseID)
	assert.Equal(t, knowledge.FilePath, payload.FilePath)
	assert.Equal(t, knowledge.FileName, payload.FileName)
}

func TestMoveOneKnowledgeKeepsProcessingTransitionBeforeFolderReset(t *testing.T) {
	repo := &knowledgeCrossKBMoveRepositoryStub{
		knowledge: knowledgeCrossKBMoveTestKnowledge(),
	}
	chunkRepo := &knowledgeCrossKBMoveChunkRepositoryStub{}
	knowledgeSvc := &knowledgeService{
		repo:      repo,
		chunkRepo: chunkRepo,
	}

	err := knowledgeSvc.moveOneKnowledge(
		knowledgeCrossKBMoveTestContext(),
		repo.knowledge.ID,
		&types.KnowledgeBase{ID: "kb-source", TenantID: 7},
		&types.KnowledgeBase{ID: "kb-target", TenantID: 7},
		"reuse_vectors",
	)

	require.NoError(t, err)
	require.Equal(t, 1, repo.genericUpdates)
	require.Len(t, repo.genericSaved, 1)
	assert.Equal(t, types.ParseStatusProcessing, repo.genericSaved[0].ParseStatus)
	assert.Equal(t, "kb-source", repo.genericSaved[0].KnowledgeBaseID)
	assert.Equal(t, knowledgeFolderMoveTestFolderA, repo.genericSaved[0].FolderID)
	assert.Equal(t, uint64(9), repo.genericSaved[0].FolderVersion)
	assert.Equal(t, uint64(8), repo.genericSaved[0].FolderIndexedVersion)
	requireKnowledgeCrossKBMoveFolderReset(t, repo)
	assert.Equal(t, types.ParseStatusCompleted, repo.saved.ParseStatus)
	assert.Equal(t, []string{"processing", "cross-kb"}, repo.updateOrder)
}

func TestMoveOneKnowledgeRejectsMissingCrossKBFolderResetBeforeMutation(
	t *testing.T,
) {
	repo := &knowledgeCrossKBMoveUnsupportedRepositoryStub{
		knowledge: knowledgeCrossKBMoveTestKnowledge(),
	}
	knowledgeSvc := &knowledgeService{repo: repo}

	err := knowledgeSvc.moveOneKnowledge(
		knowledgeCrossKBMoveTestContext(),
		repo.knowledge.ID,
		&types.KnowledgeBase{ID: "kb-source", TenantID: 7},
		&types.KnowledgeBase{ID: "kb-target", TenantID: 7},
		"reparse",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "folder reset is unavailable")
	assert.Zero(t, repo.genericUpdates)
	assert.Equal(t, types.ParseStatusCompleted, repo.knowledge.ParseStatus)
	assert.Equal(t, knowledgeFolderMoveTestFolderA, repo.knowledge.FolderID)
	assert.Equal(t, uint64(9), repo.knowledge.FolderVersion)
	assert.Equal(t, uint64(8), repo.knowledge.FolderIndexedVersion)
}
