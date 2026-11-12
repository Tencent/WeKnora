package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type nativeTreeHandlerStub struct {
	interfaces.KnowledgeService
	called  bool
	options types.KnowledgeChildrenOptions
	mark    bool
	roots   []types.KnowledgeTreeRoot
}

func TestNativeKnowledgeHybridExpansionKeepsScoredHits(t *testing.T) {
	svc := &hybridSearchTestService{
		results: []*types.SearchResult{{ID: "hit", KnowledgeID: "root", Score: 0.83, Content: "scored chunk"}},
	}
	tree := &nativeTreeHandlerStub{}
	r := newHybridSearchTestRouter(svc, tree)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb/hybrid-search",
		strings.NewReader(`{"query_text":"question","children_depth":2}`),
	)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.True(t, tree.called)
	require.Equal(t, 2, tree.options.Depth)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	data := body["data"].([]any)
	require.Len(t, data, 1)
	require.Equal(t, 0.83, data[0].(map[string]any)["score"])
	require.Equal(t, "hit", data[0].(map[string]any)["id"])
	expanded := body["expanded"].([]any)
	require.Len(t, expanded, 1)
	require.NotContains(t, expanded[0].(map[string]any), "score")
}

func (s *nativeTreeHandlerStub) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "root", TenantID: 7, KnowledgeBaseID: "kb"}, nil
}

func (s *nativeTreeHandlerStub) GetKnowledgeByID(context.Context, string) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "root", TenantID: 7, KnowledgeBaseID: "kb"}, nil
}

func (s *nativeTreeHandlerStub) KnowledgeChildren(
	_ context.Context,
	roots []types.KnowledgeTreeRoot,
	o types.KnowledgeChildrenOptions,
) ([]types.KnowledgeChild, error) {
	s.called = true
	s.options = o
	s.roots = roots
	if len(roots) == 0 {
		return nil, nil
	}
	return []types.KnowledgeChild{
		{
			KnowledgeID:       "child",
			ParentKnowledgeID: "root",
			AnchorKnowledgeID: roots[0].KnowledgeID,
			Depth:             1,
			Content:           "full child",
			ContentURL:        "/api/v1/knowledge/child/preview",
		},
	}, nil
}

func (s *nativeTreeHandlerStub) PrepareKnowledgeCascade(
	_ context.Context,
	_ uint64,
	_ string,
	ids []string,
	mark bool,
) ([]string, error) {
	s.called = true
	s.mark = mark
	return []string{ids[0], "child", "grand"}, nil
}

func (s *nativeTreeHandlerStub) CheckKnowledgeTreeMove(context.Context, uint64, string, string) error {
	return nil
}

func nativeTreeRouter(h *KnowledgeHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := types.WithCaller(
			c.Request.Context(),
			types.Caller{TenantID: 7, UserID: "owner", Role: types.TenantRoleAdmin},
		)
		c.Request = c.Request.WithContext(types.WithExecutionTenant(ctx, 7))
		c.Next()
	})
	r.GET("/knowledge/:id", h.GetKnowledge)
	r.DELETE("/knowledge/:id", h.DeleteKnowledge)
	return r
}

func TestNativeKnowledgeHTTPReadIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		query  string
		called bool
		code   int
	}{
		{"", false, 200},
		{"?children_depth=1", true, 200},
		{"?children_depth=2", true, 200},
		{"?children_depth=-1", true, 200},
		{"?children_depth=bad", false, 400},
	} {
		svc := &nativeTreeHandlerStub{}
		h := &KnowledgeHandler{kgService: svc}
		r := nativeTreeRouter(h)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/knowledge/root"+tc.query, nil))
		require.Equal(t, tc.code, w.Code, w.Body.String())
		require.Equal(t, tc.called, svc.called)
		if tc.called {
			var response map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			data := response["data"].(map[string]any)
			require.Len(t, data["children"].([]any), 1)
		} else if tc.code == 200 {
			require.NotContains(t, w.Body.String(), "children")
		}
	}
}

func TestNativeKnowledgeHTTPDeleteCascadePayload(t *testing.T) {
	for _, cascade := range []bool{false, true} {
		svc := &nativeTreeHandlerStub{}
		queue := &rebuildIndexEnqueuer{}
		h := &KnowledgeHandler{kgService: svc, asynqClient: queue}
		r := nativeTreeRouter(h)
		w := httptest.NewRecorder()
		path := "/knowledge/root"
		if cascade {
			path += "?cascade_children=true"
		}
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, path, nil))
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Len(t, queue.tasks, 1)
		var payload types.KnowledgeListDeletePayload
		require.NoError(t, json.Unmarshal(queue.tasks[0].Payload(), &payload))
		require.Equal(t, cascade, payload.CascadeChildren)
		require.Equal(t, cascade, svc.called)
		require.False(t, svc.mark, "HTTP admission must not mark deleting before enqueue succeeds")
		if cascade {
			require.Equal(t, []string{"root", "child", "grand"}, payload.KnowledgeIDs)
		} else {
			require.Equal(t, []string{"root"}, payload.KnowledgeIDs)
		}
	}
}

type nativeImportHandlerStub struct {
	interfaces.KnowledgeService
	parent string
}

func (s *nativeImportHandlerStub) CreateKnowledgeFromManual(
	_ context.Context,
	kb string,
	p *types.ManualKnowledgePayload,
	_ string,
) (*types.Knowledge, error) {
	s.parent = p.ParentKnowledgeID
	return &types.Knowledge{ID: "new-child", KnowledgeBaseID: kb, ParentKnowledgeID: s.parent}, nil
}

