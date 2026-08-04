package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListKnowledgeIDsByFolderPath(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()

	insertKnowledgeInFolder(t, db, tenantID, kbID, "", "readme.md") // root, out of scope
	docsID := insertKnowledgeInFolder(t, db, tenantID, kbID, "docs", "intro.md")
	specID := insertKnowledgeInFolder(t, db, tenantID, kbID, "docs/spec", "design.md")
	insertKnowledgeInFolder(t, db, tenantID, kbID, "docsets", "x.md")    // prefix sibling, out of scope
	insertKnowledgeInFolder(t, db, tenantID, kbID, "assets", "logo.png") // unrelated

	// The folder itself plus its descendants; a folder that merely shares the
	// prefix ("docsets") must not be swept in - that is what the "/%" ESCAPE
	// predicate guards against.
	ids, err := repo.ListKnowledgeIDsByFolderPath(ctx, tenantID, kbID, "docs")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{docsID, specID}, ids)

	// A leaf folder returns just its own entry.
	ids, err = repo.ListKnowledgeIDsByFolderPath(ctx, tenantID, kbID, "docs/spec")
	require.NoError(t, err)
	assert.Equal(t, []string{specID}, ids)

	// Rows already mid-deletion are skipped so a retried delete never re-enqueues
	// an entry that is already being torn down.
	require.NoError(t, db.Exec(`UPDATE knowledges SET parse_status = ? WHERE id = ?`,
		types.ParseStatusDeleting, specID).Error)
	ids, err = repo.ListKnowledgeIDsByFolderPath(ctx, tenantID, kbID, "docs")
	require.NoError(t, err)
	assert.Equal(t, []string{docsID}, ids)

	// An unknown folder is empty, not an error.
	ids, err = repo.ListKnowledgeIDsByFolderPath(ctx, tenantID, kbID, "nope")
	require.NoError(t, err)
	assert.Empty(t, ids)
}
