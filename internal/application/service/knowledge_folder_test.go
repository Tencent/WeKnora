package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// fakeFolderRepo is an in-memory implementation of interfaces.KnowledgeFolderRepository.
type fakeFolderRepo struct {
	folders   map[string]*types.KnowledgeFolder
	knowledge map[string]*types.Knowledge
}

func newFakeFolderRepo() *fakeFolderRepo {
	return &fakeFolderRepo{
		folders:   make(map[string]*types.KnowledgeFolder),
		knowledge: make(map[string]*types.Knowledge),
	}
}

func (r *fakeFolderRepo) Create(ctx context.Context, folder *types.KnowledgeFolder) error {
	r.folders[folder.ID] = folder
	return nil
}

func (r *fakeFolderRepo) GetByID(ctx context.Context, tenantID uint64, id string) (*types.KnowledgeFolder, error) {
	f, ok := r.folders[id]
	if !ok || f.TenantID != tenantID {
		return nil, repository.ErrFolderNotFound
	}
	cp := *f
	cp.Children = nil
	return &cp, nil
}

func (r *fakeFolderRepo) ListByParent(ctx context.Context, tenantID uint64, kbID string, parentID *string) ([]*types.KnowledgeFolder, error) {
	var result []*types.KnowledgeFolder
	for _, f := range r.folders {
		if f.TenantID != tenantID || f.KnowledgeBaseID != kbID {
			continue
		}
		if parentID == nil && f.ParentFolderID == nil {
			cp := *f
			cp.Children = nil
			result = append(result, &cp)
		} else if parentID != nil && f.ParentFolderID != nil && *f.ParentFolderID == *parentID {
			cp := *f
			cp.Children = nil
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (r *fakeFolderRepo) GetAllInKB(ctx context.Context, tenantID uint64, kbID string) ([]*types.KnowledgeFolder, error) {
	var result []*types.KnowledgeFolder
	for _, f := range r.folders {
		if f.TenantID == tenantID && f.KnowledgeBaseID == kbID {
			cp := *f
			cp.Children = nil
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (r *fakeFolderRepo) Update(ctx context.Context, folder *types.KnowledgeFolder) error {
	r.folders[folder.ID] = folder
	return nil
}

func (r *fakeFolderRepo) Delete(ctx context.Context, tenantID uint64, id string) error {
	f, ok := r.folders[id]
	if !ok || f.TenantID != tenantID {
		return repository.ErrFolderNotFound
	}
	delete(r.folders, id)
	return nil
}

func (r *fakeFolderRepo) Move(ctx context.Context, id string, newParentID *string, newPath string, newDepth int) error {
	f, ok := r.folders[id]
	if !ok {
		return repository.ErrFolderNotFound
	}
	f.ParentFolderID = newParentID
	f.Path = newPath
	f.Depth = newDepth
	return nil
}

func (r *fakeFolderRepo) GetByPath(ctx context.Context, tenantID uint64, kbID string, path string) (*types.KnowledgeFolder, error) {
	for _, f := range r.folders {
		if f.TenantID == tenantID && f.KnowledgeBaseID == kbID && f.Path == path {
			cp := *f
			cp.Children = nil
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeFolderRepo) GetDescendants(ctx context.Context, folderID string) ([]*types.KnowledgeFolder, error) {
	self, ok := r.folders[folderID]
	if !ok {
		return nil, repository.ErrFolderNotFound
	}
	var result []*types.KnowledgeFolder
	for _, f := range r.folders {
		if f.ID == folderID {
			continue
		}
		if len(f.Path) >= len(self.Path) && f.Path[:len(self.Path)] == self.Path {
			cp := *f
			cp.Children = nil
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (r *fakeFolderRepo) CountKnowledge(ctx context.Context, tenantID uint64, folderID string) (int64, error) {
	var count int64
	for _, k := range r.knowledge {
		if k.FolderID != nil && *k.FolderID == folderID {
			count++
		}
	}
	return count, nil
}

func (r *fakeFolderRepo) CountKnowledgeByKB(ctx context.Context, tenantID uint64, kbID string) (map[string]int64, error) {
	result := make(map[string]int64)
	for _, k := range r.knowledge {
		if k.TenantID == tenantID && k.KnowledgeBaseID == kbID && k.FolderID != nil {
			result[*k.FolderID]++
		}
	}
	return result, nil
}

func (r *fakeFolderRepo) CountKnowledgeRecursive(ctx context.Context, tenantID uint64, folderID string) (int64, error) {
	self, ok := r.folders[folderID]
	if !ok {
		return 0, repository.ErrFolderNotFound
	}
	var count int64
	for _, k := range r.knowledge {
		if k.FolderID == nil {
			continue
		}
		if *k.FolderID == folderID {
			count++
			continue
		}
		if kf, ok := r.folders[*k.FolderID]; ok {
			if len(kf.Path) >= len(self.Path) && kf.Path[:len(self.Path)] == self.Path {
				count++
			}
		}
	}
	return count, nil
}

func (r *fakeFolderRepo) CheckNameExists(ctx context.Context, tenantID uint64, kbID string, parentID *string, name string, excludeID string) (bool, error) {
	for _, f := range r.folders {
		if f.TenantID != tenantID || f.KnowledgeBaseID != kbID || f.Name != name {
			continue
		}
		if excludeID != "" && f.ID == excludeID {
			continue
		}
		if parentID == nil && f.ParentFolderID == nil {
			return true, nil
		}
		if parentID != nil && f.ParentFolderID != nil && *f.ParentFolderID == *parentID {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeFolderRepo) BatchUpdateDescendantPaths(ctx context.Context, oldPath string, newPath string, depthDelta int) error {
	for _, f := range r.folders {
		if len(f.Path) >= len(oldPath) && f.Path[:len(oldPath)] == oldPath && f.Path != oldPath {
			f.Path = newPath + f.Path[len(oldPath):]
			f.Depth += depthDelta
		}
	}
	return nil
}

// --- Helper & Setup ---

func ptr(s string) *string { return &s }

func createSQLiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// setupServiceTest creates a folder service backed by a fake folder repo.
// The kgRepo dependency is passed as nil because knowledgeFolderService does not
// use it (it operates on the knowledge table through s.db directly).
func setupServiceTest(t *testing.T) (interfaces.KnowledgeFolderService, *fakeFolderRepo) {
	t.Helper()
	repo := newFakeFolderRepo()
	db := createSQLiteDB(t)
	svc := NewKnowledgeFolderService(repo, nil, db)
	return svc, repo
}

// --- CreateFolder ---

func TestCreateFolder_RootLevel(t *testing.T) {
	svc, _ := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	folder, err := svc.CreateFolder(ctx, "kb-1", &types.CreateFolderRequest{Name: "my-folder"})
	require.NoError(t, err)
	assert.Equal(t, "my-folder", folder.Name)
	assert.Equal(t, 1, folder.Depth)
	assert.Nil(t, folder.ParentFolderID)
	assert.Contains(t, folder.Path, folder.ID)
}

func TestCreateFolder_UnderParent(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	parentID := uuid.New().String()
	repo.folders[parentID] = &types.KnowledgeFolder{
		ID: parentID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "parent",
		Path: "/" + parentID + "/", Depth: 1,
	}

	folder, err := svc.CreateFolder(ctx, "kb-1", &types.CreateFolderRequest{
		Name: "child", ParentFolderID: &parentID,
	})
	require.NoError(t, err)
	assert.Equal(t, "child", folder.Name)
	assert.Equal(t, 2, folder.Depth)
	assert.Equal(t, parentID, *folder.ParentFolderID)
}

func TestCreateFolder_DuplicateName(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	repo.folders["existing"] = &types.KnowledgeFolder{
		ID: "existing", TenantID: 1, KnowledgeBaseID: "kb-1", Name: "existing",
		Path: "/existing/", Depth: 1,
	}

	_, err := svc.CreateFolder(ctx, "kb-1", &types.CreateFolderRequest{Name: "existing"})
	assert.ErrorIs(t, err, repository.ErrFolderNameExists)
}

func TestCreateFolder_MaxDepthExceeded(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	deepID := uuid.New().String()
	repo.folders[deepID] = &types.KnowledgeFolder{
		ID: deepID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "deep",
		Path: "/deep/", Depth: types.MaxFolderDepth,
	}

	_, err := svc.CreateFolder(ctx, "kb-1", &types.CreateFolderRequest{
		Name: "too-deep", ParentFolderID: &deepID,
	})
	assert.ErrorIs(t, err, repository.ErrMaxDepthExceeded)
}

func TestCreateFolder_EmptyName(t *testing.T) {
	svc, _ := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	_, err := svc.CreateFolder(ctx, "kb-1", &types.CreateFolderRequest{Name: "   "})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "folder name cannot be empty")
}

// --- GetFolder ---

func TestGetFolder_Success(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	folderID := uuid.New().String()
	repo.folders[folderID] = &types.KnowledgeFolder{
		ID: folderID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "test",
		Path: "/" + folderID + "/", Depth: 1,
	}

	result, err := svc.GetFolder(ctx, folderID)
	require.NoError(t, err)
	assert.Equal(t, "test", result.Name)
}

func TestGetFolder_NotFound(t *testing.T) {
	svc, _ := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	_, err := svc.GetFolder(ctx, "nonexistent")
	assert.ErrorIs(t, err, repository.ErrFolderNotFound)
}

// --- ListByParent ---

func TestListByParent_Success(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	repo.folders["f1"] = &types.KnowledgeFolder{ID: "f1", TenantID: 1, KnowledgeBaseID: "kb-1", Name: "a", Path: "/f1/", Depth: 1}
	repo.folders["f2"] = &types.KnowledgeFolder{ID: "f2", TenantID: 1, KnowledgeBaseID: "kb-1", Name: "b", Path: "/f2/", Depth: 1}

	folders, err := svc.ListByParent(ctx, "kb-1", nil)
	require.NoError(t, err)
	assert.Len(t, folders, 2)
}

// --- GetTree ---

func TestGetTree_BuildsHierarchy(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	rootID := uuid.New().String()
	childID := uuid.New().String()
	gcID := uuid.New().String()

	repo.folders[rootID] = &types.KnowledgeFolder{
		ID: rootID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "root",
		Path: fmt.Sprintf("/%s/", rootID), Depth: 1,
	}
	repo.folders[childID] = &types.KnowledgeFolder{
		ID: childID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "child",
		ParentFolderID: ptr(rootID), Path: fmt.Sprintf("/%s/%s/", rootID, childID), Depth: 2,
	}
	repo.folders[gcID] = &types.KnowledgeFolder{
		ID: gcID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "grandchild",
		ParentFolderID: ptr(childID), Path: fmt.Sprintf("/%s/%s/%s/", rootID, childID, gcID), Depth: 3,
	}

	tree, err := svc.GetTree(ctx, "kb-1")
	require.NoError(t, err)
	assert.Len(t, tree, 1)
	assert.Equal(t, "root", tree[0].Name)
	assert.Len(t, tree[0].Children, 1)
	assert.Equal(t, "child", tree[0].Children[0].Name)
	assert.Len(t, tree[0].Children[0].Children, 1)
}

// --- UpdateFolder ---

func TestUpdateFolder_Rename(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	folderID := uuid.New().String()
	repo.folders[folderID] = &types.KnowledgeFolder{
		ID: folderID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "old",
		Path: fmt.Sprintf("/%s/", folderID), Depth: 1,
	}

	newName := "new-name"
	result, err := svc.UpdateFolder(ctx, folderID, &types.UpdateFolderRequest{Name: &newName})
	require.NoError(t, err)
	assert.Equal(t, "new-name", result.Name)
}

func TestUpdateFolder_RenameConflict(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	repo.folders["f1"] = &types.KnowledgeFolder{ID: "f1", TenantID: 1, KnowledgeBaseID: "kb-1", Name: "alpha", Path: "/f1/", Depth: 1}
	repo.folders["f2"] = &types.KnowledgeFolder{ID: "f2", TenantID: 1, KnowledgeBaseID: "kb-1", Name: "beta", Path: "/f2/", Depth: 1}

	newName := "beta"
	_, err := svc.UpdateFolder(ctx, "f1", &types.UpdateFolderRequest{Name: &newName})
	assert.ErrorIs(t, err, repository.ErrFolderNameExists)
}

// --- DeleteFolder ---

func TestDeleteFolder_EmptyFolder(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	folderID := uuid.New().String()
	repo.folders[folderID] = &types.KnowledgeFolder{
		ID: folderID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "empty",
		Path: fmt.Sprintf("/%s/", folderID), Depth: 1,
	}

	err := svc.DeleteFolder(ctx, folderID, false)
	require.NoError(t, err)

	_, err = svc.GetFolder(ctx, folderID)
	assert.ErrorIs(t, err, repository.ErrFolderNotFound)
}

func TestDeleteFolder_NonEmptyNoForce(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	parentID := uuid.New().String()
	childID := uuid.New().String()

	repo.folders[parentID] = &types.KnowledgeFolder{
		ID: parentID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "parent",
		Path: fmt.Sprintf("/%s/", parentID), Depth: 1,
	}
	repo.folders[childID] = &types.KnowledgeFolder{
		ID: childID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "child",
		ParentFolderID: ptr(parentID), Path: fmt.Sprintf("/%s/%s/", parentID, childID), Depth: 2,
	}

	err := svc.DeleteFolder(ctx, parentID, false)
	assert.ErrorIs(t, err, repository.ErrFolderNotEmpty)
}

// --- MoveFolder ---

func TestMoveFolder_Success(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	srcID := uuid.New().String()
	destID := uuid.New().String()

	repo.folders[srcID] = &types.KnowledgeFolder{
		ID: srcID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "source",
		Path: fmt.Sprintf("/%s/", srcID), Depth: 1,
	}
	repo.folders[destID] = &types.KnowledgeFolder{
		ID: destID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "dest",
		Path: fmt.Sprintf("/%s/", destID), Depth: 1,
	}

	result, err := svc.MoveFolder(ctx, srcID, &types.MoveFolderRequest{TargetParentFolderID: &destID})
	require.NoError(t, err)
	assert.Equal(t, destID, *result.ParentFolderID)
}

func TestMoveFolder_CircularReference(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	parentID := uuid.New().String()
	childID := uuid.New().String()

	repo.folders[parentID] = &types.KnowledgeFolder{
		ID: parentID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "parent",
		Path: fmt.Sprintf("/%s/", parentID), Depth: 1,
	}
	repo.folders[childID] = &types.KnowledgeFolder{
		ID: childID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "child",
		ParentFolderID: ptr(parentID), Path: fmt.Sprintf("/%s/%s/", parentID, childID), Depth: 2,
	}

	_, err := svc.MoveFolder(ctx, parentID, &types.MoveFolderRequest{TargetParentFolderID: &childID})
	assert.ErrorIs(t, err, repository.ErrCircularReference)
}

func TestMoveFolder_MoveToSelf(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	folderID := uuid.New().String()
	repo.folders[folderID] = &types.KnowledgeFolder{
		ID: folderID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "self",
		Path: fmt.Sprintf("/%s/", folderID), Depth: 1,
	}

	_, err := svc.MoveFolder(ctx, folderID, &types.MoveFolderRequest{TargetParentFolderID: &folderID})
	assert.ErrorIs(t, err, repository.ErrCircularReference)
}

func TestMoveFolder_NameConflict(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	srcID := uuid.New().String()
	destID := uuid.New().String()

	repo.folders[srcID] = &types.KnowledgeFolder{
		ID: srcID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "conflict",
		Path: fmt.Sprintf("/%s/", srcID), Depth: 1,
	}
	repo.folders[destID] = &types.KnowledgeFolder{
		ID: destID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "dest",
		Path: fmt.Sprintf("/%s/", destID), Depth: 1,
	}
	repo.folders["existing"] = &types.KnowledgeFolder{
		ID: "existing", TenantID: 1, KnowledgeBaseID: "kb-1", Name: "conflict",
		ParentFolderID: ptr(destID), Path: fmt.Sprintf("/%s/existing/", destID), Depth: 2,
	}

	_, err := svc.MoveFolder(ctx, srcID, &types.MoveFolderRequest{TargetParentFolderID: &destID})
	assert.ErrorIs(t, err, repository.ErrFolderNameExists)
}

// --- GetBreadcrumb ---

func TestGetBreadcrumb_RootFolder(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	folderID := uuid.New().String()
	repo.folders[folderID] = &types.KnowledgeFolder{
		ID: folderID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "root",
		Path: fmt.Sprintf("/%s/", folderID), Depth: 1,
	}

	breadcrumb, err := svc.GetBreadcrumb(ctx, folderID)
	require.NoError(t, err)
	assert.Len(t, breadcrumb, 1)
}

func TestGetBreadcrumb_NestedFolder(t *testing.T) {
	svc, repo := setupServiceTest(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	rootID := uuid.New().String()
	childID := uuid.New().String()

	repo.folders[rootID] = &types.KnowledgeFolder{
		ID: rootID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "root",
		Path: fmt.Sprintf("/%s/", rootID), Depth: 1,
	}
	repo.folders[childID] = &types.KnowledgeFolder{
		ID: childID, TenantID: 1, KnowledgeBaseID: "kb-1", Name: "child",
		ParentFolderID: ptr(rootID), Path: fmt.Sprintf("/%s/%s/", rootID, childID), Depth: 2,
	}

	breadcrumb, err := svc.GetBreadcrumb(ctx, childID)
	require.NoError(t, err)
	assert.Len(t, breadcrumb, 2)
}

// --- buildFolderTree ---

func TestBuildFolderTree_SingleRoot(t *testing.T) {
	folder := &types.KnowledgeFolder{ID: "1", Name: "root", Path: "/1/", Depth: 1}
	result := buildFolderTree([]*types.KnowledgeFolder{folder})
	assert.Len(t, result, 1)
	assert.Empty(t, result[0].Children)
}

func TestBuildFolderTree_OrphansBecomeRoots(t *testing.T) {
	root := &types.KnowledgeFolder{ID: "1", Name: "root", Path: "/1/", Depth: 1}
	orphan := &types.KnowledgeFolder{
		ID: "2", Name: "orphan", ParentFolderID: ptr("nonexistent"), Path: "/1/2/", Depth: 2,
	}
	result := buildFolderTree([]*types.KnowledgeFolder{root, orphan})
	assert.Len(t, result, 2)
}

func TestBuildFolderTree_EmptyParentIDBecomesRoot(t *testing.T) {
	folder := &types.KnowledgeFolder{
		ID: "1", Name: "root", ParentFolderID: ptr(""), Path: "/1/", Depth: 1,
	}
	result := buildFolderTree([]*types.KnowledgeFolder{folder})
	assert.Len(t, result, 1)
}

// --- populateChildCounts ---

func TestPopulateChildCounts_AggregatesRecursively(t *testing.T) {
	root := &types.KnowledgeFolder{
		ID: "root", Name: "root",
		Children: []*types.KnowledgeFolder{
			{ID: "c1", Name: "c1", KnowledgeCount: 2},
			{ID: "c2", Name: "c2", KnowledgeCount: 3},
		},
		KnowledgeCount: 1,
	}
	total := populateChildCounts(root)
	assert.Equal(t, int64(6), total)
	assert.Equal(t, int64(6), root.KnowledgeCount)
}
