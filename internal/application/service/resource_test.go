package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newResourceCatalogForTest(t *testing.T) (interfaces.ResourceCatalog, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.StoredResource{}, &types.ResourceBinding{}, &types.ResourceAccessGrant{}))
	return NewResourceCatalog(repository.NewResourceRepository(db)), db
}

func TestResourceCatalogRegisterResolveAndDeduplicate(t *testing.T) {
	catalog, _ := newResourceCatalogForTest(t)
	ctx := context.Background()
	physical := "storage://backend-a/local://7/exports/a.png"

	ref, err := catalog.Register(ctx, 7, physical, interfaces.ResourceRegistration{Kind: "image", OriginalName: "a.png"})
	require.NoError(t, err)
	require.Regexp(t, `^resource://[0-9A-Za-z_-]{22}$`, ref)

	again, err := catalog.Register(ctx, 7, physical, interfaces.ResourceRegistration{})
	require.NoError(t, err)
	require.Equal(t, ref, again)

	resolvedPath, resource, err := catalog.ResolvePath(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, physical, resolvedPath)
	require.Equal(t, uint64(7), resource.TenantID)
	require.Equal(t, "backend-a", resource.StorageBackendID)
	require.Equal(t, "local", resource.Provider)
}

func TestResourceCatalogBindingAndAccessGrant(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	ctx := context.Background()
	ref, err := catalog.Register(
		ctx,
		9,
		"local://9/exports/report.pdf",
		interfaces.ResourceRegistration{OriginalName: "report.pdf"},
	)
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(ctx, ref, "knowledge", "knowledge-1", "source_file"))

	token, err := catalog.CreateAccessGrant(ctx, ref, time.Minute)
	require.NoError(t, err)
	require.Len(t, token, 22)
	var storedGrant types.ResourceAccessGrant
	require.NoError(t, db.First(&storedGrant).Error)
	require.NotEqual(t, token, storedGrant.TokenHash)
	require.Len(t, storedGrant.TokenHash, 64)
	resource, err := catalog.ResolveAccessGrant(ctx, token)
	require.NoError(t, err)
	require.Equal(t, uint64(9), resource.TenantID)
}

// Rendering one answer resolves the same image many times, and re-reading a
// message history resolves it again on every call. Each resolution used to
// insert a capability row; a live one must be reused instead.
func TestResourceCatalogReusesLiveAccessGrant(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")
	catalog, db := newResourceCatalogForTest(t)
	ctx := context.Background()
	ref, err := catalog.Register(ctx, 9, "local://9/exports/a.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)

	first, err := catalog.CreateAccessGrant(ctx, ref, time.Hour)
	require.NoError(t, err)
	second, err := catalog.CreateAccessGrant(ctx, ref, time.Hour)
	require.NoError(t, err)

	require.Equal(t, first, second)
	var grants int64
	require.NoError(t, db.Model(&types.ResourceAccessGrant{}).Count(&grants).Error)
	require.EqualValues(t, 1, grants, "a live grant must be reused, not duplicated")

	resource, err := catalog.ResolveAccessGrant(ctx, second)
	require.NoError(t, err)
	require.Equal(t, uint64(9), resource.TenantID)
}

// Two resources must never share a grant.
func TestResourceCatalogGrantsArePerResource(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")
	catalog, _ := newResourceCatalogForTest(t)
	ctx := context.Background()
	first, err := catalog.Register(ctx, 9, "local://9/exports/a.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)
	second, err := catalog.Register(ctx, 9, "local://9/exports/b.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)

	firstToken, err := catalog.CreateAccessGrant(ctx, first, time.Hour)
	require.NoError(t, err)
	secondToken, err := catalog.CreateAccessGrant(ctx, second, time.Hour)
	require.NoError(t, err)
	require.NotEqual(t, firstToken, secondToken)
}

// Revoking a grant must stick: the derived token would otherwise recompute to
// the same value and a fresh insert would revive the access it just lost.
func TestResourceCatalogDoesNotReviveRevokedGrant(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")
	catalog, db := newResourceCatalogForTest(t)
	ctx := context.Background()
	ref, err := catalog.Register(ctx, 9, "local://9/exports/a.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)

	revoked, err := catalog.CreateAccessGrant(ctx, ref, time.Hour)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, db.Model(&types.ResourceAccessGrant{}).
		Where("1 = 1").Update("revoked_at", &now).Error)

	fresh, err := catalog.CreateAccessGrant(ctx, ref, time.Hour)
	require.NoError(t, err)
	require.NotEqual(t, revoked, fresh, "a revoked token must not be handed out again")

	_, err = catalog.ResolveAccessGrant(ctx, revoked)
	require.Error(t, err, "the revoked token must stay unusable")
	resource, err := catalog.ResolveAccessGrant(ctx, fresh)
	require.NoError(t, err)
	require.Equal(t, uint64(9), resource.TenantID)
}

// Without a signing key the deployment cannot derive tokens, so grants stay
// random and per-request — the behaviour before reuse existed.
func TestResourceCatalogWithoutSigningKeyMintsFreshGrants(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	catalog, _ := newResourceCatalogForTest(t)
	ctx := context.Background()
	ref, err := catalog.Register(ctx, 9, "local://9/exports/a.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)

	first, err := catalog.CreateAccessGrant(ctx, ref, time.Hour)
	require.NoError(t, err)
	second, err := catalog.CreateAccessGrant(ctx, ref, time.Hour)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}

func TestResourceCatalogRejectsUnsupportedPhysicalPath(t *testing.T) {
	catalog, _ := newResourceCatalogForTest(t)
	_, err := catalog.Register(context.Background(), 7, "https://example.com/a.png", interfaces.ResourceRegistration{})
	require.ErrorContains(t, err, "unsupported provider")
}
