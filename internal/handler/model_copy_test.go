package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubCopyModelService struct {
	interfaces.ModelService
	sourceID    string
	displayName string
	model       *types.Model
}

func (s *stubCopyModelService) CopyModel(_ context.Context, sourceID, displayName string) (*types.Model, error) {
	s.sourceID = sourceID
	s.displayName = displayName
	return s.model, nil
}

func TestCopyModelResponseOmitsCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &stubCopyModelService{
		model: &types.Model{
			ID:          "copy-1",
			Name:        "gpt-4o",
			DisplayName: "生产 GPT 副本",
			Type:        types.ModelTypeKnowledgeQA,
			Source:      types.ModelSourceRemote,
			Status:      types.ModelStatusActive,
			Parameters: types.ModelParameters{
				BaseURL:   "https://api.example.com/v1",
				APIKey:    "sk-live",
				AppSecret: "app-secret",
			},
		},
	}
	h := NewModelHandler(stub)
	router := gin.New()
	router.POST("/models/:id/copy", func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		h.CopyModel(c)
	})

	payload := bytes.NewBufferString(`{"display_name":"生产 GPT 副本"}`)
	req := httptest.NewRequest(http.MethodPost, "/models/src/copy", payload)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(7)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	body := rec.Body.String()
	require.NotContains(t, body, "sk-live")
	require.NotContains(t, body, "app-secret")
	require.Contains(t, body, `"name":"gpt-4o"`)
	require.Contains(t, body, `"display_name":"生产 GPT 副本"`)
	require.Contains(t, body, `"api_key":{"configured":true}`)
	require.Equal(t, "src", stub.sourceID)
	require.Equal(t, "生产 GPT 副本", stub.displayName)
}
