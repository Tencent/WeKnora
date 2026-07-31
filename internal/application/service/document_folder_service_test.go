package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFolderRepo is a hand-rolled in-memory fake of
// interfaces.DocumentFolderRepository. It mirrors the production invariants we
// care about (soft-delete via the deleted map, partial unique index via
// child-by-name lookup, tenant scoping on counts) so service-level tests
// exercise real behavior, not a tautological stub.
type fakeFolderRepo struct {
	folders map[string]*types.DocumentFolder // keyed by id
	deleted map[string]bool                  // id -> soft-deleted
	docRows []fakeDocRow                     // knowledge rows for count probes
	failOn  map[string]error                 // method name -> injected error
}

type fakeDocRow struct {
	id, kbID, folderID, status string
	tenantID                   uint64
	deleted                    bool
}

type fakeFolderKnowledgeService struct {
	interfaces.KnowledgeService
	repo             *fakeFolderRepo
	indexedFolderID  string
	indexedKnowledge []string
	deletedKnowledge []string
	deleteTenantID   uint64
	deleteHasTenant  bool
	indexErr         error
	afterIndexUpdate func()
	afterDelete      func()
}

type fakeFolderTaskEnqueuer struct {
	task *asynq.Task
	err  error
}

func (e *fakeFolderTaskEnqueuer) Enqueue(
	task *asynq.Task,
	_ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	if e.err != nil {
		return nil, e.err
	}
	e.task = task
	return &asynq.TaskInfo{ID: "folder-delete-task"}, nil
}

func (s *fakeFolderKnowledgeService) UpdateKnowledgeFolderIndex(
	_ context.Context, _ string, knowledgeIDs []string, folderID string,
) error {
	if s.indexErr != nil {
		return s.indexErr
	}
	s.indexedKnowledge = append([]string(nil), knowledgeIDs...)
	s.indexedFolderID = folderID
	if s.afterIndexUpdate != nil {
		s.afterIndexUpdate()
	}
	return nil
}

func (s *fakeFolderKnowledgeService) DeleteKnowledge(ctx context.Context, id string) error {
	s.deleteTenantID, _ = types.TenantIDFromContext(ctx)
	_, s.deleteHasTenant = types.TenantInfoFromContext(ctx)
	s.deletedKnowledge = append(s.deletedKnowledge, id)
	for i := range s.repo.docRows {
		if s.repo.docRows[i].id == id {
			s.repo.docRows[i].deleted = true
		}
	}
	if s.afterDelete != nil {
		afterDelete := s.afterDelete
		s.afterDelete = nil
		afterDelete()
	}
	return nil
}

