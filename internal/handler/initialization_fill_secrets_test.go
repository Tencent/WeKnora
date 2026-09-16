package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// newFillSecretsGinContext builds a throwaway gin context for the C4 audit
// accessor (audit service stays nil here — best-effort by design).
func newFillSecretsGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

// stubFillSecretsModelService only implements GetModelByID; every other
// ModelService call panics via the nil interface embedding. Keeps the test
// focused on fillSecretsFromStoredModel's branching logic.
type stubFillSecretsModelService struct {
	interfaces.ModelService
	getModelByID func(ctx context.Context, id string) (*types.Model, error)
}

func (s *stubFillSecretsModelService) GetModelByID(ctx context.Context, id string) (*types.Model, error) {
	return s.getModelByID(ctx, id)
}

func TestFillSecretsFromStoredModel_FillsExtraConfig(t *testing.T) {
	stored := &types.Model{
		Parameters: types.ModelParameters{
			APIKey:    "sk-stored",
			AppSecret: "app-secret-stored",
			ExtraConfig: map[string]string{
				"thinking_control": "none",
				"api_version":      "2024-10-21",
			},
		},
	}
	h := &InitializationHandler{
		modelService: &stubFillSecretsModelService{
			getModelByID: func(context.Context, string) (*types.Model, error) { return stored, nil },
		},
	}
	req := &ModelTestRequest{ModelID: "m-1"}

	if err := h.fillSecretsFromStoredModel(context.Background(), newFillSecretsGinContext(), req); err != nil {
		t.Fatalf("fillSecretsFromStoredModel returned an unexpected error: %v", err)
	}

	if req.APIKey != "sk-stored" || req.AppSecret != "app-secret-stored" {
		t.Fatalf("secrets not filled from stored model: apiKey=%q appSecret=%q", req.APIKey, req.AppSecret)
	}
	if req.ExtraConfig == nil {
		t.Fatalf("ExtraConfig not filled from stored model: got nil, want thinking_control/api_version")
	}
	if req.ExtraConfig["thinking_control"] != "none" || req.ExtraConfig["api_version"] != "2024-10-21" {
		t.Fatalf("ExtraConfig contents wrong: %v", req.ExtraConfig)
	}
}

// TestFillSecretsFromStoredModel_RequestExtraConfigWins: when the caller
// actively sends an extraConfig payload (user editing the form), the stored
// one must not overwrite it — same precedence as the secret fields.
func TestFillSecretsFromStoredModel_RequestExtraConfigWins(t *testing.T) {
	stored := &types.Model{
		Parameters: types.ModelParameters{
			ExtraConfig: map[string]string{"thinking_control": "none"},
		},
	}
	h := &InitializationHandler{
		modelService: &stubFillSecretsModelService{
			getModelByID: func(context.Context, string) (*types.Model, error) { return stored, nil },
		},
	}
	req := &ModelTestRequest{
		ModelID:     "m-1",
		APIKey:      "sk-typed",
		AppSecret:   "app-typed",
		ExtraConfig: map[string]string{"thinking_control": "enabled"},
	}

	if err := h.fillSecretsFromStoredModel(context.Background(), newFillSecretsGinContext(), req); err != nil {
		t.Fatalf("fillSecretsFromStoredModel returned an unexpected error: %v", err)
	}

	if req.ExtraConfig["thinking_control"] != "enabled" {
		t.Fatalf("request ExtraConfig overwritten: %v", req.ExtraConfig)
	}
	if req.APIKey != "sk-typed" || req.AppSecret != "app-typed" {
		t.Fatalf("request secrets overwritten: %q %q", req.APIKey, req.AppSecret)
	}
}

// TestFillSecretsFromStoredModel_SecretsStillFilledWithoutExtraConfig:
// regression guard for the early-return — a request that already carries
// both secrets but no extraConfig must still pick up the stored
// extraConfig (this is the exact #3057 scenario: the frontend "test"
// button only sends modelId).
func TestFillSecretsFromStoredModel_SecretsStillFilledWithoutExtraConfig(t *testing.T) {
	stored := &types.Model{
		Parameters: types.ModelParameters{
			APIKey:      "sk-stored",
			AppSecret:   "app-secret-stored",
			ExtraConfig: map[string]string{"remote_model_name": "gpt-x"},
		},
	}
	h := &InitializationHandler{
		modelService: &stubFillSecretsModelService{
			getModelByID: func(context.Context, string) (*types.Model, error) { return stored, nil },
		},
	}
	req := &ModelTestRequest{ModelID: "m-1", APIKey: "sk-typed", AppSecret: "app-typed"}

	if err := h.fillSecretsFromStoredModel(context.Background(), newFillSecretsGinContext(), req); err != nil {
		t.Fatalf("fillSecretsFromStoredModel returned an unexpected error: %v", err)
	}

	if req.ExtraConfig == nil || req.ExtraConfig["remote_model_name"] != "gpt-x" {
		t.Fatalf("ExtraConfig not filled when secrets were provided: %v", req.ExtraConfig)
	}
}

