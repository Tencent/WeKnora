package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type manualMetadataService struct {
	interfaces.KnowledgeService
	payload *types.ManualKnowledgePayload
	err     error
}

func (s *manualMetadataService) CreateKnowledgeFromManual(
	_ context.Context, _ string, payload *types.ManualKnowledgePayload, _ string,
) (*types.Knowledge, error) {
	s.payload = payload
	return &types.Knowledge{ID: "doc", CustomMetadata: types.JSON(payload.CustomMetadata)}, s.err
}

func TestCreateManualKnowledgeCustomMetadataBinding(t *testing.T) {
	for _, reject := range []bool{false, true} {
		kg := &manualMetadataService{}
		if reject {
			kg.err = apperrors.NewValidationError("invalid custom_metadata")
		}
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
			c.Set(types.TenantIDContextKey.String(), uint64(1))
			c.Set(types.UserIDContextKey.String(), "editor")
			c.Next()
		})
		h := &KnowledgeHandler{kbService: &stubFolderKBService{}, kgService: kg}
		router.POST("/api/v1/knowledge-bases/:id/knowledge/manual", h.CreateManualKnowledge)
		w := httptest.NewRecorder()
		body := `{"title":"Guide","content":"Body","status":"publish","custom_metadata":{"source_id":"123"}}`
		req := httptest.NewRequest(
			http.MethodPost, "/api/v1/knowledge-bases/kb-1/knowledge/manual", strings.NewReader(body),
		)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.NotNil(t, kg.payload)
		require.JSONEq(t, `{"source_id":"123"}`, string(kg.payload.CustomMetadata))
		if reject {
			require.Equal(t, http.StatusBadRequest, w.Code)
		} else {
			require.Equal(t, http.StatusOK, w.Code)
			var response struct {
				Data types.Knowledge `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.JSONEq(t, `{"source_id":"123"}`, string(response.Data.CustomMetadata))
		}
	}
}
