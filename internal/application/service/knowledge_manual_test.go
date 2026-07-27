package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// TestSanitizeManualDownloadFilename covers the filename-sanitization logic used
// by the manual-knowledge download path in GetKnowledgeFile.
func TestSanitizeManualDownloadFilename(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{
			name:  "normal title produces title.md",
			title: "My Knowledge Article",
			want:  "My Knowledge Article.md",
		},
		{
			name:  "forward slash replaced with dash",
			title: "path/to/file",
			want:  "path-to-file.md",
		},
		{
			name:  "backslash replaced with dash",
			title: `windows\path`,
			want:  "windows-path.md",
		},
		{
			name:  "double-quote replaced with single-quote",
			title: `say "hello"`,
			want:  "say 'hello'.md",
		},
		{
			name:  "newline stripped",
			title: "line1\nline2",
			want:  "line1line2.md",
		},
		{
			name:  "carriage return stripped",
			title: "line1\rline2",
			want:  "line1line2.md",
		},
		{
			name:  "combination of dangerous chars",
			title: "att\nack\r/header\\ \"injection\"",
			want:  "attack-header- 'injection'.md",
		},
		{
			name:  "blank title falls back to untitled",
			title: "",
			want:  "untitled.md",
		},
		{
			name:  "whitespace-only title falls back to untitled",
			title: "   \t  ",
			want:  "untitled.md",
		},
		{
			name:  "title that sanitizes to only whitespace falls back to untitled",
			title: "\n\r",
			want:  "untitled.md",
		},
		{
			name:  "semicolon and equals preserved (safe in quoted header value)",
			title: "a=b; c=d",
			want:  "a=b; c=d.md",
		},
		{
			name:  "Chinese title preserved",
			title: "知识库文章",
			want:  "知识库文章.md",
		},
		{
			name:  "tab character stripped",
			title: "file\tname",
			want:  "filename.md",
		},
		{
			name:  "title already ending in .md not double-extended",
			title: "guide.md",
			want:  "guide.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeManualDownloadFilename(tt.title)
			if got != tt.want {
				t.Errorf("sanitizeManualDownloadFilename(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

// newManualKnowledgeService builds a knowledgeService wired to the stub folder
// service, mirroring the file/URL create-test harness in knowledge_create_test.go.
// Status defaults to "draft" so the publish/enqueue path is skipped.
func newManualKnowledgeService(repo *createKnowledgeFileRepoStub) *knowledgeService {
	return &knowledgeService{
		repo:          repo,
		folderService: &createKnowledgeFolderServiceStub{repo: repo},
		kbService:     &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:       &createKnowledgeFileServiceStub{},
	}
}

func TestCreateKnowledgeFromManualAssignsCurrentFolder(t *testing.T) {
	repo := &createKnowledgeFileRepoStub{}
	svc := newManualKnowledgeService(repo)

	knowledge, err := svc.CreateKnowledgeFromManual(
		newCreateKnowledgeFileContext(), "kb-1",
		&types.ManualKnowledgePayload{Title: "Manual Doc", Content: "Hello world", Status: "draft"},
		"web", "folder-current",
	)

	require.NoError(t, err)
	require.NotNil(t, knowledge)
	require.Equal(t, "folder-current", knowledge.FolderID)
	require.Equal(t, "folder-current", repo.createdKnowledge.FolderID)
}

func TestCreateKnowledgeFromManualAcceptsRootFolder(t *testing.T) {
	repo := &createKnowledgeFileRepoStub{}
	svc := newManualKnowledgeService(repo)

	knowledge, err := svc.CreateKnowledgeFromManual(
		newCreateKnowledgeFileContext(), "kb-1",
		&types.ManualKnowledgePayload{Title: "Manual Doc", Content: "Hello world", Status: "draft"},
		"web", types.FolderRootID,
	)

	require.NoError(t, err)
	require.NotNil(t, knowledge)
	require.Empty(t, knowledge.FolderID)
	require.Empty(t, repo.createdKnowledge.FolderID)
}

// TestCreateKnowledgeFromManualRejectsInvalidFolder uses the real folder
// service harness so the existing folder-validation path (validateTargetFolder
// inside CreateKnowledgeInFolder) is exercised end-to-end through the manual
// create entry point.
func TestCreateKnowledgeFromManualRejectsInvalidFolder(t *testing.T) {
	folderSvc, ctx, db := newKnowledgeFolderServiceHarness(t)
	svc := &knowledgeService{
		repo:          repository.NewKnowledgeRepository(db),
		folderService: folderSvc,
		kbService:     &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:       &createKnowledgeFileServiceStub{},
	}

	_, err := svc.CreateKnowledgeFromManual(
		ctx, "kb-1",
		&types.ManualKnowledgePayload{Title: "Manual Doc", Content: "Hello world", Status: "draft"},
		"web", "missing-folder",
	)

	require.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)
	var count int64
	require.NoError(t, db.Model(&types.Knowledge{}).Where("knowledge_base_id = ?", "kb-1").Count(&count).Error)
	require.Zero(t, count, "invalid folder must not persist a knowledge record")
}

func TestCreateKnowledgeFromManualRejectsCrossKBFolder(t *testing.T) {
	folderSvc, ctx, db := newKnowledgeFolderServiceHarness(t)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-2', 1)").Error)
	foreign := &types.KnowledgeFolder{
		ID: "folder-kb-2", TenantID: 1, KnowledgeBaseID: "kb-2", ParentID: types.FolderRootID,
		Name: "foreign", Path: "/folder-kb-2", Depth: 1,
	}
	require.NoError(t, db.Create(foreign).Error)
	svc := &knowledgeService{
		repo:          repository.NewKnowledgeRepository(db),
		folderService: folderSvc,
		kbService:     &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:       &createKnowledgeFileServiceStub{},
	}

	_, err := svc.CreateKnowledgeFromManual(
		ctx, "kb-1",
		&types.ManualKnowledgePayload{Title: "Manual Doc", Content: "Hello world", Status: "draft"},
		"web", foreign.ID,
	)

	require.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)
	var count int64
	require.NoError(t, db.Model(&types.Knowledge{}).Where("knowledge_base_id = ?", "kb-1").Count(&count).Error)
	require.Zero(t, count, "cross-KB folder must not persist a knowledge record")
}
