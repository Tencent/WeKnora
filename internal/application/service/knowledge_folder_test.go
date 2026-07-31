package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const knowledgeFolderServiceTestDDL = `
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
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(2048) NOT NULL,
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

func setupKnowledgeFolderServiceTest(
	t *testing.T,
) (interfaces.KnowledgeFolderService, interfaces.KnowledgeFolderRepository, *gorm.DB, context.Context) {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.Exec(knowledgeFolderServiceTestDDL).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_bases (id, tenant_id) VALUES
			('kb-1', 1),
			('kb-2', 1),
			('kb-tenant-2', 2)
	`).Error)
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := repository.NewKnowledgeFolderRepository(db)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	return NewKnowledgeFolderService(repo), repo, db, ctx
}

func knowledgeFolderServiceFixture(
	id string,
	tenantID uint64,
	kbID string,
	parentID string,
	name string,
	path string,
	depth int,
) *types.KnowledgeFolder {
	return &types.KnowledgeFolder{
		ID:              knowledgeFolderServiceTestID(id),
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		ParentID:        knowledgeFolderServiceTestID(parentID),
		Name:            name,
		Path:            knowledgeFolderServiceTestPath(path),
		Depth:           depth,
	}
}

func rawKnowledgeFolderServiceFixture(
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

func knowledgeFolderServiceTestID(id string) string {
	if id == "" {
		return ""
	}
	if parsed, err := uuid.Parse(id); err == nil && parsed.String() == id {
		return id
	}
	namespace := uuid.MustParse("99ee688b-8aa4-4fa7-a803-021c7f4dad13")
	return uuid.NewSHA1(namespace, []byte(id)).String()
}

func knowledgeFolderServiceTestPath(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if segment != "" {
			segments[index] = knowledgeFolderServiceTestID(segment)
		}
	}
	return strings.Join(segments, "/")
}

func insertKnowledgeFolderServiceFixtures(
	t *testing.T,
	db *gorm.DB,
	folders ...*types.KnowledgeFolder,
) {
	t.Helper()
	for _, folder := range folders {
		require.NoError(t, db.Create(folder).Error)
	}
}

func TestNormalizeKnowledgeFolderName(t *testing.T) {
	valid255 := strings.Repeat("界", 255)
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "trim", input: "  报告  ", want: "报告"},
		{name: "unicode", input: "研发资料", want: "研发资料"},
		{name: "255 code points", input: valid255, want: valid255},
		{name: "empty", input: " \t ", wantErr: true},
		{name: "dot", input: ".", wantErr: true},
		{name: "dot dot", input: "..", wantErr: true},
		{name: "slash", input: "a/b", wantErr: true},
		{name: "backslash", input: `a\b`, wantErr: true},
		{name: "nul", input: "a\x00b", wantErr: true},
		{name: "control", input: "a\u0085b", wantErr: true},
		{name: "256 code points", input: strings.Repeat("界", 256), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeKnowledgeFolderName(tt.input)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrKnowledgeFolderInvalidName)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeKnowledgeFolderID(t *testing.T) {
	canonical := "7b5584d0-73f5-4fd6-8f2a-efdb2a9a7641"
	tests := []struct {
		name      string
		input     string
		allowRoot bool
		want      string
		wantErr   bool
	}{
		{name: "canonical", input: canonical, want: canonical},
		{name: "trim canonical", input: " \t" + canonical + "\r\n", want: canonical},
		{name: "root allowed", input: "", allowRoot: true, want: types.KnowledgeFolderRootID},
		{name: "root rejected", input: "", wantErr: true},
		{name: "malformed", input: "not-a-uuid", wantErr: true},
		{name: "uppercase", input: strings.ToUpper(canonical), wantErr: true},
		{name: "without dashes", input: strings.ReplaceAll(canonical, "-", ""), wantErr: true},
		{name: "braced", input: "{" + canonical + "}", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeKnowledgeFolderID(tt.input, "folder_id", tt.allowRoot)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrKnowledgeFolderInvalidArgument)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

type knowledgeFolderIDValidationRepository struct {
	interfaces.KnowledgeFolderRepository
	getByIDCalls         int
	listByParentCalls    int
	listSubtreeCalls     int
	treeTransactionCalls int
}

func (r *knowledgeFolderIDValidationRepository) GetByID(
	context.Context,
	uint64,
	string,
	string,
) (*types.KnowledgeFolder, error) {
	r.getByIDCalls++
	return nil, repository.ErrKnowledgeFolderNotFound
}

func (r *knowledgeFolderIDValidationRepository) ListByParent(
	context.Context,
	uint64,
	string,
	string,
	*types.Pagination,
) ([]*types.KnowledgeFolder, int64, error) {
	r.listByParentCalls++
	return []*types.KnowledgeFolder{}, 0, nil
}

func (r *knowledgeFolderIDValidationRepository) ListSubtreeFolders(
	context.Context,
	uint64,
	string,
	string,
	string,
) ([]*types.KnowledgeFolder, error) {
	r.listSubtreeCalls++
	return []*types.KnowledgeFolder{}, nil
}

func (r *knowledgeFolderIDValidationRepository) RunTreeWriteTransaction(
	context.Context,
	uint64,
	string,
	interfaces.KnowledgeFolderTreeWriteFunc,
) error {
	r.treeTransactionCalls++
	return repository.ErrKnowledgeFolderKnowledgeBaseNotFound
}

func (r *knowledgeFolderIDValidationRepository) totalCalls() int {
	return r.getByIDCalls + r.listByParentCalls + r.listSubtreeCalls + r.treeTransactionCalls
}

func TestKnowledgeFolderService_RejectsMalformedIDsBeforeRepository(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	canonical := "7b5584d0-73f5-4fd6-8f2a-efdb2a9a7641"
	malformed := "not-a-uuid"
	name := "Renamed"
	tests := []struct {
		name string
		call func(interfaces.KnowledgeFolderService) error
	}{
		{
			name: "create parent",
			call: func(service interfaces.KnowledgeFolderService) error {
				_, err := service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
					ParentID: malformed,
					Name:     "Reports",
				})
				return err
			},
		},
		{
			name: "list parent",
			call: func(service interfaces.KnowledgeFolderService) error {
				_, err := service.ListFolders(ctx, "kb-1", malformed, &types.Pagination{})
				return err
			},
		},
		{
			name: "get folder",
			call: func(service interfaces.KnowledgeFolderService) error {
				_, err := service.GetFolder(ctx, "kb-1", malformed)
				return err
			},
		},
		{
			name: "update target folder",
			call: func(service interfaces.KnowledgeFolderService) error {
				_, err := service.UpdateFolder(ctx, "kb-1", malformed, &types.KnowledgeFolderUpdateRequest{
					Name: &name,
				})
				return err
			},
		},
		{
			name: "update destination parent",
			call: func(service interfaces.KnowledgeFolderService) error {
				parentID := malformed
				_, err := service.UpdateFolder(ctx, "kb-1", canonical, &types.KnowledgeFolderUpdateRequest{
					ParentID: &parentID,
				})
				return err
			},
		},
		{
			name: "delete folder",
			call: func(service interfaces.KnowledgeFolderService) error {
				return service.DeleteFolder(ctx, "kb-1", malformed)
			},
		},
		{
			name: "breadcrumb folder",
			call: func(service interfaces.KnowledgeFolderService) error {
				_, err := service.GetBreadcrumb(ctx, "kb-1", malformed)
				return err
			},
		},
		{
			name: "subtree folder",
			call: func(service interfaces.KnowledgeFolderService) error {
				_, err := service.ListSubtreeFolderIDs(ctx, "kb-1", malformed)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &knowledgeFolderIDValidationRepository{}
			err := tt.call(NewKnowledgeFolderService(repo))
			require.ErrorIs(t, err, ErrKnowledgeFolderInvalidArgument)
			assert.Zero(t, repo.totalCalls())
		})
	}
}

