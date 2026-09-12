package service

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingEvaluationTaskRepository struct {
	interfaces.EvaluationTaskRepository
}

func TestNewEvaluationServiceRequiresPersistentTaskRepository(t *testing.T) {
	repository := &recordingEvaluationTaskRepository{}

	created := NewEvaluationService(nil, nil, nil, nil, nil, nil, repository, nil, nil)
	service, ok := created.(*EvaluationService)
	require.True(t, ok)
	require.Same(t, repository, service.evaluationTaskRepository)
	require.NotEmpty(t, service.ownerID)
	_, err := uuid.Parse(service.ownerID)
	require.NoError(t, err)
}

func TestEvaluationDetailEntityRoundTrip(t *testing.T) {
	detail := newPersistenceEvaluationDetail()
	detail.RuntimeMetrics = &types.EvaluationRuntimeMetrics{
		SchemaVersion: 1,
		StartedAt:     time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC),
		Samples:       types.EvaluationRuntimeSamples{Total: 2, Started: 2, Success: 2},
	}
	leaseExpiresAt := time.Date(2026, time.August, 28, 12, 30, 0, 0, time.UTC)

	entity, err := evaluationDetailToEntity(detail, "temporary-kb-1", "owner-1", leaseExpiresAt)
	require.NoError(t, err)
	require.NotNil(t, entity)
	assert.Equal(t, detail.Task.ID, entity.ID)
	assert.Equal(t, detail.Task.TenantID, entity.TenantID)
	assert.Equal(t, detail.Task.DatasetID, entity.DatasetID)
	assert.Equal(t, detail.Task.Status, entity.Status)
	assert.Equal(t, detail.Task.StartTime, entity.StartTime)
	assert.Equal(t, detail.Task.EndTime, entity.EndTime)
	assert.Equal(t, detail.Task.Total, entity.Total)
	assert.Equal(t, detail.Task.Finished, entity.Finished)
	assert.Equal(t, detail.Task.ErrMsg, entity.ErrMsg)
	require.JSONEq(t, `{
		"schema_version": 1,
		"started_at": "2026-08-28T11:00:00Z",
		"durations": {},
		"samples": {
			"total": 2, "started": 2, "success": 2, "failed": 0,
			"canceled": 0, "interrupted": 0, "not_started": 0
		},
		"failure": {"numerator": 0, "denominator": 0},
		"tokens": {
			"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0,
			"reported_samples": 0, "unreported_samples": 0
		}
	}`, entity.RuntimeMetrics.ToString())
	assert.Equal(t, "temporary-kb-1", entity.TemporaryKnowledgeBaseID)
	assert.Equal(t, "owner-1", entity.OwnerID)
	require.NotNil(t, entity.LeaseExpiresAt)
	assert.Equal(t, leaseExpiresAt, *entity.LeaseExpiresAt)

	decoded, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)
	assert.Equal(t, detail, decoded)
}

func TestEvaluationEntityToDetailTreatsEmptyMetricAsNil(t *testing.T) {
	for _, metric := range []types.JSON{nil, types.JSON(`null`)} {
		entity := newPersistenceEvaluationEntity()
		entity.Metric = metric

		detail, err := evaluationEntityToDetail(entity)
		require.NoError(t, err)
		assert.Nil(t, detail.Metric)
	}
}

func TestEvaluationEntityToDetailRejectsCorruptJSON(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.EvaluationTaskEntity)
	}{
		{
			name: "params",
			mutate: func(entity *types.EvaluationTaskEntity) {
				entity.Params = types.JSON(`{"query":`)
			},
		},
		{
			name: "metric",
			mutate: func(entity *types.EvaluationTaskEntity) {
				entity.Metric = types.JSON(`{"retrieval_metrics":`)
			},
		},
		{
			name: "cleanup_errors",
			mutate: func(entity *types.EvaluationTaskEntity) {
				entity.CleanupErrors = types.JSON(`["warning"`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entity := newPersistenceEvaluationEntity()
			tt.mutate(entity)

			detail, err := evaluationEntityToDetail(entity)
			require.Error(t, err)
			assert.Nil(t, detail)
		})
	}
}

func TestEvaluationEntityToDetailRejectsMissingRequiredExecutionParams(t *testing.T) {
	entity := newPersistenceEvaluationEntity()
	entity.Params = types.JSON(`{}`)

	detail, err := evaluationEntityToDetail(entity)
	require.Error(t, err)
	assert.Nil(t, detail)
	assert.ErrorContains(t, err, "chat_model_id is required")
}

func TestEvaluationPersistenceRejectsUnknownStatus(t *testing.T) {
	t.Run("detail_to_entity", func(t *testing.T) {
		detail := newPersistenceEvaluationDetail()
		detail.Task.Status = types.EvaluationStatue(99)

		entity, err := evaluationDetailToEntity(
			detail,
			"temporary-kb-1",
			"owner-1",
			time.Date(2026, time.August, 28, 12, 30, 0, 0, time.UTC),
		)
		require.Error(t, err)
		assert.Nil(t, entity)
	})

	t.Run("entity_to_detail", func(t *testing.T) {
		entity := newPersistenceEvaluationEntity()
		entity.Status = types.EvaluationStatue(99)

		detail, err := evaluationEntityToDetail(entity)
		require.Error(t, err)
		assert.Nil(t, detail)
	})
}

