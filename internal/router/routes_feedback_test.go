package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type feedbackRouteKBLookup struct {
	tenantID uint64
}

func (s feedbackRouteKBLookup) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	if id != "kb-1" {
		return nil, repository.ErrKnowledgeBaseNotFound
	}
	return &types.KnowledgeBase{ID: id, TenantID: s.tenantID}, nil
}

type feedbackRouteChunkLookup struct {
	deleted bool
}

func (s feedbackRouteChunkLookup) GetChunkByIDOnly(
	_ context.Context, id string,
) (*types.Chunk, error) {
	if s.deleted || id != "chunk-1" {
		return nil, repository.ErrChunkNotFound
	}
	return &types.Chunk{ID: id, TenantID: 7, KnowledgeBaseID: "kb-1"}, nil
}

type feedbackRouteService struct {
	interfaces.FeedbackService
}

func (feedbackRouteService) GetChunkFeedbackDetails(
	_ context.Context, chunkID string,
) (*types.ChunkFeedbackDetails, error) {
	return &types.ChunkFeedbackDetails{ChunkID: chunkID, KnowledgeBaseID: "kb-1"}, nil
}

func TestFeedbackRoutesDefaultDenyAPIKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	RegisterFeedbackRoutes(v1, &handler.FeedbackHandler{}, g)

	jwtOnlyRoutes := map[string]bool{
		http.MethodPut + " /api/v1/sessions/:session_id/messages/:message_id/feedback":   false,
		http.MethodGet + " /api/v1/chunks/by-id/:id/feedback":                            false,
		http.MethodPost + " /api/v1/knowledge-bases/:id/chunks/:chunk_id/feedback/reset": false,
		http.MethodGet + " /api/v1/knowledge-bases/:id/chunk-feedback":                   false,
		http.MethodGet + " /api/v1/knowledge-bases/:id/chunk-feedback/:chunk_id":         false,
		http.MethodGet + " /api/v1/knowledge-bases/:id/chunk-feedback/:chunk_id/history": false,
		http.MethodPost + " /api/v1/knowledge-bases/:id/chunk-feedback/:chunk_id/reset":  false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := jwtOnlyRoutes[key]; ok {
			jwtOnlyRoutes[key] = true
			if _, declared := g.apiKeyAuthorizer.Lookup(route.Method, route.Path); declared {
				t.Fatalf("feedback route must remain API-key default-deny: %s", key)
			}
		}
	}
	for route, found := range jwtOnlyRoutes {
		if !found {
			t.Fatalf("feedback route not registered: %s", route)
		}
	}
}

func TestFeedbackCompatibilityDetailsRequireOwnerOrAdmin(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		role         types.TenantRole
		userID       string
		tenantID     uint64
		apiKey       bool
		deletedChunk bool
		wantStatus   int
	}{
		{name: "KB owner", role: types.TenantRoleContributor, userID: "owner", tenantID: 7, wantStatus: http.StatusOK},
		{name: "admin", role: types.TenantRoleAdmin, userID: "admin", tenantID: 7, wantStatus: http.StatusOK},
		{name: "viewer", role: types.TenantRoleViewer, userID: "viewer", tenantID: 7, wantStatus: http.StatusForbidden},
		{name: "API key", tenantID: 7, apiKey: true, wantStatus: http.StatusForbidden},
		{name: "cross tenant", role: types.TenantRoleAdmin, userID: "admin", tenantID: 8, wantStatus: http.StatusForbidden},
		{
			name: "deleted or forged chunk", role: types.TenantRoleAdmin, userID: "admin",
			tenantID: 7, deletedChunk: true, wantStatus: http.StatusNotFound,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			enabled := true
			g := &rbacGuards{
				cfg:              &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
				apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer(),
				chunkKBCreatorFromID: func(_ *gin.Context) (string, error) {
					return "owner", nil
				},
				kbService:    feedbackRouteKBLookup{tenantID: 7},
				chunkService: feedbackRouteChunkLookup{deleted: testCase.deletedChunk},
			}
			engine := gin.New()
			engine.Use(middleware.ErrorHandler())
			engine.Use(func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, testCase.tenantID)
				if testCase.apiKey {
					ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
						KeyID: 1, FullAccess: true,
					})
				} else {
					ctx = context.WithValue(ctx, types.TenantRoleContextKey, testCase.role)
					ctx = context.WithValue(ctx, types.UserIDContextKey, testCase.userID)
					ctx = types.WithPrincipal(ctx, types.Principal{
						Type: types.PrincipalWebUser, ID: testCase.userID,
					})
				}
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			engine.Use(g.apiKeyAuthorizer.Middleware())
			v1 := engine.Group("/api/v1")
			RegisterFeedbackRoutes(v1, handler.NewFeedbackHandler(feedbackRouteService{}), g)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet, "/api/v1/chunks/by-id/chunk-1/feedback", nil,
			)
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, testCase.wantStatus, recorder.Code, recorder.Body.String())
			if testCase.wantStatus == http.StatusOK {
				require.Contains(t, recorder.Body.String(), `"chunk_id":"chunk-1"`)
			}
		})
	}
}
