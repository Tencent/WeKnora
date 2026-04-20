package repository

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// knowledgeBasesTestDDL mirrors the `knowledge_bases` section of
// migrations/sqlite/000000_init.up.sql. We inline the DDL here instead of
// using GORM AutoMigrate because KnowledgeBase carries fields tagged with
// `type:jsonb`, which AutoMigrate does not map cleanly onto SQLite.
const knowledgeBasesTestDDL = `
CREATE TABLE IF NOT EXISTS knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    tenant_id INTEGER NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    chunking_config TEXT NOT NULL DEFAULT '{}',
    image_processing_config TEXT NOT NULL DEFAULT '{}',
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    cos_config TEXT NOT NULL DEFAULT '{}',
    storage_provider_config TEXT DEFAULT NULL,
    vlm_config TEXT NOT NULL DEFAULT '{}',
    extract_config TEXT NULL DEFAULT NULL,
    faq_config TEXT,
    question_generation_config TEXT NULL,
    is_temporary BOOLEAN NOT NULL DEFAULT 0,
    is_pinned INTEGER NOT NULL DEFAULT 0,
    pinned_at DATETIME NULL,
    asr_config TEXT,
    vector_store_id VARCHAR(36),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
`

// setupKBTestDB creates an in-memory SQLite database containing the
// knowledge_bases table with the vector_store_id column included, so that
// tests can exercise the GORM behavior of the VectorStoreID field.
func setupKBTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(knowledgeBasesTestDDL).Error)
	return db
}

// makeKB builds a minimal KnowledgeBase suitable for insert. Only the columns
// required by the schema above and any field we explicitly care about are set;
// the rest are left at their Go zero values and serialize to "{}" via their
// custom JSON valuers.
func makeKB(vectorStoreID *string) *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:               uuid.New().String(),
		Name:             "test-kb",
		TenantID:         1,
		EmbeddingModelID: "test-embed",
		SummaryModelID:   "test-summary",
		VectorStoreID:    vectorStoreID,
	}
}

func kbStrPtr(s string) *string { return &s }

// reloadKB fetches the KnowledgeBase back from the database using its ID.
func reloadKB(t *testing.T, db *gorm.DB, id string) types.KnowledgeBase {
	t.Helper()
	var got types.KnowledgeBase
	require.NoError(t, db.First(&got, "id = ?", id).Error)
	return got
}

// TestKnowledgeBase_VectorStoreID_Save_Immutable verifies that db.Save(kb)
// — which WeKnora's UpdateKnowledgeBase repository path uses today — does
// NOT overwrite the vector_store_id column, thanks to the GORM
// `<-:create` tag. This is the primary automated defense for the H1 review
// finding (silent overwrite of the binding via full-struct UPDATE).
func TestKnowledgeBase_VectorStoreID_Save_Immutable(t *testing.T) {
	db := setupKBTestDB(t)

	original := makeKB(kbStrPtr("store-A"))
	require.NoError(t, db.Create(original).Error)

	// Attempt to overwrite through the canonical Save() path.
	mutated := *original
	mutated.Name = "updated-name"
	mutated.VectorStoreID = kbStrPtr("store-B")
	require.NoError(t, db.Save(&mutated).Error)

	reloaded := reloadKB(t, db, original.ID)

	// Non-immutable fields must update.
	assert.Equal(t, "updated-name", reloaded.Name, "Name must update through Save")

	// Immutable field must NOT update.
	require.NotNil(t, reloaded.VectorStoreID, "VectorStoreID must remain set to the original value")
	assert.Equal(t, "store-A", *reloaded.VectorStoreID,
		"db.Save must not overwrite vector_store_id (enforced by GORM `<-:create` tag)")
}

// TestKnowledgeBase_VectorStoreID_Updates_Immutable verifies that
// db.Updates(struct{...}) also honors `<-:create` and silently ignores the
// vector_store_id column.
func TestKnowledgeBase_VectorStoreID_Updates_Immutable(t *testing.T) {
	db := setupKBTestDB(t)

	original := makeKB(kbStrPtr("store-A"))
	require.NoError(t, db.Create(original).Error)

	// Partial struct update that includes vector_store_id must not take effect.
	err := db.Model(&types.KnowledgeBase{}).
		Where("id = ?", original.ID).
		Updates(types.KnowledgeBase{
			Name:          "updated-name",
			VectorStoreID: kbStrPtr("store-B"),
		}).Error
	require.NoError(t, err)

	reloaded := reloadKB(t, db, original.ID)

	assert.Equal(t, "updated-name", reloaded.Name)
	require.NotNil(t, reloaded.VectorStoreID)
	assert.Equal(t, "store-A", *reloaded.VectorStoreID,
		"db.Updates must not overwrite vector_store_id (enforced by GORM `<-:create` tag)")
}

