package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Re-initializing a KB used to assign the wizard's four fields over the whole
// parameter struct of the stored row, erasing everything the model editor
// owns (provider, extra_config, custom headers, spec, concurrency, context
// window) and blanking the API key whenever the payload carried none.

func storedEditorConfiguredModel() *types.Model {
	return &types.Model{
		ID: "m-llm", Type: types.ModelTypeKnowledgeQA, Name: "gpt-4o",
		TenantID: 10042, Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider: "azure",
			BaseURL:  "https://old.example.com/v1",
			APIKey:   "stored-key",
			ExtraConfig: map[string]string{
				"api_version": "2024-06-01",
				"secret_key":  "cam-secret-stored",
			},
			CustomHeaders:  map[string]string{"X-Team": "rag"},
			MaxConcurrency: 4,
			ContextWindow:  128000,
			Spec:           &types.ModelSpecOverride{API: "openai-completions"},
		},
	}
}

// initExistingLLM runs processInitializationModels against a KB whose LLM
// slot already points at stored, and returns the row it persisted.
func initExistingLLM(t *testing.T, stored *types.Model, mutate func(*InitializationRequest)) *types.Model {
	t.Helper()
	svc := &stubTenantStampModelService{}
	svc.getModelByID = func(_ context.Context, id string) (*types.Model, error) {
		if id == stored.ID {
			return stored, nil
		}
		return nil, nil
	}
	h := &InitializationHandler{modelService: svc}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 10042, SummaryModelID: stored.ID}

	req := newTenantStampRequest()
	if mutate != nil {
		mutate(req)
	}
	_, err := h.processInitializationModels(context.Background(), kb, "kb-1", req)
	require.NoError(t, err)
	require.NotEmpty(t, svc.updated, "the existing model was not updated")
	return svc.updated[0]
}

func TestProcessInitializationModelsKeepsEditorOwnedParameters(t *testing.T) {
	stored := storedEditorConfiguredModel()
	updated := initExistingLLM(t, stored, func(req *InitializationRequest) {
		req.LLM.APIKey = "" // the wizard re-submits without re-typing the key
		req.LLM.BaseURL = "https://new.example.com/v1"
	})

	p := updated.Parameters
	assert.Equal(t, "https://new.example.com/v1", p.BaseURL, "the endpoint edit must still land")
	assert.Equal(t, "stored-key", p.APIKey, "an omitted key means unchanged, not cleared")
	assert.Equal(t, "azure", p.Provider)
	assert.Equal(t, "2024-06-01", p.ExtraConfig["api_version"])
	assert.Equal(t, "cam-secret-stored", p.ExtraConfig["secret_key"])
	assert.Equal(t, map[string]string{"X-Team": "rag"}, p.CustomHeaders)
	assert.Equal(t, 4, p.MaxConcurrency)
	assert.Equal(t, 128000, p.ContextWindow)
	assert.NotNil(t, p.Spec)
}

// Initialization's own purpose still works: endpoint and key are writable.
func TestProcessInitializationModelsStillAppliesSubmittedFields(t *testing.T) {
	stored := storedEditorConfiguredModel()
	updated := initExistingLLM(t, stored, func(req *InitializationRequest) {
		req.LLM.APIKey = "rotated-key"
		req.LLM.BaseURL = "https://new.example.com/v1"
	})
	assert.Equal(t, "rotated-key", updated.Parameters.APIKey)
	assert.Equal(t, "https://new.example.com/v1", updated.Parameters.BaseURL)
}

