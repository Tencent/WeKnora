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

func TestAgentCreatorLookup_CrossTenantIsHiddenAsNotFound(t *testing.T) {
	// Defensive: service.GetAgentByID already scopes by tenant, but
	// AgentCreatorLookup re-checks the row's TenantID anyway. If a
	// future refactor loosens the service-layer scope (or adds a
	// cross-tenant variant that gets wired here by mistake), a
	// foreign-tenant row must NEVER leak through the ownership
	// shortcut. Pinning the contract here so the defensive branch
	// survives refactors.
	h := &CustomAgentHandler{service: &stubAgentService{
		get: func(_ context.Context, _ string) (*types.CustomAgent, error) {
			return &types.CustomAgent{
				ID: "agent-1", TenantID: 999, CreatedBy: "u1",
			}, nil
		},
	}}
	_, err := h.AgentCreatorLookup(newKBLookupCtx(t, 1, "agent-1"))
	if !errors.Is(err, middleware.ErrResourceNotFound) {
		t.Fatalf("cross-tenant agent must surface as not-found, got %v", err)
	}
}

// PR 5 (#1303) per-KB ownership lookups. The chain helper lives in the
// service layer; the lookups here are pure adapters that translate the
// repository sentinels into middleware.ErrResourceNotFound. Tests focus
// on those translations — the service-side chain has its own tests in
// internal/application/service.

// stubKgService stands in for interfaces.KnowledgeService for the
// knowledge / chunk lookups. Embedding the interface keeps every
// non-stubbed method nil-panicky on purpose: a future refactor that
// reaches outside GetOwningKBCreatorID should fail loudly, not silently.
type stubKgService struct {
	interfaces.KnowledgeService
	getOwningKBCreatorID func(ctx context.Context, knowledgeID string) (string, error)
}

func (s *stubKgService) GetOwningKBCreatorID(ctx context.Context, knowledgeID string) (string, error) {
	return s.getOwningKBCreatorID(ctx, knowledgeID)
}

// newKnowledgeLookupCtx builds a gin.Context shaped like the
// /knowledge/:id route — :id holds a knowledge id.
func newKnowledgeLookupCtx(t *testing.T, tenantID uint64, knowledgeID string) *gin.Context {
	return newKBLookupCtx(t, tenantID, knowledgeID)
}

// newChunkLookupCtx builds a gin.Context shaped like the
// /chunks/:knowledge_id/:id route — only :knowledge_id is set since
// that's all the lookup reads.
func newChunkLookupCtx(t *testing.T, tenantID uint64, knowledgeID string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, tenantID)
	c.Request = c.Request.WithContext(ctx)
	c.Params = gin.Params{{Key: "knowledge_id", Value: knowledgeID}}
	return c
}

// newWikiLookupCtx builds a gin.Context shaped like the
// /knowledgebase/:kb_id/wiki/... routes.
func newWikiLookupCtx(t *testing.T, tenantID uint64, kbID string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, tenantID)
	c.Request = c.Request.WithContext(ctx)
	c.Params = gin.Params{{Key: "kb_id", Value: kbID}}
	return c
}

func TestKBCreatorLookupFromKnowledgeID_NotFoundMapsToSentinel(t *testing.T) {
	// Either sentinel from the repo (knowledge missing OR its KB missing)
	// must surface as ErrResourceNotFound — same behaviour as the
	// KB-level lookup so middleware translates uniformly.
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"knowledge-not-found", apprepo.ErrKnowledgeNotFound},
		{"kb-not-found", apprepo.ErrKnowledgeBaseNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &KnowledgeHandler{kgService: &stubKgService{
				getOwningKBCreatorID: func(_ context.Context, _ string) (string, error) {
					return "", tc.err
				},
			}}
			_, err := h.KBCreatorLookupFromKnowledgeID(newKnowledgeLookupCtx(t, 1, "kn-1"))
			if !errors.Is(err, middleware.ErrResourceNotFound) {
				t.Fatalf("expected ErrResourceNotFound, got %v", err)
			}
		})
	}
}

