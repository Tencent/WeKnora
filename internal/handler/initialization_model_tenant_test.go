package handler

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// The initialization endpoint's model creation used to insert rows without a
// tenant: toModel() carries no TenantID, so models landed at tenant_id = 0
// where the (tenant_id = ? OR is_builtin) read filter made them invisible to
// every tenant — including the one that just configured the KB — leaving the
// KB bound to unreadable model IDs (issue #3333).

type stubTenantStampModelService struct {
	interfaces.ModelService
	created      []*types.Model
	updated      []*types.Model
	getModelByID func(ctx context.Context, id string) (*types.Model, error)
}

func (s *stubTenantStampModelService) GetModelByID(ctx context.Context, id string) (*types.Model, error) {
	if s.getModelByID != nil {
		return s.getModelByID(ctx, id)
	}
	return nil, nil // default: force the create path
}

func (s *stubTenantStampModelService) CreateModel(ctx context.Context, model *types.Model) error {
	m := *model
	s.created = append(s.created, &m)
	return nil
}

func (s *stubTenantStampModelService) UpdateModel(ctx context.Context, model *types.Model) error {
	m := *model
	s.updated = append(s.updated, &m)
	return nil
}

func newTenantStampRequest() *InitializationRequest {
	req := &InitializationRequest{}
	req.LLM.Source = "remote"
	req.LLM.ModelName = "qwen2.5"
	req.LLM.BaseURL = "http://ollama.internal/v1"
	req.LLM.APIKey = "ollama"
	req.Embedding.Source = "remote"
	req.Embedding.ModelName = "bge-m3"
	req.Embedding.BaseURL = "http://ollama.internal/v1"
	req.Embedding.APIKey = "ollama"
	return req
}

func TestProcessInitializationModelsStampsKBTenantOnCreatedModels(t *testing.T) {
	stub := &stubTenantStampModelService{}
	h := &InitializationHandler{modelService: stub}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 10042}

	models, err := h.processInitializationModels(context.Background(), kb, "kb-1", newTenantStampRequest())
	if err != nil {
		t.Fatalf("processInitializationModels: %v", err)
	}
	if len(models) != 2 || len(stub.created) != 2 {
		t.Fatalf("created = %d models (returned %d), want 2", len(stub.created), len(models))
	}
	for _, m := range stub.created {
		if m.TenantID != 10042 {
			t.Fatalf("created model %q has tenant_id = %d, want 10042 (the KB's tenant)", m.Name, m.TenantID)
		}
	}
}

func TestProcessInitializationModelsKeepsExistingModelTenant(t *testing.T) {
	// The reuse path (update of an already-stored model) must not rewrite the
	// stored row's tenant: only freshly created rows get stamped.
	stored := &types.Model{
		ID: "m-existing", Type: types.ModelTypeKnowledgeQA, Name: "old-name",
		TenantID: 10042, Source: types.ModelSourceRemote,
	}
	svc := &stubTenantStampModelService{}
	svc.getModelByID = func(ctx context.Context, id string) (*types.Model, error) {
		if id == "m-existing" {
			return stored, nil
		}
		return nil, nil
	}
	h := &InitializationHandler{modelService: svc}
	kb := &types.KnowledgeBase{
		ID:             "kb-1",
		TenantID:       10042,
		SummaryModelID: "m-existing", // LLM slot reuses; embedding slot creates
	}

	models, err := h.processInitializationModels(context.Background(), kb, "kb-1", newTenantStampRequest())
	if err != nil {
		t.Fatalf("processInitializationModels: %v", err)
	}
	if len(svc.created) != 1 {
		t.Fatalf("created = %d models, want exactly 1 (embedding only)", len(svc.created))
	}
	if svc.created[0].Type != types.ModelTypeEmbedding || svc.created[0].TenantID != 10042 {
		t.Fatalf("unexpected created model: type=%v tenant=%d", svc.created[0].Type, svc.created[0].TenantID)
	}
	foundReuse := false
	for _, m := range models {
		if m.ID == "m-existing" {
			foundReuse = true
		}
	}
	if !foundReuse {
		t.Fatal("existing model not returned by the reuse path")
	}
}
