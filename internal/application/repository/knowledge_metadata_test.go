package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestFindByMetadataKey_SQLite exercises the dialect-aware JSON path
// branch of FindByMetadataKey. SQLite stores JSON as TEXT and supports
// json_extract(metadata, '$.external_id'); the historical code used the
// PostgreSQL `metadata->>? = ?` syntax which fails on MySQL with
// "Invalid JSON path expression" and is not the SQLite idiom either.
func TestFindByMetadataKey_SQLite(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()

	tenantID := uint64(1)
	kbID := uuid.NewString()

	// Insert two knowledge rows with metadata containing external_id and
	// datasource_id keys, matching the shape the datasource service writes.
	insertKnowledgeWithMetadata(t, db, tenantID, kbID, "ext-1", "doc-1")
	insertKnowledgeWithMetadata(t, db, tenantID, kbID, "ext-2", "doc-2")

	t.Run("exact match returns the right row", func(t *testing.T) {
		got, err := repo.FindByMetadataKey(ctx, tenantID, kbID, "external_id", "ext-1")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, "doc-1", got.Source)
	})

	t.Run("no match returns nil nil (not an error)", func(t *testing.T) {
		got, err := repo.FindByMetadataKey(ctx, tenantID, kbID, "external_id", "ext-missing")
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("different key works", func(t *testing.T) {
		got, err := repo.FindByMetadataKey(ctx, tenantID, kbID, "datasource_id", "ds-1")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, "doc-1", got.Source)
	})

	t.Run("soft-deleted row is not returned", func(t *testing.T) {
		// Soft-delete the first row by setting deleted_at.
		require.NoError(t, db.Exec(
			`UPDATE knowledges SET deleted_at = '2024-01-01 00:00:00' WHERE source = ?`,
			"doc-1").Error)
		got, err := repo.FindByMetadataKey(ctx, tenantID, kbID, "external_id", "ext-1")
		require.NoError(t, err)
		require.Nil(t, got)
	})
}

// insertKnowledgeWithMetadata seeds a knowledges row whose metadata
// JSON contains external_id and datasource_id keys, matching the shape
// the datasource service writes during sync. Each row gets a unique
// datasource_id so key-lookup tests can assert on a specific row.
func insertKnowledgeWithMetadata(t *testing.T, db *gorm.DB, tenantID uint64, kbID, externalID, source string) {
	t.Helper()
	id := uuid.NewString()
	// datasource_id is derived from source so the "different key works"
	// subtest can look up ds-1 and know it should get the doc-1 row.
	datasourceID := "ds-" + source[len(source)-1:]
	metadata := `{"external_id":"` + externalID + `","datasource_id":"` + datasourceID + `","source_resource_id":"rs-1"}`
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, tenantID, kbID, "file", source, source, "completed", metadata).Error)
}