func TestKnowledgeFolderService_CanonicalMissingFolderRemainsNotFound(t *testing.T) {
	repo := &knowledgeFolderIDValidationRepository{}
	service := NewKnowledgeFolderService(repo)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	_, err := service.GetFolder(ctx, "kb-1", "7b5584d0-73f5-4fd6-8f2a-efdb2a9a7641")
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	assert.Equal(t, 1, repo.getByIDCalls)
}

func TestKnowledgeFolderService_RootIDOnlyPassesRootAwareEntries(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	canonical := "7b5584d0-73f5-4fd6-8f2a-efdb2a9a7641"

	t.Run("create parent", func(t *testing.T) {
		repo := &knowledgeFolderIDValidationRepository{}
		_, err := NewKnowledgeFolderService(repo).CreateFolder(
			ctx,
			"kb-1",
			&types.KnowledgeFolderCreateRequest{Name: "Reports"},
		)
		require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
		assert.Equal(t, 1, repo.treeTransactionCalls)
	})

	t.Run("list parent", func(t *testing.T) {
		repo := &knowledgeFolderIDValidationRepository{}
		_, err := NewKnowledgeFolderService(repo).ListFolders(
			ctx,
			"kb-1",
			types.KnowledgeFolderRootID,
			&types.Pagination{},
		)
		require.NoError(t, err)
		assert.Equal(t, 1, repo.listByParentCalls)
	})

	t.Run("update destination parent", func(t *testing.T) {
		repo := &knowledgeFolderIDValidationRepository{}
		rootID := types.KnowledgeFolderRootID
		_, err := NewKnowledgeFolderService(repo).UpdateFolder(
			ctx,
			"kb-1",
			canonical,
			&types.KnowledgeFolderUpdateRequest{ParentID: &rootID},
		)
		require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
		assert.Equal(t, 1, repo.treeTransactionCalls)
	})

	t.Run("subtree root", func(t *testing.T) {
		repo := &knowledgeFolderIDValidationRepository{}
		folderIDs, err := NewKnowledgeFolderService(repo).ListSubtreeFolderIDs(
			ctx,
			"kb-1",
			types.KnowledgeFolderRootID,
		)
		require.NoError(t, err)
		assert.Empty(t, folderIDs)
		assert.Equal(t, 1, repo.listSubtreeCalls)
	})

	t.Run("get root rejected", func(t *testing.T) {
		repo := &knowledgeFolderIDValidationRepository{}
		_, err := NewKnowledgeFolderService(repo).GetFolder(
			ctx,
			"kb-1",
			types.KnowledgeFolderRootID,
		)
		require.ErrorIs(t, err, ErrKnowledgeFolderInvalidArgument)
		assert.Zero(t, repo.totalCalls())
	})
}

func TestKnowledgeFolderService_CreateHierarchyConflictsAndScope(t *testing.T) {
	service, _, db, ctx := setupKnowledgeFolderServiceTest(t)

	root, err := service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
		Name:      "  Reports  ",
		SortOrder: 4,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, root.ID)
	assert.Equal(t, "", root.ParentID)
	assert.Equal(t, "Reports", root.Name)
	assert.Equal(t, "/"+root.ID+"/", root.Path)
	assert.Equal(t, 1, root.Depth)

	child, err := service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
		ParentID: root.ID,
		Name:     "2026",
	})
	require.NoError(t, err)
	assert.Equal(t, root.ID, child.ParentID)
	assert.Equal(t, root.Path+child.ID+"/", child.Path)
	assert.Equal(t, 2, child.Depth)

	_, err = service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
		ParentID: root.ID,
		Name:     "2026",
	})
	require.ErrorIs(t, err, ErrKnowledgeFolderConflict)

	otherParent, err := service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
		Name: "Other",
	})
	require.NoError(t, err)
	_, err = service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
		ParentID: otherParent.ID,
		Name:     "2026",
	})
	require.NoError(t, err)

	_, err = service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
		ParentID: knowledgeFolderServiceTestID("missing"),
		Name:     "Missing parent",
	})
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)

	insertKnowledgeFolderServiceFixtures(t, db, knowledgeFolderServiceFixture(
		"other-kb-parent",
		1,
		"kb-2",
		"",
		"Other KB",
		"/other-kb-parent/",
		1,
	))
	insertKnowledgeFolderServiceFixtures(t, db, knowledgeFolderServiceFixture(
		"other-tenant-parent",
		2,
		"kb-tenant-2",
		"",
		"Other tenant",
		"/other-tenant-parent/",
		1,
	))
	for _, parentID := range []string{
		knowledgeFolderServiceTestID("other-kb-parent"),
		knowledgeFolderServiceTestID("other-tenant-parent"),
	} {
		_, err = service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
			ParentID: parentID,
			Name:     "Scoped",
		})
		require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	}
}

func TestKnowledgeFolderService_RejectsBrokenAncestorChainsInWrites(t *testing.T) {
	t.Run("create parent ancestor missing", func(t *testing.T) {
		service, _, db, ctx := setupKnowledgeFolderServiceTest(t)
		parent := knowledgeFolderServiceFixture(
			"parent",
			1,
			"kb-1",
			"missing",
			"Parent",
			"/missing/parent/",
			2,
		)
		insertKnowledgeFolderServiceFixtures(t, db, parent)

		_, err := service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
			ParentID: parent.ID,
			Name:     "Child",
		})
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)

		var count int64
		require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&count).Error)
		assert.Equal(t, int64(1), count)
	})

	t.Run("current ancestor missing", func(t *testing.T) {
		service, repo, db, ctx := setupKnowledgeFolderServiceTest(t)
		current := knowledgeFolderServiceFixture(
			"current",
			1,
			"kb-1",
			"missing",
			"Current",
			"/missing/current/",
			2,
		)
		insertKnowledgeFolderServiceFixtures(t, db, current)
		renamed := "Renamed"

		_, err := service.UpdateFolder(ctx, "kb-1", current.ID, &types.KnowledgeFolderUpdateRequest{
			Name: &renamed,
		})
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		persisted, getErr := repo.GetByID(ctx, 1, "kb-1", current.ID)
		require.NoError(t, getErr)
		assert.Equal(t, "Current", persisted.Name)
		assert.Equal(t, knowledgeFolderServiceTestPath("/missing/current/"), persisted.Path)
	})

	t.Run("target parent ancestor missing", func(t *testing.T) {
		service, repo, db, ctx := setupKnowledgeFolderServiceTest(t)
		source := knowledgeFolderServiceFixture("source", 1, "kb-1", "", "Source", "/source/", 1)
		target := knowledgeFolderServiceFixture(
			"target",
			1,
			"kb-1",
			"missing",
			"Target",
			"/missing/target/",
			2,
		)
		insertKnowledgeFolderServiceFixtures(t, db, source, target)

		targetID := target.ID
		_, err := service.UpdateFolder(ctx, "kb-1", source.ID, &types.KnowledgeFolderUpdateRequest{
			ParentID: &targetID,
		})
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		persisted, getErr := repo.GetByID(ctx, 1, "kb-1", source.ID)
		require.NoError(t, getErr)
		assert.Equal(t, "", persisted.ParentID)
		assert.Equal(t, knowledgeFolderServiceTestPath("/source/"), persisted.Path)
		assert.Equal(t, 1, persisted.Depth)
	})

	t.Run("globally inconsistent paths cannot bypass cycle check", func(t *testing.T) {
		service, repo, db, ctx := setupKnowledgeFolderServiceTest(t)
		insertKnowledgeFolderServiceFixtures(t, db,
			knowledgeFolderServiceFixture("root-a", 1, "kb-1", "", "A", "/root-a/", 1),
			knowledgeFolderServiceFixture("root-b", 1, "kb-1", "", "B", "/root-b/", 1),
			knowledgeFolderServiceFixture(
				"current",
				1,
				"kb-1",
				"root-a",
				"Current",
				"/root-a/current/",
				2,
			),
			knowledgeFolderServiceFixture(
				"target",
				1,
				"kb-1",
				"current",
				"Target",
				"/root-b/current/target/",
				3,
			),
		)

		targetID := knowledgeFolderServiceTestID("target")
		_, err := service.UpdateFolder(
			ctx,
			"kb-1",
			knowledgeFolderServiceTestID("current"),
			&types.KnowledgeFolderUpdateRequest{
				ParentID: &targetID,
			},
		)
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)

		current, getErr := repo.GetByID(
			ctx,
			1,
			"kb-1",
			knowledgeFolderServiceTestID("current"),
		)
		require.NoError(t, getErr)
		assert.Equal(t, knowledgeFolderServiceTestID("root-a"), current.ParentID)
		assert.Equal(t, knowledgeFolderServiceTestPath("/root-a/current/"), current.Path)
		target, getErr := repo.GetByID(
			ctx,
			1,
			"kb-1",
			knowledgeFolderServiceTestID("target"),
		)
		require.NoError(t, getErr)
		assert.Equal(t, knowledgeFolderServiceTestID("current"), target.ParentID)
		assert.Equal(t, knowledgeFolderServiceTestPath("/root-b/current/target/"), target.Path)
	})
}

