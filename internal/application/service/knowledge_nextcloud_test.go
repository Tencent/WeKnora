package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCreateKnowledgeFromFileNextcloudPreservesSourcePaths(t *testing.T) {
	db := setupKnowledgeSharedAccessDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := &gitLabKnowledgeRepo{KnowledgeRepository: repository.NewKnowledgeRepository(db)}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:   &createKnowledgeFileServiceStub{}, task: &createKnowledgeTaskEnqueuerStub{},
	}
	ctx := newCreateKnowledgeFileContext()
	create := func(source, p string) (*types.Knowledge, error) {
		t.Helper()
		return svc.CreateKnowledgeFromFile(ctx, "kb-1", newMultipartFileHeader(t, "notes.md", "# same content"),
			map[string]string{"datasource_id": source, "external_id": "nextcloud:scope:" + p},
			nil, "Nextcloud"+p, nil, types.ChannelNextcloud, nil)
	}
	first, err := create("ds-1", "/docs/notes.md")
	require.NoError(t, err)
	second, err := create("ds-1", "/docs/sub/notes.md")
	require.NoError(t, err, "same content at different Nextcloud paths must be imported independently")
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, "Nextcloud/docs/sub", second.FolderPath)
	other, err := create("ds-2", "/docs/notes.md")
	require.NoError(t, err, "another data source must retain its own knowledge")
	require.NotEqual(t, first.ID, other.ID)
	duplicate, err := create("ds-1", "/docs/sub/notes.md")
	var dupErr *types.DuplicateKnowledgeError
	require.ErrorAs(t, err, &dupErr)
	require.Equal(t, second.ID, duplicate.ID, "a retry must resolve to the same source identity")
}
