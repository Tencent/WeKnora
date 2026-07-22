package repository

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const knowledgeFolderTestDDL = `
CREATE TABLE knowledge_bases (
    id         VARCHAR(36) PRIMARY KEY,
    tenant_id  INTEGER NOT NULL,
    deleted_at DATETIME
);
CREATE TABLE knowledge_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL CHECK (name <> ''),
    path              VARCHAR(2048) NOT NULL CHECK (path <> ''),
    depth             INTEGER NOT NULL CHECK (depth BETWEEN 1 AND 32),
    sort_order        INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at        DATETIME
);
CREATE UNIQUE INDEX idx_knowledge_folders_live_sibling_name
    ON knowledge_folders (tenant_id, knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;
CREATE TABLE knowledges (
    id                     VARCHAR(36) PRIMARY KEY,
    tenant_id              INTEGER NOT NULL,
    knowledge_base_id      VARCHAR(36) NOT NULL,
    folder_id              VARCHAR(36) NOT NULL DEFAULT '',
    folder_version         INTEGER NOT NULL DEFAULT 1,
    folder_indexed_version INTEGER NOT NULL DEFAULT 0,
    deleted_at             DATETIME
);
`

func setupKnowledgeFolderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.Exec(knowledgeFolderTestDDL).Error)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func knowledgeFolderFixture(
	id string,
	tenantID uint64,
	kbID string,
	parentID string,
	name string,
	path string,
	depth int,
) *types.KnowledgeFolder {
	return rawKnowledgeFolderFixture(
		knowledgeFolderTestID(id),
		tenantID,
		kbID,
		knowledgeFolderTestID(parentID),
		name,
		knowledgeFolderTestPath(path),
		depth,
	)
}

func rawKnowledgeFolderFixture(
	id string,
	tenantID uint64,
	kbID string,
	parentID string,
	name string,
	path string,
	depth int,
) *types.KnowledgeFolder {
	return &types.KnowledgeFolder{
		ID:              id,
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		ParentID:        parentID,
		Name:            name,
		Path:            path,
		Depth:           depth,
	}
}

func knowledgeFolderTestID(id string) string {
	if id == "" {
		return ""
	}
	if parsed, err := uuid.Parse(id); err == nil && parsed.String() == id {
		return id
	}
	namespace := uuid.MustParse("e6b3c447-b349-4d6f-93a8-3115fb97b3e8")
	return uuid.NewSHA1(namespace, []byte(id)).String()
}

func knowledgeFolderTestPath(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if segment != "" {
			segments[index] = knowledgeFolderTestID(segment)
		}
	}
	return strings.Join(segments, "/")
}

func knowledgeFolderIDs(folders []*types.KnowledgeFolder) []string {
	ids := make([]string, len(folders))
	for index, folder := range folders {
		ids[index] = folder.ID
	}
	return ids
}

