package repository

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestExplicitKnowledgeFoldersSurviveReloadWithoutDocuments(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "empty"))
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "empty/child"))
	// Other tenants and KBs may independently use the same path.
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 2, "kb-tenant-2", "empty"))
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "other", "empty"))
	counts, err := NewKnowledgeRepository(db).ListKnowledgeFolderCounts(ctx, 1, "kb")
	require.NoError(t, err)
	tree := types.BuildKnowledgeFolderTree(counts)
	require.Zero(t, tree.TotalDocumentCount)
	require.Len(t, tree.Folders, 1)
	require.Equal(t, "empty", tree.Folders[0].Path)
	require.Len(t, tree.Folders[0].Children, 1)
	require.Equal(t, "empty/child", tree.Folders[0].Children[0].Path)
	var documents int64
	require.NoError(t, db.Model(&types.Knowledge{}).Count(&documents).Error)
	require.Zero(t, documents, "a folder must not fabricate a document")
}

func TestCreateKnowledgeFolderRejectsExplicitAndDerivedDuplicates(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "empty"))
	require.ErrorIs(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "empty"), ErrKnowledgeFolderConflict)
	insertKnowledgeInFolder(t, db, 1, "kb", "docs/spec", "intro.md")
	require.ErrorIs(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "docs"), ErrKnowledgeFolderConflict)
	require.ErrorIs(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "docs/spec"), ErrKnowledgeFolderConflict)
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "docs/new"))
	require.ErrorIs(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "missing/child"), ErrKnowledgeFolderNotFound)
	counts, err := repo.ListKnowledgeFolderCounts(ctx, 1, "kb")
	require.NoError(t, err)
	require.EqualValues(t, 1, types.BuildKnowledgeFolderTree(counts).TotalDocumentCount)
}

func TestDeleteKnowledgeFolderOnlyDeletesEmptyExplicitMarker(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "docs"))
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "docs/child"))
	require.ErrorIs(t, repo.DeleteKnowledgeFolder(ctx, 1, "kb", "docs"), ErrKnowledgeFolderNotEmpty)
	require.NoError(t, repo.DeleteKnowledgeFolder(ctx, 1, "kb", "docs/child"))
	id := insertKnowledgeInFolder(t, db, 1, "kb", "docs", "real.md")
	require.ErrorIs(t, repo.DeleteKnowledgeFolder(ctx, 1, "kb", "docs"), ErrKnowledgeFolderNotEmpty)
	require.Equal(t, "docs", folderPathOf(t, db, id))
	require.NoError(t, db.Exec("UPDATE knowledges SET parse_status = ? WHERE id = ?",
		types.ParseStatusDeleting, id).Error)
	require.ErrorIs(t, repo.DeleteKnowledgeFolder(ctx, 1, "kb", "docs"), ErrKnowledgeFolderNotEmpty)
	require.NoError(t, db.Exec("DELETE FROM knowledges WHERE id = ?", id).Error)
	require.ErrorIs(t, repo.DeleteKnowledgeFolder(ctx, 2, "kb", "docs"), ErrKnowledgeFolderNotFound)
	require.NoError(t, repo.DeleteKnowledgeFolder(ctx, 1, "kb", "docs"))
	require.ErrorIs(t, repo.DeleteKnowledgeFolder(ctx, 1, "kb", "docs"), ErrKnowledgeFolderNotFound)
}

func TestRenameExplicitKnowledgeFoldersMovesEmptySubtreeAndMerges(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	for _, path := range []string{"docs", "docs/child", "target", "target/child"} {
		require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", path))
	}
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 2, "kb-tenant-2", "docs"))
	id := insertKnowledgeInFolder(t, db, 1, "kb", "docs", "real.md")
	affected, err := repo.RenameKnowledgeFolderPath(ctx, 1, "kb", "docs", "target")
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)
	require.Equal(t, "target", folderPathOf(t, db, id))
	var paths []string
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("tenant_id = 1 AND knowledge_base_id = ?", "kb").Pluck("path", &paths).Error)
	require.ElementsMatch(t, []string{"target", "target/child"}, paths)
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "target/child/child"))
	// An ancestor move must retain new paths that match the old prefix.
	_, err = repo.RenameKnowledgeFolderPath(ctx, 1, "kb", "target/child", "target")
	require.NoError(t, err)
	paths = nil
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("tenant_id = 1 AND knowledge_base_id = ?", "kb").Pluck("path", &paths).Error)
	require.ElementsMatch(t, []string{"target", "target/child"}, paths)
	var other int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("tenant_id = 2 AND path = ?", "docs").Count(&other).Error)
	require.EqualValues(t, 1, other)
}

func TestDeleteKnowledgeFoldersScopesWholeKBCleanup(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	for _, scope := range []struct {
		tenant uint64
		kb     string
	}{{1, "kb"}, {2, "kb-tenant-2"}, {1, "other"}} {
		require.NoError(t, repo.CreateKnowledgeFolder(ctx, scope.tenant, scope.kb, "empty"))
	}
	require.NoError(t, repo.DeleteKnowledgeFolders(ctx, 1, "kb"))
	require.NoError(t, repo.DeleteKnowledgeFolders(ctx, 1, "kb"))
	var count int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestConcurrentCreateKnowledgeFolderSamePath(t *testing.T) {
	repo := NewKnowledgeRepository(setupKnowledgeTestDB(t))
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- repo.CreateKnowledgeFolder(context.Background(), 1, "kb", "same")
		}()
	}
	wg.Wait()
	close(results)
	created, conflicts := 0, 0
	for err := range results {
		if err == nil {
			created++
		} else if errors.Is(err, ErrKnowledgeFolderConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	require.Equal(t, 1, created)
	require.Equal(t, 7, conflicts)
}

func TestRenameKnowledgeFolderMarkerAndDocumentRollbackTogether(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "docs"))
	id := insertKnowledgeInFolder(t, db, 1, "kb", "docs", "real.md")
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_folder_rewrite BEFORE UPDATE OF folder_path ON knowledges
BEGIN SELECT RAISE(ABORT, 'folder rewrite failed'); END`).Error)
	_, err := repo.RenameKnowledgeFolderPath(ctx, 1, "kb", "docs", "renamed")
	require.ErrorContains(t, err, "folder rewrite failed")
	require.Equal(t, "docs", folderPathOf(t, db, id))
	var paths []string
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Pluck("path", &paths).Error)
	require.Equal(t, []string{"docs"}, paths)
}

func TestExplicitKnowledgeFolderSubtreeEscapesWildcards(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	for _, path := range []string{"my_docs", "my_docs/child", "myXdocs", "myXdocs/child", "pct%", "pctX"} {
		require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", path))
	}
	_, err := repo.RenameKnowledgeFolderPath(ctx, 1, "kb", "my_docs", "renamed")
	require.NoError(t, err)
	require.NoError(t, repo.DeleteKnowledgeFolder(ctx, 1, "kb", "pct%"))
	var paths []string
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Pluck("path", &paths).Error)
	require.ElementsMatch(t, []string{"renamed", "renamed/child", "myXdocs", "myXdocs/child", "pctX"}, paths)
}
