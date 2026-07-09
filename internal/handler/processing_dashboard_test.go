package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func TestProcessingDashboardHandlerValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewProcessingDashboardHandler(fakeProcessingDashboardService{})
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.GET("/stages/:stage/items", h.ListStageItems)

	req := httptest.NewRequest(http.MethodGet, "/stages/nope/items?state=running", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid stage status = %d, want 400", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/stages/graph/items?state=done", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid state status = %d, want 400", w.Code)
	}
}

func TestProcessingDashboardHandlerGETEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewProcessingDashboardHandler(fakeProcessingDashboardService{})
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.GET("/dashboard", h.GetDashboard)
	r.GET("/stages/:stage/items", h.ListStageItems)
	r.GET("/knowledge/:id", h.GetKnowledgeProcessingDetail)

	cases := []string{
		"/dashboard?active_limit=5",
		"/stages/graph/items?state=queued&page_size=200",
		"/knowledge/kid?attempt=1",
	}
	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, w.Code, w.Body.String())
		}
	}
}

type fakeProcessingDashboardService struct{}

func (fakeProcessingDashboardService) GetDashboard(ctx context.Context, filter types.ProcessingDashboardFilter) (*types.ProcessingDashboardResponse, error) {
	return &types.ProcessingDashboardResponse{GeneratedAt: time.Now()}, nil
}

func (fakeProcessingDashboardService) ListStageItems(ctx context.Context, filter types.ProcessingDashboardFilter, stage types.ProcessingLogicalStage, state types.ProcessingStageState, cursor string, pageSize int) (*types.ProcessingStageItemsResponse, error) {
	if pageSize != 200 {
		return nil, nil
	}
	return &types.ProcessingStageItemsResponse{GeneratedAt: time.Now(), Stage: stage, State: state}, nil
}

func (fakeProcessingDashboardService) GetKnowledgeProcessingDetail(ctx context.Context, knowledgeID string, attempt int) (*types.ProcessingKnowledgeDetailResponse, error) {
	return &types.ProcessingKnowledgeDetailResponse{GeneratedAt: time.Now(), CurrentAttempt: attempt}, nil
}
