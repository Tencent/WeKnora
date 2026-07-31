package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errStopAfterPlacementPreservingUpdate = errors.New("stop after placement-preserving update")

type staleFolderKnowledgeRepository struct {
	interfaces.KnowledgeRepository
	loads             int
	regularUpdates    int
	preservingUpdates int
	processingStarts  int
}

func (r *staleFolderKnowledgeRepository) GetKnowledgeByID(
	_ context.Context,
	_ uint64,
	_ string,
) (*types.Knowledge, error) {
	r.loads++
	folderID := "folder-deleted"
	if r.loads > 1 {
		folderID = types.DocumentFolderRootID
	}
	return &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusFailed,
		FolderID:        folderID,
	}, nil
}

func (r *staleFolderKnowledgeRepository) UpdateKnowledge(
	_ context.Context,
	_ *types.Knowledge,
) error {
	r.regularUpdates++
	return nil
}

func (r *staleFolderKnowledgeRepository) UpdateKnowledgePreservingFolder(
	_ context.Context,
	_ *types.Knowledge,
) error {
	r.preservingUpdates++
	return errStopAfterPlacementPreservingUpdate
}

func (r *staleFolderKnowledgeRepository) StartKnowledgeProcessing(
	_ context.Context,
	knowledge *types.Knowledge,
) error {
	r.processingStarts++
	knowledge.FolderID = types.DocumentFolderRootID
	return errStopAfterPlacementPreservingUpdate
}

type processFolderRaceTenantRepository struct {
	interfaces.TenantRepository
}

func (processFolderRaceTenantRepository) GetTenantByID(
	context.Context,
	uint64,
) (*types.Tenant, error) {
	return &types.Tenant{ID: 1}, nil
}

type processFolderRaceKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
}

func (processFolderRaceKnowledgeBaseService) GetKnowledgeBaseByID(
	context.Context,
	string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "kb-1", TenantID: 1}, nil
}

func TestProcessDocumentPreservesFolderMovedAfterWorkerLoad(t *testing.T) {
	repo := &staleFolderKnowledgeRepository{}
	service := &knowledgeService{
		repo:       repo,
		tenantRepo: processFolderRaceTenantRepository{},
		kbService:  processFolderRaceKnowledgeBaseService{},
	}
	payload, err := json.Marshal(types.DocumentProcessPayload{
		TenantID:        1,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
	})
	require.NoError(t, err)

	err = service.ProcessDocument(context.Background(), asynq.NewTask(types.TypeDocumentProcess, payload))

	require.NoError(t, err)
	assert.Equal(t, 2, repo.loads, "the abort check should observe the document after it moved to root")
	assert.Zero(t, repo.regularUpdates, "a stale worker snapshot must never write folder_id")
	assert.Zero(t, repo.preservingUpdates)
	assert.Equal(t, 1, repo.processingStarts)
}

func TestProcessManualUpdatePreservesFolderMovedAfterWorkerLoad(t *testing.T) {
	repo := &staleFolderKnowledgeRepository{}
	service := &knowledgeService{
		repo:       repo,
		tenantRepo: processFolderRaceTenantRepository{},
		kbService:  processFolderRaceKnowledgeBaseService{},
	}
	payload, err := json.Marshal(types.ManualProcessPayload{
		TenantID:        1,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
	})
	require.NoError(t, err)

	err = service.ProcessManualUpdate(context.Background(), asynq.NewTask(types.TypeManualProcess, payload))

	require.NoError(t, err)
	assert.Equal(t, 2, repo.loads, "the abort check should observe the document after it moved to root")
	assert.Zero(t, repo.regularUpdates, "a stale worker snapshot must never write folder_id")
	assert.Zero(t, repo.preservingUpdates)
	assert.Equal(t, 1, repo.processingStarts)
}
