package repository

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newEvaluationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.EvaluationRun{}))
	return db
}

func testEvaluationRun(taskID string, tenantID uint64) *types.EvaluationRun {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &types.EvaluationRun{
		TaskID: taskID, TenantID: tenantID, DatasetID: "dataset-v1",
		EmbeddingModelID: "embedding-safe", ChatModelID: "chat-safe",
		Status: types.EvaluationStatuePending, CreatedAt: now, UpdatedAt: now,
		ConfigSnapshot: types.EvaluationConfigSnapshotV1{
			SnapshotSchemaVersion: 1,
			Dataset:               types.EvaluationDatasetSnapshot{DatasetID: "dataset-v1"},
			Pipeline: types.EvaluationPipelineSnapshot{
				Name: "rag", Metrics: []string{"precision", "recall"}, NDCGCutoffs: []int{3, 10},
			},
			Retrieval: types.EvaluationRetrievalSnapshot{
				VectorThreshold: .2, KeywordThreshold: .3, EmbeddingTopK: 30,
				RerankTopK: 20, RerankThreshold: .4, RetrieveDriver: "postgres",
			},
			Models: types.EvaluationModelsSnapshot{EmbeddingModelID: "embedding-safe", ChatModelID: "chat-safe"},
		},
	}
}

func testMetric() *types.MetricResult {
	return &types.MetricResult{
		RetrievalMetrics: types.RetrievalMetrics{Precision: .1, Recall: .2, NDCG3: .3, NDCG10: .4, MRR: .5, MAP: .6},
		GenerationMetrics: types.GenerationMetrics{
			BLEU1: .7, BLEU2: .8, BLEU4: .9, ROUGE1: .11, ROUGE2: .22, ROUGEL: .33,
		},
	}
}

func TestEvaluationRunRepositoryLifecycleAndRestart(t *testing.T) {
	db := newEvaluationTestDB(t)
	ctx := context.Background()
	repoA := NewEvaluationRunRepository(db)
	run := testEvaluationRun("task-lifecycle", 7)
	require.NoError(t, repoA.Create(ctx, run))
	require.NotEmpty(t, run.ID)

	pending, err := repoA.GetByTaskID(ctx, 7, run.TaskID)
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatuePending, pending.Status)
	require.Nil(t, pending.Precision)

	started := time.Now().UTC().Add(-time.Second)
	require.NoError(t, repoA.MarkRunning(ctx, 7, run.TaskID, started))
	require.NoError(t, repoA.UpdateTotal(ctx, 7, run.TaskID, 3))
	for range 3 {
		require.NoError(t, repoA.IncrementFinished(ctx, 7, run.TaskID))
	}
	require.NoError(t, repoA.MarkSuccess(ctx, 7, run.TaskID, testMetric(), time.Now().UTC()))

	// A distinct repository instance proves the result is not process-local.
	repoB := NewEvaluationRunRepository(db)
	completed, err := repoB.GetByTaskID(ctx, 7, run.TaskID)
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatueSuccess, completed.Status)
	require.Equal(t, 3, completed.Total)
	require.Equal(t, 3, completed.Finished)
	require.NotNil(t, completed.DurationMS)
	require.InDelta(t, .1, *completed.Precision, 1e-12)
	require.InDelta(t, .2, *completed.Recall, 1e-12)
	require.InDelta(t, .3, *completed.NDCG3, 1e-12)
	require.InDelta(t, .4, *completed.NDCG10, 1e-12)
	require.InDelta(t, .5, *completed.MRR, 1e-12)
	require.InDelta(t, .6, *completed.MAP, 1e-12)
	require.InDelta(t, .7, *completed.BLEU1, 1e-12)
	require.InDelta(t, .8, *completed.BLEU2, 1e-12)
	require.InDelta(t, .9, *completed.BLEU4, 1e-12)
	require.InDelta(t, .11, *completed.ROUGE1, 1e-12)
	require.InDelta(t, .22, *completed.ROUGE2, 1e-12)
	require.InDelta(t, .33, *completed.ROUGEL, 1e-12)
	assert.Equal(t, 1, completed.ConfigSnapshot.SnapshotSchemaVersion)
	assert.Equal(t, "postgres", completed.ConfigSnapshot.Retrieval.RetrieveDriver)
}

