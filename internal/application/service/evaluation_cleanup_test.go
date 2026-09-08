package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type failingEvaluationDataset struct{}

func (failingEvaluationDataset) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	return nil, errors.New("dataset unavailable")
}

type recordingEvaluationKBService struct {
	interfaces.KnowledgeBaseService
	created []*types.KnowledgeBase
	deleted []string
}

func (s *recordingEvaluationKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID:               id,
		EmbeddingModelID: "embedding-1",
		SummaryModelID:   "summary-1",
	}, nil
}

func (s *recordingEvaluationKBService) CreateKnowledgeBase(_ context.Context, kb *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	s.created = append(s.created, kb)
	created := *kb
	created.ID = "evaluation-kb"
	return &created, nil
}

func (s *recordingEvaluationKBService) DeleteKnowledgeBase(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

type emptyEvaluationModelService struct {
	interfaces.ModelService
}

func (emptyEvaluationModelService) ListModels(context.Context) ([]*types.Model, error) {
	return nil, nil
}

func TestEvaluationCleansKnowledgeBaseWhenSetupFails(t *testing.T) {
	kbService := &recordingEvaluationKBService{}
	svc := &EvaluationService{
		knowledgeBaseService: kbService,
		modelService:         emptyEvaluationModelService{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, err := svc.Evaluation(ctx, "default", "reference-kb", "", "")
	if err == nil {
		t.Fatal("Evaluation() error = nil, want missing chat model error")
	}
	if len(kbService.created) != 1 || !kbService.created[0].IsTemporary {
		t.Fatalf("created KBs = %#v, want one temporary evaluation KB", kbService.created)
	}
	if len(kbService.deleted) != 1 || kbService.deleted[0] != "evaluation-kb" {
		t.Fatalf("deleted KBs = %v, want [evaluation-kb]", kbService.deleted)
	}
}

func TestEvalDatasetCleansKnowledgeBaseWhenDatasetLoadFails(t *testing.T) {
	kbService := &recordingEvaluationKBService{}
	svc := &EvaluationService{
		dataset:              failingEvaluationDataset{},
		knowledgeBaseService: kbService,
	}
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        "evaluation-test",
			DatasetID: "default",
		},
	}

	err := svc.EvalDataset(context.Background(), detail, "evaluation-kb")
	if err == nil {
		t.Fatal("EvalDataset() error = nil, want dataset load error")
	}
	if len(kbService.deleted) != 1 || kbService.deleted[0] != "evaluation-kb" {
		t.Fatalf("deleted KBs = %v, want [evaluation-kb]", kbService.deleted)
	}
}
