package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckKnowledgeExists_FileHashIsScopedByFileType(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	tenantID := uint64(1)
	kbID := uuid.NewString()
	const fileHash = "same-content-hash"

	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, file_name, file_type, file_hash, parse_status)
		VALUES (?, ?, ?, 'file', 'document.md', 'document.md', 'md', ?, 'completed')
	`, uuid.NewString(), tenantID, kbID, fileHash).Error)

	t.Run("same content with another file type is allowed", func(t *testing.T) {
		exists, knowledge, err := repo.CheckKnowledgeExists(ctx, tenantID, kbID, &types.KnowledgeCheckParams{
			Type:     "file",
			FileHash: fileHash,
			FileType: "txt",
		})

		require.NoError(t, err)
		assert.False(t, exists)
		assert.Nil(t, knowledge)
	})

	t.Run("same content and file type remains a duplicate", func(t *testing.T) {
		exists, knowledge, err := repo.CheckKnowledgeExists(ctx, tenantID, kbID, &types.KnowledgeCheckParams{
			Type:     "file",
			FileHash: fileHash,
			FileType: "md",
		})

		require.NoError(t, err)
		assert.True(t, exists)
		require.NotNil(t, knowledge)
		assert.Equal(t, "md", knowledge.FileType)
	})

	t.Run("file type matching is case-insensitive", func(t *testing.T) {
		exists, knowledge, err := repo.CheckKnowledgeExists(ctx, tenantID, kbID, &types.KnowledgeCheckParams{
			Type:     "file",
			FileHash: fileHash,
			FileType: "MD",
		})

		require.NoError(t, err)
		assert.True(t, exists)
		require.NotNil(t, knowledge)
		assert.Equal(t, "md", knowledge.FileType)
	})
}

func TestCheckKnowledgeExists_FileSourceIdentity(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	const metadata = `{"datasource_id":"ds-1","external_id":"gitlab:1:main:README.md"}`
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (
			id, tenant_id, knowledge_base_id, type, file_name, file_type, file_size, file_hash, parse_status, metadata
		)
		VALUES (?, 1, 'kb-1', 'file', 'README.md', 'md', 10, 'same-hash', 'completed', ?)
	`, uuid.NewString(), metadata).Error)
	for _, tc := range []struct {
		name, source, externalID, hash, fileType string
		want                                     bool
	}{
		{"same source item", "ds-1", "gitlab:1:main:README.md", "same-hash", "md", true},
		{"nested path", "ds-1", "gitlab:1:main:a/b/c/README.md", "same-hash", "md", false},
		{"another branch", "ds-1", "gitlab:1:release:README.md", "same-hash", "md", false},
		{"another source", "ds-2", "gitlab:1:main:README.md", "same-hash", "md", false},
		{"updated content", "ds-1", "gitlab:1:main:README.md", "new-hash", "md", false},
		{"another format", "ds-1", "gitlab:1:main:README.md", "same-hash", "txt", false},
		{"case insensitive format", "ds-1", "gitlab:1:main:README.md", "same-hash", "MD", true},
		{"unscoped upload", "", "", "same-hash", "md", true},
		{"missing source", "", "new-path", "same-hash", "md", true},
		{"missing external ID", "ds-2", "", "same-hash", "md", true},
		{"filename fallback same item", "ds-1", "gitlab:1:main:README.md", "", "md", true},
		{"filename fallback nested item", "ds-1", "gitlab:1:main:a/b/c/README.md", "", "md", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exists, _, err := repo.CheckKnowledgeExists(context.Background(), 1, "kb-1", &types.KnowledgeCheckParams{
				Type: "file", FileName: "README.md", FileSize: 10, FileHash: tc.hash, FileType: tc.fileType,
				DataSourceID: tc.source, ExternalID: tc.externalID,
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, exists)
		})
	}
}

