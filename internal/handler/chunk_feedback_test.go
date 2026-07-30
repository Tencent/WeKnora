package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	appErrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestListLowQualityChunksPassesKnowledgeBaseAndAllRatedThreshold(t *testing.T) {
	gin.SetMode(gin.TestMode)
	chunkRepo := &handlerFeedbackChunkRepo{}
	feedbackService := service.NewChunkFeedbackService(nil, nil, nil, chunkRepo, nil)
	handler := NewChunkFeedbackHandler(feedbackService)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/chunks/low-quality?max_rate=2&limit=200&offset=-5&knowledge_base_id=kb-1", nil)
	c.Set(types.TenantIDContextKey.String(), uint64(7))

	handler.ListLowQualityChunks(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.InDelta(t, 1.01, chunkRepo.maxRate, 0.0001)
	require.Equal(t, "kb-1", chunkRepo.knowledgeBaseID)
	require.Equal(t, 20, chunkRepo.limit)
	require.Equal(t, 0, chunkRepo.offset)
}

func TestFeedbackScopeUserIDPrefersSessionOwnerPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx := types.WithPrincipal(context.Background(), types.Principal{
		Type: types.PrincipalAPIExternalUser,
		ID:   "7:alice-external-user",
	})
	ctx = context.WithValue(ctx, types.UserIDContextKey, "system-7")
	c.Request = httptest.NewRequest(http.MethodGet, "/feedback/user-feedback", nil).WithContext(ctx)
	c.Set(types.UserIDContextKey.String(), "system-7")

	require.Equal(t, "api_external_user:7:alice-external-user", feedbackScopeUserID(c))
}

func TestHandleFeedbackServiceErrorMapsWrappedChunkNotFoundTo404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	handleFeedbackServiceError(c, fmt.Errorf("lookup chunk: %w", service.ErrChunkNotFound))

	require.Len(t, c.Errors, 1)
	var appErr *appErrors.AppError
	require.ErrorAs(t, c.Errors.Last().Err, &appErr)
	require.Equal(t, http.StatusNotFound, appErr.HTTPCode)
	require.Equal(t, appErrors.ErrNotFound, appErr.Code)
}

func TestHandleFeedbackServiceErrorMapsIncompleteMessageTo409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	handleFeedbackServiceError(c, fmt.Errorf("feedback race: %w", service.ErrFeedbackTargetNotCompleted))

	require.Len(t, c.Errors, 1)
	var appErr *appErrors.AppError
	require.ErrorAs(t, c.Errors.Last().Err, &appErr)
	require.Equal(t, http.StatusConflict, appErr.HTTPCode)
	require.Equal(t, appErrors.ErrConflict, appErr.Code)
}

type handlerFeedbackChunkRepo struct {
	interfaces.ChunkRepository

	tenantID        uint64
	knowledgeBaseID string
	maxRate         float64
	limit           int
	offset          int
}

func (r *handlerFeedbackChunkRepo) ListLowQualityChunks(ctx context.Context, tenantID uint64, knowledgeBaseID string, maxRate float64, limit, offset int) ([]*types.Chunk, error) {
	r.tenantID = tenantID
	r.knowledgeBaseID = knowledgeBaseID
	r.maxRate = maxRate
	r.limit = limit
	r.offset = offset
	return []*types.Chunk{
		{ID: "chunk-1", Content: "content", PositiveRate: 1, LikeCount: 1, RecallWeight: 1},
	}, nil
}

func (r *handlerFeedbackChunkRepo) CountLowQualityChunks(ctx context.Context, tenantID uint64, knowledgeBaseID string, maxRate float64) (int64, error) {
	return 1, nil
}
