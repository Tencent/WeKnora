package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type resolveVLMTestKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s resolveVLMTestKnowledgeBaseService) GetKnowledgeBaseByIDOnly(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type resolveVLMTestKnowledgeRepository struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r resolveVLMTestKnowledgeRepository) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

type resolveVLMTestModelService struct {
	interfaces.ModelService
	models map[string]vlm.VLM
	errors map[string]error
	calls  []string
}

func (s *resolveVLMTestModelService) GetVLMModel(_ context.Context, modelID string) (vlm.VLM, error) {
	s.calls = append(s.calls, modelID)
	return s.models[modelID], s.errors[modelID]
}

type resolveVLMTestModel struct{ id string }

func (m resolveVLMTestModel) Predict(context.Context, [][]byte, string) (string, error) {
	return "", nil
}

func (m resolveVLMTestModel) PredictWithOptions(
	context.Context, [][]byte, string, *vlm.PredictOptions,
) (string, error) {
	return "", nil
}

func (m resolveVLMTestModel) GetModelName() string { return m.id }
func (m resolveVLMTestModel) GetModelID() string   { return m.id }

func TestResolveVLMFallsBackToKnowledgeBaseModelWhenDocumentModelUnavailable(t *testing.T) {
	documentErr := errors.New("model not found")
	kbModel := resolveVLMTestModel{id: "kb-model"}
	modelService := &resolveVLMTestModelService{
		models: map[string]vlm.VLM{"kb-model": kbModel},
		errors: map[string]error{"document-model": documentErr},
	}
	kbConfig := types.VLMConfig{Enabled: true, ModelID: "kb-model", DescriptionLanguage: "en"}
	knowledge := &types.Knowledge{ID: "document-1"}
	if err := knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{
		VLMConfig: &types.VLMConfig{Enabled: true, ModelID: "document-model", DescriptionLanguage: "zh"},
	}); err != nil {
		t.Fatalf("set process overrides: %v", err)
	}
	svc := &ImageMultimodalService{
		kbService:     resolveVLMTestKnowledgeBaseService{kb: &types.KnowledgeBase{ID: "kb-1", VLMConfig: kbConfig}},
		knowledgeRepo: resolveVLMTestKnowledgeRepository{knowledge: knowledge},
		modelService:  modelService,
	}

	gotModel, gotConfig, err := svc.resolveVLM(context.Background(), "kb-1", "document-1")
	if err != nil {
		t.Fatalf("resolveVLM() error = %v", err)
	}
	if gotModel != kbModel {
		t.Fatalf("resolveVLM() model = %v, want KB model %v", gotModel, kbModel)
	}
	if gotConfig != kbConfig {
		t.Fatalf("resolveVLM() config = %+v, want KB config %+v", gotConfig, kbConfig)
	}
	if want := []string{"document-model", "kb-model"}; !reflect.DeepEqual(modelService.calls, want) {
		t.Fatalf("GetVLMModel calls = %v, want %v", modelService.calls, want)
	}
}

func TestResolveVLMReportsDocumentAndFallbackModelErrors(t *testing.T) {
	documentErr := errors.New("document model not found")
	fallbackErr := errors.New("KB model unavailable")
	modelService := &resolveVLMTestModelService{
		errors: map[string]error{"document-model": documentErr, "kb-model": fallbackErr},
	}
	knowledge := &types.Knowledge{ID: "document-1"}
	if err := knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{
		VLMConfig: &types.VLMConfig{Enabled: true, ModelID: "document-model"},
	}); err != nil {
		t.Fatalf("set process overrides: %v", err)
	}
	svc := &ImageMultimodalService{
		kbService: resolveVLMTestKnowledgeBaseService{kb: &types.KnowledgeBase{
			ID: "kb-1", VLMConfig: types.VLMConfig{Enabled: true, ModelID: "kb-model"},
		}},
		knowledgeRepo: resolveVLMTestKnowledgeRepository{knowledge: knowledge},
		modelService:  modelService,
	}

	_, _, err := svc.resolveVLM(context.Background(), "kb-1", "document-1")
	if err == nil ||
		!strings.Contains(err.Error(), documentErr.Error()) ||
		!strings.Contains(err.Error(), fallbackErr.Error()) {
		t.Fatalf("resolveVLM() error = %v, want both document and fallback errors", err)
	}
	if want := []string{"document-model", "kb-model"}; !reflect.DeepEqual(modelService.calls, want) {
		t.Fatalf("GetVLMModel calls = %v, want %v", modelService.calls, want)
	}
}