func TestEvaluationConfigSnapshotV11RoundTrip(t *testing.T) {
	db := newEvaluationTestDB(t)
	repo := NewEvaluationRunRepository(db)
	run := testEvaluationRun("task-v1-1-snapshot", 12)
	run.ConfigSnapshot = types.EvaluationConfigSnapshotV1{
		SnapshotSchemaVersion:    1,
		BenchmarkContractVersion: types.BenchmarkContractVersionV11,
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
			EmbeddingModelID: "embedding-safe", ChatModelID: "chat-safe", SummaryModelID: "summary-safe",
			Embedding: &types.EvaluationConfiguredModelSnapshot{
				ID: "embedding-safe", Name: "Embedding", Type: "embedding", Source: "openai",
				Provider: "openai", InterfaceType: "openai",
				Embedding: &types.EvaluationEmbeddingSnapshot{
					Dimension: 1024, TruncatePromptTokens: 8192, SupportsDimensionOverride: true,
				},
			},
			Chat: &types.EvaluationConfiguredModelSnapshot{
				ID: "chat-safe", Name: "Chat", Type: "knowledge_qa", Source: "openai",
				Provider: "openai", InterfaceType: "openai",
			},
			Summary: &types.EvaluationConfiguredModelSnapshot{
				ID: "summary-safe", Name: "Summary", Type: "knowledge_qa", Source: "openai",
				Provider: "openai", InterfaceType: "openai",
			},
		},
		Execution: types.EvaluationExecutionSnapshot{WorkerLimit: 3},
	}
	require.NoError(t, repo.Create(context.Background(), run))

	stored, err := repo.GetByTaskID(context.Background(), 12, run.TaskID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, types.BenchmarkContractVersionV11, stored.ConfigSnapshot.BenchmarkContractVersion)
	require.Equal(t, strings.Repeat("a", 64), stored.ConfigSnapshot.Dataset.DatasetSemanticSHA256)
	require.Equal(t, 32, stored.ConfigSnapshot.Dataset.CorpusCount)
	require.Equal(t, 15, stored.ConfigSnapshot.Dataset.QuestionCount)
	require.Equal(t, 15, stored.ConfigSnapshot.Dataset.QrelsCount)
	require.Equal(t, 15, stored.ConfigSnapshot.Dataset.AnswerCount)
	require.Equal(t, "pre_chunked_passages", stored.ConfigSnapshot.Dataset.CorpusMode)
	require.False(t, stored.ConfigSnapshot.Dataset.ChunkingApplied)
	require.Equal(t, 3, stored.ConfigSnapshot.Execution.WorkerLimit)
	require.Equal(t, "builtin", stored.ConfigSnapshot.Pipeline.Tokenizer.DictionaryMode)
	require.Equal(t, "embedding-safe", stored.ConfigSnapshot.Models.Embedding.ID)
	require.Equal(t, 1024, stored.ConfigSnapshot.Models.Embedding.Embedding.Dimension)
}

func TestEvaluationRunRepositoryFailedAndTerminalProtection(t *testing.T) {
	db := newEvaluationTestDB(t)
	repo := NewEvaluationRunRepository(db)
	ctx := context.Background()
	run := testEvaluationRun("task-failed", 8)
	require.NoError(t, repo.Create(ctx, run))
	require.NoError(t, repo.MarkRunning(ctx, 8, run.TaskID, time.Now().Add(-time.Second)))
	require.NoError(t, repo.MarkFailed(ctx, 8, run.TaskID, nil, "provider unavailable", time.Now()))

	require.Error(t, repo.IncrementFinished(ctx, 8, run.TaskID))
	require.Error(t, repo.MarkSuccess(ctx, 8, run.TaskID, testMetric(), time.Now()))
	failed, err := repo.GetByTaskID(ctx, 8, run.TaskID)
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatueFailed, failed.Status)
	require.Equal(t, "provider unavailable", failed.ErrorMessage)
	require.Nil(t, failed.Precision)
}

