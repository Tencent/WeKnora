package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newKnowledgeFolderServiceHarness(t *testing.T) (KnowledgeFolderService, context.Context, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledge_bases (
			id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, deleted_at DATETIME
		);
		INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 1);
		CREATE TABLE knowledge_folders (
			id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, knowledge_base_id TEXT NOT NULL,
			parent_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, path TEXT NOT NULL,
			depth INTEGER NOT NULL, sort_order INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME, updated_at DATETIME, deleted_at DATETIME,
			UNIQUE (tenant_id, knowledge_base_id, parent_id, name)
		);
		CREATE TABLE knowledges (
			id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, knowledge_base_id TEXT NOT NULL,
			folder_id TEXT NOT NULL DEFAULT '', type TEXT, title TEXT, description TEXT, source TEXT,
			channel TEXT, parse_status TEXT, pending_subtasks_count INTEGER, summary_status TEXT,
			enable_status TEXT, embedding_model_id TEXT, file_name TEXT, file_type TEXT, file_size INTEGER,
			file_hash TEXT, file_path TEXT, storage_size INTEGER, metadata TEXT, last_faq_import_result TEXT,
			created_at DATETIME, updated_at DATETIME, processed_at DATETIME, error_message TEXT, deleted_at DATETIME
		);
	`).Error)
	folderRepo := repository.NewKnowledgeFolderRepository(db)
	knowledgeRepo := repository.NewKnowledgeRepository(db)
	kbRepo := repository.NewKnowledgeBaseRepository(db)
	svc := NewKnowledgeFolderService(folderRepo, knowledgeRepo, kbRepo)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	return svc, ctx, db
}

func createFolder(
	t *testing.T, svc KnowledgeFolderService, ctx context.Context, kbID, parentID, name string,
) *types.KnowledgeFolder {
	t.Helper()
	folder, err := svc.CreateFolder(ctx, kbID, &types.CreateFolderRequest{ParentID: parentID, Name: name})
	require.NoError(t, err)
	return folder
}

func TestKnowledgeFolderService_ResolveOrCreatePathsRootNamedSharedAndIdempotent(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)

	first, err := svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
		Paths: []string{"Project/docs/2026", "Project/assets", "Project/docs", ""},
	})
	require.NoError(t, err)
	require.Len(t, first.Paths, 4)
	byPath := make(map[string]string, len(first.Paths))
	for _, resolved := range first.Paths {
		byPath[resolved.RelativePath] = resolved.FolderID
	}
	require.NotEmpty(t, byPath["Project/docs/2026"])
	require.NotEmpty(t, byPath["Project/assets"])
	require.NotEmpty(t, byPath["Project/docs"])
	require.Empty(t, byPath[""])

	var projectCount, docsCount int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("tenant_id = 1 AND knowledge_base_id = 'kb-1' AND parent_id = '' AND name = 'Project'").
		Count(&projectCount).Error)
	require.Equal(t, int64(1), projectCount, "shared prefixes must be created once")
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("id = ? AND parent_id = ?", byPath["Project/docs/2026"], byPath["Project/docs"]).
		Count(&docsCount).Error)
	require.Equal(t, int64(1), docsCount)

	second, err := svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
		Paths: []string{"Project/docs/2026", "Project/docs"},
	})
	require.NoError(t, err)
	require.Equal(t, byPath["Project/docs/2026"], second.Paths[0].FolderID)
	require.Equal(t, byPath["Project/docs"], second.Paths[1].FolderID)

	anchor := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "anchor")
	named, err := svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
		CurrentFolderID: anchor.ID,
		Paths:           []string{"existing/child", ""},
	})
	require.NoError(t, err)
	require.Len(t, named.Paths, 2)
	require.Equal(t, anchor.ID, named.Paths[1].FolderID)
	child, err := svc.GetFolder(ctx, "kb-1", named.Paths[0].FolderID)
	require.NoError(t, err)
	require.Equal(t, 3, child.Depth)
}

func TestKnowledgeFolderService_ResolveOrCreatePathsReusesExistingPrefix(t *testing.T) {
	svc, ctx, _ := newKnowledgeFolderServiceHarness(t)
	existing := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "Project")

	resolved, err := svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
		Paths: []string{"Project/docs"},
	})
	require.NoError(t, err)
	docs, err := svc.GetFolder(ctx, "kb-1", resolved.Paths[0].FolderID)
	require.NoError(t, err)
	require.Equal(t, existing.ID, docs.ParentID)
}

func TestKnowledgeFolderService_ResolveOrCreatePathsRejectsInvalidBatchWithoutWrites(t *testing.T) {
	invalidPaths := []string{
		"/absolute", `Project\docs`, "Project//docs", "Project/./docs", "Project/../docs",
		"Project/control\x00name", "Project/" + strings.Repeat("x", 256),
	}
	for _, invalid := range invalidPaths {
		t.Run(fmt.Sprintf("%q", invalid), func(t *testing.T) {
			svc, ctx, db := newKnowledgeFolderServiceHarness(t)
			_, err := svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
				Paths: []string{"would-be-created", invalid},
			})
			require.ErrorIs(t, err, types.ErrInvalidArgument)
			var count int64
			require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&count).Error)
			require.Zero(t, count, "all path syntax must be validated before the transaction writes")
		})
	}

	svc, ctx, _ := newKnowledgeFolderServiceHarness(t)
	_, err := svc.ResolveOrCreatePaths(ctx, "kb-1", nil)
	require.ErrorIs(t, err, types.ErrInvalidArgument)
	_, err = svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{})
	require.ErrorIs(t, err, types.ErrInvalidArgument)
	_, err = svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
		Paths: make([]string, types.MaxResolveFolderPaths+1),
	})
	require.ErrorIs(t, err, types.ErrInvalidArgument)
}

func TestKnowledgeFolderService_ResolveOrCreatePathsRejectsPreflightBoundsWithoutWrites(t *testing.T) {
	tests := map[string]string{
		"too many segments": strings.Join([]string{
			"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k",
		}, "/"),
		"whole path too long": strings.Join([]string{
			strings.Repeat("a", 255), strings.Repeat("b", 255), strings.Repeat("c", 255),
			strings.Repeat("d", 255), strings.Repeat("e", 255), strings.Repeat("f", 255),
			strings.Repeat("g", 255), strings.Repeat("h", 255), strings.Repeat("i", 255),
			strings.Repeat("j", 256),
		}, "/"),
	}
	for name, invalid := range tests {
		t.Run(name, func(t *testing.T) {
			svc, ctx, db := newKnowledgeFolderServiceHarness(t)
			_, err := svc.ResolveOrCreatePaths(ctx, "missing-kb", &types.ResolveFolderPathsRequest{
				Paths: []string{"would-be-created", invalid},
			})
			require.ErrorIs(t, err, types.ErrInvalidArgument)
			var count int64
			require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&count).Error)
			require.Zero(t, count, "path bounds must be rejected during preflight before any folder write")
		})
	}
}

func TestKnowledgeFolderService_ResolveOrCreatePathsRejectsDriveAbsolutePathWithoutWrites(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)

	_, err := svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
		Paths: []string{"would-be-created", "C:/Project/docs"},
	})
	require.ErrorIs(t, err, types.ErrInvalidArgument)
	var count int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&count).Error)
	require.Zero(t, count, "drive-style absolute paths must be rejected before any folder write")
}

func TestKnowledgeFolderService_ResolveOrCreatePathsPreservesSegmentWhitespace(t *testing.T) {
	svc, ctx, _ := newKnowledgeFolderServiceHarness(t)
	resolved, err := svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
		Paths: []string{" Project / docs "},
	})
	require.NoError(t, err)
	leaf, err := svc.GetFolder(ctx, "kb-1", resolved.Paths[0].FolderID)
	require.NoError(t, err)
	require.Equal(t, " docs ", leaf.Name)
}

func TestKnowledgeFolderService_ResolveOrCreatePathsDepthAndAnchorFailuresRollback(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	anchor := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "anchor")
	for depth := 2; depth <= 9; depth++ {
		anchor = createFolder(t, svc, ctx, "kb-1", anchor.ID, fmt.Sprintf("depth-%d", depth))
	}
	var before int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&before).Error)

	_, err := svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
		CurrentFolderID: anchor.ID,
		Paths:           []string{"allowed", "too/deep"},
	})
	require.ErrorIs(t, err, types.ErrInvalidArgument)
	var after int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&after).Error)
	require.Equal(t, before, after, "depth failure must roll back every new folder")

	_, err = svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
		CurrentFolderID: "missing",
		Paths:           []string{"new"},
	})
	require.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&after).Error)
	require.Equal(t, before, after)
}

func TestKnowledgeFolderService_ResolveOrCreatePathsDatabaseFailureRollsBack(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER fail_folder_insert BEFORE INSERT ON knowledge_folders
		WHEN NEW.name = 'fail' BEGIN SELECT RAISE(FAIL, 'forced insert failure'); END;
	`).Error)

	_, err := svc.ResolveOrCreatePaths(ctx, "kb-1", &types.ResolveFolderPathsRequest{
		Paths: []string{"created-first", "fail/child"},
	})
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&count).Error)
	require.Zero(t, count, "database failure must roll back folders inserted earlier in the batch")
}

