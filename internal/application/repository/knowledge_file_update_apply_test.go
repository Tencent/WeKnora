package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// insertFileKnowledge seeds a file knowledge row with an explicit source
// version (path + hash) so the in-place replacement helpers can be exercised
// against their exact optimistic-lock predicates.
func insertFileKnowledge(
	t *testing.T, db *gorm.DB, tenantID uint64, kbID, status, filePath, fileHash string,
) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges
			(id, tenant_id, knowledge_base_id, type, title, source, parse_status, file_name, file_size, file_path, file_hash)
		VALUES (?, ?, ?, 'file', 'replace-test', 'file', ?, 'report.pdf', 1024, ?, ?)
	`, id, tenantID, kbID, status, filePath, fileHash).Error)
	return id
}

func reloadFileVersion(t *testing.T, db *gorm.DB, id string) (status, path, hash string) {
	t.Helper()
	row := db.Raw(`SELECT parse_status, file_path, file_hash FROM knowledges WHERE id = ?`, id).Row()
	require.NoError(t, row.Scan(&status, &path, &hash))
	return status, path, hash
}

func reloadFileDisplay(t *testing.T, db *gorm.DB, id string) (title, fileName, folderPath string) {
	t.Helper()
	row := db.Raw(`SELECT title, file_name, folder_path FROM knowledges WHERE id = ?`, id).Row()
	require.NoError(t, row.Scan(&title, &fileName, &folderPath))
	return title, fileName, folderPath
}

// TestClaimKnowledgeFileUpdate_Success verifies the happy path: a terminal
// row matching the observed version transitions to replacing exactly once.
func TestClaimKnowledgeFileUpdate_Success(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	kbID := uuid.New().String()
	id := insertFileKnowledge(t, db, 1, kbID, types.ParseStatusCompleted, "old/path.pdf", "hash-old")

	claimed, err := repo.ClaimKnowledgeFileUpdate(ctx, 1, id, kbID,
		types.ParseStatusCompleted, "old/path.pdf", "hash-old")
	require.NoError(t, err)
	assert.True(t, claimed)

	status, _, _ := reloadFileVersion(t, db, id)
	assert.Equal(t, types.ParseStatusReplacing, status)
}

// TestClaimKnowledgeFileUpdate_GuardsMismatch verifies that a claim fails
// without mutating the row when any part of the observed identity is stale:
// wrong status, wrong hash, wrong path, wrong tenant, or wrong KB.
func TestClaimKnowledgeFileUpdate_GuardsMismatch(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	kbID := uuid.New().String()
	id := insertFileKnowledge(t, db, 1, kbID, types.ParseStatusCompleted, "old/path.pdf", "hash-old")

	cases := []struct {
		name   string
		tenant uint64
		kb     string
		status string
		path   string
		hash   string
	}{
		{"wrong status", 1, kbID, types.ParseStatusPending, "old/path.pdf", "hash-old"},
		{"wrong hash", 1, kbID, types.ParseStatusCompleted, "old/path.pdf", "hash-different"},
		{"wrong path", 1, kbID, types.ParseStatusCompleted, "other/path.pdf", "hash-old"},
		{"wrong tenant", 2, kbID, types.ParseStatusCompleted, "old/path.pdf", "hash-old"},
		{"wrong kb", 1, uuid.New().String(), types.ParseStatusCompleted, "old/path.pdf", "hash-old"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claimed, err := repo.ClaimKnowledgeFileUpdate(ctx, tc.tenant, id, tc.kb, tc.status, tc.path, tc.hash)
			require.NoError(t, err)
			assert.False(t, claimed)
			status, _, _ := reloadFileVersion(t, db, id)
			assert.Equal(t, types.ParseStatusCompleted, status, "row must stay in its terminal state")
		})
	}
}

// TestClaimKnowledgeFileUpdate_ConcurrentExactlyOne is the core concurrency
// guarantee: two requests reading the same version must produce exactly one
// winner, so only one replacement task is ever enqueued for a knowledge.
func TestClaimKnowledgeFileUpdate_ConcurrentExactlyOne(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	kbID := uuid.New().String()
	id := insertFileKnowledge(t, db, 1, kbID, types.ParseStatusFailed, "old/path.pdf", "hash-old")

	const n = 16
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := repo.ClaimKnowledgeFileUpdate(ctx, 1, id, kbID,
				types.ParseStatusFailed, "old/path.pdf", "hash-old")
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if claimed {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), wins.Load(), "exactly one concurrent claim must win")
	status, _, _ := reloadFileVersion(t, db, id)
	assert.Equal(t, types.ParseStatusReplacing, status)
}

// TestUpdateApplyingKnowledgeFileColumns_GuardsVersion verifies the file switch /
// compensation write only lands while the row is still the replacing version
// the task originally claimed.
func TestUpdateApplyingKnowledgeFileColumns_GuardsVersion(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	kbID := uuid.New().String()
	id := insertFileKnowledge(t, db, 1, kbID, types.ParseStatusReplacing, "old/path.pdf", "hash-old")

	// Stale version (wrong hash) must not update.
	updated, err := repo.UpdateApplyingKnowledgeFileColumns(ctx, 1, id, kbID, "old/path.pdf", "stale-hash",
		map[string]interface{}{"parse_status": types.ParseStatusFailed})
	require.NoError(t, err)
	assert.False(t, updated)

	// Empty values map is a no-op.
	updated, err = repo.UpdateApplyingKnowledgeFileColumns(ctx, 1, id, kbID, "old/path.pdf", "hash-old", nil)
	require.NoError(t, err)
	assert.False(t, updated)

	// Correct version applies the switch.
	updated, err = repo.UpdateApplyingKnowledgeFileColumns(ctx, 1, id, kbID, "old/path.pdf", "hash-old",
		map[string]interface{}{
			"file_path":    "new/path.pdf",
			"file_name":    "new-name.pdf",
			"title":        "new-name.pdf",
			"folder_path":  "docs/spec",
			"file_hash":    "hash-new",
			"parse_status": types.ParseStatusPending,
		})
	require.NoError(t, err)
	assert.True(t, updated)

	status, path, hash := reloadFileVersion(t, db, id)
	assert.Equal(t, types.ParseStatusPending, status)
	assert.Equal(t, "new/path.pdf", path)
	assert.Equal(t, "hash-new", hash)
	title, fileName, folderPath := reloadFileDisplay(t, db, id)
	assert.Equal(t, "new-name.pdf", title)
	assert.Equal(t, "new-name.pdf", fileName)
	assert.Equal(t, "docs/spec", folderPath)

	// A second write with the old version is now stale and must not land.
	updated, err = repo.UpdateApplyingKnowledgeFileColumns(ctx, 1, id, kbID, "old/path.pdf", "hash-old",
		map[string]interface{}{"parse_status": types.ParseStatusFailed})
	require.NoError(t, err)
	assert.False(t, updated)
}

// TestCheckKnowledgeExistsExcluding verifies duplicate detection skips the row
// being replaced but still catches a collision with a different knowledge.
func TestCheckKnowledgeExistsExcluding(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	kbID := uuid.New().String()
	selfID := insertFileKnowledge(t, db, 1, kbID, types.ParseStatusCompleted, "self/path.pdf", "hash-shared")

	// Same hash but excluding self: no duplicate.
	exists, dup, err := repo.CheckKnowledgeExistsExcluding(ctx, 1, kbID, selfID, &types.KnowledgeCheckParams{
		Type: "file", FileHash: "hash-shared",
	})
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Nil(t, dup)

	// A different knowledge with the same hash is a real duplicate.
	otherID := insertFileKnowledge(t, db, 1, kbID, types.ParseStatusCompleted, "other/path.pdf", "hash-shared")
	exists, dup, err = repo.CheckKnowledgeExistsExcluding(ctx, 1, kbID, selfID, &types.KnowledgeCheckParams{
		Type: "file", FileHash: "hash-shared",
	})
	require.NoError(t, err)
	assert.True(t, exists)
	require.NotNil(t, dup)
	assert.Equal(t, otherID, dup.ID)
}