func TestKnowledgeFolderRepository_CreateValidatesPersistenceStructure(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()

	rootID := knowledgeFolderTestID("root")
	parentID := rootID
	childID := knowledgeFolderTestID("child")
	otherID := knowledgeFolderTestID("other")
	validRoot := rawKnowledgeFolderFixture(
		rootID,
		1,
		"kb-1",
		"",
		"Root",
		"/"+rootID+"/",
		1,
	)
	validChild := rawKnowledgeFolderFixture(
		childID,
		1,
		"kb-1",
		parentID,
		"Child",
		"/"+parentID+"/"+childID+"/",
		2,
	)
	withoutID := rawKnowledgeFolderFixture("", 1, "kb-1", "", "Missing ID", "/missing-id/", 1)
	tests := []struct {
		name   string
		folder *types.KnowledgeFolder
	}{
		{name: "nil folder", folder: nil},
		{
			name:   "empty tenant",
			folder: rawKnowledgeFolderFixture(rootID, 0, "kb-1", "", "Root", "/"+rootID+"/", 1),
		},
		{
			name:   "empty knowledge base",
			folder: rawKnowledgeFolderFixture(rootID, 1, "", "", "Root", "/"+rootID+"/", 1),
		},
		{
			name:   "non-canonical knowledge base",
			folder: rawKnowledgeFolderFixture(rootID, 1, " kb-1 ", "", "Root", "/"+rootID+"/", 1),
		},
		{name: "empty ID", folder: withoutID},
		{
			name: "non-canonical ID",
			folder: rawKnowledgeFolderFixture(
				strings.ToUpper(rootID),
				1,
				"kb-1",
				"",
				"Root",
				"/"+strings.ToUpper(rootID)+"/",
				1,
			),
		},
		{
			name:   "empty name",
			folder: rawKnowledgeFolderFixture(rootID, 1, "kb-1", "", " ", "/"+rootID+"/", 1),
		},
		{
			name: "name above maximum",
			folder: rawKnowledgeFolderFixture(
				rootID,
				1,
				"kb-1",
				"",
				strings.Repeat("界", types.KnowledgeFolderMaxNameRunes+1),
				"/"+rootID+"/",
				1,
			),
		},
		{
			name: "invalid UTF-8 name",
			folder: rawKnowledgeFolderFixture(
				rootID,
				1,
				"kb-1",
				"",
				string([]byte{0xff}),
				"/"+rootID+"/",
				1,
			),
		},
		{
			name:   "empty path",
			folder: rawKnowledgeFolderFixture(rootID, 1, "kb-1", "", "Empty path", "", 1),
		},
		{
			name: "path missing leading slash",
			folder: rawKnowledgeFolderFixture(
				rootID,
				1,
				"kb-1",
				"",
				"Root",
				rootID+"/",
				1,
			),
		},
		{
			name: "path missing trailing slash",
			folder: rawKnowledgeFolderFixture(
				rootID,
				1,
				"kb-1",
				"",
				"Root",
				"/"+rootID,
				1,
			),
		},
		{
			name: "non-canonical parent ID",
			folder: rawKnowledgeFolderFixture(
				childID,
				1,
				"kb-1",
				strings.ToUpper(parentID),
				"Child",
				"/"+strings.ToUpper(parentID)+"/"+childID+"/",
				2,
			),
		},
		{
			name: "path does not end in ID",
			folder: rawKnowledgeFolderFixture(
				rootID,
				1,
				"kb-1",
				"",
				"Root",
				"/"+otherID+"/",
				1,
			),
		},
		{
			name: "parent does not match path",
			folder: rawKnowledgeFolderFixture(
				childID,
				1,
				"kb-1",
				otherID,
				"Child",
				"/"+parentID+"/"+childID+"/",
				2,
			),
		},
		{
			name: "depth does not match path",
			folder: rawKnowledgeFolderFixture(
				childID,
				1,
				"kb-1",
				parentID,
				"Child",
				"/"+parentID+"/"+childID+"/",
				3,
			),
		},
		{
			name:   "depth zero",
			folder: rawKnowledgeFolderFixture(rootID, 1, "kb-1", "", "Depth zero", "/"+rootID+"/", 0),
		},
		{
			name:   "depth above maximum",
			folder: rawKnowledgeFolderFixture(rootID, 1, "kb-1", "", "Depth 33", "/"+rootID+"/", 33),
		},
		{
			name: "first level has parent",
			folder: rawKnowledgeFolderFixture(
				rootID,
				1,
				"kb-1",
				parentID,
				"Root",
				"/"+rootID+"/",
				1,
			),
		},
		{
			name: "path has empty segment",
			folder: rawKnowledgeFolderFixture(
				childID,
				1,
				"kb-1",
				parentID,
				"Child",
				"/"+parentID+"//"+childID+"/",
				2,
			),
		},
		{
			name: "path contains name",
			folder: rawKnowledgeFolderFixture(
				childID,
				1,
				"kb-1",
				parentID,
				"Child",
				"/reports/"+childID+"/",
				2,
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.Create(ctx, tt.folder)
			require.ErrorIs(t, err, ErrKnowledgeFolderInvalid)
		})
	}

	assert.Empty(t, withoutID.ID)
	require.NoError(t, repo.Create(ctx, validRoot))
	require.NoError(t, repo.Create(ctx, validChild))
	maxNameID := knowledgeFolderTestID("max-name")
	require.NoError(t, repo.Create(ctx, rawKnowledgeFolderFixture(
		maxNameID,
		1,
		"kb-1",
		"",
		strings.Repeat("界", types.KnowledgeFolderMaxNameRunes),
		"/"+maxNameID+"/",
		1,
	)))
	var count int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&count).Error)
	assert.Equal(t, int64(3), count)
}