func TestEvaluationRunRepositoryAtomicProgressAndTenantIsolation(t *testing.T) {
	db := newEvaluationTestDB(t)
	repo := NewEvaluationRunRepository(db)
	ctx := context.Background()
	run := testEvaluationRun("task-concurrent", 9)
	require.NoError(t, repo.Create(ctx, run))
	require.NoError(t, repo.MarkRunning(ctx, 9, run.TaskID, time.Now()))

	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.IncrementFinished(ctx, 9, run.TaskID)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, err := repo.GetByTaskID(ctx, 9, run.TaskID)
	require.NoError(t, err)
	require.Equal(t, workers, got.Finished)

	other, err := repo.GetByTaskID(ctx, 10, run.TaskID)
	require.NoError(t, err)
	require.Nil(t, other)
	require.Error(t, repo.UpdateTotal(ctx, 10, run.TaskID, 99))
}

func TestEvaluationRunRepositoryRejectsMalformedMetrics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{name: "nan", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "negative", value: -.01},
		{name: "over one", value: 1.01},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newEvaluationTestDB(t)
			repo := NewEvaluationRunRepository(db)
			run := testEvaluationRun("task-invalid", 11)
			require.NoError(t, repo.Create(context.Background(), run))
			require.NoError(t, repo.MarkRunning(context.Background(), 11, run.TaskID, time.Now()))
			metric := testMetric()
			metric.RetrievalMetrics.Precision = tc.value
			require.Error(t, repo.MarkSuccess(context.Background(), 11, run.TaskID, metric, time.Now()))
			stored, err := repo.GetByTaskID(context.Background(), 11, run.TaskID)
			require.NoError(t, err)
			require.Equal(t, types.EvaluationStatueRunning, stored.Status)
			require.Nil(t, stored.Precision)
		})
	}
}

func TestEvaluationConfigSnapshotSecretExclusion(t *testing.T) {
	snapshot := types.EvaluationConfigSnapshotV1{
		SnapshotSchemaVersion: 1,
		Models:                types.EvaluationModelsSnapshot{EmbeddingModelID: "embedding", ChatModelID: "chat"},
	}
	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{
		"api_key", "app_secret", "authorization", "cookie", "custom_headers", "provider_response",
	} {
		require.NotContains(t, lower, forbidden)
	}
}

func TestEvaluationMigrationParity(t *testing.T) {
	postgres, err := os.ReadFile("../../../migrations/versioned/000091_evaluation_runs.up.sql")
	require.NoError(t, err)
	sqliteDDL, err := os.ReadFile("../../../migrations/sqlite/000013_evaluation_runs.up.sql")
	require.NoError(t, err)
	for _, column := range []string{
		"task_id", "tenant_id", "dataset_id", "source_knowledge_base_id", "embedding_model_id",
		"rerank_model_id", "chat_model_id", "status", "total", "finished", "precision", "recall",
		"ndcg_3", "ndcg_10", "mrr", "map", "bleu_1", "bleu_2", "bleu_4", "rouge_1",
		"rouge_2", "rouge_l", "config_snapshot", "started_at", "finished_at", "duration_ms",
		"error_message", "created_at", "updated_at",
	} {
		require.Contains(t, string(postgres), column)
		require.Contains(t, string(sqliteDDL), column)
	}
	require.Contains(t, string(postgres), "JSONB")
	require.Contains(t, string(sqliteDDL), "config_snapshot TEXT")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(sqliteDDL)).Error)
	require.True(t, db.Migrator().HasTable("evaluation_runs"))
}

func TestEvaluationRunRepositoryPostgreSQLTenantScopedRead(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DisableAutomaticPing: true})
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT .* FROM "evaluation_runs" WHERE tenant_id = \$1 AND task_id = \$2.*LIMIT \$3`).
		WithArgs(uint64(77), "task-pg", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "tenant_id"}))
	run, err := NewEvaluationRunRepository(db).GetByTaskID(context.Background(), 77, "task-pg")
	require.NoError(t, err)
	require.Nil(t, run)
	mock.ExpectClose()
	require.NoError(t, sqlDB.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}
