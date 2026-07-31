package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// documentFoldersTestDDL mirrors the production document_folders DDL
// (migrations/versioned/000075_document_folders.up.sql) for SQLite.
// INTEGER for BIGINT/int, DATETIME for TIMESTAMP WITH TIME ZONE — matching
// the wiki_folders test DDL precedent.
const documentFoldersTestDDL = `
CREATE TABLE IF NOT EXISTS document_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at        DATETIME
);
`

// knowledgesFolderTestDDL is a SQLite-compatible projection of the knowledges
// table restricted to the columns folder-related tests touch. Borrowed from
// knowledge_finalize_test.go's knowledgesTestDDL pattern.
const knowledgesFolderTestDDL = `
CREATE TABLE IF NOT EXISTS knowledges (
    id                   VARCHAR(36) PRIMARY KEY,
    tenant_id            INTEGER NOT NULL,
    knowledge_base_id    VARCHAR(36) NOT NULL,
    type                 VARCHAR(32) NOT NULL DEFAULT 'document',
    title                VARCHAR(512) NOT NULL DEFAULT '',
    description          TEXT NOT NULL DEFAULT '',
    source               VARCHAR(2048) NOT NULL DEFAULT '',
    channel              VARCHAR(50) NOT NULL DEFAULT 'web',
    parse_status         VARCHAR(32) NOT NULL DEFAULT 'pending',
    pending_subtasks_count INTEGER NOT NULL DEFAULT 0,
    summary_status       VARCHAR(32) NOT NULL DEFAULT 'none',
    enable_status        VARCHAR(32) NOT NULL DEFAULT 'active',
    embedding_model_id   VARCHAR(64) NOT NULL DEFAULT '',
    file_name            VARCHAR(1024) NOT NULL DEFAULT '',
    file_type            VARCHAR(32) NOT NULL DEFAULT '',
    file_size            INTEGER NOT NULL DEFAULT 0,
    file_hash            VARCHAR(64) NOT NULL DEFAULT '',
    file_path            VARCHAR(1024) NOT NULL DEFAULT '',
    storage_size         INTEGER NOT NULL DEFAULT 0,
    metadata             TEXT DEFAULT '{}',
    custom_metadata      TEXT NOT NULL DEFAULT '{}',
    last_faq_import_result TEXT DEFAULT '{}',
    processed_at         DATETIME,
    error_message        TEXT NOT NULL DEFAULT '',
    folder_id            VARCHAR(36) NOT NULL DEFAULT '',
    created_at           DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at           DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at           DATETIME
);
CREATE INDEX IF NOT EXISTS idx_knowledges_folder ON knowledges (tenant_id, knowledge_base_id, folder_id);
`

const knowledgeBasesFolderTestDDL = `
CREATE TABLE IF NOT EXISTS knowledge_bases (
    id         VARCHAR(36) PRIMARY KEY,
    tenant_id  INTEGER NOT NULL,
    name       VARCHAR(255) NOT NULL,
    type       VARCHAR(32) NOT NULL DEFAULT 'document',
    deleted_at DATETIME
);
`

func setupDocumentFolderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(documentFoldersTestDDL).Error)
	require.NoError(t, db.Exec(knowledgesFolderTestDDL).Error)
	require.NoError(t, db.Exec(knowledgeBasesFolderTestDDL).Error)
	return db
}

