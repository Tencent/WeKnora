package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type folderAnchorServiceStub struct {
	interfaces.KnowledgeService
	anchor  string
	present bool
	calls   int
	err     error
}

func (s *folderAnchorServiceStub) capture(ctx context.Context) {
	s.calls++
	s.anchor, s.present = types.KnowledgeFolderAnchorFromContext(ctx)
}

func (s *folderAnchorServiceStub) CreateKnowledgeFromFile(ctx context.Context, kb string, _ *multipart.FileHeader,
	_ map[string]string, _ *bool, _ string, _ []string, _ string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.capture(ctx)
	return &types.Knowledge{ID: "uploaded", TenantID: 1, KnowledgeBaseID: kb}, s.err
}

func (s *folderAnchorServiceStub) GetKnowledgeBatch(
	_ context.Context,
	tenant uint64,
	ids []string,
) ([]*types.Knowledge, error) {
	rows := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, &types.Knowledge{ID: id, TenantID: tenant, KnowledgeBaseID: "kb-1"})
	}
	return rows, nil
}

func (s *folderAnchorServiceStub) MoveKnowledgeToFolder(
	ctx context.Context,
	_ string,
	ids []string,
	_ string,
) (int64, error) {
	s.capture(ctx)
	return int64(len(ids)), s.err
}

func newFolderAnchorRouter(svc interfaces.KnowledgeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	enabled := true
	h := &KnowledgeHandler{
		cfg:       &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
		kbService: &folderMutationKBServiceStub{kb: folderMutationKB()}, kgService: svc,
	}
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		caller := folderMutationAdmin()
		ctx := types.WithCaller(c.Request.Context(), caller)
		ctx = context.WithValue(ctx, types.TenantIDContextKey, caller.TenantID)
		ctx = context.WithValue(ctx, types.UserIDContextKey, caller.UserID)
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, caller.Role)
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), caller.TenantID)
		c.Set(types.UserIDContextKey.String(), caller.UserID)
		c.Next()
	})
	r.POST("/knowledge-bases/:id/knowledge/file", h.CreateKnowledgeFromFile)
	r.POST("/knowledge/folder", h.MoveKnowledgeToFolder)
	return r
}

func TestFolderAnchorHandlerPreservesAbsentRootAndExisting(t *testing.T) {
	for _, endpoint := range []string{"upload", "move"} {
		for _, input := range []struct {
			name, anchor string
			present      bool
		}{
			{"absent", "", false}, {"root", "", true}, {"existing", "reports", true},
		} {
			t.Run(endpoint+"/"+input.name, func(t *testing.T) {
				svc := &folderAnchorServiceStub{}
				r := newFolderAnchorRouter(svc)
				var req *http.Request
				if endpoint == "upload" {
					var body bytes.Buffer
					writer := multipart.NewWriter(&body)
					file, err := writer.CreateFormFile("file", "unique.txt")
					require.NoError(t, err)
					_, err = file.Write([]byte("unique synthetic anchor handler content"))
					require.NoError(t, err)
					require.NoError(t, writer.WriteField("fileName", "reports/new/unique.txt"))
					if input.present {
						require.NoError(t, writer.WriteField("existing_folder_anchor", input.anchor))
					}
					require.NoError(t, writer.Close())
					req = httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/knowledge/file", &body)
					req.Header.Set("Content-Type", writer.FormDataContentType())
				} else {
					body := map[string]any{
						"kb_id": "kb-1", "knowledge_ids": []string{"doc"}, "folder_path": "reports/new",
					}
					if input.present {
						body["existing_folder_anchor"] = input.anchor
					}
					data, err := json.Marshal(body)
					require.NoError(t, err)
					req = httptest.NewRequest(http.MethodPost, "/knowledge/folder", bytes.NewReader(data))
					req.Header.Set("Content-Type", "application/json")
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				require.Equal(t, 1, svc.calls)
				require.Equal(t, input.present, svc.present)
				require.Equal(t, input.anchor, svc.anchor)
			})
		}
	}
}

func TestFolderAnchorMoveHandlerPreservesConflict(t *testing.T) {
	svc := &folderAnchorServiceStub{err: apperrors.NewConflictError("folder changed")}
	r := newFolderAnchorRouter(svc)
	w := serveFolderMutation(
		r,
		http.MethodPost,
		"/knowledge/folder",
		`{"kb_id":"kb-1","knowledge_ids":["doc"],"folder_path":"reports/new","existing_folder_anchor":"reports"}`,
	)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	require.Equal(t, 1, svc.calls)
}

func TestFolderAnchorUploadHandlerPreservesConflict(t *testing.T) {
	svc := &folderAnchorServiceStub{err: apperrors.NewConflictError("folder changed")}
	r := newFolderAnchorRouter(svc)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "unique.txt")
	require.NoError(t, err)
	_, err = file.Write([]byte("another unique synthetic anchor handler content"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("fileName", "reports/new/unique.txt"))
	require.NoError(t, writer.WriteField("existing_folder_anchor", "reports"))
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/knowledge/file", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	require.Equal(t, 1, svc.calls)
}
