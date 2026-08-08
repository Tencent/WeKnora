package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type folderMoveRepoStub struct {
	interfaces.KnowledgeRepository

	movedIDs    []string
	movedTo     string
	moveCalls   int
	renameFrom  string
	renameTo    string
	renameCalls int
}

func (r *folderMoveRepoStub) UpdateKnowledgeFolderPath(
	_ context.Context, _ uint64, _ string, ids []string, folderPath string,
) (int64, error) {
	r.moveCalls++
	r.movedIDs = ids
	r.movedTo = folderPath
	return int64(len(ids)), nil
}

func (r *folderMoveRepoStub) RenameKnowledgeFolderPath(
	_ context.Context, _ uint64, _ string, from string, to string,
) (int64, error) {
	r.renameCalls++
	r.renameFrom = from
	r.renameTo = to
	return 1, nil
}

func folderMoveContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func TestMoveKnowledgeToFolderNormalizesDestination(t *testing.T) {
	repo := &folderMoveRepoStub{}
	svc := &knowledgeService{repo: repo}

	affected, err := svc.MoveKnowledgeToFolder(folderMoveContext(), "kb-1", []string{"k1", "k2"}, "/docs//spec/")
	require.NoError(t, err)
	assert.Equal(t, int64(2), affected)
	assert.Equal(t, "docs/spec", repo.movedTo)
	assert.Equal(t, []string{"k1", "k2"}, repo.movedIDs)
}

func TestMoveKnowledgeToFolderAcceptsRootAndRejectsEmptyBatch(t *testing.T) {
	repo := &folderMoveRepoStub{}
	svc := &knowledgeService{repo: repo}

	// The empty destination is meaningful: it files documents back at the top level.
	_, err := svc.MoveKnowledgeToFolder(folderMoveContext(), "kb-1", []string{"k1"}, "")
	require.NoError(t, err)
	assert.Equal(t, "", repo.movedTo)

	_, err = svc.MoveKnowledgeToFolder(folderMoveContext(), "kb-1", nil, "docs")
	assert.Error(t, err)
	assert.Equal(t, 1, repo.moveCalls, "an empty batch must not reach the repository")
}

func TestMoveKnowledgeToFolderRejectsTraversalAndUnsafeInput(t *testing.T) {
	repo := &folderMoveRepoStub{}
	svc := &knowledgeService{repo: repo}

	_, err := svc.MoveKnowledgeToFolder(folderMoveContext(), "kb-1", []string{"k1"}, "../../etc/docs")
	require.NoError(t, err)
	assert.Equal(t, "etc/docs", repo.movedTo, "traversal must not escape the knowledge base")

	_, err = svc.MoveKnowledgeToFolder(folderMoveContext(), "kb-1", []string{"k1"}, "<script>alert(1)</script>")
	assert.Error(t, err, "folder names are rendered as tree labels, so unsafe input is rejected")
}

func TestRenameKnowledgeFolderValidatesPaths(t *testing.T) {
	repo := &folderMoveRepoStub{}
	svc := &knowledgeService{repo: repo}
	ctx := folderMoveContext()

	affected, err := svc.RenameKnowledgeFolder(ctx, "kb-1", "docs", "handbook")
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)
	assert.Equal(t, "docs", repo.renameFrom)
	assert.Equal(t, "handbook", repo.renameTo)

	// Renaming a folder into its own subtree would orphan that subtree.
	_, err = svc.RenameKnowledgeFolder(ctx, "kb-1", "docs", "docs/spec")
	assert.Error(t, err)

	// A sibling that merely shares the prefix is a legitimate destination.
	_, err = svc.RenameKnowledgeFolder(ctx, "kb-1", "docs", "docsets")
	require.NoError(t, err)

	_, err = svc.RenameKnowledgeFolder(ctx, "kb-1", "", "handbook")
	assert.Error(t, err)

	_, err = svc.RenameKnowledgeFolder(ctx, "kb-1", "docs", "")
	assert.Error(t, err)

	// Renaming to the same path is a no-op rather than a pointless write.
	before := repo.renameCalls
	affected, err = svc.RenameKnowledgeFolder(ctx, "kb-1", "docs", "/docs/")
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)
	assert.Equal(t, before, repo.renameCalls)
}