type replayKnowledgeFolderCreateRepository struct {
	interfaces.KnowledgeFolderReader
	created []*types.KnowledgeFolder
}

type replayKnowledgeFolderCreateTreeRepository struct {
	interfaces.KnowledgeFolderTreeRepository
	owner *replayKnowledgeFolderCreateRepository
}

func (r *replayKnowledgeFolderCreateRepository) RunTreeWriteTransaction(
	_ context.Context,
	_ uint64,
	_ string,
	fn interfaces.KnowledgeFolderTreeWriteFunc,
) error {
	treeRepo := &replayKnowledgeFolderCreateTreeRepository{owner: r}
	for attempt := 0; attempt < 2; attempt++ {
		if err := fn(treeRepo); err != nil {
			return err
		}
	}
	return nil
}

func (r *replayKnowledgeFolderCreateTreeRepository) GetByParentAndName(
	context.Context,
	uint64,
	string,
	string,
	string,
) (*types.KnowledgeFolder, error) {
	return nil, repository.ErrKnowledgeFolderNotFound
}

func (r *replayKnowledgeFolderCreateTreeRepository) Create(
	_ context.Context,
	folder *types.KnowledgeFolder,
) error {
	copyOfFolder := *folder
	r.owner.created = append(r.owner.created, &copyOfFolder)
	return nil
}

func TestKnowledgeFolderService_CreateGeneratesStableIDBeforeRetry(t *testing.T) {
	repo := &replayKnowledgeFolderCreateRepository{}
	service := NewKnowledgeFolderService(repo)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	created, err := service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
		Name: "Reports",
	})
	require.NoError(t, err)
	require.Len(t, repo.created, 2)
	assert.Equal(t, repo.created[0].ID, repo.created[1].ID)
	assert.Equal(t, repo.created[0].Path, repo.created[1].Path)
	assert.Equal(t, "/"+repo.created[0].ID+"/", repo.created[0].Path)
	assert.Equal(t, repo.created[1].ID, created.ID)
}