func (s *fakeFolderKnowledgeService) DeleteKnowledgeList(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := s.DeleteKnowledge(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

type fakeFolderTenantRepository struct {
	interfaces.TenantRepository
	tenant *types.Tenant
	err    error
}

func (r *fakeFolderTenantRepository) GetTenantByID(_ context.Context, _ uint64) (*types.Tenant, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.tenant, nil
}

func newFakeFolderRepo() *fakeFolderRepo {
	return &fakeFolderRepo{
		folders: make(map[string]*types.DocumentFolder),
		deleted: make(map[string]bool),
		failOn:  make(map[string]error),
	}
}

// compile-time guard — keeps the fake in sync with the interface.
var _ interfaces.DocumentFolderRepository = (*fakeFolderRepo)(nil)

func (r *fakeFolderRepo) maybeFail(method string) error {
	if err, ok := r.failOn[method]; ok {
		return err
	}
	return nil
}

func (r *fakeFolderRepo) CreateFolder(ctx context.Context, folder *types.DocumentFolder) error {
	if err := r.maybeFail("CreateFolder"); err != nil {
		return err
	}
	cp := *folder
	r.folders[folder.ID] = &cp
	return nil
}

func (r *fakeFolderRepo) GetFolderByID(ctx context.Context, kbID, id string) (*types.DocumentFolder, error) {
	if err := r.maybeFail("GetFolderByID"); err != nil {
		return nil, err
	}
	f, ok := r.folders[id]
	if !ok || r.deleted[id] || f.KnowledgeBaseID != kbID {
		return nil, ErrDocumentFolderNotFoundInRepo
	}
	cp := *f
	return &cp, nil
}

func (r *fakeFolderRepo) GetChildFolderByName(ctx context.Context, kbID, parentID, name string) (*types.DocumentFolder, error) {
	if err := r.maybeFail("GetChildFolderByName"); err != nil {
		return nil, err
	}
	for _, f := range r.folders {
		if r.deleted[f.ID] {
			continue
		}
		if f.KnowledgeBaseID == kbID && f.ParentID == parentID && f.Name == name {
			cp := *f
			return &cp, nil
		}
	}
	return nil, ErrDocumentFolderNotFoundInRepo
}

func (r *fakeFolderRepo) ListChildFolders(
	ctx context.Context,
	kbID string,
	parentID string,
	keyword string,
	after *types.DocumentFolderPageCursor,
	limit int,
) ([]*types.DocumentFolder, bool, error) {
	if err := r.maybeFail("ListChildFolders"); err != nil {
		return nil, false, err
	}
	var out []*types.DocumentFolder
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	for _, f := range r.folders {
		if r.deleted[f.ID] {
			continue
		}
		if f.KnowledgeBaseID == kbID && f.ParentID == parentID {
			if keyword != "" &&
				!strings.Contains(strings.ToLower(f.Name), keyword) &&
				!strings.Contains(strings.ToLower(f.Path), keyword) {
				continue
			}
			cp := *f
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	if after != nil {
		first := 0
		for first < len(out) {
			folder := out[first]
			if folder.Name > after.Name ||
				(folder.Name == after.Name && folder.ID > after.ID) {
				break
			}
			first++
		}
		out = out[first:]
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

func (r *fakeFolderRepo) ListAllFolders(ctx context.Context, kbID string) ([]*types.DocumentFolder, error) {
	if err := r.maybeFail("ListAllFolders"); err != nil {
		return nil, err
	}
	var out []*types.DocumentFolder
	for _, f := range r.folders {
		if r.deleted[f.ID] {
			continue
		}
		if f.KnowledgeBaseID == kbID {
			cp := *f
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeFolderRepo) UpdateFolder(ctx context.Context, folder *types.DocumentFolder) error {
	if err := r.maybeFail("UpdateFolder"); err != nil {
		return err
	}
	if _, ok := r.folders[folder.ID]; !ok || r.deleted[folder.ID] {
		return ErrDocumentFolderNotFoundInRepo
	}
	cp := *folder
	r.folders[folder.ID] = &cp
	return nil
}

func (r *fakeFolderRepo) UpdateFoldersInTransaction(
	ctx context.Context,
	_ string,
	fn func(txFolderRepo interfaces.DocumentFolderRepository) error,
) error {
	// The fake has no real transaction semantics — it snapshots the state so
	// a failed fn can "roll back" by restoring. This is enough for service-
	// level tests that check atomicity at the outcome level.
	snapshotFolders := make(map[string]*types.DocumentFolder, len(r.folders))
	snapshotDeleted := make(map[string]bool, len(r.deleted))
	snapshotDocs := append([]fakeDocRow(nil), r.docRows...)
	for k, v := range r.folders {
		cp := *v
		snapshotFolders[k] = &cp
	}
	for k, v := range r.deleted {
		snapshotDeleted[k] = v
	}
	// The tx repo shares the fake's maps — mutations inside fn are visible.
	if err := fn(r); err != nil {
		// Roll back.
		r.folders = snapshotFolders
		r.deleted = snapshotDeleted
		r.docRows = snapshotDocs
		return err
	}
	return nil
}

func (r *fakeFolderRepo) DeleteFolder(ctx context.Context, kbID, id string) error {
	if err := r.maybeFail("DeleteFolder"); err != nil {
		return err
	}
	if _, ok := r.folders[id]; !ok || r.deleted[id] {
		return ErrDocumentFolderNotFoundInRepo
	}
	r.deleted[id] = true
	return nil
}

func (r *fakeFolderRepo) DeleteFoldersByKnowledgeBase(ctx context.Context, kbID string) error {
	if err := r.maybeFail("DeleteFoldersByKnowledgeBase"); err != nil {
		return err
	}
	for id, f := range r.folders {
		if f.KnowledgeBaseID == kbID {
			r.deleted[id] = true
		}
	}
	return nil
}

func (r *fakeFolderRepo) HasChildFolders(ctx context.Context, kbID, parentID string) (bool, error) {
	if err := r.maybeFail("HasChildFolders"); err != nil {
		return false, err
	}
	for _, f := range r.folders {
		if r.deleted[f.ID] {
			continue
		}
		if f.KnowledgeBaseID == kbID && f.ParentID == parentID {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeFolderRepo) HasChildFoldersBatch(
	ctx context.Context,
	kbID string,
	parentIDs []string,
) (map[string]bool, error) {
	if err := r.maybeFail("HasChildFoldersBatch"); err != nil {
		return nil, err
	}
	parentSet := make(map[string]bool, len(parentIDs))
	for _, parentID := range parentIDs {
		parentSet[parentID] = true
	}
	out := make(map[string]bool, len(parentIDs))
	for _, f := range r.folders {
		if !r.deleted[f.ID] && f.KnowledgeBaseID == kbID && parentSet[f.ParentID] {
			out[f.ParentID] = true
		}
	}
	return out, nil
}

func (r *fakeFolderRepo) CountDocumentsInFolders(ctx context.Context, tenantID uint64, kbID string, folderIDs []string) (map[string]int64, error) {
	if err := r.maybeFail("CountDocumentsInFolders"); err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(folderIDs))
	for _, id := range folderIDs {
		want[id] = true
	}
	out := make(map[string]int64)
	for _, d := range r.docRows {
		if d.deleted || d.tenantID != tenantID || d.kbID != kbID || !want[d.folderID] {
			continue
		}
		out[d.folderID]++
	}
	return out, nil
}

func (r *fakeFolderRepo) CountAllFolders(ctx context.Context, kbID string) (int64, error) {
	if err := r.maybeFail("CountAllFolders"); err != nil {
		return 0, err
	}
	var n int64
	for _, f := range r.folders {
		if r.deleted[f.ID] {
			continue
		}
		if f.KnowledgeBaseID == kbID {
			n++
		}
	}
	return n, nil
}

func (r *fakeFolderRepo) HasDocumentsInSubtree(ctx context.Context, tenantID uint64, kbID string, subtreeIDs []string) (bool, error) {
	if err := r.maybeFail("HasDocumentsInSubtree"); err != nil {
		return false, err
	}
	want := make(map[string]bool, len(subtreeIDs))
	for _, id := range subtreeIDs {
		want[id] = true
	}
	for _, d := range r.docRows {
		if d.deleted || d.tenantID != tenantID || d.kbID != kbID || !want[d.folderID] {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (r *fakeFolderRepo) ListKnowledgeInFolders(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
) ([]*types.Knowledge, error) {
	if err := r.maybeFail("ListKnowledgeInFolders"); err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(folderIDs))
	for _, id := range folderIDs {
		wanted[id] = true
	}
	var knowledges []*types.Knowledge
	for _, row := range r.docRows {
		if row.deleted || row.tenantID != tenantID || row.kbID != kbID || !wanted[row.folderID] {
			continue
		}
		knowledges = append(knowledges, &types.Knowledge{
			ID:              row.id,
			TenantID:        row.tenantID,
			KnowledgeBaseID: row.kbID,
			FolderID:        row.folderID,
			ParseStatus:     row.status,
		})
	}
	return knowledges, nil
}

func (r *fakeFolderRepo) SetKnowledgeFolderID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	knowledgeIDs []string,
	folderID string,
) (int64, error) {
	if err := r.maybeFail("SetKnowledgeFolderID"); err != nil {
		return 0, err
	}
	wanted := make(map[string]bool, len(knowledgeIDs))
	for _, id := range knowledgeIDs {
		wanted[id] = true
	}
	var affected int64
	for index := range r.docRows {
		row := &r.docRows[index]
		if row.deleted || row.tenantID != tenantID || row.kbID != kbID || !wanted[row.id] {
			continue
		}
		row.folderID = folderID
		affected++
	}
	return affected, nil
}

func (r *fakeFolderRepo) SearchFoldersInScopes(
	ctx context.Context,
	scopes []types.KnowledgeSearchScope,
	keyword string,
	offset int,
	limit int,
) ([]*types.DocumentFolderSearchResult, bool, int64, error) {
	if err := r.maybeFail("SearchFoldersInScopes"); err != nil {
		return nil, false, 0, err
	}
	allowed := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		allowed[fmt.Sprintf("%d/%s", scope.TenantID, scope.KBID)] = true
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	var all []*types.DocumentFolderSearchResult
	for _, folder := range r.folders {
		if r.deleted[folder.ID] ||
			!allowed[fmt.Sprintf("%d/%s", folder.TenantID, folder.KnowledgeBaseID)] {
			continue
		}
		if keyword != "" &&
			!strings.Contains(strings.ToLower(folder.Name), keyword) {
			continue
		}
		all = append(all, &types.DocumentFolderSearchResult{
			ID:              folder.ID,
			Name:            folder.Name,
			Path:            folder.Path,
			ParentID:        folder.ParentID,
			KnowledgeBaseID: folder.KnowledgeBaseID,
		})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].ID < all[j].ID
	})
	total := int64(len(all))
	if offset > len(all) {
		offset = len(all)
	}
	all = all[offset:]
	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}
	return all, hasMore, total, nil
}

// ErrDocumentFolderNotFoundInRepo aliases the repository's not-found sentinel
// so the fake can return the exact value production code matches against. We
// import the real sentinel (not redefine it) to keep errors.Is chains honest
// — a hand-rolled stand-in would let the service code compile against a
// phantom error and miss real wiring bugs.
var ErrDocumentFolderNotFoundInRepo = repository.ErrDocumentFolderNotFound

// addFolder is a test helper that seeds a folder into the fake.
func (r *fakeFolderRepo) addFolder(kbID, id, parentID, name, path string, depth int) *types.DocumentFolder {
	f := &types.DocumentFolder{
		ID: id, TenantID: 1, KnowledgeBaseID: kbID,
		ParentID: parentID, Name: name, Path: path, Depth: depth,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	r.folders[id] = f
	return f
}

func (r *fakeFolderRepo) addDoc(id, kbID, folderID, status string) {
	r.docRows = append(r.docRows, fakeDocRow{
		id: id, kbID: kbID, folderID: folderID, status: status, tenantID: 1,
	})
}

// ---- Service tests ----

func newFolderService(repo interfaces.DocumentFolderRepository) interfaces.DocumentFolderService {
	return NewDocumentFolderService(repo, nil, nil, nil)
}

func TestDocumentFolderService_CreateFolder_AtRoot(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)

	folder, err := svc.CreateFolder(context.Background(), "kb-1", 1, "", "Alpha")
	require.NoError(t, err)
	assert.Equal(t, "Alpha", folder.Name)
	assert.Equal(t, "", folder.ParentID)
	assert.Equal(t, 1, folder.Depth)
	assert.Equal(t, "Alpha", folder.Path)

	// Persisted
	got, err := repo.GetFolderByID(context.Background(), "kb-1", folder.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alpha", got.Name)
}

func TestDocumentFolderService_CreateFolder_Nested(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	parent := repo.addFolder("kb-1", "p", "", "Parent", "Parent", 1)

	folder, err := svc.CreateFolder(context.Background(), "kb-1", 1, parent.ID, "Child")
	require.NoError(t, err)
	assert.Equal(t, parent.ID, folder.ParentID)
	assert.Equal(t, 2, folder.Depth)
	assert.Equal(t, "Parent/Child", folder.Path)
}

func TestDocumentFolderService_CreateFolder_DuplicateNameRejected(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "f-1", "", "Alpha", "Alpha", 1)

	_, err := svc.CreateFolder(context.Background(), "kb-1", 1, "", "Alpha")
	assert.ErrorIs(t, err, ErrFolderConflict)
}

func TestDocumentFolderService_CreateFolder_InvalidName(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)

	cases := []string{"", "   ", "a/b", "a｜b", "a／b"}
	for _, name := range cases {
		_, err := svc.CreateFolder(context.Background(), "kb-1", 1, "", name)
		assert.ErrorIs(t, err, ErrFolderNameInvalid, "name=%q should be rejected", name)
	}
}

func TestDocumentFolderService_CreateFolder_DepthExceeded(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)

	// Build a chain at MaxFolderDepth so the next create must fail.
	prevID := ""
	prevPath := ""
	for d := 1; d <= types.MaxFolderDepth; d++ {
		name := "L" + itoa(d)
		path := name
		if prevPath != "" {
			path = prevPath + "/" + name
		}
		cur := repo.addFolder("kb-1", "id-"+itoa(d), prevID, name, path, d)
		prevID = cur.ID
		prevPath = path
	}

	_, err := svc.CreateFolder(context.Background(), "kb-1", 1, prevID, "Too Deep")
	assert.ErrorIs(t, err, ErrFolderDepthExceeded)
}

func TestDocumentFolderService_CreateFolder_ParentMissing(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)

	_, err := svc.CreateFolder(context.Background(), "kb-1", 1, "missing-parent", "Orphan")
	assert.ErrorIs(t, err, repository.ErrDocumentFolderNotFound)
}

func TestDocumentFolderService_CreateFolder_LimitExceeded(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	for i := 0; i < types.MaxFoldersPerKB; i++ {
		repo.addFolder("kb-1", "f-"+itoa(i), "", "F"+itoa(i), "F"+itoa(i), 1)
	}
	_, err := svc.CreateFolder(context.Background(), "kb-1", 1, "", "One Too Many")
	assert.ErrorIs(t, err, ErrFolderLimitExceeded)
}

func TestDocumentFolderService_RenameFolder(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "f-1", "", "Old", "Old", 1)

	got, err := svc.RenameFolder(context.Background(), "kb-1", "f-1", "New")
	require.NoError(t, err)
	assert.Equal(t, "New", got.Name)
	assert.Equal(t, "New", got.Path)
}

func TestDocumentFolderService_RenameFolder_UpdatesSubtreePathsOnly(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "root", "", "root", "root", 1)
	repo.addFolder("kb-1", "a", "root", "a", "root/a", 2)
	repo.addFolder("kb-1", "a1", "a", "a1", "root/a/a1", 3)
	repo.addFolder("kb-1", "a2", "a", "a2", "root/a/a2", 3)
	repo.addFolder("kb-1", "b", "root", "b", "root/b", 2)

	_, err := svc.RenameFolder(context.Background(), "kb-1", "a", "renamed")
	require.NoError(t, err)

	a, _ := repo.GetFolderByID(context.Background(), "kb-1", "a")
	assert.Equal(t, "root", a.ParentID)
	assert.Equal(t, "root/renamed", a.Path)
	assert.Equal(t, 2, a.Depth)

	a1, _ := repo.GetFolderByID(context.Background(), "kb-1", "a1")
	assert.Equal(t, "a", a1.ParentID)
	assert.Equal(t, "root/renamed/a1", a1.Path)
	assert.Equal(t, 3, a1.Depth)

	a2, _ := repo.GetFolderByID(context.Background(), "kb-1", "a2")
	assert.Equal(t, "root/renamed/a2", a2.Path)

	b, _ := repo.GetFolderByID(context.Background(), "kb-1", "b")
	assert.Equal(t, "root/b", b.Path)
}

func TestDocumentFolderService_RenameFolder_RejectsSubtreePathOverflowAtomically(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	root := repo.addFolder("kb-1", "root", "", "a", "a", 1)
	childPath := "a/" + strings.Repeat("x", types.MaxFolderPathLen-2)
	child := repo.addFolder("kb-1", "child", root.ID, "child", childPath, 2)

	_, err := svc.RenameFolder(context.Background(), "kb-1", root.ID, "renamed")
	assert.ErrorIs(t, err, ErrFolderDepthExceeded)

	gotRoot, getErr := repo.GetFolderByID(context.Background(), "kb-1", root.ID)
	require.NoError(t, getErr)
	assert.Equal(t, "a", gotRoot.Name)
	assert.Equal(t, "a", gotRoot.Path)

	gotChild, getErr := repo.GetFolderByID(context.Background(), "kb-1", child.ID)
	require.NoError(t, getErr)
	assert.Equal(t, childPath, gotChild.Path)
}

func TestDocumentFolderService_DeleteFolder_Empty(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "f-1", "", "X", "X", 1)

	require.NoError(t, svc.DeleteFolder(context.Background(), "kb-1", "f-1"))

	// Now soft-deleted
	_, err := repo.GetFolderByID(context.Background(), "kb-1", "f-1")
	assert.ErrorIs(t, err, ErrDocumentFolderNotFoundInRepo)
}

func TestDocumentFolderService_DeleteFolder_HasChildren(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "p", "", "P", "P", 1)
	repo.addFolder("kb-1", "c", "p", "C", "P/C", 2)

	err := svc.DeleteFolder(context.Background(), "kb-1", "p")
	assert.ErrorIs(t, err, ErrFolderNotEmpty)
}