// mkFolder builds a DocumentFolder for insert. parentID == "" means root.
func mkFolder(kbID, id, parentID, name, path string, depth int) *types.DocumentFolder {
	return &types.DocumentFolder{
		ID:              id,
		TenantID:        1,
		KnowledgeBaseID: kbID,
		ParentID:        parentID,
		Name:            name,
		Path:            path,
		Depth:           depth,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
}

// TestDocumentFolderRepo_CreateAndGet exercises the basic CRUD primitive:
// a created folder must be retrievable by id and must round-trip its fields.
func TestDocumentFolderRepo_CreateAndGet(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	folder := mkFolder("kb-1", "f-root", types.DocumentFolderRootID, "Root Folder", "Root Folder", 1)
	require.NoError(t, repo.CreateFolder(ctx, folder))

	got, err := repo.GetFolderByID(ctx, "kb-1", "f-root")
	require.NoError(t, err)
	assert.Equal(t, "f-root", got.ID)
	assert.Equal(t, "Root Folder", got.Name)
	assert.Equal(t, types.DocumentFolderRootID, got.ParentID)
	assert.Equal(t, 1, got.Depth)
	assert.Equal(t, "Root Folder", got.Path)
}

// TestDocumentFolderRepo_GetFolderByID_NotFound maps a missing folder to a
// sentinel error so the service can translate it to HTTP 404 without string
// matching.
func TestDocumentFolderRepo_GetFolderByID_NotFound(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)

	_, err := repo.GetFolderByID(context.Background(), "kb-1", "missing")
	assert.ErrorIs(t, err, ErrDocumentFolderNotFound)
}

// TestDocumentFolderRepo_GetFolderByID_CrossKBReturnsNotFound proves IDOR is
// fail-closed: a folder belonging to kb-A must not be retrievable via kb-B,
// and the error is NotFound (not a leak of existence).
func TestDocumentFolderRepo_GetFolderByID_CrossKBReturnsNotFound(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-A", "f-1", "", "A", "A", 1)))

	_, err := repo.GetFolderByID(ctx, "kb-B", "f-1")
	assert.ErrorIs(t, err, ErrDocumentFolderNotFound)
}

// TestDocumentFolderRepo_GetChildFolderByName covers the uniqueness probe:
// returns nil,nil when a sibling with the same name exists, and
// ErrDocumentFolderNotFound when no such sibling exists.
func TestDocumentFolderRepo_GetChildFolderByName(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "f-1", "", "Alpha", "Alpha", 1)))

	got, err := repo.GetChildFolderByName(ctx, "kb-1", "", "Alpha")
	require.NoError(t, err)
	assert.Equal(t, "f-1", got.ID)

	_, err = repo.GetChildFolderByName(ctx, "kb-1", "", "Beta")
	assert.ErrorIs(t, err, ErrDocumentFolderNotFound)
}

// TestDocumentFolderRepo_ListChildFolders verifies the direct-children listing
// honors parent_id scoping and sort ordering.
func TestDocumentFolderRepo_ListChildFolders(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "p", "", "Parent", "Parent", 1)))
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "c1", "p", "Zeta", "Parent/Zeta", 2)))
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "c2", "p", "Alpha", "Parent/Alpha", 2)))
	// Unrelated: different parent + different KB
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "root1", "", "Root1", "Root1", 1)))
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-2", "c3", "p", "Other", "Other", 2)))

	got, hasMore, err := repo.ListChildFolders(ctx, "kb-1", "p", "", nil, 20)
	require.NoError(t, err)
	assert.False(t, hasMore)
	require.Len(t, got, 2)
	// Sorted by name ASC
	assert.Equal(t, "Alpha", got[0].Name)
	assert.Equal(t, "Zeta", got[1].Name)
}

func TestDocumentFolderRepo_ListChildFolders_PaginatesAndSearches(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	for _, folder := range []*types.DocumentFolder{
		mkFolder("kb-1", "f-a", "", "Alpha", "Alpha", 1),
		mkFolder("kb-1", "f-b", "", "Beta", "Beta", 1),
		mkFolder("kb-1", "f-c", "", "Gamma", "Gamma", 1),
	} {
		require.NoError(t, repo.CreateFolder(ctx, folder))
	}

	first, hasMore, err := repo.ListChildFolders(ctx, "kb-1", "", "", nil, 2)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Equal(t, []string{"f-a", "f-b"}, []string{first[0].ID, first[1].ID})

	second, hasMore, err := repo.ListChildFolders(
		ctx,
		"kb-1",
		"",
		"",
		&types.DocumentFolderPageCursor{
			Name: first[1].Name,
			ID:   first[1].ID,
		},
		2,
	)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Len(t, second, 1)
	assert.Equal(t, "f-c", second[0].ID)

	matches, hasMore, err := repo.ListChildFolders(ctx, "kb-1", "", "amm", nil, 20)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Len(t, matches, 1)
	assert.Equal(t, "Gamma", matches[0].Name)
}