func TestKnowledgeFolderService_ListAndGetUseBatchNavigationStats(t *testing.T) {
	service, _, db, ctx := setupKnowledgeFolderServiceTest(t)
	insertKnowledgeFolderServiceFixtures(t, db,
		knowledgeFolderServiceFixture("folder-a", 1, "kb-1", "", "A", "/folder-a/", 1),
		knowledgeFolderServiceFixture("folder-b", 1, "kb-1", "", "B", "/folder-b/", 1),
		knowledgeFolderServiceFixture("folder-a-child", 1, "kb-1", "folder-a", "Child", "/folder-a/folder-a-child/", 2),
	)
	folderAID := knowledgeFolderServiceTestID("folder-a")
	folderBID := knowledgeFolderServiceTestID("folder-b")
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id, deleted_at) VALUES
			('knowledge-a-1', 1, 'kb-1', ?, NULL),
			('knowledge-a-2', 1, 'kb-1', ?, NULL),
			('knowledge-a-deleted', 1, 'kb-1', ?, CURRENT_TIMESTAMP),
			('knowledge-b', 1, 'kb-1', ?, NULL)
	`, folderAID, folderAID, folderAID, folderBID).Error)

	page, err := service.ListFolders(
		ctx,
		"kb-1",
		"",
		&types.Pagination{Page: 1, PageSize: 20},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(2), page.Total)
	folders, ok := page.Data.([]*types.KnowledgeFolderWithStats)
	require.True(t, ok)
	require.Len(t, folders, 2)
	assert.Equal(t, folderAID, folders[0].ID)
	assert.Equal(t, int64(2), folders[0].KnowledgeCount)
	assert.True(t, folders[0].HasChildren)
	assert.Equal(t, folderBID, folders[1].ID)
	assert.Equal(t, int64(1), folders[1].KnowledgeCount)
	assert.False(t, folders[1].HasChildren)

	childPage, err := service.ListFolders(
		ctx,
		"kb-1",
		folderAID,
		&types.Pagination{Page: 1, PageSize: 20},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), childPage.Total)
	children := childPage.Data.([]*types.KnowledgeFolderWithStats)
	require.Len(t, children, 1)
	assert.Equal(t, knowledgeFolderServiceTestID("folder-a-child"), children[0].ID)

	_, err = service.ListFolders(ctx, "kb-1", knowledgeFolderServiceTestID("missing"), &types.Pagination{})
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)

	folder, err := service.GetFolder(ctx, "kb-1", folderAID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), folder.KnowledgeCount)
	assert.True(t, folder.HasChildren)

	_, err = service.GetFolder(ctx, "kb-2", folderAID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	tenant2Ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2))
	_, err = service.GetFolder(tenant2Ctx, "kb-1", folderAID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

type knowledgeFolderBatchListRepository struct {
	interfaces.KnowledgeFolderRepository
	countCalls       int
	hasChildrenCalls int
	countFolderIDs   []string
	childParentIDs   []string
}

func (r *knowledgeFolderBatchListRepository) ListByParent(
	context.Context,
	uint64,
	string,
	string,
	*types.Pagination,
) ([]*types.KnowledgeFolder, int64, error) {
	return []*types.KnowledgeFolder{
		knowledgeFolderServiceFixture("a", 1, "kb-1", "", "A", "/a/", 1),
		knowledgeFolderServiceFixture("b", 1, "kb-1", "", "B", "/b/", 1),
		knowledgeFolderServiceFixture("c", 1, "kb-1", "", "C", "/c/", 1),
	}, 3, nil
}

func (r *knowledgeFolderBatchListRepository) CountKnowledgeByFolderIDs(
	_ context.Context,
	_ uint64,
	_ string,
	folderIDs []string,
) (map[string]int64, error) {
	r.countCalls++
	r.countFolderIDs = append([]string(nil), folderIDs...)
	return map[string]int64{
		knowledgeFolderServiceTestID("a"): 1,
		knowledgeFolderServiceTestID("b"): 0,
		knowledgeFolderServiceTestID("c"): 0,
	}, nil
}

func (r *knowledgeFolderBatchListRepository) FindParentIDsWithChildren(
	_ context.Context,
	_ uint64,
	_ string,
	parentIDs []string,
) (map[string]bool, error) {
	r.hasChildrenCalls++
	r.childParentIDs = append([]string(nil), parentIDs...)
	return map[string]bool{
		knowledgeFolderServiceTestID("a"): false,
		knowledgeFolderServiceTestID("b"): true,
		knowledgeFolderServiceTestID("c"): false,
	}, nil
}

func TestKnowledgeFolderService_ListBatchesCountsAndChildren(t *testing.T) {
	repo := &knowledgeFolderBatchListRepository{}
	service := NewKnowledgeFolderService(repo)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	result, err := service.ListFolders(ctx, "kb-1", "", &types.Pagination{})
	require.NoError(t, err)
	assert.Equal(t, 1, repo.countCalls)
	assert.Equal(t, 1, repo.hasChildrenCalls)
	expectedIDs := []string{
		knowledgeFolderServiceTestID("a"),
		knowledgeFolderServiceTestID("b"),
		knowledgeFolderServiceTestID("c"),
	}
	assert.Equal(t, expectedIDs, repo.countFolderIDs)
	assert.Equal(t, expectedIDs, repo.childParentIDs)
	folders := result.Data.([]*types.KnowledgeFolderWithStats)
	require.Len(t, folders, 3)
	assert.Equal(t, int64(1), folders[0].KnowledgeCount)
	assert.True(t, folders[1].HasChildren)
	assert.Zero(t, folders[2].KnowledgeCount)
	assert.False(t, folders[2].HasChildren)
}

type knowledgeFolderBreadcrumbRepository struct {
	interfaces.KnowledgeFolderRepository
	folders        map[string]*types.KnowledgeFolder
	omitID         string
	listByIDsCalls int
	requestedIDs   []string
}

func (r *knowledgeFolderBreadcrumbRepository) GetByID(
	_ context.Context,
	tenantID uint64,
	kbID string,
	folderID string,
) (*types.KnowledgeFolder, error) {
	folder := r.folders[folderID]
	if folder == nil || folder.TenantID != tenantID || folder.KnowledgeBaseID != kbID {
		return nil, repository.ErrKnowledgeFolderNotFound
	}
	return folder, nil
}

func (r *knowledgeFolderBreadcrumbRepository) ListByIDs(
	_ context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
) ([]*types.KnowledgeFolder, error) {
	r.listByIDsCalls++
	r.requestedIDs = append([]string(nil), folderIDs...)
	folders := make([]*types.KnowledgeFolder, 0, len(folderIDs))
	for index := len(folderIDs) - 1; index >= 0; index-- {
		folderID := folderIDs[index]
		folder := r.folders[folderID]
		if folderID == r.omitID || folder == nil ||
			folder.TenantID != tenantID || folder.KnowledgeBaseID != kbID {
			continue
		}
		folders = append(folders, folder)
	}
	return folders, nil
}

func knowledgeFolderBreadcrumbFixtures() map[string]*types.KnowledgeFolder {
	root := knowledgeFolderServiceFixture("root", 1, "kb-1", "", "Root", "/root/", 1)
	child := knowledgeFolderServiceFixture("child", 1, "kb-1", "root", "Child", "/root/child/", 2)
	leaf := knowledgeFolderServiceFixture("leaf", 1, "kb-1", "child", "Leaf", "/root/child/leaf/", 3)
	return map[string]*types.KnowledgeFolder{
		root.ID:  root,
		child.ID: child,
		leaf.ID:  leaf,
	}
}

func TestKnowledgeFolderService_BreadcrumbUsesOneBatchAndNeverReturnsPartialChain(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	t.Run("reorders one batch", func(t *testing.T) {
		repo := &knowledgeFolderBreadcrumbRepository{
			folders: knowledgeFolderBreadcrumbFixtures(),
		}
		service := NewKnowledgeFolderService(repo)

		chain, err := service.GetBreadcrumb(ctx, "kb-1", knowledgeFolderServiceTestID("leaf"))
		require.NoError(t, err)
		require.Len(t, chain, 3)
		assert.Equal(t, []string{
			knowledgeFolderServiceTestID("root"),
			knowledgeFolderServiceTestID("child"),
			knowledgeFolderServiceTestID("leaf"),
		}, []string{
			chain[0].ID,
			chain[1].ID,
			chain[2].ID,
		})
		assert.Equal(t, 1, repo.listByIDsCalls)
		assert.Equal(t, []string{
			knowledgeFolderServiceTestID("root"),
			knowledgeFolderServiceTestID("child"),
			knowledgeFolderServiceTestID("leaf"),
		}, repo.requestedIDs)
	})

	t.Run("missing ancestor returns no partial chain", func(t *testing.T) {
		repo := &knowledgeFolderBreadcrumbRepository{
			folders: knowledgeFolderBreadcrumbFixtures(),
			omitID:  knowledgeFolderServiceTestID("child"),
		}
		service := NewKnowledgeFolderService(repo)

		chain, err := service.GetBreadcrumb(ctx, "kb-1", knowledgeFolderServiceTestID("leaf"))
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		assert.Nil(t, chain)
		assert.Equal(t, 1, repo.listByIDsCalls)
		assert.Equal(t, []string{
			knowledgeFolderServiceTestID("root"),
			knowledgeFolderServiceTestID("child"),
			knowledgeFolderServiceTestID("leaf"),
		}, repo.requestedIDs)
	})

	t.Run("scope mismatch returns no partial chain", func(t *testing.T) {
		repo := &knowledgeFolderBreadcrumbRepository{
			folders: knowledgeFolderBreadcrumbFixtures(),
		}
		folder := knowledgeFolderServiceFixture(
			"leaf",
			2,
			"kb-1",
			"child",
			"Leaf",
			"/root/child/leaf/",
			3,
		)

		chain, err := loadValidatedKnowledgeFolderChain(
			ctx,
			repo,
			1,
			"kb-1",
			folder,
		)
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		assert.Nil(t, chain)
		assert.Zero(t, repo.listByIDsCalls)
	})
}

func TestKnowledgeFolderService_BreadcrumbValidatesWholeChain(t *testing.T) {
	t.Run("ordered chain", func(t *testing.T) {
		service, _, db, ctx := setupKnowledgeFolderServiceTest(t)
		insertKnowledgeFolderServiceFixtures(t, db,
			knowledgeFolderServiceFixture("root", 1, "kb-1", "", "Root", "/root/", 1),
			knowledgeFolderServiceFixture("child", 1, "kb-1", "root", "Child", "/root/child/", 2),
			knowledgeFolderServiceFixture("leaf", 1, "kb-1", "child", "Leaf", "/root/child/leaf/", 3),
		)
		rootBreadcrumb, err := service.GetBreadcrumb(
			ctx,
			"kb-1",
			knowledgeFolderServiceTestID("root"),
		)
		require.NoError(t, err)
		require.Len(t, rootBreadcrumb, 1)
		assert.Equal(t, knowledgeFolderServiceTestID("root"), rootBreadcrumb[0].ID)

		breadcrumb, err := service.GetBreadcrumb(
			ctx,
			"kb-1",
			knowledgeFolderServiceTestID("leaf"),
		)
		require.NoError(t, err)
		require.Len(t, breadcrumb, 3)
		assert.Equal(t, []string{
			knowledgeFolderServiceTestID("root"),
			knowledgeFolderServiceTestID("child"),
			knowledgeFolderServiceTestID("leaf"),
		}, []string{
			breadcrumb[0].ID,
			breadcrumb[1].ID,
			breadcrumb[2].ID,
		})
	})

	tests := []struct {
		name    string
		corrupt func(*testing.T, *gorm.DB)
	}{
		{
			name: "missing ancestor",
			corrupt: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Model(&types.KnowledgeFolder{}).
					Where("id = ?", knowledgeFolderServiceTestID("child")).
					Update("deleted_at", time.Now().UTC()).Error)
			},
		},
		{
			name: "parent chain mismatch",
			corrupt: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Model(&types.KnowledgeFolder{}).
					Where("id = ?", knowledgeFolderServiceTestID("leaf")).
					Update("parent_id", knowledgeFolderServiceTestID("root")).Error)
			},
		},
		{
			name: "final path id mismatch",
			corrupt: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Model(&types.KnowledgeFolder{}).
					Where("id = ?", knowledgeFolderServiceTestID("leaf")).
					Update("path", knowledgeFolderServiceTestPath("/root/child/not-leaf/")).Error)
			},
		},
		{
			name: "depth mismatch",
			corrupt: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Model(&types.KnowledgeFolder{}).
					Where("id = ?", knowledgeFolderServiceTestID("leaf")).
					Update("depth", 2).Error)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, db, ctx := setupKnowledgeFolderServiceTest(t)
			insertKnowledgeFolderServiceFixtures(t, db,
				knowledgeFolderServiceFixture("root", 1, "kb-1", "", "Root", "/root/", 1),
				knowledgeFolderServiceFixture("child", 1, "kb-1", "root", "Child", "/root/child/", 2),
				knowledgeFolderServiceFixture("leaf", 1, "kb-1", "child", "Leaf", "/root/child/leaf/", 3),
			)
			tt.corrupt(t, db)
			breadcrumb, err := service.GetBreadcrumb(
				ctx,
				"kb-1",
				knowledgeFolderServiceTestID("leaf"),
			)
			require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
			assert.Nil(t, breadcrumb)
		})
	}
}

func TestKnowledgeFolderService_GetListAndDeleteFailClosedOnDirtyTree(t *testing.T) {
	t.Run("get rejects missing ancestor", func(t *testing.T) {
		service, _, db, ctx := setupKnowledgeFolderServiceTest(t)
		root := knowledgeFolderServiceFixture("root", 1, "kb-1", "", "Root", "/root/", 1)
		child := knowledgeFolderServiceFixture("child", 1, "kb-1", "root", "Child", "/root/child/", 2)
		leaf := knowledgeFolderServiceFixture("leaf", 1, "kb-1", "child", "Leaf", "/root/child/leaf/", 3)
		insertKnowledgeFolderServiceFixtures(t, db, root, child, leaf)
		require.NoError(t, db.Model(&types.KnowledgeFolder{}).
			Where("id = ?", child.ID).
			Update("deleted_at", time.Now().UTC()).Error)

		folder, err := service.GetFolder(ctx, "kb-1", leaf.ID)
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		assert.Nil(t, folder)
	})

	t.Run("list rejects dirty parent chain", func(t *testing.T) {
		service, _, db, ctx := setupKnowledgeFolderServiceTest(t)
		parent := knowledgeFolderServiceFixture(
			"parent",
			1,
			"kb-1",
			"missing",
			"Parent",
			"/missing/parent/",
			2,
		)
		insertKnowledgeFolderServiceFixtures(t, db, parent)

		page, err := service.ListFolders(ctx, "kb-1", parent.ID, &types.Pagination{})
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		assert.Nil(t, page)
	})

	t.Run("list rejects dirty direct child", func(t *testing.T) {
		service, _, db, ctx := setupKnowledgeFolderServiceTest(t)
		root := knowledgeFolderServiceFixture("root", 1, "kb-1", "", "Root", "/root/", 1)
		child := knowledgeFolderServiceFixture("child", 1, "kb-1", "root", "Child", "/root/child/", 2)
		insertKnowledgeFolderServiceFixtures(t, db, root, child)
		require.NoError(t, db.Model(&types.KnowledgeFolder{}).
			Where("id = ?", child.ID).
			Update("path", knowledgeFolderServiceTestPath("/root/not-child/")).Error)

		page, err := service.ListFolders(ctx, "kb-1", root.ID, &types.Pagination{})
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		assert.Nil(t, page)
	})

	t.Run("delete rejects dirty ancestor chain", func(t *testing.T) {
		service, repo, db, ctx := setupKnowledgeFolderServiceTest(t)
		current := knowledgeFolderServiceFixture(
			"current",
			1,
			"kb-1",
			"missing",
			"Current",
			"/missing/current/",
			2,
		)
		insertKnowledgeFolderServiceFixtures(t, db, current)

		err := service.DeleteFolder(ctx, "kb-1", current.ID)
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		_, getErr := repo.GetByID(ctx, 1, "kb-1", current.ID)
		require.NoError(t, getErr)
	})
}

type knowledgeFolderStatsIntegrityRepository struct {
	interfaces.KnowledgeFolderRepository
	counts      map[string]int64
	hasChildren map[string]bool
}

func (r *knowledgeFolderStatsIntegrityRepository) CountKnowledgeByFolderIDs(
	context.Context,
	uint64,
	string,
	[]string,
) (map[string]int64, error) {
	return r.counts, nil
}

func (r *knowledgeFolderStatsIntegrityRepository) FindParentIDsWithChildren(
	context.Context,
	uint64,
	string,
	[]string,
) (map[string]bool, error) {
	return r.hasChildren, nil
}

func TestKnowledgeFolderService_EnrichRejectsInvalidOrIncompleteRows(t *testing.T) {
	valid := knowledgeFolderServiceFixture("valid", 1, "kb-1", "", "Valid", "/valid/", 1)
	tests := []struct {
		name        string
		folders     []*types.KnowledgeFolder
		counts      map[string]int64
		hasChildren map[string]bool
	}{
		{
			name:        "nil folder",
			folders:     []*types.KnowledgeFolder{nil},
			counts:      map[string]int64{},
			hasChildren: map[string]bool{},
		},
		{
			name:        "duplicate folder",
			folders:     []*types.KnowledgeFolder{valid, valid},
			counts:      map[string]int64{valid.ID: 0},
			hasChildren: map[string]bool{valid.ID: false},
		},
		{
			name:        "missing count",
			folders:     []*types.KnowledgeFolder{valid},
			counts:      map[string]int64{},
			hasChildren: map[string]bool{valid.ID: false},
		},
		{
			name:        "missing has children",
			folders:     []*types.KnowledgeFolder{valid},
			counts:      map[string]int64{valid.ID: 0},
			hasChildren: map[string]bool{},
		},
		{
			name:        "negative count",
			folders:     []*types.KnowledgeFolder{valid},
			counts:      map[string]int64{valid.ID: -1},
			hasChildren: map[string]bool{valid.ID: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &knowledgeFolderStatsIntegrityRepository{
				counts:      tt.counts,
				hasChildren: tt.hasChildren,
			}
			service := &knowledgeFolderService{repo: repo}
			result, err := service.enrichKnowledgeFolders(
				context.Background(),
				1,
				"kb-1",
				tt.folders,
			)
			require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
			assert.Nil(t, result)
		})
	}
}

func TestKnowledgeFolderService_UpdateRenameSortMoveAndNoOp(t *testing.T) {
	service, repo, db, ctx := setupKnowledgeFolderServiceTest(t)
	insertKnowledgeFolderServiceFixtures(t, db,
		knowledgeFolderServiceFixture("source", 1, "kb-1", "", "Source", "/source/", 1),
		knowledgeFolderServiceFixture("child", 1, "kb-1", "source", "Child", "/source/child/", 2),
		knowledgeFolderServiceFixture("grandchild", 1, "kb-1", "child", "Grandchild", "/source/child/grandchild/", 3),
		knowledgeFolderServiceFixture("target", 1, "kb-1", "", "Target", "/target/", 1),
	)
	sourceID := knowledgeFolderServiceTestID("source")
	childID := knowledgeFolderServiceTestID("child")
	grandchildID := knowledgeFolderServiceTestID("grandchild")
	targetID := knowledgeFolderServiceTestID("target")
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id)
		VALUES ('knowledge-child', 1, 'kb-1', ?)
	`, childID).Error)

	renamed := "Renamed"
	updated, err := service.UpdateFolder(ctx, "kb-1", sourceID, &types.KnowledgeFolderUpdateRequest{
		Name: &renamed,
	})
	require.NoError(t, err)
	assert.Equal(t, "Renamed", updated.Name)
	assert.Equal(t, knowledgeFolderServiceTestPath("/source/"), updated.Path)
	childBeforeMove, err := repo.GetByID(ctx, 1, "kb-1", childID)
	require.NoError(t, err)
	assert.Equal(t, knowledgeFolderServiceTestPath("/source/child/"), childBeforeMove.Path)

	sortOrder := 12
	updated, err = service.UpdateFolder(ctx, "kb-1", sourceID, &types.KnowledgeFolderUpdateRequest{
		SortOrder: &sortOrder,
	})
	require.NoError(t, err)
	assert.Equal(t, 12, updated.SortOrder)
	assert.Equal(t, knowledgeFolderServiceTestPath("/source/"), updated.Path)
	assert.Equal(t, 1, updated.Depth)

	beforeNoOp := updated.UpdatedAt
	sameParent := ""
	sameName := "Renamed"
	updated, err = service.UpdateFolder(ctx, "kb-1", sourceID, &types.KnowledgeFolderUpdateRequest{
		ParentID:  &sameParent,
		Name:      &sameName,
		SortOrder: &sortOrder,
	})
	require.NoError(t, err)
	assert.True(t, updated.UpdatedAt.Equal(beforeNoOp))
	persistedNoOp, err := repo.GetByID(ctx, 1, "kb-1", sourceID)
	require.NoError(t, err)
	assert.True(t, persistedNoOp.UpdatedAt.Equal(beforeNoOp))

	targetParent := targetID
	updated, err = service.UpdateFolder(ctx, "kb-1", sourceID, &types.KnowledgeFolderUpdateRequest{
		ParentID: &targetParent,
	})
	require.NoError(t, err)
	assert.Equal(t, targetID, updated.ParentID)
	assert.Equal(t, knowledgeFolderServiceTestPath("/target/source/"), updated.Path)
	assert.Equal(t, 2, updated.Depth)

	child, err := repo.GetByID(ctx, 1, "kb-1", childID)
	require.NoError(t, err)
	assert.Equal(t, knowledgeFolderServiceTestPath("/target/source/child/"), child.Path)
	assert.Equal(t, 3, child.Depth)
	grandchild, err := repo.GetByID(ctx, 1, "kb-1", grandchildID)
	require.NoError(t, err)
	assert.Equal(t, knowledgeFolderServiceTestPath("/target/source/child/grandchild/"), grandchild.Path)
	assert.Equal(t, 4, grandchild.Depth)

	var knowledgeFolderID string
	require.NoError(t, db.Raw(
		`SELECT folder_id FROM knowledges WHERE id = 'knowledge-child'`,
	).Scan(&knowledgeFolderID).Error)
	assert.Equal(t, childID, knowledgeFolderID)

	rootParent := ""
	updated, err = service.UpdateFolder(ctx, "kb-1", sourceID, &types.KnowledgeFolderUpdateRequest{
		ParentID: &rootParent,
	})
	require.NoError(t, err)
	assert.Equal(t, knowledgeFolderServiceTestPath("/source/"), updated.Path)
	assert.Equal(t, 1, updated.Depth)

	targetParent = targetID
	updatedChild, err := service.UpdateFolder(ctx, "kb-1", childID, &types.KnowledgeFolderUpdateRequest{
		ParentID: &targetParent,
	})
	require.NoError(t, err)
	assert.Equal(t, targetID, updatedChild.ParentID)
	assert.Equal(t, knowledgeFolderServiceTestPath("/target/child/"), updatedChild.Path)
	assert.Equal(t, 2, updatedChild.Depth)
	grandchild, err = repo.GetByID(ctx, 1, "kb-1", grandchildID)
	require.NoError(t, err)
	assert.Equal(t, knowledgeFolderServiceTestPath("/target/child/grandchild/"), grandchild.Path)
	assert.Equal(t, 3, grandchild.Depth)

	conflictingName := "Target"
	_, err = service.UpdateFolder(ctx, "kb-1", sourceID, &types.KnowledgeFolderUpdateRequest{
		Name: &conflictingName,
	})
	require.ErrorIs(t, err, ErrKnowledgeFolderConflict)

	_, err = service.UpdateFolder(ctx, "kb-1", sourceID, &types.KnowledgeFolderUpdateRequest{})
	require.ErrorIs(t, err, ErrKnowledgeFolderInvalidArgument)
}

