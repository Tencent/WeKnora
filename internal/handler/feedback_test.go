package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type feedbackHandlerServiceStub struct {
	interfaces.FeedbackService
	resetErr    error
	resetCalls  int
	detailCalls int
}

func (s *feedbackHandlerServiceStub) ResetChunkFeedback(
	_ context.Context,
	_, _ string,
) error {
	s.resetCalls++
	return s.resetErr
}

func (s *feedbackHandlerServiceStub) GetChunkFeedbackGovernanceDetails(
	_ context.Context,
	_, _ string,
) (*types.ChunkFeedbackDetails, error) {
	s.detailCalls++
	return nil, errors.New("detail unavailable")
}

func newFeedbackResetContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	ctx.Params = gin.Params{
		{Key: "id", Value: "kb-a"},
		{Key: "chunk_id", Value: "chunk-a"},
	}
	return ctx, recorder
}

func TestGovernanceResetReportsCommittedMutationWithoutReadback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &feedbackHandlerServiceStub{}
	ctx, recorder := newFeedbackResetContext()

	NewFeedbackHandler(service).ResetChunkFeedbackGovernance(ctx)
	ctx.Writer.WriteHeaderNow()

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, 1, service.resetCalls)
	assert.Zero(t, service.detailCalls, "post-commit readback must not reverse reset success")
	assert.Empty(t, ctx.Errors)
}

func TestGovernanceResetStillReportsMutationFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &feedbackHandlerServiceStub{resetErr: errors.New("reset failed")}
	ctx, recorder := newFeedbackResetContext()

	NewFeedbackHandler(service).ResetChunkFeedbackGovernance(ctx)

	require.Len(t, ctx.Errors, 1)
	assert.Equal(t, 1, service.resetCalls)
	assert.Zero(t, service.detailCalls)
	assert.NotEqual(t, http.StatusNoContent, recorder.Code)
}
