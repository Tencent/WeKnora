package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type evaluationCleanupDataset struct {
	interfaces.DatasetService
}

func (evaluationCleanupDataset) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	return nil, nil
}

type evaluationCleanupKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r *evaluationCleanupKnowledgeRepo) GetKnowledgeBatch(
	context.Context, uint64, []string,
) ([]*types.Knowledge, error) {
	if r.knowledge == nil {
		return nil, nil
	}
	return []*types.Knowledge{r.knowledge}, nil
}

type evaluationCleanupKnowledge struct {
	interfaces.KnowledgeService
	repo       *evaluationCleanupKnowledgeRepo
	knowledge  *types.Knowledge
	createErr  error
	deletedIDs []string
}

func (s *evaluationCleanupKnowledge) CreateKnowledgeFromPassageSync(
	context.Context, string, []string, string,
) (*types.Knowledge, error) {
	return s.knowledge, s.createErr
}

func (s *evaluationCleanupKnowledge) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *evaluationCleanupKnowledge) DeleteKnowledgeList(_ context.Context, ids []string) error {
	s.deletedIDs = append(s.deletedIDs, ids...)
	return nil
}

type evaluationCleanupKB struct {
	interfaces.KnowledgeBaseService
	deletedIDs []string
}

func (s *evaluationCleanupKB) DeleteKnowledgeBase(_ context.Context, id string) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return nil
}

func TestEvalDatasetCleansUpTemporaryResources(t *testing.T) {
	processingErr := errors.New("indexing failed")
	for _, tc := range []struct {
		name                 string
		withoutKnowledge     bool
		createErr            error
		expectedKnowledgeIDs []string
	}{
		{
			name: "processing failure", createErr: processingErr,
			expectedKnowledgeIDs: []string{"knowledge-1"},
		},
		{
			name: "processing failure without knowledge", withoutKnowledge: true, createErr: processingErr,
		},
		{
			name: "successful evaluation", expectedKnowledgeIDs: []string{"knowledge-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var knowledge *types.Knowledge
			if !tc.withoutKnowledge {
				knowledge = &types.Knowledge{
					ID: "knowledge-1", TenantID: 7, KnowledgeBaseID: "kb-1",
				}
			}
			knowledgeSvc := &evaluationCleanupKnowledge{
				repo:      &evaluationCleanupKnowledgeRepo{knowledge: knowledge},
				knowledge: knowledge, createErr: tc.createErr,
			}
			kbSvc := &evaluationCleanupKB{}
			svc := &EvaluationService{
				dataset: evaluationCleanupDataset{}, knowledgeService: knowledgeSvc,
				knowledgeBaseService: kbSvc, evaluationMemoryStorage: newEvaluationMemoryStorage(),
			}
			detail := &types.EvaluationDetail{Task: &types.EvaluationTask{
				ID: "task-1", DatasetID: "dataset-1",
			}}
			svc.evaluationMemoryStorage.register(detail)

			err := svc.EvalDataset(types.WithExecutionTenant(t.Context(), 7), detail, "kb-1")

			if tc.createErr != nil {
				require.ErrorIs(t, err, tc.createErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.expectedKnowledgeIDs, knowledgeSvc.deletedIDs)
			require.Equal(t, []string{"kb-1"}, kbSvc.deletedIDs)
		})
	}
}
