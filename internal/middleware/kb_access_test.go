package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// stubKBLookup is a tiny KBLookup stand-in for tests; satisfies the
// KBLookup interface (a single method) without dragging in the full
// KnowledgeBaseService surface.
type stubKBLookup struct {
	kbs    map[string]*types.KnowledgeBase
	getErr error
}

func (s *stubKBLookup) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, apprepo.ErrKnowledgeBaseNotFound
}

// stubKBShareForGuard implements just the methods the guard touches —
// CheckTenantKBPermission and GetKBSourceTenant. The other methods on
// the interface panic so any unintended new dependency surfaces
// immediately.
type stubKBShareForGuard struct {
	permission map[string]types.OrgMemberRole
	shared     map[string]bool
	source     map[string]uint64
}

func (s *stubKBShareForGuard) CheckTenantKBPermission(_ context.Context, kbID string, _ uint64, _ types.TenantRole) (types.OrgMemberRole, bool, error) {
	if s.shared[kbID] {
		return s.permission[kbID], true, nil
	}
	return "", false, nil
}

func (s *stubKBShareForGuard) GetKBSourceTenant(_ context.Context, kbID string) (uint64, error) {
	if v, ok := s.source[kbID]; ok {
		return v, nil
	}
	return 0, errors.New("not found")
}

func (s *stubKBShareForGuard) ShareKnowledgeBase(context.Context, string, string, string, uint64, types.OrgMemberRole) (*types.KnowledgeBaseShare, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) UpdateSharePermission(context.Context, string, types.OrgMemberRole, string, uint64) error {
	panic("not implemented")
}
func (s *stubKBShareForGuard) RemoveShare(context.Context, string, string, uint64) error {
	panic("not implemented")
}
func (s *stubKBShareForGuard) ListSharesByKnowledgeBase(context.Context, string, uint64) ([]*types.KnowledgeBaseShare, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) ListSharesByOrganization(context.Context, string) ([]*types.KnowledgeBaseShare, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) ListSharedKnowledgeBases(context.Context, uint64, types.TenantRole) ([]*types.SharedKnowledgeBaseInfo, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) ListSharedKnowledgeBasesInOrganization(context.Context, string, uint64, types.TenantRole) ([]*types.OrganizationSharedKnowledgeBaseItem, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) ListSharedKnowledgeBaseIDsByOrganizations(context.Context, []string, uint64) (map[string][]string, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) GetShare(context.Context, string) (*types.KnowledgeBaseShare, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) GetShareByKBAndOrg(context.Context, string, string) (*types.KnowledgeBaseShare, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) HasTenantKBPermission(context.Context, string, uint64, types.TenantRole, types.OrgMemberRole) (bool, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) CountSharesByKnowledgeBaseIDs(context.Context, []string) (map[string]int64, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) CountByOrganizations(context.Context, []string) (map[string]int64, error) {
	panic("not implemented")
}

// runGuard fires a single request through the guard and returns the
// gin recorder + the kb access (if any) the guard stashed.
func runGuard(t *testing.T, tenantID uint64, kbID string, requiredPerm types.OrgMemberRole, kb *types.KnowledgeBase, share *stubKBShareForGuard) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: kbID}}
	req := httptest.NewRequest("GET", "/", nil)
	ctx := context.WithValue(req.Context(), types.TenantIDContextKey, tenantID)
	c.Request = req.WithContext(ctx)

	kbsvc := &stubKBLookup{kbs: map[string]*types.KnowledgeBase{}}
	if kb != nil {
		kbsvc.kbs[kbID] = kb
	}
	guard := RequireKBAccess(KBIDFromParam("id"), requiredPerm, kbsvc, share, nil)
	guard(c)
	return rec, c
}

func TestRequireKBAccess_OwnKB(t *testing.T) {
	rec, c := runGuard(t, 100, "kb-1",
		types.OrgRoleViewer,
		&types.KnowledgeBase{ID: "kb-1", TenantID: 100},
		nil,
	)
	require.False(t, c.IsAborted(), "should pass through")
	require.Equal(t, 200, rec.Code) // gin's default; nothing wrote a status
	access, ok := KBAccessFromContext(c)
	require.True(t, ok)
	require.Equal(t, uint64(100), access.EffectiveTenantID)
	require.Equal(t, types.OrgRoleAdmin, access.Permission, "own KB grants admin")
	// The request context's tenant should still be the caller's own.
	got, ok := types.TenantIDFromContext(c.Request.Context())
	require.True(t, ok)
	require.Equal(t, uint64(100), got)
}

func TestRequireKBAccess_NotFound_Aborts(t *testing.T) {
	rec, c := runGuard(t, 100, "kb-missing", types.OrgRoleViewer, nil, nil)
	require.True(t, c.IsAborted(), "missing KB must abort")
	require.NotEmpty(t, c.Errors)
	_, ok := KBAccessFromContext(c)
	require.False(t, ok, "no access should be stashed on failure")
	_ = rec
}

func TestRequireKBAccess_SharedKB_RewritesTenantContext(t *testing.T) {
	share := &stubKBShareForGuard{
		permission: map[string]types.OrgMemberRole{"kb-shared": types.OrgRoleEditor},
		shared:     map[string]bool{"kb-shared": true},
		source:     map[string]uint64{"kb-shared": 200},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleEditor,
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		share,
	)
	require.False(t, c.IsAborted())
	access, ok := KBAccessFromContext(c)
	require.True(t, ok)
	require.Equal(t, uint64(200), access.EffectiveTenantID)
	// The downstream handler should see tenant=200 from context.
	got, _ := types.TenantIDFromContext(c.Request.Context())
	require.Equal(t, uint64(200), got, "guard must rewrite context to source tenant")
}

func TestRequireKBAccess_SharedKB_PermissionBelowMin_Aborts(t *testing.T) {
	share := &stubKBShareForGuard{
		permission: map[string]types.OrgMemberRole{"kb-shared": types.OrgRoleViewer},
		shared:     map[string]bool{"kb-shared": true},
		source:     map[string]uint64{"kb-shared": 200},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleEditor, // require Editor
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		share,
	)
	require.True(t, c.IsAborted(), "Viewer share must reject when Editor required")
}

func TestRequireKBAccess_NoTenant_Aborts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "kb-x"}}
	c.Request = httptest.NewRequest("GET", "/", nil) // no tenant in context
	guard := RequireKBAccess(KBIDFromParam("id"), types.OrgRoleViewer, &stubKBLookup{}, nil, nil)
	guard(c)
	require.True(t, c.IsAborted())
}
