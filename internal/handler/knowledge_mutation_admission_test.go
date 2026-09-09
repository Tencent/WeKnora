package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type mutationAdmissionKnowledge struct {
	interfaces.KnowledgeService
	rows       []*types.Knowledge
	err        error
	tagsCalled bool
	writeGrant bool
}

func (s *mutationAdmissionKnowledge) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return s.rows[0], nil
}

func (s *mutationAdmissionKnowledge) GetKnowledgeByID(context.Context, string) (*types.Knowledge, error) {
	return s.rows[0], nil
}

func (s *mutationAdmissionKnowledge) GetKnowledgeBatch(
	ctx context.Context,
	_ uint64,
	_ []string,
) ([]*types.Knowledge, error) {
	s.writeGrant = access.RequireKBWrite(ctx, &types.KnowledgeBase{ID: "kb", TenantID: 7}) == nil
	return s.rows, nil
}

func (s *mutationAdmissionKnowledge) ListKnowledgeByKnowledgeBaseID(
	context.Context,
	string,
) ([]*types.Knowledge, error) {
	return s.rows, nil
}

func (s *mutationAdmissionKnowledge) UpdateKnowledge(context.Context, *types.Knowledge) error {
	return s.err
}

func (s *mutationAdmissionKnowledge) UpdateImageInfo(context.Context, string, string, string) error {
	return s.err
}

func (s *mutationAdmissionKnowledge) RegenerateKnowledgeSummary(context.Context, string) (*types.Knowledge, error) {
	return nil, s.err
}

func (s *mutationAdmissionKnowledge) UpdateKnowledgeTagBatch(context.Context, string, map[string][]string) error {
	s.tagsCalled = true
	return s.err
}

func mutationRequest(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func admissionKnowledge(moving bool) *types.Knowledge {
	k := &types.Knowledge{ID: "doc", TenantID: 7, KnowledgeBaseID: "kb"}
	if moving {
		k.Metadata = []byte(`{"_knowledge_transfer":{"operation":"move","phase":"moving"}}`)
	}
	return k
}

func admissionKB(creator string) *stubKBService {
	return &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
		return &types.KnowledgeBase{ID: "kb", TenantID: 7, CreatorID: creator}, nil
	}}
}

func TestMovingDocumentAdmissionRejectsBeforeEnqueue(t *testing.T) {
	for _, route := range []string{"single", "batch", "clear", "reparse"} {
		t.Run(route, func(t *testing.T) {
			kg := &mutationAdmissionKnowledge{rows: []*types.Knowledge{admissionKnowledge(true)}}
			if route == "batch" || route == "reparse" {
				kg.rows = append([]*types.Knowledge{{ID: "other", TenantID: 7, KnowledgeBaseID: "kb"}}, kg.rows...)
			}
			queue := &documentDeleteEnqueuer{}
			h := &KnowledgeHandler{
				cfg:         transferHandlerConfig(),
				kbService:   admissionKB("user"),
				kgService:   kg,
				asynqClient: queue,
			}
			r := documentHandlerRouter()
			method, path, body := http.MethodDelete, "/doc", ""
			switch route {
			case "single":
				r.DELETE("/:id", h.DeleteKnowledge)
			case "batch":
				r.POST("/batch", h.BatchDeleteKnowledge)
				method, path, body = http.MethodPost, "/batch", `{"kb_id":"kb","ids":["other","doc"]}`
			case "clear":
				r.DELETE("/:id", h.ClearKnowledgeBaseContents)
				path = "/kb"
			case "reparse":
				r.POST("/reparse", h.BatchReparseKnowledge)
				method, path, body = http.MethodPost, "/reparse", `{"kb_id":"kb","ids":["other","doc"]}`
			}
			w := mutationRequest(r, method, path, body)
			require.Equal(t, 409, w.Code, w.Body.String())
			require.Nil(t, queue.task)
		})
	}
}

