package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// The lookup helpers translate handler/service errors into the
// middleware sentinel set; that translation is where bugs hide (404
// becoming 403, cross-tenant leaks, etc.), so we test those edges
// directly rather than via end-to-end HTTP.

// stubKBService implements just enough of interfaces.KnowledgeBaseService
// to drive KBCreatorLookup. Any other method panics so the test fails
// loudly if a future lookup refactor reaches outside the contract.
type stubKBService struct {
	interfaces.KnowledgeBaseService
	get func(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

func (s *stubKBService) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	return s.get(ctx, id)
}

func newKBLookupCtx(t *testing.T, tenantID uint64, paramID string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, tenantID)
	c.Request = c.Request.WithContext(ctx)
	c.Params = gin.Params{{Key: "id", Value: paramID}}
	return c
}

func TestKBCreatorLookup_NotFoundMapsToSentinel(t *testing.T) {
	h := &KnowledgeBaseHandler{service: &stubKBService{
		get: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return nil, apprepo.ErrKnowledgeBaseNotFound
		},
	}}
	_, err := h.KBCreatorLookup(newKBLookupCtx(t, 1, "kb-1"))
	if !errors.Is(err, middleware.ErrResourceNotFound) {
		t.Fatalf("expected ErrResourceNotFound, got %v", err)
	}
}

func TestKBCreatorLookup_CrossTenantIsHiddenAsNotFound(t *testing.T) {
	// A foreign-tenant KB must NEVER leak via the ownership shortcut.
	// Returning the row's CreatorID would let a user-id collision pass
	// the middleware's "creator == uid" branch; hiding it as not-found
	// keeps the lookup strictly tenant-scoped.
	h := &KnowledgeBaseHandler{service: &stubKBService{
		get: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb-1", TenantID: 999, CreatorID: "u1"}, nil
		},
	}}
	_, err := h.KBCreatorLookup(newKBLookupCtx(t, 1, "kb-1"))
	if !errors.Is(err, middleware.ErrResourceNotFound) {
		t.Fatalf("cross-tenant KB must surface as not-found, got %v", err)
	}
}

func TestKBCreatorLookup_OwnerMatchReturnsCreatorID(t *testing.T) {
	h := &KnowledgeBaseHandler{service: &stubKBService{
		get: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb-1", TenantID: 1, CreatorID: "u-creator"}, nil
		},
	}}
	creator, err := h.KBCreatorLookup(newKBLookupCtx(t, 1, "kb-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creator != "u-creator" {
		t.Fatalf("expected creator=u-creator, got %q", creator)
	}
}

func TestKBCreatorLookup_MissingTenantContext(t *testing.T) {
	// Without tenant context, the lookup can't decide scope. Surfacing
	// a real error (which middleware turns into 503) is safer than
	// silently returning ErrResourceNotFound: the request shouldn't be
	// happening at all.
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	c.Params = gin.Params{{Key: "id", Value: "kb-1"}}
	h := &KnowledgeBaseHandler{service: &stubKBService{
		get: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			t.Fatalf("service must not be called without tenant context")
			return nil, nil
		},
	}}
	_, err := h.KBCreatorLookup(c)
	if err == nil {
		t.Fatalf("expected error when tenant context missing")
	}
	if errors.Is(err, middleware.ErrResourceNotFound) {
		t.Fatalf("missing tenant must not be reported as not-found: %v", err)
	}
}

// stubAgentService mirrors stubKBService for the agent lookup tests.
type stubAgentService struct {
	interfaces.CustomAgentService
	get func(ctx context.Context, id string) (*types.CustomAgent, error)
}

func (s *stubAgentService) GetAgentByID(ctx context.Context, id string) (*types.CustomAgent, error) {
	return s.get(ctx, id)
}

func TestAgentCreatorLookup_BuiltinIsTenantOwned(t *testing.T) {
	h := &CustomAgentHandler{service: &stubAgentService{
		get: func(_ context.Context, _ string) (*types.CustomAgent, error) {
			return &types.CustomAgent{
				ID: "smart-reasoning", TenantID: 1, IsBuiltin: true, CreatedBy: "ignored",
			}, nil
		},
	}}
	creator, err := h.AgentCreatorLookup(newKBLookupCtx(t, 1, "smart-reasoning"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creator != "" {
		t.Fatalf("built-in agent must surface as tenant-owned (empty creator), got %q", creator)
	}
}

func TestAgentCreatorLookup_AgentNotFoundMapsToSentinel(t *testing.T) {
	h := &CustomAgentHandler{service: &stubAgentService{
		get: func(_ context.Context, _ string) (*types.CustomAgent, error) {
			return nil, service.ErrAgentNotFound
		},
	}}
	_, err := h.AgentCreatorLookup(newKBLookupCtx(t, 1, "missing-agent"))
	if !errors.Is(err, middleware.ErrResourceNotFound) {
		t.Fatalf("expected ErrResourceNotFound, got %v", err)
	}
}
