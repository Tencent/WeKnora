package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestPartitionBatchCancelKnowledge(t *testing.T) {
	knowledge := []*types.Knowledge{
		{ID: "pending", ParseStatus: types.ParseStatusPending},
		{ID: "processing", ParseStatus: types.ParseStatusProcessing},
		{ID: "finalizing", ParseStatus: types.ParseStatusFinalizing},
		{ID: "cancelled", ParseStatus: types.ParseStatusCancelled},
		{ID: "completed", ParseStatus: types.ParseStatusCompleted},
		{ID: "failed", ParseStatus: types.ParseStatusFailed},
		{ID: "draft", ParseStatus: types.ManualKnowledgeStatusDraft},
	}

	cancellable, skipped := partitionBatchCancelKnowledge(knowledge)

	assert.Equal(t, []string{"pending", "processing", "finalizing", "cancelled"}, cancellable)
	assert.Equal(t, []batchCancelSkippedKnowledge{
		{ID: "completed", ParseStatus: types.ParseStatusCompleted},
		{ID: "failed", ParseStatus: types.ParseStatusFailed},
		{ID: "draft", ParseStatus: types.ManualKnowledgeStatusDraft},
	}, skipped)
}

type batchCancelKBServiceStub struct {
	interfaces.KnowledgeBaseService
}

func (s *batchCancelKBServiceStub) GetKnowledgeBaseByID(
	_ context.Context,
	id string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: id, TenantID: 1}, nil
}

type batchCancelKnowledgeServiceStub struct {
	interfaces.KnowledgeService
	knowledge []*types.Knowledge
	cancelled []string
}

func (s *batchCancelKnowledgeServiceStub) GetKnowledgeBatch(
	_ context.Context,
	_ uint64,
	_ []string,
) ([]*types.Knowledge, error) {
	return s.knowledge, nil
}

func (s *batchCancelKnowledgeServiceStub) CancelKnowledgeParse(
	_ context.Context,
	id string,
) (*types.Knowledge, error) {
	s.cancelled = append(s.cancelled, id)
	return &types.Knowledge{ID: id, ParseStatus: types.ParseStatusCancelled}, nil
}

func newBatchCancelRouter(kg interfaces.KnowledgeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	h := &KnowledgeHandler{
		kbService: &batchCancelKBServiceStub{},
		kgService: kg,
	}
	r.POST("/batch-cancel-parse", h.BatchCancelKnowledgeParse)
	return r
}

func performBatchCancelRequest(t *testing.T, router *gin.Engine, ids []string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"kb_id": "kb-1", "ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/batch-cancel-parse", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestBatchCancelKnowledgeParseCancelsOnlyInFlightKnowledge(t *testing.T) {
	kg := &batchCancelKnowledgeServiceStub{knowledge: []*types.Knowledge{
		{ID: "processing", KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusProcessing},
		{ID: "failed", KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusFailed},
	}}

	w := performBatchCancelRequest(t, newBatchCancelRouter(kg), []string{"processing", "failed"})

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []string{"processing"}, kg.cancelled)
	assert.Contains(t, w.Body.String(), `"cancelled_count":1`)
	assert.Contains(t, w.Body.String(), `"skipped_count":1`)
}

func TestBatchCancelKnowledgeParseRejectsCrossKBBeforeMutation(t *testing.T) {
	kg := &batchCancelKnowledgeServiceStub{knowledge: []*types.Knowledge{
		{ID: "processing", KnowledgeBaseID: "kb-other", ParseStatus: types.ParseStatusProcessing},
	}}

	w := performBatchCancelRequest(t, newBatchCancelRouter(kg), []string{"processing"})

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Empty(t, kg.cancelled)
	assert.True(t, strings.Contains(w.Body.String(), "does not belong"),
		"response should explain the KB boundary: %s", w.Body.String())
}