func TestKBCreatorLookupFromKnowledgeID_OwnerMatchReturnsCreatorID(t *testing.T) {
	h := &KnowledgeHandler{kgService: &stubKgService{
		getOwningKBCreatorID: func(_ context.Context, _ string) (string, error) {
			return "u-kb-creator", nil
		},
	}}
	creator, err := h.KBCreatorLookupFromKnowledgeID(newKnowledgeLookupCtx(t, 1, "kn-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creator != "u-kb-creator" {
		t.Fatalf("expected creator=u-kb-creator, got %q", creator)
	}
}

func TestKBCreatorLookupFromKnowledgeID_MissingTenantContext(t *testing.T) {
	// Mirror KBCreatorLookup_MissingTenantContext: no tenant context
	// means auth didn't complete; the lookup must NOT call the service
	// and must NOT return ErrResourceNotFound (which would silently turn
	// into a "fail open" pass).
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	c.Params = gin.Params{{Key: "id", Value: "kn-1"}}
	h := &KnowledgeHandler{kgService: &stubKgService{
		getOwningKBCreatorID: func(_ context.Context, _ string) (string, error) {
			t.Fatalf("service must not be called without tenant context")
			return "", nil
		},
	}}
	_, err := h.KBCreatorLookupFromKnowledgeID(c)
	if err == nil {
		t.Fatalf("expected error when tenant context missing")
	}
	if errors.Is(err, middleware.ErrResourceNotFound) {
		t.Fatalf("missing tenant must not be reported as not-found: %v", err)
	}
}

func TestKBCreatorLookupFromKnowledgeIDParam_ChunkRouteShape(t *testing.T) {
	// Chunk routes use :knowledge_id rather than :id; the lookup must
	// read from the right param name. A bug here would silently break
	// chunk delete/update on rollout.
	h := &ChunkHandler{kgService: &stubKgService{
		getOwningKBCreatorID: func(_ context.Context, knowledgeID string) (string, error) {
			if knowledgeID != "kn-from-chunk-route" {
				t.Fatalf("lookup must read :knowledge_id, got %q", knowledgeID)
			}
			return "u-kb-creator", nil
		},
	}}
	creator, err := h.KBCreatorLookupFromKnowledgeIDParam(
		newChunkLookupCtx(t, 1, "kn-from-chunk-route"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creator != "u-kb-creator" {
		t.Fatalf("expected creator=u-kb-creator, got %q", creator)
	}
}

func TestKBCreatorLookupFromKnowledgeIDParam_NotFoundMapsToSentinel(t *testing.T) {
	h := &ChunkHandler{kgService: &stubKgService{
		getOwningKBCreatorID: func(_ context.Context, _ string) (string, error) {
			return "", apprepo.ErrKnowledgeNotFound
		},
	}}
	_, err := h.KBCreatorLookupFromKnowledgeIDParam(newChunkLookupCtx(t, 1, "kn-1"))
	if !errors.Is(err, middleware.ErrResourceNotFound) {
		t.Fatalf("expected ErrResourceNotFound, got %v", err)
	}
}

func TestKBCreatorLookupFromKBPath_NotFoundMapsToSentinel(t *testing.T) {
	// Wiki lookup path goes straight to the KB service (no chain hop),
	// so its translation behaviour mirrors KBCreatorLookup.
	h := &WikiPageHandler{kbService: &stubKBService{
		get: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return nil, apprepo.ErrKnowledgeBaseNotFound
		},
	}}
	_, err := h.KBCreatorLookupFromKBPath(newWikiLookupCtx(t, 1, "kb-1"))
	if !errors.Is(err, middleware.ErrResourceNotFound) {
		t.Fatalf("expected ErrResourceNotFound, got %v", err)
	}
}

func TestKBCreatorLookupFromKBPath_CrossTenantIsHiddenAsNotFound(t *testing.T) {
	// Same security boundary as KBCreatorLookup_CrossTenantIsHiddenAsNotFound.
	// repo.GetKnowledgeBaseByID is unscoped, so the lookup MUST re-check
	// TenantID; otherwise a probed cross-tenant kb_id whose CreatorID
	// happens to match the caller's user id would slip past the
	// ownership shortcut.
	h := &WikiPageHandler{kbService: &stubKBService{
		get: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb-1", TenantID: 999, CreatorID: "u1"}, nil
		},
	}}
	_, err := h.KBCreatorLookupFromKBPath(newWikiLookupCtx(t, 1, "kb-1"))
	if !errors.Is(err, middleware.ErrResourceNotFound) {
		t.Fatalf("cross-tenant KB must surface as not-found, got %v", err)
	}
}

func TestKBCreatorLookupFromKBPath_OwnerMatchReturnsCreatorID(t *testing.T) {
	h := &WikiPageHandler{kbService: &stubKBService{
		get: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb-1", TenantID: 1, CreatorID: "u-creator"}, nil
		},
	}}
	creator, err := h.KBCreatorLookupFromKBPath(newWikiLookupCtx(t, 1, "kb-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creator != "u-creator" {
		t.Fatalf("expected creator=u-creator, got %q", creator)
	}
}
