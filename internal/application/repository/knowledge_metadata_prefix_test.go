package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFindByMetadataKeyPrefix verifies that FindByMetadataKeyPrefix returns only
// knowledge items whose metadata["external_id"] starts with the given prefix.
//
// Setup: three rows in the same KB:
//   - "nodeA"          — the parent document; must NOT be matched by prefix "nodeA#"
//   - "nodeA#file#x"   — an attachment child; MUST be matched
//   - "nodeB"          — a sibling document; must NOT be matched
//
// A fourth row in a different KB with a matching external_id is also inserted to
// verify tenant/KB isolation.
func TestFindByMetadataKeyPrefix(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID uint64 = 42
	kbID := uuid.New().String()
	otherKBID := uuid.New().String()

	insertRow := func(tid uint64, kb, extID string) string {
		id := uuid.New().String()
		metadata := fmt.Sprintf(`{"external_id":%q}`, extID)
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges
			  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata)
			VALUES (?, ?, ?, 'document', ?, 'feishu', 'completed', ?)
		`, id, tid, kb, extID, metadata).Error)
		return id
	}

	_ = insertRow(tenantID, kbID, "nodeA")               // parent — must NOT match
	childID := insertRow(tenantID, kbID, "nodeA#file#x") // attachment child — MUST match
	_ = insertRow(tenantID, kbID, "nodeB")               // sibling — must NOT match
	_ = insertRow(tenantID, otherKBID, "nodeA#file#y")   // different KB — must be excluded

	results, err := repo.FindByMetadataKeyPrefix(ctx, tenantID, kbID, "external_id", "nodeA#")
	require.NoError(t, err)

	require.Len(t, results, 1, "only the attachment child should match the prefix 'nodeA#'")
	assert.Equal(t, childID, results[0].ID)
}

// TestFindByMetadataKeyPrefix_DeletedRowsExcluded verifies that soft-deleted rows
// (deleted_at IS NOT NULL) are excluded from the prefix query.
func TestFindByMetadataKeyPrefix_DeletedRowsExcluded(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID uint64 = 43
	kbID := uuid.New().String()

	id := uuid.New().String()
	metadata := fmt.Sprintf(`{"external_id":%q}`, "nodeC#file#z")
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges
		  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata, deleted_at)
		VALUES (?, ?, ?, 'document', 'deleted-child', 'feishu', 'completed', ?, '2026-01-01 00:00:00')
	`, id, tenantID, kbID, metadata).Error)

	results, err := repo.FindByMetadataKeyPrefix(ctx, tenantID, kbID, "external_id", "nodeC#")
	require.NoError(t, err)
	assert.Empty(t, results, "soft-deleted rows must not appear in prefix results")
}

// TestFindByMetadataKeyPrefix_UnderscoreIsLiteral verifies that an underscore in
// the prefix is treated as a literal character and does NOT act as a SQL LIKE
// wildcard. Without escapeLikeKeyword applied to the prefix, a query for "ab_c#"
// would also match "abXc#file#y" because '_' is a wildcard.
func TestFindByMetadataKeyPrefix_UnderscoreIsLiteral(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID uint64 = 44
	kbID := uuid.New().String()

	insertRow := func(extID string) string {
		id := uuid.New().String()
		metadata := fmt.Sprintf(`{"external_id":%q}`, extID)
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges
			  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata)
			VALUES (?, ?, ?, 'document', ?, 'feishu', 'completed', ?)
		`, id, tenantID, kbID, extID, metadata).Error)
		return id
	}

	// The prefix we will search for is "ab_c#". Without escaping, '_' is a
	// wildcard that matches any single character, so "abXc#file#y" would also
	// be returned. With escaping it must NOT be returned.
	targetID := insertRow("ab_c#file#x") // literal underscore — MUST match
	_ = insertRow("abXc#file#y")         // 'X' instead of '_' — must NOT match

	results, err := repo.FindByMetadataKeyPrefix(ctx, tenantID, kbID, "external_id", "ab_c#")
	require.NoError(t, err)

	require.Len(t, results, 1, "underscore in prefix must be treated as literal: only 'ab_c#file#x' should match")
	assert.Equal(t, targetID, results[0].ID)
}