// TestDocumentFolderRepo_ListAllFolders returns the whole tree for subtree BFS
// and path recompute, ordered depth-first for determinism.
func TestDocumentFolderRepo_ListAllFolders(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "r", "", "R", "R", 1)))
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "a", "r", "A", "R/A", 2)))
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-2", "r2", "", "R2", "R2", 1))) // other KB

	got, err := repo.ListAllFolders(ctx, "kb-1")
	require.NoError(t, err)
	require.Len(t, got, 2)
}

// TestDocumentFolderRepo_UpdateFolder persists field changes and returns
// ErrDocumentFolderNotFound when the id is unknown (so a lost race between
// read and update surfaces cleanly).
func TestDocumentFolderRepo_UpdateFolder(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "f-1", "", "Old", "Old", 1)))
	updated := mkFolder("kb-1", "f-1", "", "New", "New", 1)
	require.NoError(t, repo.UpdateFolder(ctx, updated))

	got, err := repo.GetFolderByID(ctx, "kb-1", "f-1")
	require.NoError(t, err)
	assert.Equal(t, "New", got.Name)

	err = repo.UpdateFolder(ctx, mkFolder("kb-1", "missing", "", "X", "X", 1))
	assert.ErrorIs(t, err, ErrDocumentFolderNotFound)
}

// TestDocumentFolderRepo_DeleteFolder verifies soft delete semantics: a
// deleted folder is no longer returned by GetFolderByID, and deleting a
// missing id yields ErrDocumentFolderNotFound.
func TestDocumentFolderRepo_DeleteFolder(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "f-1", "", "X", "X", 1)))
	require.NoError(t, repo.DeleteFolder(ctx, "kb-1", "f-1"))

	_, err := repo.GetFolderByID(ctx, "kb-1", "f-1")
	assert.ErrorIs(t, err, ErrDocumentFolderNotFound)

	err = repo.DeleteFolder(ctx, "kb-1", "missing")
	assert.ErrorIs(t, err, ErrDocumentFolderNotFound)
}

// TestDocumentFolderRepo_SoftDeleteExcludesFromList confirms gorm.DeletedAt
// keeps soft-deleted rows out of ListChildFolders/ListAllFolders.
func TestDocumentFolderRepo_SoftDeleteExcludesFromList(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "f-1", "", "A", "A", 1)))
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "f-2", "", "B", "B", 1)))
	require.NoError(t, repo.DeleteFolder(ctx, "kb-1", "f-1"))

	got, hasMore, err := repo.ListChildFolders(ctx, "kb-1", "", "", nil, 20)
	require.NoError(t, err)
	assert.False(t, hasMore)
	require.Len(t, got, 1)
	assert.Equal(t, "f-2", got[0].ID)
}

// TestDocumentFolderRepo_CountDocumentsInFolders counts direct (non-recursive)
// documents for multiple folders in one query, honoring tenant/KB scoping.
func TestDocumentFolderRepo_CountDocumentsInFolders(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()
	seedKnowledgeRows(t, db, "kb-1")

	counts, err := repo.CountDocumentsInFolders(ctx, 1, "kb-1", []string{"f-a", "f-empty"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), counts["f-a"])
	assert.Equal(t, int64(0), counts["f-empty"])
}

// TestDocumentFolderRepo_HasChildFolders is used by the delete-non-empty
// guard.
func TestDocumentFolderRepo_HasChildFolders(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "p", "", "P", "P", 1)))
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "c", "p", "C", "P/C", 2)))

	has, err := repo.HasChildFolders(ctx, "kb-1", "p")
	require.NoError(t, err)
	assert.True(t, has)

	has, err = repo.HasChildFolders(ctx, "kb-1", "c")
	require.NoError(t, err)
	assert.False(t, has)

	batch, err := repo.HasChildFoldersBatch(ctx, "kb-1", []string{"p", "c", "missing"})
	require.NoError(t, err)
	assert.True(t, batch["p"])
	assert.False(t, batch["c"])
	assert.False(t, batch["missing"])
}

