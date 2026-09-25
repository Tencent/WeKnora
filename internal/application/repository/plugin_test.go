package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

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
	p.RemoteURL, p.RemoteSecret = "https://plugins.example.com", "enc:v1:x"
	require.NoError(t, repo.SavePlugin(ctx, p), "save is an upsert")

	got, err = repo.GetPlugin(ctx, "acme.kit")
	require.NoError(t, err)
	require.Equal(t, "1.1.0", got.ActiveVersion)
	require.Equal(t, types.PluginStateDisabled, got.DesiredState)
	require.Equal(t, "https://plugins.example.com", got.RemoteURL)
	require.Equal(t, "enc:v1:x", got.RemoteSecret)

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

func TestPluginKVRepository(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.PluginKV{}))
	repo := NewPluginKVRepository(db)
	ctx := context.Background()
	put := func(tenant uint64, key, value string, expires *time.Time) {
		t.Helper()
		require.NoError(t, repo.Put(ctx, &types.PluginKV{
			PluginID: "acme.x", TenantID: tenant, Key: key, Value: types.JSON(value),
			ExpiresAt: expires, UpdatedAt: time.Now(),
		}))
	}
	past := time.Now().Add(-time.Minute)
	put(1, "cursor:a", `1`, nil)
	put(1, "cursor:b", `2`, nil)
	put(1, "cursor_x", `3`, nil) // "_" must not act as a LIKE wildcard
	put(1, "old", `4`, &past)
	put(2, "cursor:a", `99`, nil)
	put(1, "cursor:a", `10`, nil) // upsert

	got, err := repo.Get(ctx, "acme.x", 1, "cursor:a")
	require.NoError(t, err)
	require.JSONEq(t, `10`, string(got.Value))
	got, err = repo.Get(ctx, "acme.x", 1, "old")
	require.NoError(t, err)
	require.Nil(t, got, "expired entries read as missing")

	list, err := repo.List(ctx, "acme.x", 1, "cursor:", "", 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	list, err = repo.List(ctx, "acme.x", 1, "cursor:", "cursor:a", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "cursor:b", list[0].Key)

	n, err := repo.Count(ctx, "acme.x", 1)
	require.NoError(t, err)
	require.EqualValues(t, 3, n)
	removed, err := repo.DeleteExpired(ctx, time.Now())
	require.NoError(t, err)
	require.EqualValues(t, 1, removed)
	ok, err := repo.Delete(ctx, "acme.x", 1, "cursor:b")
	require.NoError(t, err)
	require.True(t, ok)
	got, _ = repo.Get(ctx, "acme.x", 2, "cursor:a")
	require.JSONEq(t, `99`, string(got.Value), "tenants are separate partitions")
}

// A column added to installed plugins must be saved on update too, or
// changing it silently does nothing.
func TestPluginUpdateColumnsCoverTheTable(t *testing.T) {
	s, err := schema.Parse(&types.InstalledPlugin{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	for _, name := range s.DBNames {
		switch name {
		case "id", "created_at", "created_by":
			continue
		}
		require.Contains(t, pluginUpdateColumns, name, "SavePlugin does not update %s", name)
	}
}
