package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type generationKnowledgeService struct {
	interfaces.KnowledgeService
	getByID func(ctx context.Context, id string) (*types.Knowledge, error)
}

func (s *generationKnowledgeService) GetKnowledgeByID(ctx context.Context, id string) (*types.Knowledge, error) {
	return s.getByID(ctx, id)
}

func TestGetChunkByIDOnlyHidesNonActiveGenerationChunk(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewChunkHandler(
		&stubChunkService{
			getByIDOnly: func(_ context.Context, id string) (*types.Chunk, error) {
				return &types.Chunk{
					ID:           id,
					TenantID:     1,
					KnowledgeID:  "knowledge-1",
					GenerationID: "building-generation",
				}, nil
			},
		},
		&generationKnowledgeService{
			getByID: func(_ context.Context, id string) (*types.Knowledge, error) {
				if id != "knowledge-1" {
					t.Fatalf("GetKnowledgeByID id = %q, want knowledge-1", id)
				}
				return &types.Knowledge{
					ID:                 id,
					TenantID:           1,
					ActiveGenerationID: "active-generation",
				}, nil
			},
		},
	)

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.GET("/chunks/by-id/:id", func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
		c.Request = c.Request.WithContext(ctx)
		h.GetChunkByIDOnly(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chunks/by-id/chunk-1", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}