func TestValidateKnowledgeFolderStructureDoesNotMutateInput(t *testing.T) {
	id := knowledgeFolderTestID("immutable")
	valid := rawKnowledgeFolderFixture(id, 1, "kb-1", "", " Folder ", "/"+id+"/", 1)
	validBefore := *valid
	_, err := types.ValidateKnowledgeFolderStructure(valid)
	require.NoError(t, err)
	assert.Equal(t, validBefore, *valid)

	invalid := rawKnowledgeFolderFixture(
		id,
		1,
		"kb-1",
		"",
		strings.Repeat("界", types.KnowledgeFolderMaxNameRunes+1),
		"/"+id+"/",
		1,
	)
	invalidBefore := *invalid
	_, err = types.ValidateKnowledgeFolderStructure(invalid)
	require.Error(t, err)
	assert.Equal(t, invalidBefore, *invalid)
}

func TestKnowledgeFolderRepository_CreateChecksContextBeforeInsert(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	id := knowledgeFolderTestID("context")
	folder := rawKnowledgeFolderFixture(id, 1, "kb-1", "", "Context", "/"+id+"/", 1)

	err := repo.Create(nil, folder)
	require.ErrorIs(t, err, ErrKnowledgeFolderInvalid)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = repo.Create(ctx, folder)
	require.ErrorIs(t, err, context.Canceled)

	var count int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestKnowledgeFolderRepository_CreateHierarchyAndSiblingUniqueness(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()

	root := knowledgeFolderFixture("folder-root", 1, "kb-1", "", "Root", "/folder-root/", 1)
	child := knowledgeFolderFixture(
		"folder-child",
		1,
		"kb-1",
		root.ID,
		"Reports",
		"/folder-root/folder-child/",
		2,
	)
	require.NoError(t, repo.Create(ctx, root))
	require.NoError(t, repo.Create(ctx, child))

	gotRoot, err := repo.GetByID(ctx, 1, "kb-1", root.ID)
	require.NoError(t, err)
	assert.Equal(t, types.KnowledgeFolderRootID, gotRoot.ParentID)
	assert.Equal(t, 1, gotRoot.Depth)
	assert.Equal(t, knowledgeFolderTestPath("/folder-root/"), gotRoot.Path)

	gotChild, err := repo.GetByParentAndName(ctx, 1, "kb-1", root.ID, "Reports")
	require.NoError(t, err)
	assert.Equal(t, child.ID, gotChild.ID)
	assert.Equal(t, 2, gotChild.Depth)

	duplicate := knowledgeFolderFixture(
		"folder-duplicate",
		1,
		"kb-1",
		root.ID,
		"Reports",
		"/folder-root/folder-duplicate/",
		2,
	)
	err = repo.Create(ctx, duplicate)
	require.ErrorIs(t, err, ErrKnowledgeFolderConflict)
	assert.True(t, IsKnowledgeFolderUniqueViolation(err))

	duplicateID := *root
	duplicateID.Name = "Different name"
	err = repo.Create(ctx, &duplicateID)
	require.ErrorIs(t, err, ErrKnowledgeFolderConflict)
	assert.True(t, IsKnowledgeFolderUniqueViolation(err))

	otherParent := knowledgeFolderFixture("folder-other", 1, "kb-1", "", "Other", "/folder-other/", 1)
	require.NoError(t, repo.Create(ctx, otherParent))
	sameName := knowledgeFolderFixture(
		"folder-same-name",
		1,
		"kb-1",
		otherParent.ID,
		"Reports",
		"/folder-other/folder-same-name/",
		2,
	)
	require.NoError(t, repo.Create(ctx, sameName))
}

func TestKnowledgeFolderRepository_TenantAndKnowledgeBaseIsolation(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()

	folders := []*types.KnowledgeFolder{
		knowledgeFolderFixture("tenant-1-kb-1", 1, "kb-1", "", "Shared name", "/tenant-1-kb-1/", 1),
		knowledgeFolderFixture("tenant-2-kb-1", 2, "kb-1", "", "Shared name", "/tenant-2-kb-1/", 1),
		knowledgeFolderFixture("tenant-1-kb-2", 1, "kb-2", "", "Shared name", "/tenant-1-kb-2/", 1),
	}
	for _, folder := range folders {
		require.NoError(t, db.Create(folder).Error)
	}

	tenantOneFolderID := knowledgeFolderTestID("tenant-1-kb-1")
	_, err := repo.GetByID(ctx, 2, "kb-1", tenantOneFolderID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	_, err = repo.GetByID(ctx, 1, "kb-2", tenantOneFolderID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	_, err = repo.GetByParentAndName(ctx, 3, "kb-1", "", "Shared name")
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)

	got, total, err := repo.ListByParent(ctx, 1, "kb-1", "", &types.Pagination{Page: 1, PageSize: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, tenantOneFolderID, got[0].ID)

	err = repo.DeleteEmpty(ctx, 2, "kb-1", tenantOneFolderID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeFolderRepository_ListByParentPaginationAndStableOrder(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()
	baseTime := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	folders := []*types.KnowledgeFolder{
		knowledgeFolderFixture("f-delta", 1, "kb-1", "", "delta", "/f-delta/", 1),
		knowledgeFolderFixture("f-beta", 1, "kb-1", "", "beta", "/f-beta/", 1),
		knowledgeFolderFixture("f-alpha", 1, "kb-1", "", "alpha", "/f-alpha/", 1),
		knowledgeFolderFixture("f-charlie", 1, "kb-1", "", "charlie", "/f-charlie/", 1),
		knowledgeFolderFixture("f-last", 1, "kb-1", "", "alpha-last", "/f-last/", 1),
		knowledgeFolderFixture("f-child", 1, "kb-1", "f-alpha", "child", "/f-alpha/f-child/", 2),
		knowledgeFolderFixture("f-deleted", 1, "kb-1", "", "deleted", "/f-deleted/", 1),
	}
	folders[0].SortOrder = 1
	folders[4].SortOrder = 2
	for i, folder := range folders {
		folder.CreatedAt = baseTime.Add(time.Duration(i) * time.Second)
		folder.UpdatedAt = folder.CreatedAt
		require.NoError(t, db.Create(folder).Error)
	}
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("id = ?", knowledgeFolderTestID("f-deleted")).
		Update("deleted_at", time.Now().UTC()).Error)

	page1, total, err := repo.ListByParent(
		ctx,
		1,
		"kb-1",
		"",
		&types.Pagination{Page: 1, PageSize: 2},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	require.Len(t, page1, 2)
	assert.Equal(t, []string{
		knowledgeFolderTestID("f-alpha"),
		knowledgeFolderTestID("f-beta"),
	}, knowledgeFolderIDs(page1))

	page2, total, err := repo.ListByParent(
		ctx,
		1,
		"kb-1",
		"",
		&types.Pagination{Page: 2, PageSize: 2},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	require.Len(t, page2, 2)
	assert.Equal(t, []string{
		knowledgeFolderTestID("f-charlie"),
		knowledgeFolderTestID("f-delta"),
	}, knowledgeFolderIDs(page2))

	page3, total, err := repo.ListByParent(
		ctx,
		1,
		"kb-1",
		"",
		&types.Pagination{Page: 3, PageSize: 2},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	require.Len(t, page3, 1)
	assert.Equal(t, knowledgeFolderTestID("f-last"), page3[0].ID)
}

func TestKnowledgeFolderRepository_ListByParentUsesSharedPaginationBounds(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()

	for index := 0; index < 25; index++ {
		key := fmt.Sprintf("page-%02d", index)
		require.NoError(t, db.Create(knowledgeFolderFixture(
			key,
			1,
			"kb-1",
			"",
			key,
			"/"+key+"/",
			1,
		)).Error)
	}

	defaultPage, total, err := repo.ListByParent(ctx, 1, "kb-1", "", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(25), total)
	assert.Len(t, defaultPage, 20)

	normalizedPage, total, err := repo.ListByParent(
		ctx,
		1,
		"kb-1",
		"",
		&types.Pagination{Page: -1, PageSize: 0},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(25), total)
	assert.Equal(t, knowledgeFolderIDs(defaultPage), knowledgeFolderIDs(normalizedPage))

	clampedPage, total, err := repo.ListByParent(
		ctx,
		1,
		"kb-1",
		"",
		&types.Pagination{Page: 1, PageSize: 1001},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(25), total)
	assert.Len(t, clampedPage, 25)
}

func TestKnowledgeFolderRepository_ListByParentUsesIDAsFinalTieBreaker(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()
	// The live-sibling unique index normally prevents a complete tie; remove it
	// here to verify the repository remains deterministic for legacy/corrupt rows.
	require.NoError(t, db.Exec(`DROP INDEX idx_knowledge_folders_live_sibling_name`).Error)

	createdAt := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"tie-b", "tie-a"} {
		folder := knowledgeFolderFixture(id, 1, "kb-1", "", "same-name", "/"+id+"/", 1)
		folder.SortOrder = 7
		folder.CreatedAt = createdAt
		folder.UpdatedAt = createdAt
		require.NoError(t, db.Create(folder).Error)
	}

	folders, total, err := repo.ListByParent(
		ctx,
		1,
		"kb-1",
		"",
		&types.Pagination{Page: 1, PageSize: 20},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, folders, 2)
	expectedIDs := []string{knowledgeFolderTestID("tie-a"), knowledgeFolderTestID("tie-b")}
	sort.Strings(expectedIDs)
	assert.Equal(t, expectedIDs, knowledgeFolderIDs(folders))
}

func TestKnowledgeFolderRepository_BatchCountsAndHasChildren(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()

	for _, folder := range []*types.KnowledgeFolder{
		knowledgeFolderFixture("f-a", 1, "kb-1", "", "A", "/f-a/", 1),
		knowledgeFolderFixture("f-b", 1, "kb-1", "", "B", "/f-b/", 1),
		knowledgeFolderFixture("f-zero", 1, "kb-1", "", "Zero", "/f-zero/", 1),
		knowledgeFolderFixture("f-a-child", 1, "kb-1", "f-a", "Child", "/f-a/f-a-child/", 2),
		knowledgeFolderFixture("f-b-deleted-child", 1, "kb-1", "f-b", "Deleted", "/f-b/f-b-deleted-child/", 2),
		knowledgeFolderFixture("f-other-tenant-child", 2, "kb-1", "f-b", "Other tenant", "/f-b/f-other-tenant-child/", 2),
	} {
		require.NoError(t, db.Create(folder).Error)
	}
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("id = ?", knowledgeFolderTestID("f-b-deleted-child")).
		Update("deleted_at", time.Now().UTC()).Error)

	folderAID := knowledgeFolderTestID("f-a")
	folderBID := knowledgeFolderTestID("f-b")
	folderZeroID := knowledgeFolderTestID("f-zero")
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id, deleted_at) VALUES
			('k-a-1', 1, 'kb-1', ?, NULL),
			('k-a-2', 1, 'kb-1', ?, NULL),
			('k-a-deleted', 1, 'kb-1', ?, CURRENT_TIMESTAMP),
			('k-b-1', 1, 'kb-1', ?, NULL),
			('k-other-tenant', 2, 'kb-1', ?, NULL),
			('k-other-kb', 1, 'kb-2', ?, NULL),
			('k-root', 1, 'kb-1', '', NULL)
	`, folderAID, folderAID, folderAID, folderBID, folderBID, folderBID).Error)

	counts, err := repo.CountKnowledgeByFolderIDs(
		ctx,
		1,
		"kb-1",
		[]string{folderAID, folderBID, folderZeroID},
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{folderAID: 2, folderBID: 1, folderZeroID: 0}, counts)

	hasChildren, err := repo.FindParentIDsWithChildren(
		ctx,
		1,
		"kb-1",
		[]string{folderAID, folderBID, folderZeroID},
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{folderAID: true, folderBID: false, folderZeroID: false}, hasChildren)

	duplicateCounts, err := repo.CountKnowledgeByFolderIDs(
		ctx,
		1,
		"kb-1",
		[]string{folderAID, folderAID, ""},
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{folderAID: 2, "": 1}, duplicateCounts)

	duplicateParents, err := repo.FindParentIDsWithChildren(
		ctx,
		1,
		"kb-1",
		[]string{folderAID, folderAID, folderBID},
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{folderAID: true, folderBID: false}, duplicateParents)

	emptyCounts, err := repo.CountKnowledgeByFolderIDs(ctx, 1, "kb-1", nil)
	require.NoError(t, err)
	assert.Empty(t, emptyCounts)
	emptyChildren, err := repo.FindParentIDsWithChildren(ctx, 1, "kb-1", nil)
	require.NoError(t, err)
	assert.Empty(t, emptyChildren)
}

func TestKnowledgeFolderRepository_DeleteEmptyAndReuseName(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()

	folder := knowledgeFolderFixture("empty-folder", 1, "kb-1", "", "Reusable", "/empty-folder/", 1)
	require.NoError(t, db.Create(folder).Error)
	require.NoError(t, repo.DeleteEmpty(ctx, 1, "kb-1", folder.ID))

	_, err := repo.GetByID(ctx, 1, "kb-1", folder.ID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)

	replacement := knowledgeFolderFixture(
		"replacement-folder",
		1,
		"kb-1",
		"",
		"Reusable",
		"/replacement-folder/",
		1,
	)
	require.NoError(t, repo.Create(ctx, replacement))
}

func TestKnowledgeFolderRepository_DeleteEmptyRejectsChildrenAndKnowledge(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()

	parent := knowledgeFolderFixture("parent", 1, "kb-1", "", "Parent", "/parent/", 1)
	child := knowledgeFolderFixture("child", 1, "kb-1", parent.ID, "Child", "/parent/child/", 2)
	withKnowledge := knowledgeFolderFixture("with-knowledge", 1, "kb-1", "", "Documents", "/with-knowledge/", 1)
	for _, folder := range []*types.KnowledgeFolder{parent, child, withKnowledge} {
		require.NoError(t, db.Create(folder).Error)
	}
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id)
		 VALUES ('knowledge-1', 1, 'kb-1', ?)`,
		withKnowledge.ID,
	).Error)

	err := repo.DeleteEmpty(ctx, 1, "kb-1", parent.ID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotEmpty)
	err = repo.DeleteEmpty(ctx, 1, "kb-1", withKnowledge.ID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotEmpty)
	err = repo.DeleteEmpty(ctx, 1, "kb-1", knowledgeFolderTestID("missing-folder"))
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)

	_, err = repo.GetByID(ctx, 1, "kb-1", parent.ID)
	require.NoError(t, err)
	_, err = repo.GetByID(ctx, 1, "kb-1", withKnowledge.ID)
	require.NoError(t, err)
}

func TestKnowledgeFolderRepository_DeleteEmptyIgnoresSoftDeletedContents(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()

	withDeletedChild := knowledgeFolderFixture(
		"with-deleted-child",
		1,
		"kb-1",
		"",
		"Deleted child",
		"/with-deleted-child/",
		1,
	)
	deletedChild := knowledgeFolderFixture(
		"deleted-child",
		1,
		"kb-1",
		withDeletedChild.ID,
		"Child",
		"/with-deleted-child/deleted-child/",
		2,
	)
	withDeletedKnowledge := knowledgeFolderFixture(
		"with-deleted-knowledge",
		1,
		"kb-1",
		"",
		"Deleted knowledge",
		"/with-deleted-knowledge/",
		1,
	)
	for _, folder := range []*types.KnowledgeFolder{
		withDeletedChild,
		deletedChild,
		withDeletedKnowledge,
	} {
		require.NoError(t, db.Create(folder).Error)
	}
	require.NoError(t, db.Exec(
		`UPDATE knowledge_folders SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`,
		deletedChild.ID,
	).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id, deleted_at)
		VALUES ('deleted-knowledge', 1, 'kb-1', ?, CURRENT_TIMESTAMP)
	`, withDeletedKnowledge.ID).Error)

	require.NoError(t, repo.DeleteEmpty(ctx, 1, "kb-1", withDeletedChild.ID))
	require.NoError(t, repo.DeleteEmpty(ctx, 1, "kb-1", withDeletedKnowledge.ID))
}
