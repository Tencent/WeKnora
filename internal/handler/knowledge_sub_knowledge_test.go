package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type subKnowledgeReadStub struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
	called    bool
	tenant    uint64
	kb        string
}

func (s *subKnowledgeReadStub) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

func (s *subKnowledgeReadStub) GetSubKnowledge(
	ctx context.Context,
	_ string,
	kb string,
	_ string,
	_ int,
) (*types.SubKnowledgeGroup, error) {
	s.called = true
	s.tenant = types.MustTenantIDFromContext(ctx)
	s.kb = kb
	return &types.SubKnowledgeGroup{
		KnowledgeBaseID: s.knowledge.KnowledgeBaseID,
		Items:           []types.SubKnowledgeItem{},
	}, nil
}

func (s *subKnowledgeReadStub) SetSubKnowledge(context.Context, string, string, *types.SubKnowledgeInput) error {
	return nil
}

func TestSubKnowledgeGroupHandlerAuthorizesAnchorBeforeLookup(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner uint64
		share interfaces.KBShareService
		scope *types.TenantAPIKeyScope
		code  int
	}{
		{"own", 7, nil, nil, 200},
		{"shared_viewer", 8, &downloadShareStub{permission: types.OrgRoleViewer}, nil, 200},
		{"key_outside_kb", 7, nil, &types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"other"}}, 403},
		{
			"cross_tenant_denied", 8, &downloadShareStub{permission: types.OrgRoleViewer},
			&types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"other"}}, 403,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			svc := &subKnowledgeReadStub{
				knowledge: &types.Knowledge{ID: "seed", TenantID: tc.owner, KnowledgeBaseID: "kb"},
			}
			h := &KnowledgeHandler{kgService: svc, kbShareService: tc.share}
			r := gin.New()
			r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				ctx := types.WithCaller(
					c.Request.Context(),
					types.Caller{TenantID: 7, UserID: "viewer", Role: types.TenantRoleViewer},
				)
				ctx = types.WithExecutionTenant(ctx, 7)
				if tc.scope != nil {
					ctx = types.WithTenantAPIKeyScope(ctx, *tc.scope)
				}
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			r.GET("/knowledge/:id/sub-knowledge", h.GetSubKnowledge)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/knowledge/seed/sub-knowledge", nil))
			require.Equal(t, tc.code, w.Code, w.Body.String())
			require.Equal(t, tc.code == 200, svc.called)
			if svc.called {
				require.Equal(t, tc.owner, svc.tenant)
				require.Equal(t, "kb", svc.kb, "the service must receive the KB whose grant was checked")
			}
		})
	}
}

func TestSubKnowledgeGroupResponseBudgetAndImageProjection(t *testing.T) {
	x := &types.SubKnowledgeGroup{KnowledgeBaseID: "kb", Items: []types.SubKnowledgeItem{
		{
			KnowledgeID: "one",
			ParseStatus: types.ParseStatusCompleted,
			Content:     "中文图文",
			Images: []types.ImageInfo{
				{URL: "local://tenant/private/image.png", Caption: "internal caption"},
				{URL: "https://img.example/a.png"},
				{URL: "javascript:alert(1)"},
			},
		},
		{KnowledgeID: "two", Content: "第二条"},
	}}
	prepareSubKnowledgeResponse(x, 8, nil, true)
	require.True(t, utf8.ValidString(x.Items[0].Content))
	require.True(t, x.Items[0].ContentTruncated)
	require.LessOrEqual(t, len(x.Items[0].Content)+len(x.Items[1].Content), 8)
	require.Equal(t, "/api/v1/knowledge/one/preview", x.Items[0].ContentURL)
	require.Len(t, x.Items[0].ImageReferences, 2)
	require.Contains(t, x.Items[0].ImageReferences[0].URL, "/api/v1/knowledge-bases/kb/files?file_path=")
	b, e := json.Marshal(x)
	require.NoError(t, e)
	for _, forbidden := range []string{
		"internal caption", "javascript:", "original_url", "metadata", "tenant_id", "file_path\"",
	} {
		require.False(t, strings.Contains(string(b), forbidden), string(b))
	}
	prepareSubKnowledgeResponse(
		x,
		8,
		url.Values{"agent_id": {"shared-agent"}, types.AgentSourceTenantIDParam: {"8"}},
		true,
	)
	require.Contains(t, x.Items[0].ContentURL, "agent_id=shared-agent")
	require.Contains(t, x.Items[0].ContentURL, "agent_source_tenant_id=8")
}

func TestSubKnowledgeResponseRespectsFilePermissionAndPartialMetadata(t *testing.T) {
	x := &types.SubKnowledgeGroup{
		KnowledgeBaseID: "kb",
		Items: []types.SubKnowledgeItem{
			{
				KnowledgeID:  "one",
				ParseStatus:  types.ParseStatusCompleted,
				ImagesStatus: "PARTIAL_METADATA_UNAVAILABLE",
				Images: []types.ImageInfo{
					{URL: "local://tenant/image.png"},
					{URL: "https://public.invalid/image.png"},
				},
			},
		},
	}
	prepareSubKnowledgeResponse(x, 1024, nil, false)
	require.Equal(t, "PARTIAL_METADATA_UNAVAILABLE", x.Items[0].ImagesStatus)
	require.Equal(t, "unavailable_for_api_key_scope", x.Items[0].ImageReferences[0].Access)
	require.Empty(t, x.Items[0].ImageReferences[0].URL)
	require.Equal(t, "external", x.Items[0].ImageReferences[1].Access)
}
