package service

import (
	"context"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inPlaceKS records which knowledge write ingestItem chose for an item.
type inPlaceKS struct {
	interfaces.KnowledgeService
	repo       interfaces.KnowledgeRepository
	calls      []string
	metadata   map[string]string
	replaceErr error
}

func (k *inPlaceKS) GetRepository() interfaces.KnowledgeRepository { return k.repo }

func (k *inPlaceKS) ReplaceKnowledgeFile(
	_ context.Context, knowledgeID string, _ *multipart.FileHeader, customFileName string,
	metadata map[string]string,
) (*types.Knowledge, error) {
	k.calls = append(k.calls, "replace:"+knowledgeID+":"+customFileName)
	k.metadata = metadata
	return &types.Knowledge{ID: knowledgeID}, k.replaceErr
}

func (k *inPlaceKS) DeleteKnowledge(_ context.Context, id string) error {
	k.calls = append(k.calls, "delete:"+id)
	return nil
}

func (k *inPlaceKS) CreateKnowledgeFromFile(
	_ context.Context, _ string, _ *multipart.FileHeader, _ map[string]string, _ *bool,
	customFileName string, _ []string, _ string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	k.calls = append(k.calls, "create:"+customFileName)
	return &types.Knowledge{ID: "new-knowledge"}, nil
}

func inPlaceItem(updateInPlace bool) *types.FetchedItem {
	return &types.FetchedItem{
		ExternalID:    "local_folder:notes/a.md",
		Content:       []byte("# a"),
		FileName:      "notes/a.md",
		UpdateInPlace: updateInPlace,
	}
}

var inPlaceDataSource = &types.DataSource{
	ID: "ds-1", TenantID: 1, KnowledgeBaseID: "kb-1", Type: types.ConnectorTypeLocalFolder,
}

func TestIngestItem_UpdateInPlaceReplacesExistingFileKnowledge(t *testing.T) {
	repo := &deletionLookupKnowledgeRepo{knowledge: &types.Knowledge{ID: "k-1", Type: "file"}}
	ks := &inPlaceKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}

	isUpdate, err := svc.ingestItem(context.Background(), inPlaceDataSource, inPlaceItem(true), nil)

	require.NoError(t, err)
	assert.True(t, isUpdate)
	assert.Equal(t, []string{"replace:k-1:notes/a.md"}, ks.calls, "no delete and no re-create")
	assert.Empty(t, repo.hardDeleted)
	assert.Equal(t, "ds-1", ks.metadata["datasource_id"])
	assert.Equal(t, "local_folder:notes/a.md", ks.metadata["external_id"])
}

func TestApplyFetchedItem_UpdateInPlaceWithUnchangedContentCountsSkipped(t *testing.T) {
	repo := &deletionLookupKnowledgeRepo{knowledge: &types.Knowledge{ID: "k-1", Type: "file"}}
	ks := &inPlaceKS{repo: repo, replaceErr: types.NewDuplicateFileError(&types.Knowledge{ID: "k-1"})}
	svc := &DataSourceService{knowledgeService: ks}
	result := &types.SyncResult{}

	svc.applyFetchedItem(context.Background(), inPlaceDataSource, inPlaceItem(true), nil, result)

	assert.Equal(t, 1, result.Skipped)
	assert.Zero(t, result.Updated)
	assert.Zero(t, result.Failed)
}

func TestIngestItem_UpdateInPlaceCreatesWhenNoKnowledgeExists(t *testing.T) {
	ks := &inPlaceKS{repo: &deletionLookupKnowledgeRepo{}}
	svc := &DataSourceService{knowledgeService: ks}

	isUpdate, err := svc.ingestItem(context.Background(), inPlaceDataSource, inPlaceItem(true), nil)

	require.NoError(t, err)
	assert.False(t, isUpdate)
	assert.Equal(t, []string{"create:notes/a.md"}, ks.calls)
}

func TestIngestItem_WithoutUpdateInPlaceKeepsDeleteAndRecreate(t *testing.T) {
	repo := &deletionLookupKnowledgeRepo{knowledge: &types.Knowledge{ID: "k-1", Type: "file"}}
	ks := &inPlaceKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}

	isUpdate, err := svc.ingestItem(context.Background(), inPlaceDataSource, inPlaceItem(false), nil)

	require.NoError(t, err)
	assert.True(t, isUpdate)
	assert.Equal(t, []string{"delete:k-1", "create:notes/a.md"}, ks.calls)
	assert.Equal(t, []string{"k-1"}, repo.hardDeleted)
}

func TestApplyFetchedItem_EmptiedFileReplacesExistingKnowledgeInPlace(t *testing.T) {
	repo := &deletionLookupKnowledgeRepo{knowledge: &types.Knowledge{ID: "k-1", Type: "file"}}
	ks := &inPlaceKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}
	item := inPlaceItem(true)
	item.Content = nil
	result := &types.SyncResult{}

	svc.applyFetchedItem(context.Background(), inPlaceDataSource, item, nil, result)

	assert.Equal(t, 1, result.Updated)
	assert.Zero(t, result.Skipped)
	assert.Equal(t, []string{"replace:k-1:notes/a.md"}, ks.calls, "same identity, old content replaced")
}

func TestApplyFetchedItem_EmptyItemWithoutUpdateInPlaceIsStillSkipped(t *testing.T) {
	repo := &deletionLookupKnowledgeRepo{knowledge: &types.Knowledge{ID: "k-1", Type: "file"}}
	ks := &inPlaceKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}
	item := inPlaceItem(false)
	item.Content = nil
	result := &types.SyncResult{}

	svc.applyFetchedItem(context.Background(), inPlaceDataSource, item, nil, result)

	assert.Equal(t, 1, result.Skipped)
	assert.Empty(t, ks.calls)
}
