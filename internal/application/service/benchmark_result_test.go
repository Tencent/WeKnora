package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type benchmarkEvaluationRepositoryStub struct {
	tenantID uint64
	taskID   string
	run      *types.EvaluationRun
}

func (s *benchmarkEvaluationRepositoryStub) Create(context.Context, *types.EvaluationRun) error {
	return nil
}

func (s *benchmarkEvaluationRepositoryStub) GetByTaskID(
	_ context.Context, tenantID uint64, taskID string,
) (*types.EvaluationRun, error) {
	if tenantID != s.tenantID || taskID != s.taskID {
		return nil, nil
	}
	return s.run, nil
}

func (s *benchmarkEvaluationRepositoryStub) MarkRunning(context.Context, uint64, string, time.Time) error {
	return nil
}

func (s *benchmarkEvaluationRepositoryStub) UpdateTotal(context.Context, uint64, string, int) error {
	return nil
}

func (s *benchmarkEvaluationRepositoryStub) IncrementFinished(context.Context, uint64, string) error {
	return nil
}

func (s *benchmarkEvaluationRepositoryStub) MarkSuccess(
	context.Context, uint64, string, *types.MetricResult, time.Time,
) error {
	return nil
}

func (s *benchmarkEvaluationRepositoryStub) MarkFailed(
	context.Context, uint64, string, *types.MetricResult, string, time.Time,
) error {
	return nil
}

type benchmarkUsageRepositoryStub struct {
	tenantID uint64
	runID    string
	result   *types.EvaluationModelUsageAggregate
}

func (s *benchmarkUsageRepositoryStub) Create(context.Context, *types.ModelUsage) error { return nil }

func (s *benchmarkUsageRepositoryStub) GetByID(context.Context, uint64, string) (*types.ModelUsage, error) {
	return nil, nil
}

func (s *benchmarkUsageRepositoryStub) AggregateEvaluationRun(
	_ context.Context, tenantID uint64, runID string,
) (*types.EvaluationModelUsageAggregate, error) {
	s.tenantID, s.runID = tenantID, runID
	return s.result, nil
}

func (s *benchmarkUsageRepositoryStub) AggregateAnalytics(
	context.Context, uint64, types.ModelUsageAnalyticsQuery,
) (*types.ModelUsageAnalyticsResult, error) {
	return nil, nil
}

func completeBenchmarkSnapshot() types.EvaluationConfigSnapshotV1 {
	model := func(id string) *types.EvaluationConfiguredModelSnapshot {
		return &types.EvaluationConfiguredModelSnapshot{
			ID: id, Name: id, Type: "test", Source: string(types.ModelSourceRemote),
			EndpointFingerprint: strings.Repeat("b", 64),
		}
	}
	embedding := model("embedding")
	embedding.Embedding = &types.EvaluationEmbeddingSnapshot{Dimension: 1024}
	return types.EvaluationConfigSnapshotV1{
		SnapshotSchemaVersion: 1, BenchmarkContractVersion: types.BenchmarkContractVersionV11,
		Dataset: types.EvaluationDatasetSnapshot{
			DatasetID: "benchmark_v1", DatasetSemanticSHA256: strings.Repeat("a", 64),
			CorpusCount: 32, QuestionCount: 15, QrelsCount: 15, AnswerCount: 15,
			CorpusMode: "pre_chunked_passages", ChunkingApplied: false,
		},
		Pipeline: types.EvaluationPipelineSnapshot{
			Name: "rag", Metrics: []string{"precision", "recall", "ndcg", "mrr", "map", "bleu", "rouge"},
			NDCGCutoffs: []int{3, 10},
			Tokenizer:   types.EvaluationTokenizerSnapshot{Name: "jieba", DictionaryMode: "builtin"},
		},
		Models: types.EvaluationModelsSnapshot{
			EmbeddingModelID: "embedding", ChatModelID: "chat", SummaryModelID: "summary",
			Embedding: embedding, Chat: model("chat"), Summary: model("summary"),
		},
		Execution: types.EvaluationExecutionSnapshot{WorkerLimit: 2},
	}
}

