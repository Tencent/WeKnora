package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type folderMutationKBServiceStub struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *folderMutationKBServiceStub) GetKnowledgeBaseByID(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type folderMutationServiceStub struct {
	interfaces.KnowledgeService
	calls      int
	kbID       string
	parentPath string
	name       string
	path       string
	tenantID   uint64
	caller     types.Caller
	err        error
}

func (s *folderMutationServiceStub) capture(ctx context.Context, kbID string) {
	s.calls++
	s.kbID = kbID
	s.tenantID, _ = types.TenantIDFromContext(ctx)
	s.caller = types.CallerFromContext(ctx)
}

func (s *folderMutationServiceStub) CreateKnowledgeFolder(
	ctx context.Context, kbID, parentPath, name string,
) (*types.KnowledgeFolderNode, error) {
	s.capture(ctx, kbID)
	s.parentPath, s.name = parentPath, name
	if s.err != nil {
		return nil, s.err
	}
	path := name
	if parentPath != "" {
		path = parentPath + "/" + name
	}
	return &types.KnowledgeFolderNode{Path: path, Name: name}, nil
}

func (s *folderMutationServiceStub) DeleteKnowledgeFolder(ctx context.Context, kbID, path string) error {
	s.capture(ctx, kbID)
	s.path = path
	return s.err
}

func newFolderMutationRouter(svc interfaces.KnowledgeService, caller types.Caller, kb *types.KnowledgeBase,
	scope *types.TenantAPIKeyScope,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	enabled := true
	h := &KnowledgeHandler{
		cfg:       &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
		kbService: &folderMutationKBServiceStub{kb: kb},
		kgService: svc,
	}
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := types.WithCaller(c.Request.Context(), caller)
		ctx = context.WithValue(ctx, types.TenantIDContextKey, caller.TenantID)
		ctx = context.WithValue(ctx, types.UserIDContextKey, caller.UserID)
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, caller.Role)
		if scope != nil {
			ctx = types.WithTenantAPIKeyScope(ctx, *scope)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), caller.TenantID)
		c.Set(types.UserIDContextKey.String(), caller.UserID)
		c.Next()
	})
	r.POST("/knowledge-bases/:id/knowledge/folders", h.CreateKnowledgeFolder)
	r.DELETE("/knowledge-bases/:id/knowledge/folders/empty", h.DeleteKnowledgeFolder)
	return r
}

func folderMutationAdmin() types.Caller {
	return types.Caller{TenantID: 1, UserID: "folder-owner", Role: types.TenantRoleAdmin}
}

func folderMutationKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID: "kb-1", TenantID: 1, CreatorID: "folder-owner", Type: types.KnowledgeBaseTypeDocument,
	}
}

