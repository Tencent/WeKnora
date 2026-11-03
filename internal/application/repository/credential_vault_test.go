package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func newCredentialVaultTestRepo(t *testing.T) interfaces.CredentialVaultRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.CredentialVaultItem{}))
	// AutoMigrate cannot express the partial unique index; match the migration.
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX uq_credential_vault_items_owner_site_user
		ON credential_vault_items(tenant_id, user_id, site, username) WHERE deleted_at IS NULL`).Error)
	return NewCredentialVaultRepository(db)
}

func vaultItem(id, user, site, username string) *types.CredentialVaultItem {
	now := time.Now()
	return &types.CredentialVaultItem{
		ID: id, TenantID: 1, UserID: user, Kind: types.CredentialKindLogin, Title: site, Site: site,
		Domains: types.StringArray{site}, Username: username, SecretCiphertext: "enc:v1:x",
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestCredentialVaultRepositoryLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := newCredentialVaultTestRepo(t)

	require.NoError(t, repo.Create(ctx, vaultItem("i1", "web_user:a", "github.com", "alice")))
	require.NoError(t, repo.Create(ctx, vaultItem("i2", "web_user:a", "gitlab.com", "alice")))
	require.NoError(t, repo.Create(ctx, vaultItem("i3", "web_user:b", "github.com", "bob")))
	err := repo.Create(ctx, vaultItem("i4", "web_user:a", "github.com", "alice"))
	require.ErrorIs(t, err, types.ErrCredentialConflict)

	all, err := repo.List(ctx, 1, "web_user:a", "")
	require.NoError(t, err)
	assert.Len(t, all, 2)
	site, err := repo.List(ctx, 1, "web_user:a", "github.com")
	require.NoError(t, err)
	require.Len(t, site, 1)
	assert.Equal(t, types.StringArray{"github.com"}, site[0].Domains)

	other, err := repo.Get(ctx, 1, "web_user:b", "i1")
	require.NoError(t, err)
	assert.Nil(t, other, "another user's row is invisible")
	found, err := repo.FindBySiteUsername(ctx, 1, "web_user:a", "github.com", "alice")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "i1", found.ID)

	n, err := repo.Count(ctx, 1, "web_user:a")
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)

	found.Title = "工作账号"
	found.Domains = types.StringArray{"github.com", "api.github.com"}
	require.NoError(t, repo.Update(ctx, found))
	got, err := repo.Get(ctx, 1, "web_user:a", "i1")
	require.NoError(t, err)
	assert.Equal(t, "工作账号", got.Title)
	assert.Equal(t, types.StringArray{"github.com", "api.github.com"}, got.Domains)

	stranger := *got
	stranger.UserID = "web_user:b"
	require.ErrorIs(t, repo.Update(ctx, &stranger), types.ErrCredentialNotFound)

	at := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	require.NoError(t, repo.Touch(ctx, 1, "web_user:a", "i1", at))
	got, err = repo.Get(ctx, 1, "web_user:a", "i1")
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
	assert.True(t, got.LastUsedAt.Equal(at))

	ok, err := repo.Delete(ctx, 1, "web_user:b", "i1")
	require.NoError(t, err)
	assert.False(t, ok, "another user cannot delete it")
	ok, err = repo.Delete(ctx, 1, "web_user:a", "i1")
	require.NoError(t, err)
	assert.True(t, ok)
	require.NoError(t, repo.Create(ctx, vaultItem("i5", "web_user:a", "github.com", "alice")),
		"a deleted row frees its site and username")
}