func TestDocumentFolderService_DeleteFolder_HasDocuments(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "f-1", "", "X", "X", 1)
	repo.addDoc("k-1", "kb-1", "f-1", types.ParseStatusCompleted)

	err := svc.DeleteFolder(context.Background(), "kb-1", "f-1")
	assert.ErrorIs(t, err, ErrFolderNotEmpty)
}

func TestDocumentFolderService_DeleteFolder_SubtreeHasDocuments(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "p", "", "P", "P", 1)
	repo.addFolder("kb-1", "c", "p", "C", "P/C", 2)
	// Document filed under the child folder — "p" looks empty directly but
	// the subtree has a doc.
	repo.addDoc("k-1", "kb-1", "c", types.ParseStatusCompleted)

	err := svc.DeleteFolder(context.Background(), "kb-1", "p")
	assert.ErrorIs(t, err, ErrFolderNotEmpty)
}

func TestDocumentFolderService_DeleteFolder_NotFound(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	err := svc.DeleteFolder(context.Background(), "kb-1", "missing")
	assert.ErrorIs(t, err, repository.ErrDocumentFolderNotFound)
}

func TestDocumentFolderService_GetDeleteImpactCountsEntireSubtree(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "parent", "", "Parent", "Parent", 1)
	repo.addFolder("kb-1", "child", "parent", "Child", "Parent/Child", 2)
	repo.addFolder("kb-1", "sibling", "", "Sibling", "Sibling", 1)
	repo.addDoc("completed", "kb-1", "parent", types.ParseStatusCompleted)
	repo.addDoc("pending", "kb-1", "child", types.ParseStatusPending)
	repo.addDoc("finalizing", "kb-1", "child", types.ParseStatusFinalizing)
	repo.addDoc("outside", "kb-1", "sibling", types.ParseStatusCompleted)

	impact, err := svc.GetDeleteImpact(context.Background(), "kb-1", 1, "parent")

	require.NoError(t, err)
	assert.Equal(t, 2, impact.FolderCount)
	assert.Equal(t, 3, impact.DocumentCount)
	assert.Equal(t, 2, impact.ActiveDocumentCount)
}