func TestEvaluationEntityToDetailReturnsIndependentSnapshots(t *testing.T) {
	entity := newPersistenceEvaluationEntity()
	first, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)
	second, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	require.NotNil(t, first.Task.EndTime)
	*first.Task.EndTime = first.Task.EndTime.Add(time.Hour)
	first.Task.CleanupErrors[0] = "changed cleanup"
	first.Params.KnowledgeBaseIDs[0] = "changed-kb"
	first.Metric.RetrievalMetrics.Precision = 0.01

	assert.Equal(t, time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC), *entity.EndTime)
	assert.JSONEq(t, `["cleanup warning"]`, entity.CleanupErrors.ToString())
	assert.JSONEq(t, `{"knowledge_base_ids":["kb-1"],"chat_model_id":"chat-1"}`, entity.Params.ToString())
	assert.JSONEq(
		t,
		`{"retrieval_metrics":{"precision":0.75},"generation_metrics":{"bleu1":0.5}}`,
		entity.Metric.ToString(),
	)

	require.NotNil(t, second.Task.EndTime)
	assert.Equal(t, time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC), *second.Task.EndTime)
	assert.Equal(t, []string{"cleanup warning"}, second.Task.CleanupErrors)
	assert.Equal(t, []string{"kb-1"}, second.Params.KnowledgeBaseIDs)
	assert.Equal(t, 0.75, second.Metric.RetrievalMetrics.Precision)
}

func newPersistenceEvaluationDetail() *types.EvaluationDetail {
	citationEnabled := false
	thinkingEnabled := true
	endTime := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	return &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:            "task-1",
			TenantID:      7,
			DatasetID:     "dataset-1",
			StartTime:     time.Date(2026, time.August, 28, 11, 0, 0, 0, time.UTC),
			EndTime:       &endTime,
			Status:        types.EvaluationStatueSuccess,
			ErrMsg:        "completed with warning",
			CleanupErrors: []string{"cleanup warning"},
			Total:         9,
			Finished:      8,
		},
		Params: &types.ChatManage{
			PipelineRequest: types.PipelineRequest{
				SessionID:           "session-1",
				UserID:              "user-1",
				Query:               "question",
				MaxRounds:           4,
				KnowledgeBaseIDs:    []string{"kb-1", "kb-2"},
				KnowledgeIDs:        []string{"knowledge-1"},
				VectorThreshold:     0.25,
				KeywordThreshold:    0.15,
				EmbeddingTopK:       20,
				VectorDatabase:      "qdrant",
				RerankModelID:       "rerank-1",
				RerankTopK:          6,
				RerankThreshold:     0.4,
				ChatModelID:         "chat-1",
				FallbackStrategy:    types.FallbackStrategyModel,
				FallbackResponse:    "fallback response",
				FallbackPrompt:      "fallback prompt",
				CitationEnabled:     &citationEnabled,
				EnableRewrite:       true,
				RewritePromptSystem: "rewrite system",
				RewritePromptUser:   "rewrite user",
				SummaryConfig: types.SummaryConfig{
					MaxTokens:   512,
					Temperature: 0.2,
					Thinking:    &thinkingEnabled,
				},
			},
			PipelineState: types.PipelineState{
				RewriteQuery: "rewritten question",
				Intent:       types.IntentKBSearch,
			},
		},
		Metric: &types.MetricResult{
			RetrievalMetrics: types.RetrievalMetrics{
				Precision: 0.75,
				Recall:    0.625,
				NDCG3:     0.8,
				NDCG10:    0.9,
				MRR:       0.7,
				MAP:       0.6,
			},
			GenerationMetrics: types.GenerationMetrics{
				BLEU1:  0.5,
				BLEU2:  0.4,
				BLEU4:  0.3,
				ROUGE1: 0.65,
				ROUGE2: 0.55,
				ROUGEL: 0.6,
			},
		},
	}
}

func newPersistenceEvaluationEntity() *types.EvaluationTaskEntity {
	endTime := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	return &types.EvaluationTaskEntity{
		ID:            "task-1",
		TenantID:      7,
		DatasetID:     "dataset-1",
		Status:        types.EvaluationStatueSuccess,
		StartTime:     time.Date(2026, time.August, 28, 11, 0, 0, 0, time.UTC),
		EndTime:       &endTime,
		Total:         1,
		Finished:      1,
		ErrMsg:        "",
		CleanupErrors: types.JSON(`["cleanup warning"]`),
		Params:        types.JSON(`{"knowledge_base_ids":["kb-1"],"chat_model_id":"chat-1"}`),
		Metric:        types.JSON(`{"retrieval_metrics":{"precision":0.75},"generation_metrics":{"bleu1":0.5}}`),
	}
}
