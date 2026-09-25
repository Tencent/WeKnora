package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSandboxConfigTestRepo(t *testing.T) (TenantSandboxConfigRepository, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.TenantSandboxConfigEntity{}))
	// AutoMigrate cannot express the partial unique index, so add it here to
	// match the production migration.
	require.NoError(t, db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_sandbox_configs_tenant_name
		 ON tenant_sandbox_configs (tenant_id, name) WHERE deleted_at IS NULL`).Error)
	return NewTenantSandboxConfigRepository(db), db
}

func TestSandboxConfigRepoIsolatesTenants(t *testing.T) {
	repo, _ := newSandboxConfigTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    1,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      &types.TenantSandboxConfig{SandboxType: "e2b"},
	}))
	require.NoError(t, repo.Create(ctx, &types.TenantSandboxConfigEntity{
		ID:          "cfg-b",
		TenantID:    2,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      &types.TenantSandboxConfig{SandboxType: "e2b"},
	}))

	// Same name in a different workspace is fine; cross-tenant reads are not.
	got, err := repo.GetByID(ctx, 1, "cfg-b")
	require.NoError(t, err)
	require.Nil(t, got, "must not read another workspace's config")

	list, err := repo.ListByTenant(ctx, 1)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "cfg-a", list[0].ID)
}

func TestSandboxConfigRepoRejectsDuplicateNameInTenant(t *testing.T) {
	repo, _ := newSandboxConfigTestRepo(t)
	ctx := context.Background()

	base := func(id string) *types.TenantSandboxConfigEntity {
		return &types.TenantSandboxConfigEntity{
			ID:          id,
			TenantID:    1,
			Name:        "prod",
			SandboxType: "e2b",
			Config:      &types.TenantSandboxConfig{SandboxType: "e2b"},
		}
	}
	require.NoError(t, repo.Create(ctx, base("cfg-1")))
	require.Error(t, repo.Create(ctx, base("cfg-2")))
}

func TestSandboxConfigRepoSoftDeleteHidesRow(t *testing.T) {
	repo, _ := newSandboxConfigTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    1,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      &types.TenantSandboxConfig{SandboxType: "e2b"},
	}))
	require.NoError(t, repo.SoftDelete(ctx, 1, "cfg-a"))

	got, err := repo.GetByID(ctx, 1, "cfg-a")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestSandboxConfigRepoCordonRoundTrip(t *testing.T) {
	repo, _ := newSandboxConfigTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    1,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      &types.TenantSandboxConfig{SandboxType: "e2b"},
	}))

	at := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.SetCordon(ctx, 1, "cfg-a", at))

	got, err := repo.GetByID(ctx, 1, "cfg-a")
	require.NoError(t, err)
	require.NotNil(t, got.CordonedAt)
	require.True(t, got.IsCordoned(at.Add(time.Second), types.SandboxCordonLease))

	require.NoError(t, repo.ClearCordon(ctx, 1, "cfg-a"))
	got, err = repo.GetByID(ctx, 1, "cfg-a")
	require.NoError(t, err)
	require.Nil(t, got.CordonedAt)
}