func TestDocumentFolderService_DeleteFolderTreeKeepsDocumentsAtRoot(t *testing.T) {
	repo := newFakeFolderRepo()
	knowledgeService := &fakeFolderKnowledgeService{repo: repo}
	svc := NewDocumentFolderService(repo, knowledgeService, nil, nil)
	repo.addFolder("kb-1", "parent", "", "Parent", "Parent", 1)
	repo.addFolder("kb-1", "child", "parent", "Child", "Parent/Child", 2)
	repo.addDoc("parent-doc", "kb-1", "parent", types.ParseStatusCompleted)
	repo.addDoc("child-doc", "kb-1", "child", types.ParseStatusCompleted)

	err := svc.DeleteFolderTree(
		context.Background(), "kb-1", 1, "parent", types.DocumentFolderDeleteModeKeepDocuments,
	)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"parent-doc", "child-doc"}, knowledgeService.indexedKnowledge)
	assert.Equal(t, types.DocumentFolderRootID, knowledgeService.indexedFolderID)
	assert.Empty(t, knowledgeService.deletedKnowledge)
	for _, row := range repo.docRows {
		assert.False(t, row.deleted)
		assert.Equal(t, types.DocumentFolderRootID, row.folderID)
	}
	_, err = repo.GetFolderByID(context.Background(), "kb-1", "parent")
	assert.ErrorIs(t, err, repository.ErrDocumentFolderNotFound)
	_, err = repo.GetFolderByID(context.Background(), "kb-1", "child")
	assert.ErrorIs(t, err, repository.ErrDocumentFolderNotFound)
}