func serveFolderMutation(r *gin.Engine, method, url, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCreateKnowledgeFolderHandlerReturnsEmptyFolder(t *testing.T) {
	for _, parent := range []string{"", "项目/资料"} {
		t.Run(parent, func(t *testing.T) {
			svc := &folderMutationServiceStub{}
			caller := folderMutationAdmin()
			r := newFolderMutationRouter(svc, caller, folderMutationKB(), nil)
			body, err := json.Marshal(CreateKnowledgeFolderRequest{ParentPath: parent, Name: "会议"})
			require.NoError(t, err)
			w := serveFolderMutation(r, http.MethodPost, "/knowledge-bases/kb-1/knowledge/folders", string(body))
			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			var response struct {
				Success bool                      `json:"success"`
				Data    types.KnowledgeFolderNode `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.True(t, response.Success)
			wantPath := "会议"
			if parent != "" {
				wantPath = parent + "/会议"
			}
			require.Equal(t, wantPath, response.Data.Path)
			require.Zero(t, response.Data.TotalCount)
			require.Equal(t, 1, svc.calls)
			require.Equal(t, "kb-1", svc.kbID)
			require.Equal(t, parent, svc.parentPath)
			require.Equal(t, "会议", svc.name)
			require.Equal(t, uint64(1), svc.tenantID)
			require.Equal(t, caller, svc.caller)
		})
	}
}

func TestCreateKnowledgeFolderHandlerRejectsInvalidBody(t *testing.T) {
	for _, body := range []string{`{`, `{}`, `{"name":""}`, `{"name":123}`, `{"name":"会议","parent_path":123}`} {
		t.Run(body, func(t *testing.T) {
			svc := &folderMutationServiceStub{}
			r := newFolderMutationRouter(svc, folderMutationAdmin(), folderMutationKB(), nil)
			w := serveFolderMutation(r, http.MethodPost, "/knowledge-bases/kb-1/knowledge/folders", body)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Zero(t, svc.calls)
		})
	}
}

func TestDeleteKnowledgeFolderHandlerReturnsPath(t *testing.T) {
	svc := &folderMutationServiceStub{}
	caller := folderMutationAdmin()
	r := newFolderMutationRouter(svc, caller, folderMutationKB(), nil)
	w := serveFolderMutation(
		r,
		http.MethodDelete,
		"/knowledge-bases/kb-1/knowledge/folders/empty?path=docs%2Fempty",
		"",
	)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, "docs/empty", response.Data.Path)
	require.Equal(t, "docs/empty", svc.path)
	require.Equal(t, 1, svc.calls)
	require.Equal(t, uint64(1), svc.tenantID)
	require.Equal(t, caller, svc.caller)
}

func TestDeleteKnowledgeFolderHandlerRejectsMissingOrRootPath(t *testing.T) {
	for _, query := range []string{"", "?path=", "?path=%20%20"} {
		t.Run(query, func(t *testing.T) {
			svc := &folderMutationServiceStub{}
			r := newFolderMutationRouter(svc, folderMutationAdmin(), folderMutationKB(), nil)
			w := serveFolderMutation(r, http.MethodDelete, "/knowledge-bases/kb-1/knowledge/folders/empty"+query, "")
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Zero(t, svc.calls)
		})
	}
}

func TestKnowledgeFolderMutationHandlersPreserveServiceErrors(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		for _, tc := range []struct {
			name   string
			err    error
			status int
		}{
			{"invalid path or name", apperrors.NewBadRequestError("invalid folder"), http.StatusBadRequest},
			{"permission", apperrors.NewForbiddenError("permission changed"), http.StatusForbidden},
			{"missing folder", apperrors.NewNotFoundError("missing folder"), http.StatusNotFound},
			{"duplicate or nonempty", apperrors.NewConflictError("folder conflict"), http.StatusConflict},
			{"storage failure", errors.New("database unavailable"), http.StatusInternalServerError},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				svc := &folderMutationServiceStub{err: tc.err}
				r := newFolderMutationRouter(svc, folderMutationAdmin(), folderMutationKB(), nil)
				route := "/knowledge-bases/kb-1/knowledge/folders"
				if method == http.MethodDelete {
					route += "/empty"
				}
				w := serveFolderMutation(r, method, route+"?path=docs", `{"name":"会议","parent_path":"docs"}`)
				require.Equal(t, tc.status, w.Code, w.Body.String())
				require.Equal(t, 1, svc.calls)
			})
		}
	}
}

func TestKnowledgeFolderMutationHandlersEnforcePermissions(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		for _, tc := range []struct {
			name    string
			caller  types.Caller
			creator string
			scope   *types.TenantAPIKeyScope
			status  int
		}{
			{
				"viewer",
				types.Caller{TenantID: 1, UserID: "viewer", Role: types.TenantRoleViewer},
				"folder-owner",
				nil,
				http.StatusForbidden,
			},
			{
				"contributor noncreator",
				types.Caller{TenantID: 1, UserID: "other", Role: types.TenantRoleContributor},
				"folder-owner",
				nil,
				http.StatusForbidden,
			},
			{
				"contributor creator",
				types.Caller{TenantID: 1, UserID: "folder-owner", Role: types.TenantRoleContributor},
				"folder-owner",
				nil,
				0,
			},
			{
				"API key out of KB scope",
				folderMutationAdmin(),
				"folder-owner",
				&types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"other-kb"}},
				http.StatusForbidden,
			},
			{"unauthenticated", types.Caller{}, "folder-owner", nil, http.StatusUnauthorized},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				kb := folderMutationKB()
				kb.CreatorID = tc.creator
				svc := &folderMutationServiceStub{}
				r := newFolderMutationRouter(svc, tc.caller, kb, tc.scope)
				route := "/knowledge-bases/kb-1/knowledge/folders"
				if method == http.MethodDelete {
					route += "/empty"
				}
				w := serveFolderMutation(r, method, route+"?path=docs", `{"name":"会议"}`)
				want := tc.status
				if want == 0 {
					want = http.StatusCreated
					if method == http.MethodDelete {
						want = http.StatusOK
					}
				}
				require.Equal(t, want, w.Code, w.Body.String())
				if tc.status == 0 {
					require.Equal(t, 1, svc.calls)
				} else {
					require.Zero(t, svc.calls)
				}
			})
		}
	}
}

func TestClearKnowledgeBaseEnqueuesFolderCleanup(t *testing.T) {
	for _, rows := range [][]*types.Knowledge{nil, {admissionKnowledge(false)}} {
		name := "empty KB"
		if len(rows) > 0 {
			name = "KB with documents"
		}
		t.Run(name, func(t *testing.T) {
			queue := &documentDeleteEnqueuer{}
			// Only the list method is implemented. A synchronous folder mutation
			// would panic through the embedded interface instead of enqueueing it.
			kg := &mutationAdmissionKnowledge{rows: rows}
			h := &KnowledgeHandler{
				cfg: transferHandlerConfig(), kbService: admissionKB("user"),
				kgService: kg, asynqClient: queue,
			}
			r := documentHandlerRouter()
			r.DELETE("/:id", h.ClearKnowledgeBaseContents)
			w := serveFolderMutation(r, http.MethodDelete, "/kb", "")
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NotNil(t, queue.task, "even a KB containing only empty folders needs cleanup")
			require.Equal(t, types.TypeKnowledgeListDelete, queue.task.Type())
			var payload types.KnowledgeListDeletePayload
			require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
			require.True(t, payload.ClearFolders)
			require.Equal(t, "kb", payload.KnowledgeBaseID)
			require.Equal(t, uint64(7), payload.TenantID)
			if len(rows) == 0 {
				require.Empty(t, payload.KnowledgeIDs)
			} else {
				require.Equal(t, []string{"doc"}, payload.KnowledgeIDs)
			}
		})
	}
}

func TestDocumentDeleteHandlersDoNotRequestFolderCleanup(t *testing.T) {
	for _, route := range []string{"single", "batch"} {
		t.Run(route, func(t *testing.T) {
			queue := &documentDeleteEnqueuer{}
			kg := &mutationAdmissionKnowledge{rows: []*types.Knowledge{admissionKnowledge(false)}}
			h := &KnowledgeHandler{
				cfg: transferHandlerConfig(), kbService: admissionKB("user"),
				kgService: kg, asynqClient: queue,
			}
			r := documentHandlerRouter()
			method, path, body := http.MethodDelete, "/doc", ""
			if route == "single" {
				r.DELETE("/:id", h.DeleteKnowledge)
			} else {
				r.POST("/batch", h.BatchDeleteKnowledge)
				method, path, body = http.MethodPost, "/batch", `{"kb_id":"kb","ids":["doc"]}`
			}
			w := mutationRequest(r, method, path, body)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NotNil(t, queue.task)
			var payload types.KnowledgeListDeletePayload
			require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
			require.False(t, payload.ClearFolders)
			require.Equal(t, []string{"doc"}, payload.KnowledgeIDs)
		})
	}
}
