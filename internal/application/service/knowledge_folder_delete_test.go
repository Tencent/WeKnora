package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type folderDeleteRepoStub struct {
	interfaces.KnowledgeRepository

	gotPath  string
	listCall int
	ids      []string
}

func (r *folderDeleteRepoStub) ListKnowledgeIDsByFolderPath(
	_ context.Context, _ uint64, _ string, folderPath string,
) ([]string, error) {
	r.listCall++
	r.gotPath = folderPath
	return r.ids, nil
}

func TestListKnowledgeIDsInFolderNormalizesPath(t *testing.T) {
	repo := &folderDeleteRepoStub{ids: []string{"k1", "k2"}}
	svc := &knowledgeService{repo: repo}

	ids, err := svc.ListKnowledgeIDsInFolder(folderMoveContext(), "kb-1", "/docs//spec/")
	require.NoError(t, err)
	assert.Equal(t, []string{"k1", "k2"}, ids)
	assert.Equal(t, "docs/spec", repo.gotPath, "the repository sees the canonical folder path")
}

func TestListKnowledgeIDsInFolderRejectsRoot(t *testing.T) {
	repo := &folderDeleteRepoStub{}
	svc := &knowledgeService{repo: repo}

	// Anything that normalizes to the empty path is the knowledge base root;
	// deleting it would wipe every document, which is ClearKnowledgeBaseContents'
	// job, not a folder delete's.
	for _, path := range []string{"", "   ", "/", ".."} {
		_, err := svc.ListKnowledgeIDsInFolder(folderMoveContext(), "kb-1", path)
		assert.Error(t, err, "root-equivalent path %q must be rejected", path)
	}
	assert.Equal(t, 0, repo.listCall, "a rejected root path must never reach the repository")
}
