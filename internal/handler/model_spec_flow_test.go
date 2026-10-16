package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolvePostUsesUnsavedSpec(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/models/catalog/resolve", strings.NewReader(`{
  "provider": "openai",
  "model": "gpt-5",
  "base_url": "https://api.openai.com/v1",
  "spec": {
    "api": "openai-completions",
    "context_window": 12345,
    "compat": {
      "supports_temperature": false
    }
  }
}`))
	c.Request.Header.Set("Content-Type", "application/json")
	(&ModelHandler{}).ResolveModelCatalog(c)
	require.Empty(t, c.Errors)
	var body struct {
		Data struct {
			API          string `json:"api"`
			Capabilities struct {
				ContextWindow int `json:"context_window"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "openai-completions", body.Data.API)
	require.Equal(t, 12345, body.Data.Capabilities.ContextWindow)
}

func TestConnectionTestModelCarriesSpecForEveryCapability(t *testing.T) {
	spec := &types.ModelSpecOverride{Compat: map[string]any{"path": "/custom"}}
	for _, kind := range []types.ModelType{
		types.ModelTypeKnowledgeQA,
		types.ModelTypeEmbedding,
		types.ModelTypeRerank,
		types.ModelTypeASR,
		types.ModelTypeVLLM,
	} {
		row := (&InitializationHandler{}).buildTestModel(
			&ModelTestRequest{
				ModelName: "test",
				Provider:  "generic",
				APIKey:    "row-key",
				Spec:      spec,
			},
			kind,
			types.ModelSourceRemote,
		)
		require.Equal(t, spec, row.Parameters.Spec)
		require.Equal(t, "row-key", row.Parameters.APIKey)
	}
}