func TestMapKnowledgeFolderErrorTreatsRepositoryContractViolationAsInternal(t *testing.T) {
	err := mapKnowledgeFolderError(repository.ErrKnowledgeFolderInvalid)
	require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
	assert.NotErrorIs(t, err, ErrKnowledgeFolderInvalidArgument)
}

func TestKnowledgeFolderService_UpdateRejectsCyclesAndMissingParents(t *testing.T) {
	service, _, db, ctx := setupKnowledgeFolderServiceTest(t)
	insertKnowledgeFolderServiceFixtures(t, db,
		knowledgeFolderServiceFixture("root", 1, "kb-1", "", "Root", "/root/", 1),
		knowledgeFolderServiceFixture("child", 1, "kb-1", "root", "Child", "/root/child/", 2),
		knowledgeFolderServiceFixture("leaf", 1, "kb-1", "child", "Leaf", "/root/child/leaf/", 3),
		knowledgeFolderServiceFixture("other-kb-parent", 1, "kb-2", "", "Other", "/other-kb-parent/", 1),
		knowledgeFolderServiceFixture("other-tenant-parent", 2, "kb-tenant-2", "", "Other tenant", "/other-tenant-parent/", 1),
	)

	rootID := knowledgeFolderServiceTestID("root")
	for _, parentID := range []string{
		rootID,
		knowledgeFolderServiceTestID("child"),
		knowledgeFolderServiceTestID("leaf"),
	} {
		parentID := parentID
		_, err := service.UpdateFolder(ctx, "kb-1", rootID, &types.KnowledgeFolderUpdateRequest{
			ParentID: &parentID,
		})
		require.ErrorIs(t, err, ErrKnowledgeFolderCycle)
	}
	for _, parentID := range []string{
		knowledgeFolderServiceTestID("missing"),
		knowledgeFolderServiceTestID("other-kb-parent"),
		knowledgeFolderServiceTestID("other-tenant-parent"),
	} {
		parentID := parentID
		_, err := service.UpdateFolder(ctx, "kb-1", rootID, &types.KnowledgeFolderUpdateRequest{
			ParentID: &parentID,
		})
		require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	}
}