// The embedding slot carries a dimension, and changing it is the one reason
// an operator re-runs initialization against an existing embedding row.
func TestProcessInitializationModelsAppliesEmbeddingDimension(t *testing.T) {
	stored := &types.Model{
		ID: "m-embed", Type: types.ModelTypeEmbedding, Name: "bge-m3", TenantID: 10042,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider:            "siliconflow",
			BaseURL:             "https://old.example.com/v1",
			APIKey:              "stored-key",
			ExtraConfig:         map[string]string{"remote_model_name": "BAAI/bge-m3"},
			EmbeddingParameters: types.EmbeddingParameters{Dimension: 768, TruncatePromptTokens: 512},
		},
	}
	svc := &stubTenantStampModelService{}
	svc.getModelByID = func(_ context.Context, id string) (*types.Model, error) {
		if id == "m-embed" {
			return stored, nil
		}
		return nil, nil
	}
	h := &InitializationHandler{modelService: svc}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 10042, EmbeddingModelID: "m-embed"}

	req := newTenantStampRequest()
	req.Embedding.Dimension = 1024
	req.Embedding.APIKey = ""
	_, err := h.processInitializationModels(context.Background(), kb, "kb-1", req)
	require.NoError(t, err)
	require.NotEmpty(t, svc.updated)

	p := svc.updated[0].Parameters
	assert.Equal(t, 1024, p.EmbeddingParameters.Dimension, "the dimension edit must still land")
	assert.Equal(t, 512, p.EmbeddingParameters.TruncatePromptTokens,
		"the rest of the embedding parameters is not part of the payload")
	assert.Equal(t, "BAAI/bge-m3", p.ExtraConfig["remote_model_name"])
	assert.Equal(t, "stored-key", p.APIKey)
}

type stubInitKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *stubInitKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type stubInitKBRepository struct {
	interfaces.KnowledgeBaseRepository
}

func (s *stubInitKBRepository) UpdateKnowledgeBase(context.Context, *types.KnowledgeBase) error {
	return nil
}

// POST /initialization/initialize/{kbId} used to marshal the raw model rows,
// and types.Model carries api_key in plaintext. Now that the reuse path keeps
// the stored credential rather than overwriting it with the payload's, that
// body would hand back a key the caller never submitted.
func TestInitializeByKB_ResponseDoesNotEchoStoredAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stored := storedEditorConfiguredModel()
	svc := &stubTenantStampModelService{}
	svc.getModelByID = func(_ context.Context, id string) (*types.Model, error) {
		if id == stored.ID {
			return stored, nil
		}
		return nil, nil
	}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 10042, SummaryModelID: stored.ID}
	h := &InitializationHandler{
		modelService: svc,
		kbService:    &stubInitKBService{kb: kb},
		kbRepository: &stubInitKBRepository{},
	}

	req := newTenantStampRequest()
	req.LLM.APIKey = ""
	// The full handler runs SSRF validation, which would otherwise resolve
	// the host over the network. Whitelist one name of our own and clear it
	// again, so no other test in this package sees a relaxed check.
	secutils.SetSSRFWhitelistFromRaw("init-merge-test.example.com")
	t.Cleanup(func() { secutils.SetSSRFWhitelistFromRaw("") })
	req.LLM.BaseURL = "https://init-merge-test.example.com/v1"
	req.Embedding.BaseURL = "https://init-merge-test.example.com/v1"
	req.DocumentSplitting.ChunkSize = 512
	req.DocumentSplitting.ChunkOverlap = 64
	req.DocumentSplitting.Separators = []string{"\n"}
	body, err := json.Marshal(req)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/initialization/initialize/kb-1", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request = c.Request.WithContext(types.WithCaller(c.Request.Context(), types.Caller{
		TenantID: 10042, UserID: "u-1", Role: types.TenantRoleAdmin,
	}))
	c.Params = gin.Params{{Key: "kbId", Value: "kb-1"}}

	h.InitializeByKB(c)
	require.Empty(t, c.Errors.Errors())
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "stored-key")
	assert.NotContains(t, w.Body.String(), "cam-secret-stored")
	assert.Contains(t, w.Body.String(), stored.ID, "the model ids the caller needs are still returned")
}

// A rerank row is never reached by this path at all: the KB has no rerank
// slot, so findExistingModelID has nothing to return and every run creates a
// fresh row. Pinning it keeps the reasoning above honest if a slot is added.
func TestFindExistingModelIDHasNoRerankSlot(t *testing.T) {
	h := &InitializationHandler{}
	kb := &types.KnowledgeBase{SummaryModelID: "m-llm", EmbeddingModelID: "m-embed"}
	assert.Empty(t, h.findExistingModelID(kb, types.ModelTypeRerank))
	assert.Equal(t, "m-llm", h.findExistingModelID(kb, types.ModelTypeKnowledgeQA))
}
