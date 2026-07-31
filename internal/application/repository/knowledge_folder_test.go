package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const knowledgeFolderTestDDL = `
CREATE TABLE IF NOT EXISTS knowledge_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    parent_folder_id VARCHAR(36),
    path TEXT NOT NULL,
    depth INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER DEFAULT 0,
    color VARCHAR(32),
    description TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
`

func setupKnowledgeFolderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec(knowledgeFolderTestDDL).Error)
	return db
}

func seedKnowledgeFolderFixture(t *testing.T, db *gorm.DB) (tenantID uint64, kbID, rootFolderA, childFolderB, folderC string) {
	t.Helper()
	tenantID = 1
	kbID = uuid.New().String()
	rootFolderA = uuid.New().String()
	childFolderB = uuid.New().String()
	folderC = uuid.New().String()

	// Create folder structure:
	//   A/ (rootFolderA, path=/rootFolderA/)
	//     B/ (childFolderB, path=/rootFolderA/childFolderB/)
	//   C/ (folderC, path=/folderC/)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_folders (id, tenant_id, knowledge_base_id, name, parent_folder_id, path, depth)
		VALUES (?, ?, ?, 'FolderA', NULL, ?, 1)
	`, rootFolderA, tenantID, kbID, "/"+rootFolderA+"/").Error)

	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_folders (id, tenant_id, knowledge_base_id, name, parent_folder_id, path, depth)
		VALUES (?, ?, ?, 'FolderB', ?, ?, 2)
	`, childFolderB, tenantID, kbID, rootFolderA, "/"+rootFolderA+"/"+childFolderB+"/").Error)

	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_folders (id, tenant_id, knowledge_base_id, name, parent_folder_id, path, depth)
		VALUES (?, ?, ?, 'FolderC', NULL, ?, 1)
	`, folderC, tenantID, kbID, "/"+folderC+"/").Error)

	// Create knowledge entries
	// kA: in FolderA
	// kB: in FolderB (child of A)
	// kC: in FolderC
	// kRoot: in root (folder_id IS NULL)
	kA := uuid.New().String()
	kB := uuid.New().String()
	kC := uuid.New().String()
	kRoot := uuid.New().String()

	for _, entry := range []struct {
		id       string
		folderID string
		title    string
	}{
		{kA, rootFolderA, "Knowledge in A"},
		{kB, childFolderB, "Knowledge in B"},
		{kC, folderC, "Knowledge in C"},
		{kRoot, "", "Knowledge in Root"},
	} {
		query := `INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status, folder_id)
			VALUES (?, ?, ?, 'document', ?, 'manual', 'completed', ?)`
		folderID := interface{}(nil)
		if entry.folderID != "" {
			folderID = entry.folderID
		}
		require.NoError(t, db.Exec(query, entry.id, tenantID, kbID, entry.title, folderID).Error)
	}

	return
}

func TestListKnowledgeIDsByFolderIDs_NonRecursive(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	tenantID, kbID, rootFolderA, _, folderC := seedKnowledgeFolderFixture(t, db)
	repo := NewKnowledgeRepository(db)

	// Non-recursive: should return knowledge directly in FolderA (kA) + FolderC (kC), not in subfolder B
	ids, err := repo.ListKnowledgeIDsByFolderIDs(context.Background(), tenantID, kbID, []string{rootFolderA, folderC}, false)
	require.NoError(t, err)
	assert.Len(t, ids, 2, "non-recursive should return only direct children of FolderA and FolderC")
}

func TestListKnowledgeIDsByFolderIDs_Recursive(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	tenantID, kbID, rootFolderA, _, folderC := seedKnowledgeFolderFixture(t, db)
	repo := NewKnowledgeRepository(db)

	// Recursive: should return kA + kB (FolderA and its descendant FolderB) + kC (FolderC) = 3 total
	ids, err := repo.ListKnowledgeIDsByFolderIDs(context.Background(), tenantID, kbID, []string{rootFolderA, folderC}, true)
	require.NoError(t, err)
	assert.Len(t, ids, 3, "recursive should return knowledge from FolderA, all its descendants, and FolderC")
}

func TestListKnowledgeIDsByFolderIDs_RootFolder(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	tenantID, kbID, _, _, _ := seedKnowledgeFolderFixture(t, db)
	repo := NewKnowledgeRepository(db)

	// "__root__" should return knowledge with folder_id IS NULL
	ids, err := repo.ListKnowledgeIDsByFolderIDs(context.Background(), tenantID, kbID, []string{"__root__"}, false)
	require.NoError(t, err)
	assert.Len(t, ids, 1, "root folder should return knowledge with folder_id IS NULL")
}

func TestListKnowledgeIDsByFolderIDs_MultipleFolders(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	tenantID, kbID, rootFolderA, _, folderC := seedKnowledgeFolderFixture(t, db)
	repo := NewKnowledgeRepository(db)

	// Multiple folders: should return kA + kC
	ids, err := repo.ListKnowledgeIDsByFolderIDs(context.Background(), tenantID, kbID, []string{rootFolderA, folderC}, false)
	require.NoError(t, err)
	assert.Len(t, ids, 2, "should return knowledge from both FolderA and FolderC")
}

func TestListKnowledgeIDsByFolderIDs_RootPlusFolder(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	tenantID, kbID, rootFolderA, _, _ := seedKnowledgeFolderFixture(t, db)
	repo := NewKnowledgeRepository(db)

	// "__root__" + FolderA: should return root knowledge + kA
	ids, err := repo.ListKnowledgeIDsByFolderIDs(context.Background(), tenantID, kbID, []string{"__root__", rootFolderA}, false)
	require.NoError(t, err)
	assert.Len(t, ids, 2, "should return root knowledge + FolderA knowledge")
}

func TestListKnowledgeIDsByFolderIDs_EmptyInput(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	tenantID, kbID, _, _, _ := seedKnowledgeFolderFixture(t, db)
	repo := NewKnowledgeRepository(db)

	ids, err := repo.ListKnowledgeIDsByFolderIDs(context.Background(), tenantID, kbID, []string{}, false)
	require.NoError(t, err)
	assert.Nil(t, ids, "empty folder IDs should return nil")
}

func TestListKnowledgeIDsByFolderIDs_UnknownFolder(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	tenantID, kbID, _, _, _ := seedKnowledgeFolderFixture(t, db)
	repo := NewKnowledgeRepository(db)

	ids, err := repo.ListKnowledgeIDsByFolderIDs(context.Background(), tenantID, kbID, []string{uuid.New().String()}, false)
	require.NoError(t, err)
	assert.Len(t, ids, 0, "unknown folder ID should return empty list")
}
