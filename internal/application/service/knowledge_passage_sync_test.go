package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type syncPassageFailureRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r *syncPassageFailureRepo) CreateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	r.knowledge = knowledge
	return nil
}

func (r *syncPassageFailureRepo) GetKnowledgeByID(
	_ context.Context, _ uint64, _ string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *syncPassageFailureRepo) UpdateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	r.knowledge = knowledge
	return nil
}

type syncPassageFailureModels struct {
	interfaces.ModelService
	err error
}

func (m *syncPassageFailureModels) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return nil, m.err
}

func TestCreateKnowledgeFromPassageSyncReturnsProcessingFailure(t *testing.T) {
	processingErr := errors.New("embedding model unavailable")
	repo := &syncPassageFailureRepo{}
	svc := &knowledgeService{
		repo: repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
			ID: "kb-1", EmbeddingModelID: "embedding-1",
			IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
		}},
		modelService: &syncPassageFailureModels{err: processingErr},
	}

	knowledge, err := svc.CreateKnowledgeFromPassageSync(
		newCreateKnowledgeFileContext(), "kb-1", []string{"passage"}, "",
	)

	require.Error(t, err)
	require.ErrorContains(t, err, processingErr.Error())
	require.NotNil(t, knowledge)
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
}