func TestDocumentFolderRepo_SearchFoldersInScopes(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id, name, type) VALUES (?, ?, ?, ?), (?, ?, ?, ?)",
		"kb-1", 1, "产品知识库", types.KnowledgeBaseTypeDocument,
		"kb-2", 2, "其他知识库", types.KnowledgeBaseTypeDocument,
	).Error)
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "f-1", "", "发布说明", "产品/发布说明", 1)))
	other := mkFolder("kb-2", "f-2", "", "发布说明", "其他/发布说明", 1)
	other.TenantID = 2
	require.NoError(t, repo.CreateFolder(ctx, other))

	results, hasMore, total, err := repo.SearchFoldersInScopes(
		ctx,
		[]types.KnowledgeSearchScope{{TenantID: 1, KBID: "kb-1"}},
		"发布",
		0,
		20,
	)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Equal(t, int64(1), total)
	require.Len(t, results, 1)
	assert.Equal(t, "f-1", results[0].ID)
	assert.Equal(t, "产品知识库", results[0].KnowledgeBaseName)
}

func TestDocumentFolderRepo_SearchFoldersInScopes_DoesNotMatchAncestorPath(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id, name, type) VALUES (?, ?, ?, ?)",
		"kb-1", 1, "产品知识库", types.KnowledgeBaseTypeDocument,
	).Error)
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "product", "", "产品资料", "产品资料", 1)))
	require.NoError(t, repo.CreateFolder(ctx, mkFolder(
		"kb-1",
		"release-notes",
		"product",
		"发布说明",
		"产品资料/发布说明",
		2,
	)))

	results, hasMore, total, err := repo.SearchFoldersInScopes(
		ctx,
		[]types.KnowledgeSearchScope{{TenantID: 1, KBID: "kb-1"}},
		"产品",
		0,
		20,
	)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Equal(t, int64(1), total)
	require.Len(t, results, 1)
	assert.Equal(t, "product", results[0].ID)
}

func TestKnowledgeRepo_SearchKnowledgeInScopes_ReturnsFolderPath(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	ctx := context.Background()
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id, name, type) VALUES (?, ?, ?, ?)",
		"kb-1", 1, "产品知识库", types.KnowledgeBaseTypeDocument,
	).Error)
	folderRepo := NewDocumentFolderRepository(db)
	require.NoError(t, folderRepo.CreateFolder(
		ctx,
		mkFolder("kb-1", "folder-1", "", "发布说明", "产品/发布说明", 1),
	))
	insertKnowledgeFolderRow(t, db, "knowledge-1", "kb-1", "folder-1")

	knowledgeRepo := NewKnowledgeRepository(db).(*knowledgeRepository)
	results, hasMore, total, err := knowledgeRepo.SearchKnowledgeInScopes(
		ctx,
		[]types.KnowledgeSearchScope{{TenantID: 1, KBID: "kb-1"}},
		"knowledge-1",
		0,
		20,
		nil,
	)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Equal(t, int64(1), total)
	require.Len(t, results, 1)
	assert.Equal(t, "产品/发布说明", results[0].FolderPath)
}

