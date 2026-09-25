package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestPluginRepository(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.InstalledPlugin{}, &types.PluginVersion{}))
	repo := NewPluginRepository(db)
	ctx := context.Background()

	got, err := repo.GetPlugin(ctx, "acme.kit")
	require.NoError(t, err)
	require.Nil(t, got, "missing plugins read as nil")

	now := time.Now()
	for _, v := range []string{"1.0.0", "1.1.0"} {
		require.NoError(t, repo.SaveVersion(ctx, &types.PluginVersion{
			PluginID: "acme.kit", Version: v, Digest: "sha256:" + v, Manifest: types.JSON(`{}`),
			PackageURI: "local://p-" + v, CreatedAt: now,
		}))
		now = now.Add(time.Second)
	}
	p := &types.InstalledPlugin{
		ID: "acme.kit", ActiveVersion: "1.0.0", DesiredState: types.PluginStateEnabled, Runtime: "declarative",
		Source: types.JSON(`{"kind":"upload"}`), GrantedPerms: types.JSON(`{}`),
	}
	require.NoError(t, repo.SavePlugin(ctx, p))
	p.ActiveVersion = "1.1.0"
	p.DesiredState = types.PluginStateDisabled
	require.NoError(t, repo.SavePlugin(ctx, p), "save is an upsert")

	got, err = repo.GetPlugin(ctx, "acme.kit")
	require.NoError(t, err)
	require.Equal(t, "1.1.0", got.ActiveVersion)
	require.Equal(t, types.PluginStateDisabled, got.DesiredState)

	versions, err := repo.ListVersions(ctx, "acme.kit")
	require.NoError(t, err)
	require.Len(t, versions, 2)
	require.Equal(t, "1.1.0", versions[0].Version, "newest first")

	v, err := repo.GetVersion(ctx, "acme.kit", "1.0.0")
	require.NoError(t, err)
	require.Equal(t, "local://p-1.0.0", v.PackageURI)

	require.NoError(t, repo.DeletePlugin(ctx, "acme.kit"))
	all, err := repo.ListPlugins(ctx)
	require.NoError(t, err)
	require.Empty(t, all)
	versions, err = repo.ListVersions(ctx, "acme.kit")
	require.NoError(t, err)
	require.Empty(t, versions, "deleting a plugin removes its versions")
}

func TestPluginTenantSettingUpsertColumns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.PluginTenantSetting{}))
	repo := NewPluginTenantSettingRepository(db)
	ctx := context.Background()

	got, err := repo.Get(ctx, 1, "acme.kit")
	require.NoError(t, err)
	require.Nil(t, got)

	require.NoError(t, repo.Upsert(ctx, &types.PluginTenantSetting{
		TenantID: 1, PluginID: "acme.kit", Enabled: true, UpdatedAt: time.Now(),
	}, "enabled"))
	require.NoError(t, repo.Upsert(ctx, &types.PluginTenantSetting{
		TenantID: 1, PluginID: "acme.kit", Enabled: false, Config: types.JSON(`{"region":"eu"}`), UpdatedAt: time.Now(),
	}, "config"))
	got, err = repo.Get(ctx, 1, "acme.kit")
	require.NoError(t, err)
	require.True(t, got.Enabled, "saving config must leave the switch alone")
	require.JSONEq(t, `{"region":"eu"}`, string(got.Config))

	require.NoError(t, repo.Upsert(ctx, &types.PluginTenantSetting{
		TenantID: 1, PluginID: "acme.kit", Enabled: false, UpdatedAt: time.Now(),
	}, "enabled"))
	got, _ = repo.Get(ctx, 1, "acme.kit")
	require.False(t, got.Enabled)
	require.JSONEq(t, `{"region":"eu"}`, string(got.Config), "flipping the switch must keep the config")
}