func TestKnowledgeFolderService_ValidatesNamesAndSiblingUniqueness(t *testing.T) {
	svc, ctx, _ := newKnowledgeFolderServiceHarness(t)
	for _, name := range []string{"", "  ", ".", "..", "/", `\`, "a/b", `a\b`, "a\nb"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			_, err := svc.CreateFolder(ctx, "kb-1", &types.CreateFolderRequest{Name: name})
			assert.ErrorIs(t, err, types.ErrInvalidArgument)
		})
	}
	folder := createFolder(t, svc, ctx, "kb-1", "", "  valid  ")
	assert.Equal(t, "valid", folder.Name)
	assert.Equal(t, "/"+folder.ID, folder.Path)
	_, err := svc.CreateFolder(ctx, "kb-1", &types.CreateFolderRequest{Name: "valid"})
	assert.ErrorIs(t, err, types.ErrFolderAlreadyExists)
}

func TestKnowledgeFolderService_DepthMoveBreadcrumbAndTreeCounts(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	parent := createFolder(t, svc, ctx, "kb-1", "", "parent")
	child := createFolder(t, svc, ctx, "kb-1", parent.ID, "child")
	grandchild := createFolder(t, svc, ctx, "kb-1", child.ID, "grandchild")

	breadcrumb, err := svc.GetBreadcrumb(ctx, "kb-1", grandchild.ID)
	require.NoError(t, err)
	require.Len(t, breadcrumb, 3)
	assert.Equal(t, []string{parent.ID, child.ID, grandchild.ID}, []string{
		breadcrumb[0].ID, breadcrumb[1].ID, breadcrumb[2].ID,
	})

	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id) VALUES (?, 1, 'kb-1', ?), (?, 1, 'kb-1', ?)",
		"doc-parent", parent.ID, "doc-grandchild", grandchild.ID,
	).Error)
	tree, err := svc.GetTree(ctx, "kb-1")
	require.NoError(t, err)
	require.Len(t, tree, 1)
	assert.Equal(t, int64(2), tree[0].KnowledgeCount)
	assert.Equal(t, int64(1), tree[0].Children[0].KnowledgeCount)

	_, err = svc.MoveFolder(ctx, "kb-1", parent.ID, &types.MoveFolderRequest{ParentID: grandchild.ID})
	assert.ErrorIs(t, err, types.ErrInvalidArgument)
	_, err = svc.MoveFolder(ctx, "kb-1", parent.ID, &types.MoveFolderRequest{ParentID: parent.ID})
	assert.ErrorIs(t, err, types.ErrInvalidArgument)

	deep := grandchild
	for i := 4; i <= types.MaxFolderDepth; i++ {
		deep = createFolder(t, svc, ctx, "kb-1", deep.ID, fmt.Sprintf("level-%d", i))
	}
	_, err = svc.CreateFolder(ctx, "kb-1", &types.CreateFolderRequest{ParentID: deep.ID, Name: "too-deep"})
	assert.ErrorIs(t, err, types.ErrInvalidArgument)
}

func TestKnowledgeFolderService_MoveUpdatesStablePathsAndRename(t *testing.T) {
	svc, ctx, _ := newKnowledgeFolderServiceHarness(t)
	left := createFolder(t, svc, ctx, "kb-1", "", "left")
	right := createFolder(t, svc, ctx, "kb-1", "", "right")
	child := createFolder(t, svc, ctx, "kb-1", left.ID, "child")
	grandchild := createFolder(t, svc, ctx, "kb-1", child.ID, "grandchild")

	renamed, err := svc.UpdateFolder(ctx, "kb-1", child.ID, &types.UpdateFolderRequest{Name: " renamed "})
	require.NoError(t, err)
	assert.Equal(t, "renamed", renamed.Name)
	assert.Equal(t, left.Path+"/"+child.ID, renamed.Path)

	moved, err := svc.MoveFolder(ctx, "kb-1", child.ID, &types.MoveFolderRequest{ParentID: right.ID})
	require.NoError(t, err)
	assert.Equal(t, right.ID, moved.ParentID)
	assert.Equal(t, right.Path+"/"+child.ID, moved.Path)
	assert.Equal(t, 2, moved.Depth)

	updatedGrandchild, err := svc.GetFolder(ctx, "kb-1", grandchild.ID)
	require.NoError(t, err)
	assert.Equal(t, moved.Path+"/"+grandchild.ID, updatedGrandchild.Path)
	assert.Equal(t, 3, updatedGrandchild.Depth)
}

func TestKnowledgeFolderService_MoveChecksWholeSubtreeDepth(t *testing.T) {
	svc, ctx, _ := newKnowledgeFolderServiceHarness(t)
	source := createFolder(t, svc, ctx, "kb-1", "", "source")
	leaf := source
	for i := 2; i <= 4; i++ {
		leaf = createFolder(t, svc, ctx, "kb-1", leaf.ID, fmt.Sprintf("source-%d", i))
	}
	target := createFolder(t, svc, ctx, "kb-1", "", "target")
	for i := 2; i <= 8; i++ {
		target = createFolder(t, svc, ctx, "kb-1", target.ID, fmt.Sprintf("target-%d", i))
	}
	_, err := svc.MoveFolder(ctx, "kb-1", source.ID, &types.MoveFolderRequest{ParentID: target.ID})
	assert.ErrorIs(t, err, types.ErrInvalidArgument)
}

func TestKnowledgeFolderService_RootBreadcrumbStillValidatesScope(t *testing.T) {
	svc, _, _ := newKnowledgeFolderServiceHarness(t)
	otherTenant := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2))
	_, err := svc.GetBreadcrumb(otherTenant, "kb-1", types.FolderRootID)
	assert.Error(t, err)
}

func TestKnowledgeFolderService_ResolveKnowledgeScope(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	folder := createFolder(t, svc, ctx, "kb-1", "", "folder")
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id) VALUES ('doc', 1, 'kb-1', ?)", folder.ID,
	).Error)
	scope, err := svc.ResolveKnowledgeScope(ctx, "kb-1", []string{folder.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"doc"}, scope.KnowledgeIDs)
	assert.False(t, scope.FullKnowledgeBase)
}

func TestKnowledgeFolderService_StructuralWritesAcquireKnowledgeBaseLock(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	lockCalls := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(
		"test:count_knowledge_base_locks", func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "knowledge_bases" {
				lockCalls++
			}
		},
	))

	folder := createFolder(t, svc, ctx, "kb-1", "", "folder")
	assert.Equal(t, 1, lockCalls)
	_, err := svc.UpdateFolder(ctx, "kb-1", folder.ID, &types.UpdateFolderRequest{Name: "renamed"})
	require.NoError(t, err)
	assert.Equal(t, 2, lockCalls)
	parent := createFolder(t, svc, ctx, "kb-1", "", "parent")
	assert.Equal(t, 3, lockCalls)
	_, err = svc.MoveFolder(ctx, "kb-1", folder.ID, &types.MoveFolderRequest{ParentID: parent.ID})
	require.NoError(t, err)
	assert.Equal(t, 4, lockCalls)
}

func TestKnowledgeFolderService_RenameAndMoveNoOpsSkipFolderWrites(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	folder := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "folder")
	folderWrites := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(
		"test:count_knowledge_folder_writes", func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "knowledge_folders" {
				folderWrites++
			}
		},
	))

	renamed, err := svc.UpdateFolder(
		ctx, "kb-1", folder.ID, &types.UpdateFolderRequest{Name: "  folder  "},
	)
	require.NoError(t, err)
	assert.Equal(t, folder.Name, renamed.Name)
	assert.Equal(t, folder.ParentID, renamed.ParentID)
	assert.Equal(t, folder.Path, renamed.Path)
	assert.Equal(t, folder.Depth, renamed.Depth)
	assert.Zero(t, folderWrites)

	moved, err := svc.MoveFolder(
		ctx, "kb-1", folder.ID, &types.MoveFolderRequest{ParentID: types.FolderRootID},
	)
	require.NoError(t, err)
	assert.Equal(t, folder.Name, moved.Name)
	assert.Equal(t, folder.ParentID, moved.ParentID)
	assert.Equal(t, folder.Path, moved.Path)
	assert.Equal(t, folder.Depth, moved.Depth)
	assert.Zero(t, folderWrites)
}

func TestKnowledgeFolderService_RenameConflictMapsDomainError(t *testing.T) {
	svc, ctx, _ := newKnowledgeFolderServiceHarness(t)
	first := createFolder(t, svc, ctx, "kb-1", "", "first")
	createFolder(t, svc, ctx, "kb-1", "", "second")
	_, err := svc.UpdateFolder(ctx, "kb-1", first.ID, &types.UpdateFolderRequest{Name: "second"})
	assert.ErrorIs(t, err, types.ErrFolderAlreadyExists)
}

func TestKnowledgeFolderService_CreateKnowledgeInFolderValidatesAfterKBLock(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	folder := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "current")
	lockCalls := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(
		"test:count_assignment_knowledge_base_locks", func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "knowledge_bases" {
				lockCalls++
			}
		},
	))

	knowledge := &types.Knowledge{ID: "doc-current", TenantID: 1, KnowledgeBaseID: "kb-1"}
	require.NoError(t, svc.CreateKnowledgeInFolder(ctx, knowledge, folder.ID))
	assert.Equal(t, 1, lockCalls)
	var stored types.Knowledge
	require.NoError(t, db.First(&stored, "id = ?", knowledge.ID).Error)
	assert.Equal(t, folder.ID, stored.FolderID)

	invalid := &types.Knowledge{ID: "doc-invalid", TenantID: 1, KnowledgeBaseID: "kb-1"}
	err := svc.CreateKnowledgeInFolder(ctx, invalid, "missing-folder")
	assert.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)
	assert.Equal(t, 2, lockCalls)
	var count int64
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", invalid.ID).Count(&count).Error)
	assert.Zero(t, count, "folder validation and knowledge insert must roll back together")
}

func TestKnowledgeFolderService_RejectsCrossKBFolderForCreateAndMove(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-2', 1)").Error)
	foreign := &types.KnowledgeFolder{
		ID: "folder-kb-2", TenantID: 1, KnowledgeBaseID: "kb-2", ParentID: types.FolderRootID,
		Name: "foreign", Path: "/folder-kb-2", Depth: 1,
	}
	require.NoError(t, db.Create(foreign).Error)

	invalid := &types.Knowledge{ID: "doc-cross-create", TenantID: 1, KnowledgeBaseID: "kb-1"}
	err := svc.CreateKnowledgeInFolder(ctx, invalid, foreign.ID)
	assert.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)

	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc-cross-move", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: types.FolderRootID,
	}).Error)
	err = svc.MoveKnowledgeToFolder(ctx, "doc-cross-move", foreign.ID)
	assert.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)
	var folderID string
	require.NoError(t, db.Model(&types.Knowledge{}).Select("folder_id").Where("id = ?", "doc-cross-move").Scan(&folderID).Error)
	assert.Equal(t, types.FolderRootID, folderID)
}

func TestKnowledgeFolderService_MoveKnowledgeNoOpAndRollback(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	folder := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "current")
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: folder.ID,
	}).Error)
	knowledgeWrites := 0
	lockCalls := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(
		"test:count_move_updates", func(tx *gorm.DB) {
			if tx.Statement == nil {
				return
			}
			switch tx.Statement.Table {
			case "knowledge_bases":
				lockCalls++
			case "knowledges":
				knowledgeWrites++
			}
		},
	))

	require.NoError(t, svc.MoveKnowledgeToFolder(ctx, "doc", folder.ID))
	assert.Equal(t, 1, lockCalls)
	assert.Zero(t, knowledgeWrites, "same-folder move must not issue a document update")

	err := svc.MoveKnowledgeToFolder(ctx, "doc", "missing-folder")
	assert.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)
	assert.Equal(t, 2, lockCalls)
	assert.Zero(t, knowledgeWrites)
	var stored types.Knowledge
	require.NoError(t, db.First(&stored, "id = ?", "doc").Error)
	assert.Equal(t, folder.ID, stored.FolderID)
}

func TestKnowledgeFolderService_CreateAndMoveToRootAreValid(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	knowledge := &types.Knowledge{ID: "doc-root", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: "stale"}
	require.NoError(t, svc.CreateKnowledgeInFolder(ctx, knowledge, types.FolderRootID))
	require.Empty(t, knowledge.FolderID)

	folder := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "target")
	require.NoError(t, svc.MoveKnowledgeToFolder(ctx, knowledge.ID, folder.ID))
	require.NoError(t, svc.MoveKnowledgeToFolder(ctx, knowledge.ID, types.FolderRootID))

	var stored types.Knowledge
	require.NoError(t, db.First(&stored, "id = ?", knowledge.ID).Error)
	require.Empty(t, stored.FolderID)
}

func TestKnowledgeFolderService_BatchMoveIsAtomicAndRejectsCrossKB(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	target := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "target")
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-2', 1)").Error)
	require.NoError(t, db.Create([]*types.Knowledge{
		{ID: "doc-a", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: types.FolderRootID},
		{ID: "doc-b", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: types.FolderRootID},
		{ID: "doc-other-kb", TenantID: 1, KnowledgeBaseID: "kb-2", FolderID: types.FolderRootID},
	}).Error)

	require.NoError(t, svc.MoveKnowledgeBatchToFolder(ctx, []string{"doc-a", "doc-b"}, target.ID))
	var moved int64
	require.NoError(t, db.Model(&types.Knowledge{}).
		Where("id IN ? AND folder_id = ?", []string{"doc-a", "doc-b"}, target.ID).Count(&moved).Error)
	require.Equal(t, int64(2), moved)

	err := svc.MoveKnowledgeBatchToFolder(ctx, []string{"doc-a", "doc-other-kb"}, types.FolderRootID)
	require.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)
	var folderID string
	require.NoError(t, db.Model(&types.Knowledge{}).Select("folder_id").Where("id = ?", "doc-a").Scan(&folderID).Error)
	require.Equal(t, target.ID, folderID, "cross-KB batches must not partially update valid documents")
}

func TestBatchFolderMixedMovePrevalidatesWithoutPartialWrites(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	source := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "source")
	child := createFolder(t, svc, ctx, "kb-1", source.ID, "child")
	target := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "target")
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc-mixed", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: source.ID,
	}).Error)

	err := svc.MoveBatchToFolder(ctx, "kb-1", []string{"doc-mixed"}, []string{source.ID}, child.ID)
	require.ErrorIs(t, err, types.ErrInvalidArgument)
	var doc types.Knowledge
	require.NoError(t, db.First(&doc, "id = ?", "doc-mixed").Error)
	require.Equal(t, source.ID, doc.FolderID, "failed folder validation must prevent document writes")
	unchanged, err := svc.GetFolder(ctx, "kb-1", source.ID)
	require.NoError(t, err)
	require.Equal(t, types.FolderRootID, unchanged.ParentID)

	require.NoError(t, svc.MoveBatchToFolder(
		ctx, "kb-1", []string{"doc-mixed"}, []string{source.ID}, target.ID,
	))
	require.NoError(t, db.First(&doc, "id = ?", "doc-mixed").Error)
	require.Equal(t, target.ID, doc.FolderID)
	moved, err := svc.GetFolder(ctx, "kb-1", source.ID)
	require.NoError(t, err)
	require.Equal(t, target.ID, moved.ParentID)
}

func TestBatchFolderDeleteEmptySubtreeRetainsNonEmptyOnFailure(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	folder := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "folder")
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc-delete", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: folder.ID,
	}).Error)

	err := svc.DeleteEmptySubtrees(ctx, "kb-1", []string{folder.ID})
	require.ErrorIs(t, err, types.ErrFolderNotEmpty)
	_, err = svc.GetFolder(ctx, "kb-1", folder.ID)
	require.NoError(t, err, "failed knowledge deletion must retain folders for retry")

	require.NoError(t, db.Delete(&types.Knowledge{}, "id = ?", "doc-delete").Error)
	require.NoError(t, svc.DeleteEmptySubtrees(ctx, "kb-1", []string{folder.ID}))
	_, err = svc.GetFolder(ctx, "kb-1", folder.ID)
	require.Error(t, err)
}

type batchFolderScopeServiceStub struct {
	interfaces.KnowledgeFolderService
	scope *types.FolderKnowledgeScope
	err   error
}

func (s *batchFolderScopeServiceStub) ResolveKnowledgeScope(
	context.Context, string, []string,
) (*types.FolderKnowledgeScope, error) {
	return s.scope, s.err
}

type batchFolderKnowledgeRepoStub struct {
	interfaces.KnowledgeRepository
	all []*types.Knowledge
}

func (s *batchFolderKnowledgeRepoStub) ListKnowledgeByKnowledgeBaseID(
	context.Context, uint64, string,
) ([]*types.Knowledge, error) {
	return s.all, nil
}

func TestBatchFolderScopeMergeEmptyAndFullEnumeration(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	t.Run("explicit and folder merge stable dedup", func(t *testing.T) {
		svc := &knowledgeService{folderService: &batchFolderScopeServiceStub{scope: &types.FolderKnowledgeScope{
			KnowledgeIDs: []string{"shared", "folder"},
		}}}
		ids, err := svc.ResolveBatchKnowledgeScope(
			ctx, "kb-1", []string{" explicit ", "shared", "shared", ""}, []string{"folder-1"}, true,
		)
		require.NoError(t, err)
		require.Equal(t, []string{"explicit", "shared", "folder"}, ids)
	})
	t.Run("named empty scope stays empty", func(t *testing.T) {
		svc := &knowledgeService{folderService: &batchFolderScopeServiceStub{scope: &types.FolderKnowledgeScope{}}}
		ids, err := svc.ResolveBatchKnowledgeScope(ctx, "kb-1", nil, []string{"empty"}, true)
		require.NoError(t, err)
		require.Empty(t, ids)
	})
	t.Run("root full KB explicitly enumerates destructive scope", func(t *testing.T) {
		svc := &knowledgeService{
			folderService: &batchFolderScopeServiceStub{scope: &types.FolderKnowledgeScope{FullKnowledgeBase: true}},
			repo: &batchFolderKnowledgeRepoStub{all: []*types.Knowledge{
				{ID: "root-doc"}, {ID: "nested-doc"},
			}},
		}
		ids, err := svc.ResolveBatchKnowledgeScope(ctx, "kb-1", nil, []string{types.FolderRootID}, true)
		require.NoError(t, err)
		require.Equal(t, []string{"root-doc", "nested-doc"}, ids)
		require.NotNil(t, ids)
	})
}

func TestBatchFolderFinalizeRootDeletesAllRealFolders(t *testing.T) {
	svc, ctx, db := newKnowledgeFolderServiceHarness(t)
	parent := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "root-parent-all")
	createFolder(t, svc, ctx, "kb-1", parent.ID, "root-child-all")
	require.NoError(t, svc.DeleteEmptySubtrees(ctx, "kb-1", []string{types.FolderRootID}))
	var count int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestBatchFolderMixedMoveRejectsSelectedNameConflictBeforeWrites(t *testing.T) {
	svc, ctx, _ := newKnowledgeFolderServiceHarness(t)
	left := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "conflict-left")
	right := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "conflict-right")
	target := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "conflict-target")
	first := createFolder(t, svc, ctx, "kb-1", left.ID, "same-selected")
	second := createFolder(t, svc, ctx, "kb-1", right.ID, "same-selected")

	err := svc.MoveBatchToFolder(ctx, "kb-1", nil, []string{first.ID, second.ID}, target.ID)
	require.ErrorIs(t, err, types.ErrFolderAlreadyExists)
	unchangedFirst, err := svc.GetFolder(ctx, "kb-1", first.ID)
	require.NoError(t, err)
	unchangedSecond, err := svc.GetFolder(ctx, "kb-1", second.ID)
	require.NoError(t, err)
	require.Equal(t, left.ID, unchangedFirst.ParentID)
	require.Equal(t, right.ID, unchangedSecond.ParentID)
}

func TestBatchFolderMixedMoveRejectsExistingTargetNameBeforeWrites(t *testing.T) {
	svc, ctx, _ := newKnowledgeFolderServiceHarness(t)
	source := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "existing-source")
	target := createFolder(t, svc, ctx, "kb-1", types.FolderRootID, "existing-target")
	moving := createFolder(t, svc, ctx, "kb-1", source.ID, "same-existing")
	createFolder(t, svc, ctx, "kb-1", target.ID, "same-existing")

	err := svc.MoveBatchToFolder(ctx, "kb-1", nil, []string{moving.ID}, target.ID)
	require.ErrorIs(t, err, types.ErrFolderAlreadyExists)
	unchanged, err := svc.GetFolder(ctx, "kb-1", moving.ID)
	require.NoError(t, err)
	require.Equal(t, source.ID, unchanged.ParentID)
}
