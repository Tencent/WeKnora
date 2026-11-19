package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type graphSearchHandlerServiceStub struct {
	calls int
	req   types.GraphSearchRequest
	kb    *types.KnowledgeBase
	grant bool
	err   error
}

func (s *graphSearchHandlerServiceStub) Search(
	ctx context.Context, kb *types.KnowledgeBase, req types.GraphSearchRequest,
) (*types.GraphSearchResult, error) {
	s.calls++
	s.req, s.kb = req, kb
	s.grant = access.HasKBGrant(ctx, kb.ID, kb.TenantID, types.OrgRoleViewer)
	return &types.GraphSearchResult{
		KnowledgeBaseID: kb.ID, Query: req.Query,
		Entities: []types.GraphSearchEntity{}, Relationships: []types.GraphSearchRelation{},
		SourceChunks: []types.GraphSearchSourceChunk{},
	}, s.err
}

func graphSearchHandlerRouter(
	kbService interfaces.KnowledgeBaseService, shares interfaces.KBShareService,
	svc interfaces.GraphSearchService, caller uint64, scope *types.TenantAPIKeyScope,
	agents ...interfaces.AgentShareService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), caller)
		c.Set(types.UserIDContextKey.String(), "reader")
		if scope != nil {
			c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), *scope))
		}
		c.Next()
	})
	kbHandler := &KnowledgeBaseHandler{service: kbService, kbShareService: shares}
	if len(agents) > 0 {
		kbHandler.agentShareService = agents[0]
	}
	h := NewGraphSearchHandler(kbHandler, svc)
	r.POST("/knowledge-bases/:id/graph-search", h.Search)
	return r
}

func graphSearchHandlerKB(owner uint64) *stubKBOnlyService {
	return &stubKBOnlyService{getByID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
		return &types.KnowledgeBase{ID: id, TenantID: owner}, nil
	}}
}

func TestGraphSearchHandlerValidation(t *testing.T) {
	for name, body := range map[string]string{
		"empty":            "",
		"missing query":    `{}`,
		"null root":        `null`,
		"query whitespace": `{"query":"   "}`,
		"wrong query type": `{"query":1}`,
		"unknown field":    `{"query":"Acme","tenant_id":7}`,
		"trailing object":  `{"query":"Acme"} {}`,
		"fractional limit": `{"query":"Acme","max_nodes":1.5}`,
		"negative limit":   `{"query":"Acme","max_source_chunks":-1}`,
		"excessive limit":  `{"query":"Acme","max_relations":201}`,
		"long query":       `{"query":"` + strings.Repeat("中", 513) + `"}`,
		"large body":       `{"query":"Acme"}` + strings.Repeat(" ", 8192),
	} {
		t.Run(name, func(t *testing.T) {
			svc := &graphSearchHandlerServiceStub{}
			w := httptest.NewRecorder()
			graphSearchHandlerRouter(graphSearchHandlerKB(1), nil, svc, 1, nil).
				ServeHTTP(w, httptest.NewRequest(http.MethodPost,
					"/knowledge-bases/kb/graph-search", strings.NewReader(body)))
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Zero(t, svc.calls)
			require.Contains(t, w.Body.String(), `"code":1000`)
		})
	}
}

func TestGraphSearchHandlerSharedAccessAndErrors(t *testing.T) {
	shares := &handlerKBShareStub{}
	svc := &graphSearchHandlerServiceStub{}
	r := graphSearchHandlerRouter(graphSearchHandlerKB(7), shares, svc, 1, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb/graph-search",
		strings.NewReader(`{"query":" Acme "}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.True(t, svc.grant, "the service receives the exact KB read grant")
	require.Equal(t, uint64(7), svc.kb.TenantID)
	require.Equal(t, uint64(1), shares.caller)
	require.Equal(t, "Acme", svc.req.Query)
	require.Equal(t, 50, svc.req.MaxNodes)

	svc.err = apperrors.NewServiceUnavailableError("graph unavailable")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb/graph-search",
		strings.NewReader(`{"query":"Acme"}`)))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.NotContains(t, w.Body.String(), `"entities"`, "errors never expose partial results")
}

func TestGraphSearchHandlerDeniesBeforeQuery(t *testing.T) {
	for _, tt := range []struct {
		name      string
		caller    uint64
		scope     *types.TenantAPIKeyScope
		kbService interfaces.KnowledgeBaseService
		status    int
	}{
		{name: "unauthenticated", caller: 0, kbService: graphSearchHandlerKB(1), status: 401},
		{name: "unshared KB", caller: 1, kbService: graphSearchHandlerKB(7), status: 403},
		{
			name: "key outside allow list", caller: 1, kbService: graphSearchHandlerKB(1), status: 403,
			scope: &types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"other"}},
		},
		{
			name: "missing KB", caller: 1, status: 404,
			kbService: &stubKBOnlyService{getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
				return nil, repository.ErrKnowledgeBaseNotFound
			}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &graphSearchHandlerServiceStub{}
			r := graphSearchHandlerRouter(tt.kbService, nil, svc, tt.caller, tt.scope)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb/graph-search",
				strings.NewReader(`{"query":"Acme"}`)))
			require.Equal(t, tt.status, w.Code, w.Body.String())
			require.Zero(t, svc.calls)
		})
	}
}

type graphSearchAgentStub struct {
	interfaces.AgentShareService
	config types.CustomAgentConfig
	owner  uint64
}

func (s *graphSearchAgentStub) GetSharedAgentForTenant(
	context.Context, uint64, types.TenantRole, string, ...uint64,
) (*types.CustomAgent, error) {
	return &types.CustomAgent{ID: "shared", TenantID: s.owner, Config: s.config}, nil
}

func TestGraphSearchHandlerSharedAgentScope(t *testing.T) {
	for _, tt := range []struct {
		name, mode string
		kbs        []string
		owner      uint64
		status     int
	}{
		{"selected KB", "selected", []string{"kb"}, 7, 200},
		{"none is not all", "none", nil, 7, 403},
		{"empty selected is not all", "selected", nil, 7, 403},
		{"another selection", "selected", []string{"other"}, 7, 403},
		{"all in another owner", "all", nil, 8, 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &graphSearchHandlerServiceStub{}
			agent := &graphSearchAgentStub{
				owner:  tt.owner,
				config: types.CustomAgentConfig{KBSelectionMode: tt.mode, KnowledgeBases: tt.kbs},
			}
			r := graphSearchHandlerRouter(graphSearchHandlerKB(7), nil, svc, 1, nil, agent)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost,
				"/knowledge-bases/kb/graph-search?agent_id=shared", strings.NewReader(`{"query":"Acme"}`)))
			require.Equal(t, tt.status, w.Code, w.Body.String())
			if tt.status == http.StatusOK {
				require.Equal(t, 1, svc.calls)
				require.True(t, svc.grant)
			} else {
				require.Zero(t, svc.calls)
			}
		})
	}
}
