package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCreateKnowledgeFromFileLocalFolderPreservesSourcePaths(t *testing.T) {
	db := setupKnowledgeSharedAccessDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := &gitLabKnowledgeRepo{KnowledgeRepository: repository.NewKnowledgeRepository(db)}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:   &createKnowledgeFileServiceStub{},
		task:      &createKnowledgeTaskEnqueuerStub{},
	}
	ctx := newCreateKnowledgeFileContext()
	create := func(relPath string) (*types.Knowledge, error) {
		t.Helper()
		metadata := map[string]string{"datasource_id": "ds-1", "external_id": "local_folder:" + relPath}
		return svc.CreateKnowledgeFromFile(ctx, "kb-1", newMultipartFileHeader(t, "template.md", "# same template"),
			metadata, nil, relPath, nil, types.ConnectorTypeLocalFolder, nil)
	}

	first, err := create("projects/a/template.md")
	require.NoError(t, err)
	second, err := create("projects/b/template.md")
	require.NoError(t, err, "identical content at another path is a separate note")
	require.NotEqual(t, first.ID, second.ID)

	retry, err := create("projects/a/template.md")
	var dupErr *types.DuplicateKnowledgeError
	require.ErrorAs(t, err, &dupErr)
	require.Equal(t, first.ID, retry.ID, "a retry of the same path matches its own knowledge")
}