func (s *nativeImportHandlerStub) CreateKnowledgeFromFileWithParent(
	_ context.Context,
	kb string,
	_ *multipart.FileHeader,
	_ map[string]string,
	_ *bool,
	_ string,
	_ []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
	parent string,
) (*types.Knowledge, error) {
	s.parent = parent
	return &types.Knowledge{ID: "new-child", KnowledgeBaseID: kb, ParentKnowledgeID: parent}, nil
}

func (s *nativeImportHandlerStub) CreateKnowledgeFromURLWithParent(
	context.Context,
	string,
	string,
	string,
	string,
	*bool,
	string,
	[]string,
	string,
	*types.KnowledgeProcessOverrides,
	string,
) (*types.Knowledge, error) {
	panic("URL transport is not used in this synthetic test")
}

func TestNativeKnowledgeHTTPUploadTopLevelParent(t *testing.T) {
	for _, kind := range []string{"manual", "file"} {
		t.Run(kind, func(t *testing.T) {
			svc := &nativeImportHandlerStub{}
			h := &KnowledgeHandler{
				kgService: svc,
				kbService: &downloadKBStub{kb: &types.KnowledgeBase{ID: "kb", TenantID: 7}},
			}
			r := nativeTreeRouter(h)
			r.POST("/knowledge-bases/:id/knowledge/manual", h.CreateManualKnowledge)
			r.POST("/knowledge-bases/:id/knowledge/file", h.CreateKnowledgeFromFile)
			var body bytes.Buffer
			contentType := "application/json"
			if kind == "manual" {
				body.WriteString(`{"title":"child","content":"full child","parent_knowledge_id":"parent"}`)
			} else {
				m := multipart.NewWriter(&body)
				require.NoError(t, m.WriteField("parent_knowledge_id", "parent"))
				f, err := m.CreateFormFile("file", "child.txt")
				require.NoError(t, err)
				_, err = f.Write([]byte("full child"))
				require.NoError(t, err)
				require.NoError(t, m.Close())
				contentType = m.FormDataContentType()
			}
			req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb/knowledge/"+kind, &body)
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Equal(t, "parent", svc.parent)
			require.Contains(t, w.Body.String(), `"parent_knowledge_id":"parent"`)
		})
	}
}

type nativeMultiKBSearchStub struct {
	*hybridSearchTestService
	owners map[string]uint64
}

func (s *nativeMultiKBSearchStub) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: id, TenantID: s.owners[id]}, nil
}

func TestNativeKnowledgeHybridUsesEachAuthorizedHitKB(t *testing.T) {
	for _, tc := range []struct {
		name          string
		owner         uint64
		keyRestricted bool
		hitKB         string
		ok            bool
	}{
		{"same tenant secondary KB", 1, false, "kb-b", true},
		{"shared secondary KB", 8, false, "kb-b", true},
		{"API key denies secondary KB", 1, true, "kb-b", false},
		{"unexpected hit outside requested KBs", 1, false, "other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-a/hybrid-search", nil)
			ctx := types.WithCaller(
				c.Request.Context(),
				types.Caller{TenantID: 1, UserID: "viewer", Role: types.TenantRoleViewer},
			)
			if tc.keyRestricted {
				ctx = types.WithTenantAPIKeyScope(
					ctx,
					types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"kb-a"}},
				)
			}
			c.Request = c.Request.WithContext(types.WithExecutionTenant(ctx, 1))
			tree := &nativeTreeHandlerStub{}
			svc := &nativeMultiKBSearchStub{
				hybridSearchTestService: &hybridSearchTestService{},
				owners:                  map[string]uint64{"kb-b": tc.owner},
			}
			h := &KnowledgeBaseHandler{
				service:          svc,
				knowledgeService: tree,
				kbShareService:   &downloadShareStub{permission: types.OrgRoleViewer},
			}
			hits := []*types.SearchResult{{ID: "hit-b", KnowledgeID: "doc-b", KnowledgeBaseID: tc.hitKB, Score: .83}}
			response := gin.H{"data": hits}
			ok := h.expandHybridChildren(
				c,
				1,
				"kb-a",
				hits,
				response,
				types.KnowledgeChildrenOptions{Depth: 2, Limit: 200},
				[]string{"kb-a", "kb-b"},
			)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.ok, tree.called)
			if ok {
				require.Equal(
					t,
					[]types.KnowledgeTreeRoot{{TenantID: tc.owner, KBID: "kb-b", KnowledgeID: "doc-b"}},
					tree.roots,
				)
				require.Equal(t, .83, hits[0].Score)
			} else {
				require.NotEmpty(t, c.Errors)
			}
		})
	}
}

func TestNativeKnowledgeExpandedURLsPreserveSharedSelectors(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/knowledge/root?children_depth=1&agent_id=shared-agent&agent_source_tenant_id=8",
		nil,
	)
	tree := &nativeTreeHandlerStub{}
	rows := []*types.Knowledge{{ID: "root", TenantID: 8, KnowledgeBaseID: "kb"}}
	require.True(t, expandKnowledgeRows(c.Request.Context(), c, tree, rows))
	require.Len(t, rows[0].Children, 1)
	require.Contains(t, rows[0].Children[0].ContentURL, "agent_id=shared-agent")
	require.Contains(t, rows[0].Children[0].ContentURL, "agent_source_tenant_id=8")
}
