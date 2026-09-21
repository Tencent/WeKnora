package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteAPIKeyIdentityMigrationPreservesPolicy(t *testing.T) {
	db := openSQLiteDB(t, filepath.Join(t.TempDir(), "identity.db"))
	_, err := db.Exec(`CREATE TABLE tenants (id INTEGER PRIMARY KEY, api_principal_config TEXT);
 CREATE TABLE tenant_api_keys (id INTEGER PRIMARY KEY, tenant_id INTEGER, scope_type TEXT);
 INSERT INTO tenants VALUES (7, '{"mode":"signed_token","hmac_secret":"encrypted-secret"}'), (8, NULL);
 INSERT INTO tenant_api_keys VALUES (1,7,'tenant'), (2,7,'tenant'), (3,8,'tenant'), (4,NULL,'platform');`)
	require.NoError(t, err)
	migration, err := os.ReadFile(filepath.Join(sqliteRepoRoot(t), "migrations/sqlite/000028_api_key_identity.up.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	for _, id := range []int{1, 2} {
		var namespace, cfg string
		require.NoError(t,
			db.QueryRow("SELECT identity_namespace, api_principal_config FROM tenant_api_keys WHERE id = ?",
				id).Scan(&namespace,
				&cfg))
		require.Empty(t, namespace)
		require.JSONEq(t, `{"mode":"signed_token","hmac_secret":"encrypted-secret"}`, cfg)
	}
	var cfg string
	require.NoError(t, db.QueryRow("SELECT api_principal_config FROM tenant_api_keys WHERE id = 3").Scan(&cfg))
	require.JSONEq(t, `{"mode":"tenant"}`, cfg)
	var unconfigured bool
	require.NoError(t,
		db.QueryRow("SELECT api_principal_config IS NULL FROM tenant_api_keys WHERE id = 4").Scan(&unconfigured))
	require.True(t, unconfigured)
	_, err = db.Exec(`UPDATE tenants SET api_principal_config = '{"mode":"tenant"}' WHERE id = 7`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow("SELECT api_principal_config FROM tenant_api_keys WHERE id = 1").Scan(&cfg))
	require.Contains(t, cfg, "signed_token", "migration must snapshot rather than inherit future workspace edits")
}
