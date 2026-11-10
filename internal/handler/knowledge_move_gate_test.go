package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MoveKnowledge cross-store reuse_vectors gate — handler-level pre-flight tests.
//
// reuse_vectors copies vector indices between KBs through the source store only;
// a cross-store reuse_vectors move would corrupt vector data. The handler must
// reject it synchronously (before enqueueing the async move) and point the
// caller at reparse mode. reparse moves and same-store reuse_vectors must pass
// the gate (they fail later here only because the stub knowledge does not exist,
// which proves the gate did not short-circuit them).

type stubMoveKBService struct {
	interfaces.KnowledgeBaseService
	byID func(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

func (s *stubMoveKBService) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	return s.byID(ctx, id)
}

// stubMoveKGService.GetKnowledgeByID always reports not-found, so any request
// that PASSES the store gate fails at the later knowledge-ID validation with a
// distinct message — letting the test tell "rejected by gate" apart from
// "passed the gate."
type stubMoveKGService struct {
	interfaces.KnowledgeService
}

func (s *stubMoveKGService) GetKnowledgeByID(_ context.Context, _ string) (*types.Knowledge, error) {
	return nil, errors.New("knowledge not found")
}

func newMoveGateRouter(kb interfaces.KnowledgeBaseService, kg interfaces.KnowledgeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	// asynqClient stays nil: every case here either rejects at the gate or
	// fails at knowledge-ID validation, so enqueue is never reached. A nil
	// deref would mean the gate let a request through to enqueue unexpectedly.
	h := &KnowledgeHandler{kbService: kb, kgService: kg}
	r.POST("/move", h.MoveKnowledge)
	return r
}

func TestMoveKnowledge_CrossStoreGate(t *testing.T) {
	storeA, storeB := "store-a", "store-b"
	kbWith := func(id string, store *string) *types.KnowledgeBase {
		return &types.KnowledgeBase{ID: id, TenantID: 1, Type: "document",
			EmbeddingModelID: "m1", VectorStoreID: store}
	}

	tests := []struct {
		name           string
		mode           string
		srcStore       *string
		dstStore       *string
		wantStatus     int
		wantBodyHas    string // substring that must appear
		wantBodyNotHas string // substring that must NOT appear (gate message)
	}{
		{
			name: "reuse_vectors cross-store rejected by gate",
			mode: "reuse_vectors", srcStore: &storeA, dstStore: &storeB,
			wantStatus: http.StatusBadRequest, wantBodyHas: "different vector stores",
		},
		{
			name: "reparse cross-store passes gate (fails later at knowledge lookup)",
			mode: "reparse", srcStore: &storeA, dstStore: &storeB,
			wantStatus: http.StatusBadRequest, wantBodyNotHas: "different vector stores",
		},
		{
			name: "reuse_vectors same-store passes gate",
			mode: "reuse_vectors", srcStore: &storeA, dstStore: &storeA,
			wantStatus: http.StatusBadRequest, wantBodyNotHas: "different vector stores",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := &stubMoveKBService{byID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
				switch id {
				case "kb-src":
					return kbWith("kb-src", tt.srcStore), nil
				case "kb-dst":
					return kbWith("kb-dst", tt.dstStore), nil
				}
				return nil, errors.New("kb not found")
			}}
			router := newMoveGateRouter(kb, &stubMoveKGService{})

			body, _ := json.Marshal(map[string]any{
				"knowledge_ids": []string{"k1"},
				"source_kb_id":  "kb-src",
				"target_kb_id":  "kb-dst",
				"mode":          tt.mode,
			})
			req := httptest.NewRequest(http.MethodPost, "/move", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantBodyHas != "" && !strings.Contains(w.Body.String(), tt.wantBodyHas) {
				t.Fatalf("body %q does not contain %q", w.Body.String(), tt.wantBodyHas)
			}
			if tt.wantBodyNotHas != "" && strings.Contains(w.Body.String(), tt.wantBodyNotHas) {
				t.Fatalf("body %q unexpectedly contains gate message %q (gate short-circuited a valid move)",
					w.Body.String(), tt.wantBodyNotHas)
			}
		})
	}
}