func TestFolderMentionSearchExcludesSoftDeletedKnowledgeBases(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	ctx := context.Background()
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id, name, type, deleted_at) VALUES (?, ?, ?, ?, ?)",
		"kb-deleted",
		1,
		"已删除知识库",
		types.KnowledgeBaseTypeDocument,
		time.Now(),
	).Error)
	folderRepo := NewDocumentFolderRepository(db)
	require.NoError(t, folderRepo.CreateFolder(
		ctx,
		mkFolder("kb-deleted", "folder-deleted", "", "已删除目录", "已删除目录", 1),
	))
	insertKnowledgeFolderRow(t, db, "knowledge-deleted", "kb-deleted", "folder-deleted")

	scopes := []types.KnowledgeSearchScope{{TenantID: 1, KBID: "kb-deleted"}}
	folders, hasMore, total, err := folderRepo.SearchFoldersInScopes(ctx, scopes, "", 0, 20)
	require.NoError(t, err)
	require.Empty(t, folders)
	require.False(t, hasMore)
	require.Zero(t, total)
}

func TestDocumentFolderRepo_TransactionRequiresAndLocksKnowledgeBase(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	err := repo.UpdateFoldersInTransaction(ctx, "missing-kb", func(interfaces.DocumentFolderRepository) error {
		t.Fatal("transaction callback must not run without a live knowledge base")
		return nil
	})
	require.ErrorIs(t, err, ErrKnowledgeBaseNotFound)

	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id, name, type) VALUES (?, ?, ?, ?)",
		"kb-1", 1, "KB", types.KnowledgeBaseTypeDocument,
	).Error)
	require.NoError(t, repo.UpdateFoldersInTransaction(ctx, "kb-1", func(txRepo interfaces.DocumentFolderRepository) error {
		return txRepo.CreateFolder(ctx, mkFolder("kb-1", "folder-1", "", "Folder", "Folder", 1))
	}))

	_, err = repo.GetFolderByID(ctx, "kb-1", "folder-1")
	require.NoError(t, err)
}

func TestKnowledgeRepo_CreateKnowledgeValidatesFolderInsideTransaction(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	ctx := context.Background()
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id, name, type) VALUES (?, ?, ?, ?)",
		"kb-1", 1, "KB", types.KnowledgeBaseTypeDocument,
	).Error)
	folderRepo := NewDocumentFolderRepository(db)
	require.NoError(t, folderRepo.CreateFolder(
		ctx,
		mkFolder("kb-1", "folder-1", "", "Folder", "Folder", 1),
	))
	knowledgeRepo := NewKnowledgeRepository(db)

	valid := &types.Knowledge{
		ID:              "knowledge-valid",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            "document",
		Title:           "valid",
		FolderID:        "folder-1",
	}
	require.NoError(t, knowledgeRepo.CreateKnowledge(ctx, valid))

	missingFolder := &types.Knowledge{
		ID:              "knowledge-missing-folder",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            "document",
		Title:           "missing",
		FolderID:        "missing-folder",
	}
	require.ErrorIs(t, knowledgeRepo.CreateKnowledge(ctx, missingFolder), ErrDocumentFolderNotFound)

	wrongTenant := &types.Knowledge{
		ID:              "knowledge-wrong-tenant",
		TenantID:        2,
		KnowledgeBaseID: "kb-1",
		Type:            "document",
		Title:           "wrong tenant",
		FolderID:        "folder-1",
	}
	require.ErrorIs(t, knowledgeRepo.CreateKnowledge(ctx, wrongTenant), ErrKnowledgeBaseNotFound)

	var count int64
	require.NoError(t, db.Model(&types.Knowledge{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

// TestDocumentFolderRepo_CountAll enforces MaxFoldersPerKB is observable: the
// service uses this to refuse exceeding the cap.
func TestDocumentFolderRepo_CountAll(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	repo := NewDocumentFolderRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "f-1", "", "A", "A", 1)))
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-1", "f-2", "", "B", "B", 1)))
	require.NoError(t, repo.CreateFolder(ctx, mkFolder("kb-2", "f-3", "", "C", "C", 1))) // other KB

	n, err := repo.CountAllFolders(ctx, "kb-1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

// seedKnowledgeRows inserts raw knowledge rows for folder-count tests. Each
// row's id is deterministic so test assertions stay stable.
func seedKnowledgeRows(t *testing.T, db *gorm.DB, kbID string) {
	t.Helper()
	insertKnowledgeFolderRow(t, db, "k-1", kbID, "f-a")
	insertKnowledgeFolderRow(t, db, "k-2", kbID, "f-a")
	insertKnowledgeFolderRow(t, db, "k-3", kbID, "f-b")
	// k-4 soft-deleted — must not be counted
	insertKnowledgeFolderRow(t, db, "k-4", kbID, "f-a")
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET deleted_at = ? WHERE id = ?",
		time.Now().Format("2006-01-02 15:04:05"), "k-4",
	).Error)
}