func TestDocumentFolderService_DeleteFolderTreeKeepDocumentsRejectsActiveParsing(t *testing.T) {
	repo := newFakeFolderRepo()
	knowledgeService := &fakeFolderKnowledgeService{repo: repo}
	svc := NewDocumentFolderService(repo, knowledgeService, nil, nil)
	repo.addFolder("kb-1", "parent", "", "Parent", "Parent", 1)
	repo.addDoc("active-doc", "kb-1", "parent", types.ParseStatusProcessing)

	err := svc.DeleteFolderTree(
		context.Background(), "kb-1", 1, "parent", types.DocumentFolderDeleteModeKeepDocuments,
	)

	assert.ErrorIs(t, err, ErrFolderDocumentsProcessing)
	assert.Empty(t, knowledgeService.indexedKnowledge)
	assert.False(t, repo.docRows[0].deleted)
	assert.Equal(t, "parent", repo.docRows[0].folderID)
	_, err = repo.GetFolderByID(context.Background(), "kb-1", "parent")
	assert.NoError(t, err)
}

func TestDocumentFolderService_DeleteFolderTreeKeepDocumentsRejectsConcurrentChanges(t *testing.T) {
	repo := newFakeFolderRepo()
	knowledgeService := &fakeFolderKnowledgeService{repo: repo}
	svc := NewDocumentFolderService(repo, knowledgeService, nil, nil)
	repo.addFolder("kb-1", "parent", "", "Parent", "Parent", 1)
	repo.addDoc("planned-doc", "kb-1", "parent", types.ParseStatusCompleted)
	knowledgeService.afterIndexUpdate = func() {
		repo.addDoc("late-doc", "kb-1", "parent", types.ParseStatusCompleted)
	}

	err := svc.DeleteFolderTree(
		context.Background(), "kb-1", 1, "parent", types.DocumentFolderDeleteModeKeepDocuments,
	)

	assert.ErrorIs(t, err, ErrFolderChangedDuringDelete)
	require.Len(t, repo.docRows, 2)
	for _, row := range repo.docRows {
		assert.False(t, row.deleted)
		assert.Equal(t, "parent", row.folderID)
	}
	_, err = repo.GetFolderByID(context.Background(), "kb-1", "parent")
	assert.NoError(t, err)
}

