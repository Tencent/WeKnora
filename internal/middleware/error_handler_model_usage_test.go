package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorHandler_PreservesStructuredModelUsageDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorHandler())
	router.DELETE("/models/:id", func(c *gin.Context) {
		_ = c.Error(apperrors.NewModelInUseError(
			"model is used by 2 knowledge base(s); reconfigure or remove those references before deleting",
			types.ModelUsageDetails{
				KnowledgeBases: []types.ModelUsageResource{
					{
						ID:       "kb-1",
						Name:     "Product docs",
						Bindings: []types.ModelUsageBinding{types.ModelUsageBindingVLMModel},
					},
					{
						ID:   "kb-2",
						Name: "Engineering",
						Bindings: []types.ModelUsageBinding{
							types.ModelUsageBindingEmbeddingModel,
							types.ModelUsageBindingSummaryModel,
						},
					},
				},
				Agents:         []types.ModelUsageResource{},
				LongTermMemory: types.ModelUsageMemory{Bindings: []types.ModelUsageBinding{}},
			},
		))
	})

	request := httptest.NewRequest(http.MethodDelete, "/models/model-1", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusBadRequest, response.Code)
	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code    apperrors.ErrorCode     `json:"code"`
			Message string                  `json:"message"`
			Details types.ModelUsageDetails `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.False(t, body.Success)
	assert.Equal(t, apperrors.ErrModelInUse, body.Error.Code)
	assert.NotEmpty(t, body.Error.Message)
	require.Len(t, body.Error.Details.KnowledgeBases, 2)
	assert.Equal(t, "Product docs", body.Error.Details.KnowledgeBases[0].Name)
	assert.Equal(t,
		[]types.ModelUsageBinding{types.ModelUsageBindingEmbeddingModel, types.ModelUsageBindingSummaryModel},
		body.Error.Details.KnowledgeBases[1].Bindings,
	)
	assert.Empty(t, body.Error.Details.Agents)
	assert.Empty(t, body.Error.Details.LongTermMemory.Bindings)
}
