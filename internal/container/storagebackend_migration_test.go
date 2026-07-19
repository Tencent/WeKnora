package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateLegacyStorageBackends(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	// Schema is owned by the SQL migrations in production; provision it here so
	// the test exercises only the data-migration logic.
	require.NoError(t, db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY, name TEXT, storage_engine_config TEXT,
		default_storage_backend_id TEXT, updated_at DATETIME, deleted_at DATETIME
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases (
		id TEXT PRIMARY KEY, tenant_id INTEGER, storage_provider_config TEXT,
		storage_backend_id TEXT, cos_config TEXT, updated_at DATETIME, deleted_at DATETIME
	)`).Error)
	require.NoError(t, db.AutoMigrate(&types.StorageBackend{}))
	tenantConfig, err := (&types.StorageEngineConfig{DefaultProvider: "local", Local: &types.LocalEngineConfig{PathPrefix: "workspace-a"}}).Value()
	require.NoError(t, err)
	providerConfig, err := (types.StorageProviderConfig{Provider: "local"}).Value()
	require.NoError(t, err)
	require.NoError(t, db.Exec("INSERT INTO tenants(id, name, storage_engine_config) VALUES (?, ?, ?)", 7, "workspace", tenantConfig).Error)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases(id, tenant_id, storage_provider_config, cos_config) VALUES (?, ?, ?, ?)", "kb-a", 7, providerConfig, []byte("{}")).Error)

	migrateLegacyStorageBackends(db)

	var backend types.StorageBackend
	require.NoError(t, db.Where("tenant_id = ? AND provider = ? AND legacy_alias = ?", 7, "local", true).First(&backend).Error)
	assert.Equal(t, "workspace-a", backend.Config.PathPrefix)

	var tenantDefault, kbBackend string
	require.NoError(t, db.Raw("SELECT default_storage_backend_id FROM tenants WHERE id = 7").Scan(&tenantDefault).Error)
	require.NoError(t, db.Raw("SELECT storage_backend_id FROM knowledge_bases WHERE id = 'kb-a'").Scan(&kbBackend).Error)
	assert.Equal(t, backend.ID, tenantDefault)
	assert.Equal(t, backend.ID, kbBackend)
}