func TestBatchMutationRequiresCreatorOrAdminAndWriteGrant(t *testing.T) {
	for _, route := range []string{"reparse", "tags explicit", "tags inferred"} {
		for _, creator := range []string{"user", "colleague"} {
			t.Run(route+creator, func(t *testing.T) {
				kg := &mutationAdmissionKnowledge{rows: []*types.Knowledge{admissionKnowledge(false)}}
				queue := &documentDeleteEnqueuer{}
				h := &KnowledgeHandler{
					cfg:         transferHandlerConfig(),
					kbService:   admissionKB(creator),
					kgService:   kg,
					asynqClient: queue,
				}
				r := transferHandlerRouter(nil)
				method, body := http.MethodPost, `{"kb_id":"kb","ids":["doc"]}`
				if route == "reparse" {
					r.POST("/batch", h.BatchReparseKnowledge)
				} else {
					r.PUT("/batch", h.UpdateKnowledgeTagBatch)
					method = http.MethodPut
					body = `{"kb_id":"kb","updates":{"doc":["tag"]}}`
					if route == "tags inferred" {
						body = `{"updates":{"doc":["tag"]}}`
					}
				}
				w := mutationRequest(r, method, "/batch", body)
				if creator == "user" {
					require.Equal(t, 200, w.Code, w.Body.String())
					if route == "reparse" {
						require.True(t, kg.writeGrant)
						require.NotNil(t, queue.task)
					} else {
						require.True(t, kg.tagsCalled)
					}
				} else {
					require.Equal(t, 403, w.Code, w.Body.String())
					require.Nil(t, queue.task)
					require.False(t, kg.tagsCalled)
				}
			})
		}
	}
}

type mutationChunkService struct {
	interfaces.ChunkService
	err error
}

func (s *mutationChunkService) GetChunkByID(context.Context, string) (*types.Chunk, error) {
	return &types.Chunk{ID: "chunk", KnowledgeID: "doc"}, nil
}

func (s *mutationChunkService) UpdateDocumentChunk(
	context.Context,
	string,
	*string,
	*bool,
	*int,
) (*types.Chunk, error) {
	return nil, s.err
}

func (s *mutationChunkService) RevertDocumentChunk(context.Context, string, int, *int) (*types.Chunk, error) {
	return nil, s.err
}

func TestMutationHandlersPreserveApplicationStatus(t *testing.T) {
	for _, status := range []int{409, 403} {
		for _, route := range []string{"metadata", "image", "summary", "chunk", "revert"} {
			t.Run(fmt.Sprint(status)+route, func(t *testing.T) {
				app := apperrors.NewConflictError("moving")
				if status == 403 {
					app = apperrors.NewForbiddenError("binding changed")
				}
				failure := fmt.Errorf("operation: %w", app)
				kg := &mutationAdmissionKnowledge{rows: []*types.Knowledge{admissionKnowledge(false)}, err: failure}
				h := &KnowledgeHandler{kgService: kg}
				chunks := &ChunkHandler{service: &mutationChunkService{err: failure}}
				r := documentHandlerRouter()
				method, path, body := http.MethodPut, "/doc", `{"title":"new"}`
				switch route {
				case "metadata":
					r.PUT("/:id", h.UpdateKnowledge)
				case "image":
					r.PUT("/:id/:chunk_id", h.UpdateImageInfo)
					path = "/doc/chunk"
					body = `{"image_info":"[]"}`
				case "summary":
					r.POST("/:id", h.RegenerateKnowledgeSummary)
					method = http.MethodPost
					body = ""
				case "chunk":
					r.PUT("/:knowledge_id/:id", chunks.UpdateChunk)
					path = "/doc/chunk"
					body = `{"content":"new"}`
				case "revert":
					r.POST("/:knowledge_id/:id", chunks.RevertChunk)
					method = http.MethodPost
					path = "/doc/chunk"
					body = `{"revision":0}`
				}
				w := mutationRequest(r, method, path, body)
				require.Equal(t, status, w.Code, w.Body.String())
			})
		}
	}
}

func TestBatchReparsePreservesAdminAndRBACRolloutPolicy(t *testing.T) {
	for _, enforced := range []bool{true, false} {
		t.Run(fmt.Sprint(enforced), func(t *testing.T) {
			cfg := transferHandlerConfig()
			cfg.Tenant.EnableRBAC = &enforced
			kg := &mutationAdmissionKnowledge{rows: []*types.Knowledge{admissionKnowledge(false)}}
			queue := &documentDeleteEnqueuer{}
			h := &KnowledgeHandler{cfg: cfg, kbService: admissionKB("colleague"), kgService: kg, asynqClient: queue}
			r := transferHandlerRouter(nil)
			if enforced {
				r = documentHandlerRouter()
			} // Admin may edit another creator's KB.
			r.POST("/batch", h.BatchReparseKnowledge)
			w := mutationRequest(r, http.MethodPost, "/batch", `{"kb_id":"kb","ids":["doc"]}`)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.True(t, kg.writeGrant)
			require.NotNil(t, queue.task)
		})
	}
}
