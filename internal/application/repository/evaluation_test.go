package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newEvaluationTestRepository(t *testing.T) *evaluationRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&types.EvaluationDataset{}, &types.EvaluationSample{}, &types.EvaluationRun{}, &types.EvaluationRunResult{}); err != nil {
		t.Fatal(err)
	}
	return &evaluationRepository{db: db}
}

func TestEvaluationRepositoryTenantIsolationSoftDeleteAndRunSnapshot(t *testing.T) {
	repo := newEvaluationTestRepository(t)
	ctx := context.Background()
	dataset := &types.EvaluationDataset{TenantID: 1, Name: "dataset"}
	if err := repo.CreateDataset(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetDataset(ctx, 2, dataset.ID); err != gorm.ErrRecordNotFound {
		t.Fatalf("cross-tenant read error = %v", err)
	}
	sample := &types.EvaluationSample{TenantID: 1, DatasetID: dataset.ID, Question: "q", ReferenceAnswer: "a", ReferenceContexts: types.JSON("[]")}
	if err := repo.CreateSample(ctx, sample); err != nil {
		t.Fatal(err)
	}
	run := &types.EvaluationRun{ID: "run", TenantID: 1, DatasetID: dataset.ID, DatasetName: dataset.Name, Status: types.EvaluationRunPending, ConfigSnapshot: types.JSON("{}"), AggregateMetricScores: types.JSON("{}"), TotalSamples: 1}
	result := &types.EvaluationRunResult{TenantID: 1, RunID: run.ID, SampleID: sample.ID, Question: sample.Question, ReferenceAnswer: sample.ReferenceAnswer, ReferenceContexts: sample.ReferenceContexts, RetrievedContexts: types.JSON("[]"), MetricScores: types.JSON("{}"), Status: types.EvaluationResultPending}
	if err := repo.CreateRunWithResults(ctx, run, []*types.EvaluationRunResult{result}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteDataset(ctx, 1, dataset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetDataset(ctx, 1, dataset.ID); err != gorm.ErrRecordNotFound {
		t.Fatalf("soft-deleted dataset error = %v", err)
	}
	history, err := repo.ListAllRunResults(ctx, 1, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Question != "q" || history[0].ReferenceAnswer != "a" {
		t.Fatalf("frozen history was changed: %#v", history)
	}
}