func insertKnowledgeFolderRow(t *testing.T, db *gorm.DB, id, kbID, folderID string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, file_name, folder_id, parse_status)
		 VALUES (?, 1, ?, 'document', ?, ?, ?, 'completed')`,
		id, kbID, id, id+".pdf", folderID,
	).Error)
}

// ensure uuid import is used (some builds inline uuid in later tests).
var _ = uuid.New

// ---- L2 knowledge-folder filter tests ----

// ptr is a tiny helper to take the address of a string — keeps the three-state
// FolderID filter tests readable (nil vs &"" vs &"x").
func ptr(s string) *string { return &s }

// TestKnowledgeFolderFilter_ThreeState is the L2 core correctness test: the
// FolderID *string filter must distinguish nil (no filter), "" (root only),
// and a specific folder id.
func TestKnowledgeFolderFilter_ThreeState(t *testing.T) {
	db := setupDocumentFolderTestDB(t)
	ctx := context.Background()

	// Seed: 2 root docs, 2 docs in f-a, 1 in f-b.
	insertKnowledgeFolderRow(t, db, "r1", "kb-1", "")
	insertKnowledgeFolderRow(t, db, "r2", "kb-1", "")
	insertKnowledgeFolderRow(t, db, "a1", "kb-1", "f-a")
	insertKnowledgeFolderRow(t, db, "a2", "kb-1", "f-a")
	insertKnowledgeFolderRow(t, db, "b1", "kb-1", "f-b")

	repo := NewKnowledgeRepository(db).(*knowledgeRepository)

	// nil → no filter, returns all 5.
	list, _, err := repo.ListPagedKnowledgeByKnowledgeBaseID(
		ctx, 1, "kb-1", &types.Pagination{Page: 1, PageSize: 100}, types.KnowledgeListFilter{},
	)
	require.NoError(t, err)
	assert.Len(t, list, 5)

	// &"" → root only (r1, r2).
	list, _, err = repo.ListPagedKnowledgeByKnowledgeBaseID(
		ctx, 1, "kb-1", &types.Pagination{Page: 1, PageSize: 100}, types.KnowledgeListFilter{FolderID: ptr("")},
	)
	require.NoError(t, err)
	require.Len(t, list, 2)
	// Names are derived from id+".pdf" in the seed helper; just check folder_id.
	got := folderIDSet(list)
	assert.Equal(t, map[string]bool{"": true}, got)

	// &"f-a" → only f-a docs.
	list, _, err = repo.ListPagedKnowledgeByKnowledgeBaseID(
		ctx, 1, "kb-1", &types.Pagination{Page: 1, PageSize: 100}, types.KnowledgeListFilter{FolderID: ptr("f-a")},
	)
	require.NoError(t, err)
	require.Len(t, list, 2)
	got = folderIDSet(list)
	assert.Equal(t, map[string]bool{"f-a": true}, got)

	// &"f-b" → only f-b.
	list, _, err = repo.ListPagedKnowledgeByKnowledgeBaseID(
		ctx, 1, "kb-1", &types.Pagination{Page: 1, PageSize: 100}, types.KnowledgeListFilter{FolderID: ptr("f-b")},
	)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

// folderIDSet projects a knowledge list to a set of folder_id values — keeps
// the assertions in the filter test compact and decoupled from row ordering.
func folderIDSet(ks []*types.Knowledge) map[string]bool {
	out := make(map[string]bool, len(ks))
	for _, k := range ks {
		out[k.FolderID] = true
	}
	return out
}
