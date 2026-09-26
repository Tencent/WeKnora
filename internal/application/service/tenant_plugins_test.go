package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// Deleting a workspace uninstalls the plugins it registered and removes its
// plugin switches and data; other workspaces keep theirs.
func TestDeleteTenantRemovesItsPlugins(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	utils.SetSSRFWhitelistFromRaw("plugins.example.com")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.TenantMember{}, &types.StorageBackend{},
		&types.InstalledPlugin{}, &types.PluginVersion{}, &types.PluginTenantSetting{}, &types.PluginKV{},
		&types.PluginOAuthConnection{}))
	tenants := service.NewTenantService(repository.NewTenantRepository(db), repository.NewStorageBackendRepository(db))
	plugins := repository.NewPluginRepository(db)
	store := &plugintest.MemStore{}
	r := reconcile.New(reconcile.Options{Repo: plugins, Store: store, Registry: registry.New(), CacheDir: t.TempDir()})
	installer := install.NewService(plugins, store, r, "0.5.0").
		WithTenantPlugins(func(context.Context) bool { return true })
	service.BindTenantPlugins(tenants, installer)

	doomed, err := tenants.CreateTenant(ctx, &types.Tenant{Name: "doomed"})
	require.NoError(t, err)
	kept, err := tenants.CreateTenant(ctx, &types.Tenant{Name: "kept"})
	require.NoError(t, err)
	own := func(id string) []byte {
		return plugintest.Zip(t, map[string]string{"plugin.yaml": "schemaVersion: 1\nid: " + id +
			"\nversion: 1.0.0\napiVersion: weknora.plugin/v1\nname: Own\npublisher: { id: team }\n" +
			"runtime: { type: remote }\ncontributes:\n  webSearch:\n    - { id: s, name: S }\n"})
	}
	for tenant, id := range map[uint64]string{doomed.ID: "team.doomed", kept.ID: "team.kept"} {
		_, err := installer.InstallOwned(ctx, tenant, install.Request{
			Data: own(id), RemoteURL: "https://plugins.example.com/" + id,
		})
		require.NoError(t, err)
		require.NoError(t, db.Create(&types.PluginTenantSetting{TenantID: tenant, PluginID: id, Enabled: true}).Error)
		require.NoError(t, db.Create(&types.PluginKV{PluginID: id, TenantID: tenant, Key: "k"}).Error)
		require.NoError(t, db.Create(&types.PluginOAuthConnection{ID: id, PluginID: id, TenantID: tenant}).Error)
	}

	require.NoError(t, tenants.DeleteTenant(ctx, doomed.ID))

	gone, err := plugins.GetPlugin(ctx, "team.doomed")
	require.NoError(t, err)
	require.Nil(t, gone, "the deleted workspace's plugin is still installed")
	require.Len(t, store.Blobs, 1, "the deleted workspace's package is still stored")
	still, err := plugins.GetPlugin(ctx, "team.kept")
	require.NoError(t, err)
	require.NotNil(t, still)
	for _, model := range []any{&types.PluginTenantSetting{}, &types.PluginKV{}, &types.PluginOAuthConnection{}} {
		var n int64
		require.NoError(t, db.Model(model).Where("tenant_id = ?", doomed.ID).Count(&n).Error)
		require.Zero(t, n, "%T rows of the deleted workspace remain", model)
		require.NoError(t, db.Model(model).Where("tenant_id = ?", kept.ID).Count(&n).Error)
		require.EqualValues(t, 1, n, "%T rows of another workspace were removed", model)
	}
}
