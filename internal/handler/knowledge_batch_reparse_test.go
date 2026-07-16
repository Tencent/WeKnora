package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubBatchReparseKBService struct {
	interfaces.KnowledgeBaseService
	calls int
	kb    *types.KnowledgeBase
	err   error
}

func (s *stubBatchReparseKBService) GetKnowledgeBaseByID(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.kb, nil
}

type stubBatchReparseKGService struct {
	interfaces.KnowledgeService
	calls     int
	gotTenant uint64
	gotIDs    []string
	knowledge []*types.Knowledge
	err       error
}

func (s *stubBatchReparseKGService) GetKnowledgeBatch(_ context.Context, tenantID uint64, ids []string) ([]*types.Knowledge, error) {
	s.calls++
	s.gotTenant = tenantID
	s.gotIDs = append([]string(nil), ids...)
	if s.err != nil {
		return nil, s.err
	}
	return s.knowledge, nil
}

type captureBatchReparseEnqueuer struct {
	calls int
	task  *asynq.Task
	err   error
}

func (e *captureBatchReparseEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.calls++
	e.task = task
	if e.err != nil {
		return nil, e.err
	}
	return &asynq.TaskInfo{ID: "task-batch-reparse"}, nil
}

func newBatchReparseTestRouter(
	kb interfaces.KnowledgeBaseService,
	kg interfaces.KnowledgeService,
	enqueuer interfaces.TaskEnqueuer,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	h := &KnowledgeHandler{kbService: kb, kgService: kg, asynqClient: enqueuer}
	r.POST("/batch-reparse", h.BatchReparseKnowledge)
	return r
}

func doBatchReparseRequest(t *testing.T, router *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/batch-reparse", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestBatchReparseKnowledge_EnqueuesDedupedIDsWithProcessConfig(t *testing.T) {
	kb := &stubBatchReparseKBService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 1}}
	kg := &stubBatchReparseKGService{knowledge: []*types.Knowledge{
		{ID: "k1", TenantID: 1, KnowledgeBaseID: "kb-1"},
		{ID: "k2", TenantID: 1, KnowledgeBaseID: "kb-1"},
	}}
	enqueuer := &captureBatchReparseEnqueuer{}
	router := newBatchReparseTestRouter(kb, kg, enqueuer)

	body := map[string]any{
		"kb_id": "kb-1",
		"ids":   []string{" k1 ", "", "k2", "k1"},
		"process_config": map[string]any{
			"parser_engine_overrides": map[string]string{"pdf_force_scanned": "true"},
		},
	}

	w := doBatchReparseRequest(t, router, body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if kg.calls != 1 {
		t.Fatalf("GetKnowledgeBatch calls = %d, want 1", kg.calls)
	}
	if kg.gotTenant != 1 {
		t.Fatalf("GetKnowledgeBatch tenant = %d, want 1", kg.gotTenant)
	}
	wantIDs := []string{"k1", "k2"}
	if !reflect.DeepEqual(kg.gotIDs, wantIDs) {
		t.Fatalf("GetKnowledgeBatch ids = %#v, want %#v", kg.gotIDs, wantIDs)
	}
	if enqueuer.calls != 1 {
		t.Fatalf("Enqueue calls = %d, want 1", enqueuer.calls)
	}
	if enqueuer.task.Type() != types.TypeKnowledgeListReparse {
		t.Fatalf("task type = %q, want %q", enqueuer.task.Type(), types.TypeKnowledgeListReparse)
	}

	var payload types.KnowledgeListReparsePayload
	if err := json.Unmarshal(enqueuer.task.Payload(), &payload); err != nil {
		t.Fatalf("unmarshal task payload: %v", err)
	}
	if payload.TenantID != 1 {
		t.Fatalf("payload tenant = %d, want 1", payload.TenantID)
	}
	if !reflect.DeepEqual(payload.KnowledgeIDs, wantIDs) {
		t.Fatalf("payload ids = %#v, want %#v", payload.KnowledgeIDs, wantIDs)
	}
	if payload.ProcessConfig == nil || payload.ProcessConfig.ParserEngineOverrides["pdf_force_scanned"] != "true" {
		t.Fatalf("payload process config = %#v, want parser_engine_overrides.pdf_force_scanned=true", payload.ProcessConfig)
	}
	if !strings.Contains(w.Body.String(), `"reparse_count":2`) {
		t.Fatalf("response body %q does not include reparse_count=2", w.Body.String())
	}
}

func TestBatchReparseKnowledge_RejectsKnowledgeOutsideRequestedKB(t *testing.T) {
	kb := &stubBatchReparseKBService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 1}}
	kg := &stubBatchReparseKGService{knowledge: []*types.Knowledge{
		{ID: "k1", TenantID: 1, KnowledgeBaseID: "kb-1"},
		{ID: "k2", TenantID: 1, KnowledgeBaseID: "other-kb"},
	}}
	enqueuer := &captureBatchReparseEnqueuer{}
	router := newBatchReparseTestRouter(kb, kg, enqueuer)

	w := doBatchReparseRequest(t, router, map[string]any{
		"kb_id": "kb-1",
		"ids":   []string{"k1", "k2"},
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "does not belong to knowledge base") {
		t.Fatalf("body %q does not explain KB ownership rejection", w.Body.String())
	}
	if enqueuer.calls != 0 {
		t.Fatalf("Enqueue calls = %d, want 0", enqueuer.calls)
	}
}

func TestBatchReparseKnowledge_RejectsOversizedBatchBeforeLookupOrEnqueue(t *testing.T) {
	kb := &stubBatchReparseKBService{
		kb:  &types.KnowledgeBase{ID: "kb-1", TenantID: 1},
		err: errors.New("kb lookup should not be called"),
	}
	kg := &stubBatchReparseKGService{}
	enqueuer := &captureBatchReparseEnqueuer{}
	router := newBatchReparseTestRouter(kb, kg, enqueuer)

	ids := make([]string, 201)
	for i := range ids {
		ids[i] = "k" + strconv.Itoa(i)
	}
	w := doBatchReparseRequest(t, router, map[string]any{
		"kb_id": "kb-1",
		"ids":   ids,
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "max 200") {
		t.Fatalf("body %q does not mention max batch size", w.Body.String())
	}
	if kb.calls != 0 {
		t.Fatalf("GetKnowledgeBaseByID calls = %d, want 0", kb.calls)
	}
	if kg.calls != 0 {
		t.Fatalf("GetKnowledgeBatch calls = %d, want 0", kg.calls)
	}
	if enqueuer.calls != 0 {
		t.Fatalf("Enqueue calls = %d, want 0", enqueuer.calls)
	}
}
