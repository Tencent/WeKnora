package service

import (
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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

func TestCreateKnowledgeFromFile_FolderDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name, firstPath, secondPath, secondContent string
		duplicate                                  bool
	}{
		{"different folders", "a/report.txt", "b/report.txt", "hello", false},
		{"different folders and names", "a/report.txt", "b/other.txt", "hello", false},
		{"same folder", "a/report.txt", "a/report.txt", "hello", true},
		{"renamed in same folder", "a/report.txt", "a/other.txt", "hello", true},
		{"different content", "a/report.txt", "a/report.txt", "world", false},
		{"different types", "a/report.txt", "a/report.md", "hello", false},
		{"root to folder", "report.txt", "a/report.txt", "hello", false},
		{"folder to root", "a/report.txt", "report.txt", "hello", false},
		{"root duplicate", "report.txt", "other.txt", "hello", true},
		{"normalized folder", "a/b/report.txt", `a\b.\other.txt`, "hello", true},
		{"parent and child", "a/report.txt", "a/b/report.txt", "hello", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "upload.db")), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeTagRelation{}, &types.KnowledgeTag{}))
			repo := repository.NewKnowledgeRepository(db)
			files := &createKnowledgeFileServiceStub{}
			queue := &createKnowledgeTaskEnqueuerStub{}
			svc := &knowledgeService{
				repo: repo, fileSvc: files, task: queue,
				kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
			}
			ctx := newCreateKnowledgeFileContext()
			upload := func(name, content string) (*types.Knowledge, error) {
				return svc.CreateKnowledgeFromFile(ctx, "kb-1", newMultipartFileHeader(t, "report.txt", content),
					nil, nil, name, nil, "", nil)
			}
			first, err := upload(tc.firstPath, "hello")
			require.NoError(t, err)
			second, err := upload(tc.secondPath, tc.secondContent)
			if tc.duplicate {
				var duplicate *types.DuplicateKnowledgeError
				require.ErrorAs(t, err, &duplicate)
				require.Equal(t, first.ID, duplicate.Knowledge.ID)
				require.Equal(t, first.ID, second.ID)
				require.Equal(t, 1, files.saveCalls)
				require.Equal(t, 1, queue.calls)
				return
			}
			require.NoError(t, err)
			require.NotEqual(t, first.ID, second.ID)
			persisted, err := repo.GetKnowledgeByID(ctx, first.TenantID, second.ID)
			require.NoError(t, err)
			folder, name := types.SplitKnowledgeRelativePath(tc.secondPath)
			require.Equal(t, folder, persisted.FolderPath)
			require.Equal(t, name, persisted.FileName)
			require.Equal(t, 2, files.saveCalls)
			require.Equal(t, 2, queue.calls)
			_, err = upload(tc.secondPath, tc.secondContent)
			var duplicate *types.DuplicateKnowledgeError
			require.ErrorAs(t, err, &duplicate)
			require.Equal(t, second.ID, duplicate.Knowledge.ID)
			require.Equal(t, 2, files.saveCalls)
			require.Equal(t, 2, queue.calls)
		})
	}
}
