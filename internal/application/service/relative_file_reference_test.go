package service

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type relativeReferenceRepoStub struct {
	interfaces.KnowledgeRepository
	files     map[string]*types.Knowledge
	knowledge *types.Knowledge
}

func (r *relativeReferenceRepoStub) FindKnowledgeByLogicalPath(_ context.Context, _ uint64, _ string, folderPath, fileName string) (*types.Knowledge, error) {
	return r.files[folderPath+"/"+fileName], nil
}

func (r *relativeReferenceRepoStub) GetKnowledgeByID(_ context.Context, _ uint64, _ string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func newRelativeReferenceService() *knowledgeService {
	return &knowledgeService{repo: &relativeReferenceRepoStub{files: map[string]*types.Knowledge{
		"docs/images/a.png":       {FilePath: types.BuildResourcePath(strings.Repeat("a", types.ResourceHandleLength))},
		"docs/images/foo(1).png":  {FilePath: types.BuildResourcePath(strings.Repeat("b", types.ResourceHandleLength))},
		"docs/images/foo bar.png": {FilePath: types.BuildResourcePath(strings.Repeat("c", types.ResourceHandleLength))},
	}}}
}

func TestRewriteRelativeMarkdownImages(t *testing.T) {
	svc := newRelativeReferenceService()
	imageA := types.BuildResourcePath(strings.Repeat("a", types.ResourceHandleLength))
	imageParen := types.BuildResourcePath(strings.Repeat("b", types.ResourceHandleLength))
	imageSpace := types.BuildResourcePath(strings.Repeat("c", types.ResourceHandleLength))

	input := "![plain](images/a.png)\n" +
		"![title](images/a.png \"logo\")\n" +
		"![paren](images/foo\\(1\\).png)\n" +
		"![space](<images/foo bar.png>)\n" +
		"`![code](images/a.png)`\n" +
		"```md\n![fenced](images/a.png)\n```\n" +
		"![remote](https://example.com/a.png)\n" +
		"![missing](images/missing.png)\n"

	got := svc.RewriteRelativeMarkdownImages(context.Background(), 1, "kb-1", "docs", input)
	require.Contains(t, got, "![plain]("+imageA+")")
	require.Contains(t, got, "![title]("+imageA+" \"logo\")")
	require.Contains(t, got, "![paren]("+imageParen+")")
	require.Contains(t, got, "![space](<"+imageSpace+">)")
	require.Contains(t, got, "`![code](images/a.png)`")
	require.Contains(t, got, "![fenced](images/a.png)")
	require.Contains(t, got, "![remote](https://example.com/a.png)")
	require.Contains(t, got, "![missing](images/missing.png)")
}

func TestGetKnowledgePreviewFileRewritesWithoutChangingDownloadSource(t *testing.T) {
	svc := newRelativeReferenceService()
	original := "![logo](images/a.png)"
	knowledge := &types.Knowledge{
		ID: "readme", Type: types.KnowledgeTypeManual, FileType: "md",
		KnowledgeBaseID: "kb-1", FolderPath: "docs", FileName: "README.md",
	}
	require.NoError(t, knowledge.SetManualMetadata(types.NewManualKnowledgeMetadata(original, types.ManualKnowledgeStatusDraft, 1)))
	svc.repo.(*relativeReferenceRepoStub).knowledge = knowledge

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	preview, _, err := svc.GetKnowledgePreviewFile(ctx, knowledge.ID)
	require.NoError(t, err)
	previewBody, err := io.ReadAll(preview)
	require.NoError(t, err)
	require.NoError(t, preview.Close())
	require.Contains(t, string(previewBody), types.BuildResourcePath(strings.Repeat("a", types.ResourceHandleLength)))

	download, _, err := svc.GetKnowledgeFile(ctx, knowledge.ID)
	require.NoError(t, err)
	downloadBody, err := io.ReadAll(download)
	require.NoError(t, err)
	require.NoError(t, download.Close())
	require.Equal(t, original, string(downloadBody))
}
