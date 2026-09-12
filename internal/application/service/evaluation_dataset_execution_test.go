package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type evaluationExecutionDatasetStub struct {
	calls int
}

func (s *evaluationExecutionDatasetStub) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	s.calls++
	return []*types.QAPair{{QID: 99, Question: "legacy"}}, nil
}

type evaluationExecutionRegistryStub struct {
	content          *types.EvaluationDatasetVersionContent
	requestedTenant  uint64
	requestedVersion string
}

func (*evaluationExecutionRegistryStub) ImportDataset(
	context.Context, uint64, *types.EvaluationDatasetImportInput,
) (*types.EvaluationDatasetImportResult, error) {
	return nil, interfaces.ErrEvaluationDatasetInvalid
}

func (*evaluationExecutionRegistryStub) CreateDataset(
	context.Context, uint64, string, string,
) (*types.EvaluationDataset, error) {
	return nil, interfaces.ErrEvaluationDatasetInvalid
}

func (*evaluationExecutionRegistryStub) CreateVersion(
	context.Context, uint64, string, *types.EvaluationDatasetVersionInput,
) (*types.EvaluationDatasetVersion, error) {
	return nil, interfaces.ErrEvaluationDatasetInvalid
}

func (*evaluationExecutionRegistryStub) GetDataset(
	context.Context, uint64, string,
) (*types.EvaluationDataset, error) {
	return nil, interfaces.ErrEvaluationDatasetNotFound
}

func (*evaluationExecutionRegistryStub) ListDatasets(
	context.Context, uint64,
) ([]*types.EvaluationDataset, error) {
	return nil, nil
}

func (*evaluationExecutionRegistryStub) ListVersions(
	context.Context, uint64, string,
) ([]*types.EvaluationDatasetVersion, error) {
	return nil, nil
}

func (*evaluationExecutionRegistryStub) GetVersion(
	context.Context, uint64, string,
) (*types.EvaluationDatasetVersion, error) {
	return nil, interfaces.ErrEvaluationDatasetVersionNotFound
}

func (s *evaluationExecutionRegistryStub) GetVersionContent(
	_ context.Context, tenantID uint64, datasetVersionID string,
) (*types.EvaluationDatasetVersionContent, error) {
	s.requestedTenant = tenantID
	s.requestedVersion = datasetVersionID
	return s.content, nil
}

func (*evaluationExecutionRegistryStub) RegisterBuiltinDataset(
	context.Context, *types.EvaluationBuiltinDatasetRegistration,
) (*types.EvaluationDatasetVersion, error) {
	return nil, interfaces.ErrEvaluationDatasetInvalid
}

func evaluationExecutionContentFixture() *types.EvaluationDatasetVersionContent {
	input := &types.EvaluationDatasetVersionInput{
		Passages: []types.EvaluationDatasetPassageInput{
			{PID: "passage-a", Content: "alpha"},
			{PID: "passage-b", Content: "beta"},
			{PID: "passage-c", Content: "irrelevant"},
		},
		Questions: []types.EvaluationDatasetQuestionInput{
			{QID: "question-a", Question: "first?", Answer: "alpha"},
			{QID: "question-b", Question: "second?", Answer: "beta"},
		},
		Relevance: []types.EvaluationDatasetRelevanceInput{
			{QID: "question-a", PID: "passage-b", Grade: 2},
			{QID: "question-b", PID: "passage-a", Grade: 1},
		},
	}
	hash := types.CanonicalEvaluationDatasetContentSHA256(input)
	return &types.EvaluationDatasetVersionContent{
		Version: &types.EvaluationDatasetVersion{
			ID: "version-1", DatasetID: "dataset-1", ContentSHA256: hash,
			PassageCount: 3, QuestionCount: 2, RelevanceCount: 2,
		},
		Passages: []types.EvaluationDatasetPassage{
			{DatasetVersionID: "version-1", PID: "passage-a", Content: "alpha"},
			{DatasetVersionID: "version-1", PID: "passage-b", Content: "beta"},
			{DatasetVersionID: "version-1", PID: "passage-c", Content: "irrelevant"},
		},
		Questions: []types.EvaluationDatasetQuestion{
			{DatasetVersionID: "version-1", QID: "question-a", SampleIndex: 0, Question: "first?", Answer: "alpha"},
			{DatasetVersionID: "version-1", QID: "question-b", SampleIndex: 1, Question: "second?", Answer: "beta"},
		},
		Relevance: []types.EvaluationDatasetRelevance{
			{DatasetVersionID: "version-1", QID: "question-a", PID: "passage-b", Grade: 2},
			{DatasetVersionID: "version-1", QID: "question-b", PID: "passage-a", Grade: 1},
		},
	}
}

func TestEvaluationLoadsFrozenDatasetVersionForExecution(t *testing.T) {
	content := evaluationExecutionContentFixture()
	legacy := &evaluationExecutionDatasetStub{}
	registry := &evaluationExecutionRegistryStub{content: content}
	service := &EvaluationService{dataset: legacy, datasetRegistry: registry}
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{TenantID: 7, DatasetID: "dataset-1"},
		Experiment: &types.EvaluationExperimentSnapshot{Dataset: types.EvaluationDatasetSnapshot{
			DatasetID: "dataset-1", DatasetVersionID: "version-1", ContentSHA256: content.Version.ContentSHA256,
		}},
	}

	pairs, err := service.loadEvaluationDataset(context.Background(), detail)
	require.NoError(t, err)
	require.Len(t, pairs, 2)
	assert.Zero(t, legacy.calls)
	assert.Equal(t, uint64(7), registry.requestedTenant)
	assert.Equal(t, "version-1", registry.requestedVersion)
	assert.Equal(t, "question-a", pairs[0].DatasetQID)
	assert.Equal(t, []int{1}, pairs[0].PIDs)
	assert.Equal(t, []string{"beta"}, pairs[0].Passages)
	assert.Equal(t, "question-b", pairs[1].DatasetQID)
	assert.Equal(t, []int{0}, pairs[1].PIDs)
	assert.Equal(t, []string{"alpha", "beta", "irrelevant"}, getPassageList(pairs))
}

func TestEvaluationRejectsFrozenDatasetContentHashDrift(t *testing.T) {
	content := evaluationExecutionContentFixture()
	content.Passages[0].Content = "tampered"
	service := &EvaluationService{
		dataset:         &evaluationExecutionDatasetStub{},
		datasetRegistry: &evaluationExecutionRegistryStub{content: content},
	}
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{TenantID: 7, DatasetID: "dataset-1"},
		Experiment: &types.EvaluationExperimentSnapshot{Dataset: types.EvaluationDatasetSnapshot{
			DatasetID: "dataset-1", DatasetVersionID: "version-1", ContentSHA256: content.Version.ContentSHA256,
		}},
	}

	_, err := service.loadEvaluationDataset(context.Background(), detail)
	require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetInvalid)
}