// TestKnowledgeBase_VectorStoreID_Select_StillImmutable verifies that even
// an explicit Select("vector_store_id").Updates(...) does NOT bypass the
// `<-:create` tag. GORM's `<-:create` is strict: it withholds write
// permission from every UPDATE path, including selective updates. This is
// a stronger guarantee than a typical "column is protected unless you
// opt in" scheme.
//
// The implication for Phase 4 (cross-store migration) is that ORM-level
// updates cannot move bindings. Migration code must use raw SQL (db.Exec)
// — see TestKnowledgeBase_VectorStoreID_RawSQL_EscapeHatch below.
func TestKnowledgeBase_VectorStoreID_Select_StillImmutable(t *testing.T) {
	db := setupKBTestDB(t)

	original := makeKB(kbStrPtr("store-A"))
	require.NoError(t, db.Create(original).Error)

	err := db.Model(&types.KnowledgeBase{}).
		Where("id = ?", original.ID).
		Select("vector_store_id").
		Updates(map[string]interface{}{"vector_store_id": "store-B"}).Error
	require.NoError(t, err)

	reloaded := reloadKB(t, db, original.ID)
	require.NotNil(t, reloaded.VectorStoreID)
	assert.Equal(t, "store-A", *reloaded.VectorStoreID,
		"Select(\"vector_store_id\").Updates must NOT bypass <-:create; ORM is fully locked")
}

// TestKnowledgeBase_VectorStoreID_RawSQL_EscapeHatch documents that raw
// SQL UPDATE bypasses the GORM tag system entirely, as expected. Phase 4
// cross-store migration is the only sanctioned caller of this path, and
// it must do so through a dedicated, narrowly-scoped repository method
// rather than through general Save/Updates/Select flows.
func TestKnowledgeBase_VectorStoreID_RawSQL_EscapeHatch(t *testing.T) {
	db := setupKBTestDB(t)

	original := makeKB(kbStrPtr("store-A"))
	require.NoError(t, db.Create(original).Error)

	require.NoError(t,
		db.Exec("UPDATE knowledge_bases SET vector_store_id = ? WHERE id = ?",
			"store-B", original.ID).Error)

	reloaded := reloadKB(t, db, original.ID)
	require.NotNil(t, reloaded.VectorStoreID)
	assert.Equal(t, "store-B", *reloaded.VectorStoreID,
		"raw SQL UPDATE bypasses GORM tags; Phase 4 migration must use this path through a dedicated method")
}

// TestKnowledgeBase_VectorStoreID_Roundtrip_Nil verifies that a nil
// VectorStoreID is persisted as SQL NULL and loaded back as nil.
func TestKnowledgeBase_VectorStoreID_Roundtrip_Nil(t *testing.T) {
	db := setupKBTestDB(t)

	original := makeKB(nil)
	require.NoError(t, db.Create(original).Error)

	reloaded := reloadKB(t, db, original.ID)
	assert.Nil(t, reloaded.VectorStoreID,
		"nil VectorStoreID must round-trip through the database as nil")

	// Extra assurance: column is actually NULL, not an empty string.
	var nullCount int64
	require.NoError(t,
		db.Table("knowledge_bases").
			Where("id = ? AND vector_store_id IS NULL", original.ID).
			Count(&nullCount).Error)
	assert.Equal(t, int64(1), nullCount, "nil must be stored as SQL NULL")
}

// TestKnowledgeBase_VectorStoreID_Roundtrip_Value verifies that a non-nil
// VectorStoreID is persisted and loaded back with the same value.
func TestKnowledgeBase_VectorStoreID_Roundtrip_Value(t *testing.T) {
	db := setupKBTestDB(t)

	original := makeKB(kbStrPtr("store-uuid-42"))
	require.NoError(t, db.Create(original).Error)

	reloaded := reloadKB(t, db, original.ID)
	require.NotNil(t, reloaded.VectorStoreID)
	assert.Equal(t, "store-uuid-42", *reloaded.VectorStoreID)
}