func TestCheckKnowledgeExists_ParseStatusMatrix(t *testing.T) {
	// Pins the duplicate-check status policy (issue #3338): failed and
	// deleting rows must not block an upload — a deleting row is on its way
	// out regardless of how the async delete task concludes — while pending,
	// processing and completed rows remain real duplicates.
	for _, tc := range []struct {
		parseStatus string
		want        bool
	}{
		{"failed", false},
		{"deleting", false},
		{"pending", true},
		{"processing", true},
		{"completed", true},
	} {
		t.Run("parse_status="+tc.parseStatus, func(t *testing.T) {
			db := setupKnowledgeTestDB(t)
			repo := NewKnowledgeRepository(db)
			require.NoError(t, db.Exec(`
				INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, file_name, file_type, file_size, file_hash, parse_status)
				VALUES (?, 1, 'kb-1', 'file', 'doc.md', 'md', 10, 'hash-1', ?)
			`, uuid.NewString(), tc.parseStatus).Error)

			exists, _, err := repo.CheckKnowledgeExists(context.Background(), 1, "kb-1", &types.KnowledgeCheckParams{
				Type: "file", FileHash: "hash-1", FileType: "md",
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, exists)

			// The filename/size fallback path shares the same base filter.
			exists, _, err = repo.CheckKnowledgeExists(context.Background(), 1, "kb-1", &types.KnowledgeCheckParams{
				Type: "file", FileName: "doc.md", FileSize: 10,
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, exists)
		})
	}
}

// TestCountKnowledgeByKnowledgeBaseID_ExcludesDeleting pins the count/list
// alignment (issues #3338/#3345): the document list hides rows mid-deletion,
// so the KB card count must not include them either — otherwise a stranded
// delete shows up as "4 documents, 3 listed".
func TestCountKnowledgeByKnowledgeBaseID_ExcludesDeleting(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	for _, tc := range []struct{ id, status string }{
		{"k-done", "completed"},
		{"k-failed", "failed"},
		{"k-deleting", "deleting"},
	} {
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, file_name, parse_status)
			VALUES (?, 1, 'kb-cnt', 'file', ?, ?)
		`, tc.id, tc.id+".md", tc.status).Error)
	}

	count, err := repo.CountKnowledgeByKnowledgeBaseID(context.Background(), 1, "kb-cnt")
	require.NoError(t, err)
	// failed rows stay counted (they are visible, actionable cards); only
	// the hidden mid-deletion row drops out.
	require.Equal(t, int64(2), count)
}

// TestMarkKnowledgeDeleting_UnblocksReuploadAndDropsCount pins the remaining
// #3338 hole: the UI toasts "delete submitted" before the worker runs, so a
// processing row keeps matching CheckKnowledgeExists and inflating the KB
// count until parse_status flips to deleting. Marking at admit-time closes
// that window — the worker still finishes the delete; this only makes the
// row immediately non-blocking and uncounted, matching the list filter.
func TestMarkKnowledgeDeleting_UnblocksReuploadAndDropsCount(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	const (
		tenantID = uint64(1)
		kbID     = "kb-mark"
		hash     = "hash-processing"
	)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, file_name, file_type, file_size, file_hash, parse_status)
		VALUES
			('k-processing', ?, ?, 'file', 'stuck.md', 'md', 10, ?, 'processing'),
			('k-done', ?, ?, 'file', 'ok.md', 'md', 10, 'hash-ok', 'completed')
	`, tenantID, kbID, hash, tenantID, kbID).Error)

	params := &types.KnowledgeCheckParams{Type: "file", FileHash: hash, FileType: "md"}
	exists, _, err := repo.CheckKnowledgeExists(ctx, tenantID, kbID, params)
	require.NoError(t, err)
	require.True(t, exists, "a live processing row must still be a duplicate before delete is admitted")

	count, err := repo.CountKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)

	require.NoError(t, repo.MarkKnowledgeDeleting(ctx, tenantID, []string{"k-processing"}))

	exists, _, err = repo.CheckKnowledgeExists(ctx, tenantID, kbID, params)
	require.NoError(t, err)
	require.False(t, exists, "admitted delete must not block re-upload of the same file")

	count, err = repo.CountKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "the processing ghost must drop out of the KB count immediately")

	var status string
	require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id = ?`, "k-processing").Scan(&status).Error)
	require.Equal(t, types.ParseStatusDeleting, status)

	// Sibling completed row is untouched: still a duplicate, still counted.
	exists, _, err = repo.CheckKnowledgeExists(ctx, tenantID, kbID, &types.KnowledgeCheckParams{
		Type: "file", FileHash: "hash-ok", FileType: "md",
	})
	require.NoError(t, err)
	require.True(t, exists)
}
