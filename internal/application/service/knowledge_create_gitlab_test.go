package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type gitLabKnowledgeRepo struct {
	interfaces.KnowledgeRepository
}

func (r *gitLabKnowledgeRepo) GetKnowledgeTags(context.Context, []string) (map[string][]*types.KnowledgeTag, error) {
	return map[string][]*types.KnowledgeTag{}, nil
}

func TestCreateKnowledgeFromFileGitLabPreservesSourcePaths(t *testing.T) {
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
	create := func(channel, source, file string) (*types.Knowledge, error) {
		t.Helper()
		metadata := map[string]string{}
		if source != "" {
			metadata["datasource_id"] = source
			metadata["external_id"] = "gitlab:instance:1:main:" + file
		}
		return svc.CreateKnowledgeFromFile(ctx, "kb-1", newMultipartFileHeader(t, "README.md", "# same content"),
			metadata, nil, "docs-main/"+file, nil, channel, nil)
	}

	root, err := create(types.ConnectorTypeGitLab, "ds-1", "README.md")
	require.NoError(t, err)
	ds := &types.DataSource{ID: "ds-1", TenantID: 1, KnowledgeBaseID: "kb-1", Type: types.ConnectorTypeGitLab}
	item := &types.FetchedItem{
		ExternalID: "gitlab:instance:1:main:a/b/c/d/e/README.md",
		FileName:   "docs-main/a/b/c/d/e/README.md",
		Content:    []byte("# same content"),
	}
	result := &types.SyncResult{}
	(&DataSourceService{knowledgeService: svc}).applyFetchedItem(ctx, ds, item, nil, result)
	require.Equal(t, 1, result.Created, "identical content at a different repository path must still be imported")
	require.Zero(t, result.Skipped)
	require.Zero(t, result.Failed)
	nested, err := repo.FindByDataSourceExternalID(ctx, ds.TenantID, ds.KnowledgeBaseID, ds.ID, item.ExternalID)
	require.NoError(t, err)
	require.NotNil(t, nested)
	require.NotEqual(t, root.ID, nested.ID)
	require.Equal(t, "docs-main/a/b/c/d/e", nested.FolderPath)

	otherSource, err := create(types.ConnectorTypeGitLab, "ds-2", "README.md")
	require.NoError(t, err, "another data source must retain its own file")
	require.NotEqual(t, root.ID, otherSource.ID)

	duplicate, err := create(types.ConnectorTypeGitLab, "ds-1", "a/b/c/d/e/README.md")
	var dupErr *types.DuplicateKnowledgeError
	require.ErrorAs(t, err, &dupErr)
	require.Equal(t, nested.ID, duplicate.ID, "a retry must match this path's own knowledge")

	for _, channel := range []string{"", types.ConnectorTypeFeishu} {
		_, err := create(channel, "ds-3", "another/README.md")
		require.ErrorAs(t, err, &dupErr, "other import channels retain content deduplication")
	}
	_, err = create(types.ConnectorTypeGitLab, "", "missing-identity/README.md")
	require.ErrorAs(t, err, &dupErr, "incomplete source identity retains content deduplication")
}
