package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type readerDownloadKnowledge struct {
	downloadKnowledgeStub
	document *types.Knowledge
}

func (s *readerDownloadKnowledge) GetReaderDownloadFile(
	ctx context.Context, kbID, id string,
) (io.ReadCloser, string, error) {
	if !access.HasKBGrant(ctx, kbID, s.document.TenantID, types.OrgRoleViewer) || s.document.KnowledgeBaseID != kbID {
		return nil, "", repository.ErrKnowledgeNotFound
	}
	return s.GetKnowledgeFile(ctx, id)
}

func TestReaderDownloadAuthorization(t *testing.T) {
	const own, shared = uint64(7), uint64(9)
	for _, tc := range []struct {
		name        string
		listed      bool
		user        bool
		source      uint64
		directShare bool
		docKB       string
		docTenant   uint64
		status      int
	}{
		{"allowlisted own KB", true, true, own, false, "kb", own, 200},
		{"allowlisted direct read-only share", true, true, shared, true, "kb", shared, 200},
		{"missing opt-in", false, true, own, false, "kb", own, 403},
		{"no authenticated web user", true, false, own, false, "kb", own, 403},
		{"shared-agent visibility alone is insufficient", true, true, shared, false, "kb", shared, 403},
		{"different document KB", true, true, own, false, "other", own, 404},
		{"different document tenant", true, true, own, false, "kb", shared, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{KnowledgeBase: &config.KnowledgeBaseConfig{ReaderDownloadKBIDs: []string{"other-kb"}}}
			if tc.listed {
				cfg.KnowledgeBase.ReaderDownloadKBIDs = []string{"kb"}
			}
			files := &readerDownloadKnowledge{document: &types.Knowledge{
				ID: "doc", Type: "file", KnowledgeBaseID: tc.docKB, TenantID: tc.docTenant,
			}}
			files.expectedTenant = tc.source
			files.names = map[string]string{"doc": "document.txt"}
			h := &KnowledgeHandler{cfg: cfg, kgService: files, kbService: &downloadKBStub{
				kb: &types.KnowledgeBase{ID: "kb", TenantID: tc.source},
			}}
			agentShare := &handlerAgentAccessStub{}
			h.agentShareService = agentShare
			if tc.directShare {
				h.kbShareService = &downloadShareStub{permission: types.OrgRoleViewer}
			}
			r := gin.New()
			r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				caller := types.Caller{TenantID: own, UserID: "u", Role: types.TenantRoleViewer}
				ctx := types.WithCaller(c.Request.Context(), caller)
				ctx = types.WithExecutionTenant(ctx, own)
				if tc.user {
					ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "u"})
					ctx = types.WithPrincipalAuthSource(ctx, types.AuthSourceWeb)
				} else {
					ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalAPITenant, ID: "key"})
				}
				c.Request = c.Request.WithContext(ctx)
				c.Set(types.TenantIDContextKey.String(), own)
				c.Next()
			})
			r.GET("/kb/:id/files/:knowledge_id", h.DownloadKnowledgeFileAsReader)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kb/kb/files/doc", nil))
			require.Zero(t, agentShare.anyCalls)
			require.Equal(t, tc.status, rec.Code, "body=%s", rec.Body.String())
			if tc.status == http.StatusOK {
				require.Equal(t, []string{"doc"}, files.opened)
				require.Equal(t, "原文-doc", rec.Body.String())
			} else {
				require.Empty(t, files.opened)
			}
		})
	}
}