func syntheticKnowledgeFolderSubtree(depth int) []*types.KnowledgeFolder {
	folders := make([]*types.KnowledgeFolder, 0, depth)
	folders = append(folders, knowledgeFolderServiceFixture(
		"source",
		1,
		"kb-1",
		"",
		"Source",
		"/source/",
		1,
	))
	pathIDs := []string{"source"}
	parentID := "source"
	for level := 2; level <= depth; level++ {
		id := fmt.Sprintf("level-%02d", level)
		pathIDs = append(pathIDs, id)
		folders = append(folders, knowledgeFolderServiceFixture(
			id,
			1,
			"kb-1",
			parentID,
			fmt.Sprintf("Level %02d", level),
			"/"+strings.Join(pathIDs, "/")+"/",
			level,
		))
		parentID = id
	}
	return folders
}

func TestKnowledgeFolderService_UpdateEnforcesSubtreeMaximumDepth(t *testing.T) {
	for _, tt := range []struct {
		name     string
		maxDepth int
		wantErr  bool
	}{
		{name: "exactly 32", maxDepth: 31},
		{name: "exceeds 32", maxDepth: 32, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service, repo, db, ctx := setupKnowledgeFolderServiceTest(t)
			target := knowledgeFolderServiceFixture("target", 1, "kb-1", "", "Target", "/target/", 1)
			subtree := syntheticKnowledgeFolderSubtree(tt.maxDepth)
			deepest := subtree[len(subtree)-1]
			insertKnowledgeFolderServiceFixtures(t, db, target)
			insertKnowledgeFolderServiceFixtures(t, db, subtree...)

			targetID := target.ID
			updated, err := service.UpdateFolder(
				ctx,
				"kb-1",
				knowledgeFolderServiceTestID("source"),
				&types.KnowledgeFolderUpdateRequest{ParentID: &targetID},
			)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrKnowledgeFolderDepthExceeded)
				persisted, getErr := repo.GetByID(
					ctx,
					1,
					"kb-1",
					knowledgeFolderServiceTestID("source"),
				)
				require.NoError(t, getErr)
				assert.Equal(t, knowledgeFolderServiceTestPath("/source/"), persisted.Path)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 2, updated.Depth)
			movedDeepest, getErr := repo.GetByID(ctx, 1, "kb-1", deepest.ID)
			require.NoError(t, getErr)
			assert.Equal(t, 32, movedDeepest.Depth)
		})
	}
}