func TestValidateBenchmarkReproducibilitySnapshot(t *testing.T) {
	require.NoError(t, validateBenchmarkReproducibilitySnapshot(completeBenchmarkSnapshot()))

	tests := []struct {
		name   string
		mutate func(*types.EvaluationConfigSnapshotV1)
		want   string
	}{
		{name: "bad hash length", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Dataset.DatasetSemanticSHA256 = "abc"
		}, want: "64 lowercase hexadecimal"},
		{name: "non hex hash", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Dataset.DatasetSemanticSHA256 = strings.Repeat("g", 64)
		}, want: "64 lowercase hexadecimal"},
		{name: "custom tokenizer without fingerprint", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Pipeline.Tokenizer.DictionaryMode = "custom"
		}, want: "custom tokenizer dictionary fingerprint"},
		{name: "remote model without endpoint fingerprint", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Models.Chat.EndpointFingerprint = ""
		}, want: "remote chat model endpoint fingerprint"},
		{name: "missing model", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Models.Summary = nil
		}, want: "summary model snapshot is missing"},
		{name: "missing model identity", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Models.Chat.Name = ""
		}, want: "chat model identity is incomplete"},
		{name: "missing embedding parameters", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Models.Embedding.Embedding = nil
		}, want: "embedding model parameters are missing"},
		{name: "zero worker", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Execution.WorkerLimit = 0
		}, want: "worker limit must be positive"},
		{name: "missing pipeline name", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Pipeline.Name = ""
		}, want: "pipeline name is required"},
		{name: "wrong metrics", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Pipeline.Metrics = []string{"recall", "precision"}
		}, want: "metric suite"},
		{name: "wrong cutoffs", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Pipeline.NDCGCutoffs = []int{10, 3}
		}, want: "NDCG cutoffs"},
		{name: "qrels do not cover questions", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Dataset.QrelsCount = 14
		}, want: "qrels count"},
		{name: "answers do not cover questions", mutate: func(s *types.EvaluationConfigSnapshotV1) {
			s.Dataset.AnswerCount = 14
		}, want: "answer count"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := completeBenchmarkSnapshot()
			tc.mutate(&snapshot)
			err := validateBenchmarkReproducibilitySnapshot(snapshot)
			require.ErrorContains(t, err, tc.want)
			require.Equal(t, types.BenchmarkReproducibilityLegacyUnknown, benchmarkReproducibility(snapshot))
		})
	}

	custom := completeBenchmarkSnapshot()
	custom.Pipeline.Tokenizer.DictionaryMode = "custom"
	custom.Pipeline.Tokenizer.DictionaryFingerprint = strings.Repeat("c", 64)
	require.NoError(t, validateBenchmarkReproducibilitySnapshot(custom))
	require.Equal(t, types.BenchmarkReproducibilityComplete, benchmarkReproducibility(custom))

	legacy := types.EvaluationConfigSnapshotV1{SnapshotSchemaVersion: 1}
	require.Error(t, validateBenchmarkReproducibilitySnapshot(legacy))
	require.Equal(t, types.BenchmarkReproducibilityLegacyUnknown, benchmarkReproducibility(legacy))
}

func benchmarkRun(status types.EvaluationStatue) *types.EvaluationRun {
	now := time.Now().UTC()
	duration := int64(1234)
	run := &types.EvaluationRun{
		ID: "run-1", TaskID: "task-1", TenantID: 7, DatasetID: "benchmark_v1",
		Status: status, Total: 15, Finished: 15, ConfigSnapshot: completeBenchmarkSnapshot(),
		CreatedAt: now, StartedAt: &now, FinishedAt: &now, DurationMS: &duration,
	}
	if status == types.EvaluationStatueSuccess {
		value := 0.5
		run.Precision, run.Recall, run.NDCG3, run.NDCG10, run.MRR, run.MAP =
			&value, &value, &value, &value, &value, &value
		run.BLEU1, run.BLEU2, run.BLEU4, run.ROUGE1, run.ROUGE2, run.ROUGEL =
			&value, &value, &value, &value, &value, &value
	}
	return run
}