func TestDocumentFolderService_DeleteFolderTreeDeletesDocumentsAndFolders(t *testing.T) {
	repo := newFakeFolderRepo()
	knowledgeService := &fakeFolderKnowledgeService{repo: repo}
	svc := NewDocumentFolderService(repo, knowledgeService, nil, nil)
	repo.addFolder("kb-1", "parent", "", "Parent", "Parent", 1)
	repo.addFolder("kb-1", "child", "parent", "Child", "Parent/Child", 2)
	repo.addDoc("parent-doc", "kb-1", "parent", types.ParseStatusCompleted)
	repo.addDoc("active-child-doc", "kb-1", "child", types.ParseStatusProcessing)

	err := svc.DeleteFolderTree(
		context.Background(), "kb-1", 1, "parent", types.DocumentFolderDeleteModeDeleteAll,
	)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"parent-doc", "active-child-doc"}, knowledgeService.deletedKnowledge)
	assert.Empty(t, knowledgeService.indexedKnowledge)
	for _, row := range repo.docRows {
		assert.True(t, row.deleted)
	}
	_, err = repo.GetFolderByID(context.Background(), "kb-1", "parent")
	assert.ErrorIs(t, err, repository.ErrDocumentFolderNotFound)
	_, err = repo.GetFolderByID(context.Background(), "kb-1", "child")
	assert.ErrorIs(t, err, repository.ErrDocumentFolderNotFound)
}