func TestKnowledgeFolderService_DeleteFolderMapsEmptySemantics(t *testing.T) {
	service, repo, db, ctx := setupKnowledgeFolderServiceTest(t)
	insertKnowledgeFolderServiceFixtures(t, db,
		knowledgeFolderServiceFixture("empty", 1, "kb-1", "", "Empty", "/empty/", 1),
		knowledgeFolderServiceFixture("parent", 1, "kb-1", "", "Parent", "/parent/", 1),
		knowledgeFolderServiceFixture("child", 1, "kb-1", "parent", "Child", "/parent/child/", 2),
		knowledgeFolderServiceFixture("with-knowledge", 1, "kb-1", "", "Documents", "/with-knowledge/", 1),
		knowledgeFolderServiceFixture("soft-contents", 1, "kb-1", "", "Soft", "/soft-contents/", 1),
		knowledgeFolderServiceFixture("soft-child", 1, "kb-1", "soft-contents", "Soft child", "/soft-contents/soft-child/", 2),
	)
	emptyID := knowledgeFolderServiceTestID("empty")
	parentID := knowledgeFolderServiceTestID("parent")
	withKnowledgeID := knowledgeFolderServiceTestID("with-knowledge")
	softContentsID := knowledgeFolderServiceTestID("soft-contents")
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id, deleted_at) VALUES
			('active-knowledge', 1, 'kb-1', ?, NULL),
			('soft-knowledge', 1, 'kb-1', ?, CURRENT_TIMESTAMP)
	`, withKnowledgeID, softContentsID).Error)
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("id = ?", knowledgeFolderServiceTestID("soft-child")).
		Update("deleted_at", time.Now().UTC()).Error)

	require.NoError(t, service.DeleteFolder(ctx, "kb-1", emptyID))
	_, err := repo.GetByID(ctx, 1, "kb-1", emptyID)
	require.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)

	err = service.DeleteFolder(ctx, "kb-1", parentID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotEmpty)
	err = service.DeleteFolder(ctx, "kb-1", withKnowledgeID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotEmpty)
	require.NoError(t, service.DeleteFolder(ctx, "kb-1", softContentsID))

	err = service.DeleteFolder(ctx, "kb-1", knowledgeFolderServiceTestID("missing"))
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	err = service.DeleteFolder(ctx, "kb-2", parentID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeFolderService_ListSubtreeFolderIDs(t *testing.T) {
	service, _, db, ctx := setupKnowledgeFolderServiceTest(t)
	insertKnowledgeFolderServiceFixtures(t, db,
		knowledgeFolderServiceFixture("root-a", 1, "kb-1", "", "A", "/root-a/", 1),
		knowledgeFolderServiceFixture("child-a", 1, "kb-1", "root-a", "Child", "/root-a/child-a/", 2),
		knowledgeFolderServiceFixture("leaf-a", 1, "kb-1", "child-a", "Leaf", "/root-a/child-a/leaf-a/", 3),
		knowledgeFolderServiceFixture("deleted-a", 1, "kb-1", "root-a", "Deleted", "/root-a/deleted-a/", 2),
		knowledgeFolderServiceFixture("root-b", 1, "kb-1", "", "B", "/root-b/", 1),
		knowledgeFolderServiceFixture("other-kb", 1, "kb-2", "", "Other KB", "/other-kb/", 1),
		knowledgeFolderServiceFixture("other-tenant", 2, "kb-tenant-2", "", "Other tenant", "/other-tenant/", 1),
	)
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("id = ?", knowledgeFolderServiceTestID("deleted-a")).
		Update("deleted_at", time.Now().UTC()).Error)

	rootAID := knowledgeFolderServiceTestID("root-a")
	subtree, err := service.ListSubtreeFolderIDs(ctx, "kb-1", rootAID)
	require.NoError(t, err)
	assert.Equal(t, []string{
		rootAID,
		knowledgeFolderServiceTestID("child-a"),
		knowledgeFolderServiceTestID("leaf-a"),
	}, subtree)

	all, err := service.ListSubtreeFolderIDs(ctx, "kb-1", "")
	require.NoError(t, err)
	rootIDs := []string{rootAID, knowledgeFolderServiceTestID("root-b")}
	sort.Strings(rootIDs)
	assert.Equal(t, append(
		rootIDs,
		knowledgeFolderServiceTestID("child-a"),
		knowledgeFolderServiceTestID("leaf-a"),
	), all)

	_, err = service.ListSubtreeFolderIDs(ctx, "kb-1", knowledgeFolderServiceTestID("root-b-missing"))
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	_, err = service.ListSubtreeFolderIDs(
		ctx,
		"kb-1",
		knowledgeFolderServiceTestID("other-kb"),
	)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)

	broken := knowledgeFolderServiceFixture(
		"broken",
		1,
		"kb-1",
		"missing",
		"Broken",
		"/missing/broken/",
		2,
	)
	insertKnowledgeFolderServiceFixtures(t, db, broken)
	_, err = service.ListSubtreeFolderIDs(ctx, "kb-1", broken.ID)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeFolderService_SubtreeAndMoveRejectCorruptBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(rootID string, outsideID string) *types.KnowledgeFolder
	}{
		{
			name: "path prefix row has outside parent",
			corrupt: func(rootID string, outsideID string) *types.KnowledgeFolder {
				id := knowledgeFolderServiceTestID("path-impostor")
				return rawKnowledgeFolderServiceFixture(
					id,
					1,
					"kb-1",
					outsideID,
					"Path impostor",
					"/"+rootID+"/"+outsideID+"/"+id+"/",
					3,
				)
			},
		},
		{
			name: "reachable child path escapes prefix",
			corrupt: func(rootID string, outsideID string) *types.KnowledgeFolder {
				id := knowledgeFolderServiceTestID("reachable-outside")
				return rawKnowledgeFolderServiceFixture(
					id,
					1,
					"kb-1",
					rootID,
					"Reachable outside",
					"/"+outsideID+"/"+rootID+"/"+id+"/",
					3,
				)
			},
		},
		{
			name: "child depth does not match path",
			corrupt: func(rootID string, _ string) *types.KnowledgeFolder {
				id := knowledgeFolderServiceTestID("wrong-depth")
				return rawKnowledgeFolderServiceFixture(
					id,
					1,
					"kb-1",
					rootID,
					"Wrong depth",
					"/"+rootID+"/"+id+"/",
					3,
				)
			},
		},
		{
			name: "path prefix row is orphaned",
			corrupt: func(rootID string, _ string) *types.KnowledgeFolder {
				id := knowledgeFolderServiceTestID("orphan")
				missingID := knowledgeFolderServiceTestID("missing-parent")
				return rawKnowledgeFolderServiceFixture(
					id,
					1,
					"kb-1",
					missingID,
					"Orphan",
					"/"+rootID+"/"+missingID+"/"+id+"/",
					3,
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, repo, db, ctx := setupKnowledgeFolderServiceTest(t)
			root := knowledgeFolderServiceFixture("root", 1, "kb-1", "", "Root", "/root/", 1)
			target := knowledgeFolderServiceFixture("target", 1, "kb-1", "", "Target", "/target/", 1)
			outside := knowledgeFolderServiceFixture("outside", 1, "kb-1", "", "Outside", "/outside/", 1)
			insertKnowledgeFolderServiceFixtures(t, db, root, target, outside)
			insertKnowledgeFolderServiceFixtures(t, db, tt.corrupt(root.ID, outside.ID))

			ids, err := service.ListSubtreeFolderIDs(ctx, "kb-1", root.ID)
			require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
			assert.Nil(t, ids)

			targetID := target.ID
			_, err = service.UpdateFolder(
				ctx,
				"kb-1",
				root.ID,
				&types.KnowledgeFolderUpdateRequest{ParentID: &targetID},
			)
			require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)

			persisted, getErr := repo.GetByID(ctx, 1, "kb-1", root.ID)
			require.NoError(t, getErr)
			assert.Equal(t, "", persisted.ParentID)
			assert.Equal(t, knowledgeFolderServiceTestPath("/root/"), persisted.Path)
			assert.Equal(t, 1, persisted.Depth)
		})
	}
}

func TestKnowledgeFolderService_ConcurrentSiblingCreateLeavesOneActiveFolder(t *testing.T) {
	service, _, db, ctx := setupKnowledgeFolderServiceTest(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for attempt := 0; attempt < 2; attempt++ {
		go func() {
			<-start
			_, err := service.CreateFolder(ctx, "kb-1", &types.KnowledgeFolderCreateRequest{
				Name: "Same name",
			})
			results <- err
		}()
	}
	close(start)

	var successCount int
	var conflictCount int
	for attempt := 0; attempt < 2; attempt++ {
		err := <-results
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, ErrKnowledgeFolderConflict):
			conflictCount++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successCount)
	assert.Equal(t, 1, conflictCount)

	var count int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND parent_id = ? AND name = ?",
			1,
			"kb-1",
			"",
			"Same name",
		).
		Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestKnowledgeFolderService_ConcurrentMoveAndDeleteRemainSerializable(t *testing.T) {
	service, repo, db, ctx := setupKnowledgeFolderServiceTest(t)
	insertKnowledgeFolderServiceFixtures(t, db,
		knowledgeFolderServiceFixture("source", 1, "kb-1", "", "Source", "/source/", 1),
		knowledgeFolderServiceFixture("child", 1, "kb-1", "source", "Child", "/source/child/", 2),
		knowledgeFolderServiceFixture("target", 1, "kb-1", "", "Target", "/target/", 1),
	)

	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		targetID := knowledgeFolderServiceTestID("target")
		_, err := service.UpdateFolder(
			ctx,
			"kb-1",
			knowledgeFolderServiceTestID("source"),
			&types.KnowledgeFolderUpdateRequest{
				ParentID: &targetID,
			},
		)
		results <- err
	}()
	go func() {
		<-start
		results <- service.DeleteFolder(ctx, "kb-1", knowledgeFolderServiceTestID("child"))
	}()
	close(start)

	require.NoError(t, <-results)
	require.NoError(t, <-results)
	source, err := repo.GetByID(ctx, 1, "kb-1", knowledgeFolderServiceTestID("source"))
	require.NoError(t, err)
	assert.Equal(t, knowledgeFolderServiceTestID("target"), source.ParentID)
	assert.Equal(t, knowledgeFolderServiceTestPath("/target/source/"), source.Path)
	assert.Equal(t, 2, source.Depth)
	_, err = repo.GetByID(ctx, 1, "kb-1", knowledgeFolderServiceTestID("child"))
	require.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)
}

func TestKnowledgeFolderService_ConcurrentOppositeMovesDoNotCreateCycle(t *testing.T) {
	service, repo, db, ctx := setupKnowledgeFolderServiceTest(t)
	insertKnowledgeFolderServiceFixtures(t, db,
		knowledgeFolderServiceFixture("folder-a", 1, "kb-1", "", "A", "/folder-a/", 1),
		knowledgeFolderServiceFixture("folder-b", 1, "kb-1", "", "B", "/folder-b/", 1),
	)

	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		parentID := knowledgeFolderServiceTestID("folder-b")
		_, err := service.UpdateFolder(
			ctx,
			"kb-1",
			knowledgeFolderServiceTestID("folder-a"),
			&types.KnowledgeFolderUpdateRequest{
				ParentID: &parentID,
			},
		)
		results <- err
	}()
	go func() {
		<-start
		parentID := knowledgeFolderServiceTestID("folder-a")
		_, err := service.UpdateFolder(
			ctx,
			"kb-1",
			knowledgeFolderServiceTestID("folder-b"),
			&types.KnowledgeFolderUpdateRequest{
				ParentID: &parentID,
			},
		)
		results <- err
	}()
	close(start)

	var successCount int
	var cycleCount int
	for attempt := 0; attempt < 2; attempt++ {
		err := <-results
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, ErrKnowledgeFolderCycle):
			cycleCount++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successCount)
	assert.Equal(t, 1, cycleCount)

	folderAID := knowledgeFolderServiceTestID("folder-a")
	folderBID := knowledgeFolderServiceTestID("folder-b")
	folderA, err := repo.GetByID(ctx, 1, "kb-1", folderAID)
	require.NoError(t, err)
	folderB, err := repo.GetByID(ctx, 1, "kb-1", folderBID)
	require.NoError(t, err)
	if folderA.ParentID == folderBID {
		assert.Equal(t, "", folderB.ParentID)
		assert.Equal(t, knowledgeFolderServiceTestPath("/folder-b/"), folderB.Path)
		assert.Equal(t, 1, folderB.Depth)
		assert.Equal(t, knowledgeFolderServiceTestPath("/folder-b/folder-a/"), folderA.Path)
		assert.Equal(t, 2, folderA.Depth)
	} else {
		assert.Equal(t, folderAID, folderB.ParentID)
		assert.Equal(t, "", folderA.ParentID)
		assert.Equal(t, knowledgeFolderServiceTestPath("/folder-a/"), folderA.Path)
		assert.Equal(t, 1, folderA.Depth)
		assert.Equal(t, knowledgeFolderServiceTestPath("/folder-a/folder-b/"), folderB.Path)
		assert.Equal(t, 2, folderB.Depth)
	}
}