func TestFillSecretsFromStoredModel_NoModelIDNoop(t *testing.T) {
	h := &InitializationHandler{
		modelService: &stubFillSecretsModelService{
			getModelByID: func(context.Context, string) (*types.Model, error) {
				t.Fatal("GetModelByID must not be called without a model id")
				return nil, nil
			},
		},
	}
	req := &ModelTestRequest{}

	if err := h.fillSecretsFromStoredModel(context.Background(), newFillSecretsGinContext(), req); err != nil {
		t.Fatalf("fillSecretsFromStoredModel returned an unexpected error: %v", err)
	}

	if req.ExtraConfig != nil {
		t.Fatalf("ExtraConfig changed without model id: %v", req.ExtraConfig)
	}
}

func TestFillSecretsFromStoredModel_ModelNotFoundNoop(t *testing.T) {
	h := &InitializationHandler{
		modelService: &stubFillSecretsModelService{
			getModelByID: func(context.Context, string) (*types.Model, error) {
				return nil, errors.New("not found")
			},
		},
	}
	req := &ModelTestRequest{ModelID: "missing"}

	if err := h.fillSecretsFromStoredModel(context.Background(), newFillSecretsGinContext(), req); err != nil {
		t.Fatalf("unexpected error on model lookup failure path: %v", err)
	}

	if req.ExtraConfig != nil {
		t.Fatalf("ExtraConfig changed on model lookup failure: %v", req.ExtraConfig)
	}
}

// TestFillSecretsFromStoredModel_CrossHostRedirectDenied pins the C4 ruling
// (2026-09-13): a request pointing base_url at a different host than the
// stored model must NOT borrow stored secrets — neither key may ride to a
// caller-chosen host, and the caller gets an explicit error instead of a
// misleading downstream auth failure.
func TestFillSecretsFromStoredModel_CrossHostRedirectDenied(t *testing.T) {
	stored := &types.Model{
		Parameters: types.ModelParameters{
			BaseURL:     "https://api.openai.com/v1",
			APIKey:      "sk-stored",
			AppSecret:   "app-secret-stored",
			ExtraConfig: map[string]string{"thinking_control": "none"},
		},
	}
	h := &InitializationHandler{
		modelService: &stubFillSecretsModelService{
			getModelByID: func(context.Context, string) (*types.Model, error) { return stored, nil },
		},
	}
	req := &ModelTestRequest{ModelID: "m-1", BaseURL: "https://evil.example.com/v1"}

	err := h.fillSecretsFromStoredModel(context.Background(), newFillSecretsGinContext(), req)

	if err == nil {
		t.Fatalf("cross-host redirect must be denied")
	}
	if req.APIKey != "" || req.AppSecret != "" || req.ExtraConfig != nil {
		t.Fatalf("stored secrets leaked to a redirected host: %v", req)
	}
}

// Same-host requests keep borrowing stored credentials (the intended
// "test the saved model as configured" flow).
func TestFillSecretsFromStoredModel_SameHostStillFills(t *testing.T) {
	stored := &types.Model{
		Parameters: types.ModelParameters{
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-stored",
		},
	}
	h := &InitializationHandler{
		modelService: &stubFillSecretsModelService{
			getModelByID: func(context.Context, string) (*types.Model, error) { return stored, nil },
		},
	}
	req := &ModelTestRequest{ModelID: "m-1", BaseURL: "https://api.openai.com/v1/"}

	if err := h.fillSecretsFromStoredModel(context.Background(), newFillSecretsGinContext(), req); err != nil {
		t.Fatalf("same-host request must keep borrowing: %v", err)
	}
	if req.APIKey != "sk-stored" {
		t.Fatalf("stored api key not filled: %q", req.APIKey)
	}
}