func TestDocumentFolderService_DeleteFolderTreeDeleteAllRetriesConcurrentUpload(t *testing.T) {
	repo := newFakeFolderRepo()
	knowledgeService := &fakeFolderKnowledgeService{repo: repo}
	svc := NewDocumentFolderService(repo, knowledgeService, nil, nil)
	repo.addFolder("kb-1", "parent", "", "Parent", "Parent", 1)
	repo.addDoc("planned-doc", "kb-1", "parent", types.ParseStatusCompleted)
	knowledgeService.afterDelete = func() {
		repo.addDoc("late-doc", "kb-1", "parent", types.ParseStatusCompleted)
	}

	err := svc.DeleteFolderTree(
		context.Background(), "kb-1", 1, "parent", types.DocumentFolderDeleteModeDeleteAll,
	)

	assert.ErrorIs(t, err, ErrFolderChangedDuringDelete)
	_, err = repo.GetFolderByID(context.Background(), "kb-1", "parent")
	assert.NoError(t, err)
	require.Len(t, repo.docRows, 2)
	assert.True(t, repo.docRows[0].deleted)
	assert.False(t, repo.docRows[1].deleted)
}

func TestDocumentFolderService_SubmitDeleteFolderTreeEnqueuesExplicitMode(t *testing.T) {
	repo := newFakeFolderRepo()
	queue := &fakeFolderTaskEnqueuer{}
	svc := NewDocumentFolderService(repo, &fakeFolderKnowledgeService{repo: repo}, nil, queue)
	repo.addFolder("kb-1", "parent", "", "Parent", "Parent", 1)
	repo.addDoc("doc-1", "kb-1", "parent", types.ParseStatusCompleted)

	taskID, err := svc.SubmitDeleteFolderTree(
		context.Background(), "kb-1", 1, "parent", types.DocumentFolderDeleteModeKeepDocuments,
	)

	require.NoError(t, err)
	assert.Equal(t, "folder-delete-task", taskID)
	require.NotNil(t, queue.task)
	assert.Equal(t, types.TypeDocumentFolderDelete, queue.task.Type())
	var payload types.DocumentFolderDeletePayload
	require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
	assert.Equal(t, uint64(1), payload.TenantID)
	assert.Equal(t, "kb-1", payload.KnowledgeBaseID)
	assert.Equal(t, "parent", payload.FolderID)
	assert.Equal(t, types.DocumentFolderDeleteModeKeepDocuments, payload.Mode)
}

func TestDocumentFolderService_ProcessDeleteFolderTreeRestoresTenantContext(t *testing.T) {
	repo := newFakeFolderRepo()
	knowledgeService := &fakeFolderKnowledgeService{repo: repo}
	tenantRepo := &fakeFolderTenantRepository{tenant: &types.Tenant{ID: 1, Name: "Tenant"}}
	svc := NewDocumentFolderService(repo, knowledgeService, tenantRepo, nil)
	repo.addFolder("kb-1", "parent", "", "Parent", "Parent", 1)
	repo.addDoc("doc-1", "kb-1", "parent", types.ParseStatusCompleted)
	payload, err := json.Marshal(types.DocumentFolderDeletePayload{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		FolderID:        "parent",
		Mode:            types.DocumentFolderDeleteModeDeleteAll,
	})
	require.NoError(t, err)

	err = svc.ProcessDeleteFolderTree(
		context.Background(), asynq.NewTask(types.TypeDocumentFolderDelete, payload),
	)

	require.NoError(t, err)
	assert.Equal(t, uint64(1), knowledgeService.deleteTenantID)
	assert.True(t, knowledgeService.deleteHasTenant)
	_, err = repo.GetFolderByID(context.Background(), "kb-1", "parent")
	assert.ErrorIs(t, err, repository.ErrDocumentFolderNotFound)
}

func TestDocumentFolderService_ProcessDeleteFolderTreeTreatsCompletedReplayAsSuccess(t *testing.T) {
	repo := newFakeFolderRepo()
	tenantRepo := &fakeFolderTenantRepository{tenant: &types.Tenant{ID: 1, Name: "Tenant"}}
	svc := NewDocumentFolderService(
		repo,
		&fakeFolderKnowledgeService{repo: repo},
		tenantRepo,
		nil,
	)
	payload, err := json.Marshal(types.DocumentFolderDeletePayload{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		FolderID:        "already-deleted",
		Mode:            types.DocumentFolderDeleteModeDeleteAll,
	})
	require.NoError(t, err)

	err = svc.ProcessDeleteFolderTree(
		context.Background(),
		asynq.NewTask(types.TypeDocumentFolderDelete, payload),
	)

	require.NoError(t, err)
}