func TestBenchmarkResultServiceQualityAndOperationalFacts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      types.EvaluationStatue
		wantState   types.BenchmarkQualityState
		wantQuality bool
	}{
		{name: "pending", status: types.EvaluationStatuePending, wantState: types.BenchmarkQualityStatePending},
		{name: "running", status: types.EvaluationStatueRunning, wantState: types.BenchmarkQualityStatePending},
		{
			name: "success", status: types.EvaluationStatueSuccess,
			wantState: types.BenchmarkQualityStateComplete, wantQuality: true,
		},
		{name: "failed", status: types.EvaluationStatueFailed, wantState: types.BenchmarkQualityStateUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := benchmarkRun(tc.status)
			if tc.status != types.EvaluationStatueSuccess {
				run.Finished = 4
			}
			facts := &types.EvaluationModelUsageAggregate{
				Calls:                   types.CallCounts{Total: 2, Chat: 1, Embedding: 1},
				ObservedModels:          []types.ObservedModelIdentity{{CallType: types.CallTypeChat, Calls: 1}},
				CostByCurrency:          []types.CurrencyCostAggregate{{Currency: "CNY"}, {Currency: "USD"}},
				CostRowsWithoutCurrency: types.CallCounts{Total: 1, Chat: 1},
			}
			evaluationRepo := &benchmarkEvaluationRepositoryStub{tenantID: 7, taskID: "task-1", run: run}
			usageRepo := &benchmarkUsageRepositoryStub{result: facts}
			svc := NewBenchmarkResultService(evaluationRepo, usageRepo)
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

			got, err := svc.GetBenchmarkResult(ctx, "task-1")
			require.NoError(t, err)
			require.Equal(t, tc.wantState, got.Quality.State)
			require.Equal(t, tc.wantQuality, got.Quality.Retrieval != nil)
			require.Equal(t, tc.wantQuality, got.Quality.Answer != nil)
			require.Same(t, facts, got.ModelFacts)
			require.Equal(t, int64(2), got.ModelFacts.Calls.Total)
			require.Len(t, got.ModelFacts.CostByCurrency, 2)
			require.Equal(t, types.CallCounts{Total: 1, Chat: 1}, got.ModelFacts.CostRowsWithoutCurrency)
			require.Equal(t, run.ID, usageRepo.runID)
			require.Equal(t, uint64(7), usageRepo.tenantID)
			require.Equal(t, types.BenchmarkReproducibilityComplete, got.Reproducibility)
			require.Equal(t, int64(1234), *got.RunWallClockDurationMS)
		})
	}
}

func TestBenchmarkResultServiceSuccessfulRunRequiresAllMetrics(t *testing.T) {
	run := benchmarkRun(types.EvaluationStatueSuccess)
	run.ROUGEL = nil
	svc := NewBenchmarkResultService(
		&benchmarkEvaluationRepositoryStub{tenantID: 7, taskID: "task-1", run: run},
		&benchmarkUsageRepositoryStub{result: &types.EvaluationModelUsageAggregate{}},
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	_, err := svc.GetBenchmarkResult(ctx, "task-1")
	require.ErrorContains(t, err, "does not have all quality metrics")
}

func TestBenchmarkResultServiceLegacyAndTenantIsolation(t *testing.T) {
	run := benchmarkRun(types.EvaluationStatueFailed)
	run.ConfigSnapshot = types.EvaluationConfigSnapshotV1{
		SnapshotSchemaVersion: 1,
		Dataset:               types.EvaluationDatasetSnapshot{DatasetID: "benchmark_v1"},
	}
	repo := &benchmarkEvaluationRepositoryStub{tenantID: 7, taskID: "task-1", run: run}
	svc := NewBenchmarkResultService(repo, &benchmarkUsageRepositoryStub{result: &types.EvaluationModelUsageAggregate{}})

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	got, err := svc.GetBenchmarkResult(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, types.BenchmarkReproducibilityLegacyUnknown, got.Reproducibility)
	require.Empty(t, got.BenchmarkVersion)

	wrongTenant := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(8))
	_, err = svc.GetBenchmarkResult(wrongTenant, "task-1")
	require.ErrorContains(t, err, "task not found")
	_, err = svc.GetBenchmarkResult(ctx, "wrong-task")
	require.ErrorContains(t, err, "task not found")
}
