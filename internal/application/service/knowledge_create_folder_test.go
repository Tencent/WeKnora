package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// createKnowledgeInFolder runs the file-upload path with the given
// path-qualified `fileName` (what the browser sends for a folder upload) and
// returns the persisted knowledge row.
func createKnowledgeInFolder(t *testing.T, uploadName, customFileName string) *types.Knowledge {
	t.Helper()

	repo := &createKnowledgeFileRepoStub{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:   &createKnowledgeFileServiceStub{},
		task:      &createKnowledgeTaskEnqueuerStub{},
	}

	_, err := svc.CreateKnowledgeFromFile(
		newCreateKnowledgeFileContext(),
		"kb-1",
		newMultipartFileHeader(t, uploadName, "hello"),
		nil,
		nil,
		customFileName,
		nil,
		"",
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, repo.createdKnowledge)
	return repo.createdKnowledge
}

// A folder upload sends the browser's webkitRelativePath as `fileName`. The
// directory part must land in folder_path while file_name keeps only the base
// name, so the document list shows "design.md" instead of the whole path.
func TestCreateKnowledgeFromFile_FolderUploadSplitsPathAndName(t *testing.T) {
	t.Parallel()

	knowledge := createKnowledgeInFolder(t, "design.md", "docs/spec/design.md")

	require.Equal(t, "docs/spec", knowledge.FolderPath)
	require.Equal(t, "design.md", knowledge.FileName)
	require.Equal(t, "design.md", knowledge.Title)
	require.Equal(t, "md", knowledge.FileType)
}

func TestCreateKnowledgeFromFile_PlainUploadStaysAtRoot(t *testing.T) {
	t.Parallel()

	knowledge := createKnowledgeInFolder(t, "doc.txt", "")

	require.Empty(t, knowledge.FolderPath, "a single-file upload belongs to the KB root")
	require.Equal(t, "doc.txt", knowledge.FileName)
}

// A custom file name without any separator keeps working as a pure rename.
func TestCreateKnowledgeFromFile_CustomNameWithoutFolder(t *testing.T) {
	t.Parallel()

	knowledge := createKnowledgeInFolder(t, "upload.bin", "renamed.md")

	require.Empty(t, knowledge.FolderPath)
	require.Equal(t, "renamed.md", knowledge.FileName)
}

// Path traversal in the relative path must not escape the knowledge base root.
func TestCreateKnowledgeFromFile_FolderUploadRejectsTraversal(t *testing.T) {
	t.Parallel()

	knowledge := createKnowledgeInFolder(t, "design.md", "../../etc/docs/design.md")

	require.Equal(t, "etc/docs", knowledge.FolderPath)
	require.Equal(t, "design.md", knowledge.FileName)
}