func TestDocumentFolderService_ResolveSubtree(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "root", "", "root", "root", 1)
	repo.addFolder("kb-1", "a", "root", "a", "root/a", 2)
	repo.addFolder("kb-1", "a1", "a", "a1", "root/a/a1", 3)
	repo.addFolder("kb-1", "b", "root", "b", "root/b", 2)

	ids, err := svc.ResolveSubtreeFolderIDs(context.Background(), "kb-1", "a")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "a1"}, ids)
}

func TestDocumentFolderService_ValidateFolderExistsForUpload(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "f-1", "", "F", "F", 1)

	// Root is always valid
	assert.NoError(t, svc.ValidateFolderExistsForUpload(context.Background(), "kb-1", ""))
	// Existing folder
	assert.NoError(t, svc.ValidateFolderExistsForUpload(context.Background(), "kb-1", "f-1"))
	// Missing folder
	err := svc.ValidateFolderExistsForUpload(context.Background(), "kb-1", "missing")
	assert.ErrorIs(t, err, repository.ErrDocumentFolderNotFound)
}

func TestDocumentFolderService_ListFolders(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "f-1", "", "Alpha", "Alpha", 1)
	repo.addFolder("kb-1", "f-2", "", "Beta", "Beta", 1)
	repo.addFolder("kb-1", "c-1", "f-1", "Child", "Alpha/Child", 2)
	repo.addDoc("k-1", "kb-1", "f-1", types.ParseStatusCompleted)
	repo.addDoc("k-2", "kb-1", "f-1", types.ParseStatusCompleted)

	resp, err := svc.ListFolders(context.Background(), "kb-1", 1, "", "", "", 50)
	require.NoError(t, err)
	assert.Equal(t, "", resp.ParentID)
	require.Len(t, resp.Folders, 2)
	// Sort by name ASC
	assert.Equal(t, "Alpha", resp.Folders[0].Name)
	assert.Equal(t, "Beta", resp.Folders[1].Name)
	// Alpha has 2 docs + 1 child
	assert.Equal(t, int64(2), resp.Folders[0].DocumentCount)
	assert.True(t, resp.Folders[0].HasChildren)
	// Beta has neither
	assert.Equal(t, int64(0), resp.Folders[1].DocumentCount)
	assert.False(t, resp.Folders[1].HasChildren)
}

func TestDocumentFolderService_ListFolders_PaginatesWithCursor(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "f-1", "", "Alpha", "Alpha", 1)
	repo.addFolder("kb-1", "f-2", "", "Beta", "Beta", 1)
	repo.addFolder("kb-1", "f-3", "", "Gamma", "Gamma", 1)

	first, err := svc.ListFolders(context.Background(), "kb-1", 1, "", "", "", 2)
	require.NoError(t, err)
	require.True(t, first.HasMore)
	assert.NotEmpty(t, first.NextCursor)
	require.Len(t, first.Folders, 2)

	second, err := svc.ListFolders(
		context.Background(),
		"kb-1",
		1,
		"",
		"",
		first.NextCursor,
		2,
	)
	require.NoError(t, err)
	assert.False(t, second.HasMore)
	assert.Empty(t, second.NextCursor)
	require.Len(t, second.Folders, 1)
	assert.Equal(t, "Gamma", second.Folders[0].Name)
}

func TestDocumentFolderService_ListFolders_KeysetCursorSurvivesEarlierMutation(t *testing.T) {
	repo := newFakeFolderRepo()
	svc := newFolderService(repo)
	repo.addFolder("kb-1", "f-1", "", "Alpha", "Alpha", 1)
	repo.addFolder("kb-1", "f-2", "", "Beta", "Beta", 1)
	repo.addFolder("kb-1", "f-3", "", "Gamma", "Gamma", 1)

	first, err := svc.ListFolders(context.Background(), "kb-1", 1, "", "", "", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"Alpha", "Beta"}, []string{
		first.Folders[0].Name,
		first.Folders[1].Name,
	})

	// An insert and delete before the cursor must not shift Gamma out of page 2.
	repo.addFolder("kb-1", "f-0", "", "Aardvark", "Aardvark", 1)
	repo.deleted["f-1"] = true

	second, err := svc.ListFolders(
		context.Background(),
		"kb-1",
		1,
		"",
		"",
		first.NextCursor,
		2,
	)
	require.NoError(t, err)
	require.Len(t, second.Folders, 1)
	assert.Equal(t, "Gamma", second.Folders[0].Name)
}

func TestDocumentFolderService_ListFolders_RejectsInvalidCursor(t *testing.T) {
	svc := newFolderService(newFakeFolderRepo())

	_, err := svc.ListFolders(context.Background(), "kb-1", 1, "", "", "not-a-cursor", 20)
	assert.ErrorIs(t, err, ErrFolderCursorInvalid)
}

// itoa avoids importing strconv for a tiny test helper.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// ensure uuid import is used by later test additions
var _ = uuid.New
