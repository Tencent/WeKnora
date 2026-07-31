package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type knowledgeFolderMoveRouteServiceStub struct {
	calls    int
	tenantID uint64
	input    *types.KnowledgeFolderMoveInput
}

func (s *knowledgeFolderMoveRouteServiceStub) MoveKnowledge(
	ctx context.Context,
	input *types.KnowledgeFolderMoveInput,
) (*types.KnowledgeFolderMoveResult, error) {
	s.calls++
	s.tenantID, _ = types.TenantIDFromContext(ctx)
	if input != nil {
		inputCopy := *input
		inputCopy.KnowledgeIDs = append([]string(nil), input.KnowledgeIDs...)
		s.input = &inputCopy
	}
	return &types.KnowledgeFolderMoveResult{ChangedCount: len(input.KnowledgeIDs)}, nil
}

func TestKnowledgeFolderMoveRouteUsesFrozenTemplateAndDispatches(t *testing.T) {
	moveService := &knowledgeFolderMoveRouteServiceStub{}
	scope := &types.TenantAPIKeyScope{FullAccess: true}
	var registeredGuards *rbacGuards
	engine := newKBRouteTestEngine(t, 1, tenantKBLookupFixture(), scope, func(
		r *gin.RouterGroup,
		guards *rbacGuards,
	) {
		registeredGuards = guards
		RegisterKnowledgeFolderRoutes(
			r,
			handler.NewKnowledgeFolderHandler(nil, moveService),
			guards,
		)
	})

	foundCount := 0
	for _, route := range engine.Routes() {
		if route.Method == http.MethodPost &&
			route.Path == "/api/v1/knowledge-bases/:id/folders/move-knowledge" {
			foundCount++
		}
	}
	require.Equal(t, 1, foundCount)
	policy := mustLookupAPIKeyPolicy(
		t,
		registeredGuards,
		http.MethodPost,
		"/api/v1/knowledge-bases/:id/folders/move-knowledge",
	)
	require.True(t, policy.RequireFullAccess)
	require.True(t, policyHasCapability(policy, types.APIKeyCapabilityIngest))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/knowledge-bases/kb-allowed/folders/move-knowledge",
		strings.NewReader(
			`{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],`+
				`"target_folder_id":""}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, moveService.calls)
	require.Equal(t, uint64(1), moveService.tenantID)
	require.NotNil(t, moveService.input)
	require.Equal(t, "kb-allowed", moveService.input.KnowledgeBaseID)
}

func TestKnowledgeFolderMoveRouteAllowsOwnerContributor(t *testing.T) {
	moveService := &knowledgeFolderMoveRouteServiceStub{}
	engine := newKBRouteTestEngine(t, 1, tenantKBLookupFixture(), nil, func(
		r *gin.RouterGroup,
		guards *rbacGuards,
	) {
		r.Use(func(c *gin.Context) {
			ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, "caller")
			ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleContributor)
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		})
		guards.kbCreator = func(_ *gin.Context) (string, error) {
			return "caller", nil
		}
		RegisterKnowledgeFolderRoutes(
			r,
			handler.NewKnowledgeFolderHandler(nil, moveService),
			guards,
		)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/knowledge-bases/kb-allowed/folders/move-knowledge",
		strings.NewReader(
			`{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],`+
				`"target_folder_id":""}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, moveService.calls)
}

func TestKnowledgeFolderMoveRouteEnforcesAPIKeyCapability(t *testing.T) {
	tests := []struct {
		name      string
		scope     *types.TenantAPIKeyScope
		wantCode  int
		wantCalls int
	}{
		{
			name: "ingest allowed",
			scope: &types.TenantAPIKeyScope{
				KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
				Capabilities:     types.StringArray{string(types.APIKeyCapabilityIngest)},
			},
			wantCode:  http.StatusOK,
			wantCalls: 1,
		},
		{
			name:      "full access allowed",
			scope:     &types.TenantAPIKeyScope{FullAccess: true},
			wantCode:  http.StatusOK,
			wantCalls: 1,
		},
		{
			name: "retrieve only denied",
			scope: &types.TenantAPIKeyScope{
				KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
				Capabilities:     types.StringArray{string(types.APIKeyCapabilityRetrieve)},
			},
			wantCode:  http.StatusForbidden,
			wantCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			moveService := &knowledgeFolderMoveRouteServiceStub{}
			engine := newKBRouteTestEngine(t, 1, tenantKBLookupFixture(), tt.scope, func(
				r *gin.RouterGroup,
				guards *rbacGuards,
			) {
				r.Use(guards.ensureAPIKeyAuthorizer().Middleware())
				RegisterKnowledgeFolderRoutes(
					r,
					handler.NewKnowledgeFolderHandler(nil, moveService),
					guards,
				)
			})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/knowledge-bases/kb-allowed/folders/move-knowledge",
				strings.NewReader(
					`{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],`+
						`"target_folder_id":""}`,
				),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			require.Equal(t, tt.wantCode, recorder.Code, recorder.Body.String())
			require.Equal(t, tt.wantCalls, moveService.calls)
		})
	}
}

func TestKnowledgeFolderMoveRouteDeniesViewerAndNonOwnerContributor(t *testing.T) {
	for _, role := range []types.TenantRole{
		types.TenantRoleViewer,
		types.TenantRoleContributor,
	} {
		t.Run(string(role), func(t *testing.T) {
			moveService := &knowledgeFolderMoveRouteServiceStub{}
			engine := newKBRouteTestEngine(t, 1, tenantKBLookupFixture(), nil, func(
				r *gin.RouterGroup,
				guards *rbacGuards,
			) {
				r.Use(func(c *gin.Context) {
					ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, "caller")
					ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
					c.Request = c.Request.WithContext(ctx)
					c.Next()
				})
				guards.kbCreator = func(_ *gin.Context) (string, error) {
					return "another-user", nil
				}
				RegisterKnowledgeFolderRoutes(
					r,
					handler.NewKnowledgeFolderHandler(nil, moveService),
					guards,
				)
			})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/knowledge-bases/kb-allowed/folders/move-knowledge",
				strings.NewReader(
					`{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],`+
						`"target_folder_id":""}`,
				),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
			require.Zero(t, moveService.calls)
		})
	}
}

func TestKnowledgeFolderMoveRouteDeniesCrossTenantKnowledgeBase(t *testing.T) {
	kbLookup := &stubWikiKBLookup{
		kbs: map[string]*types.KnowledgeBase{
			"kb-victim": {ID: "kb-victim", TenantID: 999},
		},
	}
	moveService := &knowledgeFolderMoveRouteServiceStub{}
	scope := &types.TenantAPIKeyScope{FullAccess: true}
	engine := newKBRouteTestEngine(t, 1, kbLookup, scope, func(
		r *gin.RouterGroup,
		guards *rbacGuards,
	) {
		r.Use(guards.ensureAPIKeyAuthorizer().Middleware())
		RegisterKnowledgeFolderRoutes(
			r,
			handler.NewKnowledgeFolderHandler(nil, moveService),
			guards,
		)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/knowledge-bases/kb-victim/folders/move-knowledge",
		strings.NewReader(
			`{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],`+
				`"target_folder_id":""}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
	require.Zero(t, moveService.calls)
}
