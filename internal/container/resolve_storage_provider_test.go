package container

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestResolveStorageProviderPending_SQLite exercises the dialect-aware
// JSON path in resolveStorageProviderPending. SQLite stores JSON as TEXT
// and uses json_extract(..., '$.provider'); the historical code used
// PostgreSQL's ->>'provider' which errors on MySQL once any row has
// non-null JSON.
func TestResolveStorageProviderPending_SQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases (
		id TEXT PRIMARY KEY,
		storage_provider_config TEXT
	)`).Error)

	// Seed three rows: one with the sentinel, one with a real provider,
	// one NULL. Only the sentinel row should be rewritten.
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases (id, storage_provider_config) VALUES
		('kb-pending', '{"provider":"__pending_env__"}'),
		('kb-real', '{"provider":"local"}'),
		('kb-null', NULL)
	`).Error)

	t.Setenv("STORAGE_TYPE", "minio")
	resolveStorageProviderPending(db)

	type row struct {
		ID         string `gorm:"column:id"`
		ConfigJSON string `gorm:"column:storage_provider_config"`
	}
	var rows []row
	require.NoError(t, db.Raw(`SELECT id, storage_provider_config FROM knowledge_bases ORDER BY id`).Scan(&rows).Error)

	byID := make(map[string]string, len(rows))
	for _, r := range rows {
		byID[r.ID] = r.ConfigJSON
	}
	assert.Equal(t, `{"provider":"minio"}`, byID["kb-pending"],
		"sentinel row must be rewritten with the env STORAGE_TYPE")
	assert.Equal(t, `{"provider":"local"}`, byID["kb-real"],
		"non-sentinel row must be left untouched")
	assert.Equal(t, "", byID["kb-null"],
		"NULL config must remain NULL (no provider to resolve)")
}
