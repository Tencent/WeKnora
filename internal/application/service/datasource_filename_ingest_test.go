package service

import (
	"bytes"
	"context"
	"io"
	"testing"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestIngestItem_QuotedFilename(t *testing.T) {
	// Notion page titles become filenames without replacing double quotes.
	item := &types.FetchedItem{
		ExternalID:  "notion-page",
		Title:       `Q4 "Final"`,
		FileName:    `Q4 "Final".md`,
		Content:     []byte("# Q4 report\n\n季度报告\n"),
		ContentType: "text/markdown",
	}
	ds := &types.DataSource{
		ID:              "ds-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            types.ChannelNotion,
	}
	repo := &filenameKnowledgeRepo{}
	fileSvc := filesvc.NewLocalFileService(t.TempDir(), "")
	task := &createKnowledgeTaskEnqueuerStub{}
	ks := &knowledgeService{
		repo:      repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID}},
		fileSvc:   fileSvc,
		task:      task,
	}
	svc := &DataSourceService{knowledgeService: ks}
	ctx := newCreateKnowledgeFileContext()
	isUpdate, err := svc.ingestItem(ctx, ds, item, nil)
	if err != nil {
		t.Fatalf("ingestItem: %v", err)
	}
	if isUpdate {
		t.Fatal("new page was reported as an update")
	}
	knowledge := repo.createdKnowledge
	if knowledge == nil {
		t.Fatal("knowledge was not created")
	}
	if knowledge.FileName != item.FileName || knowledge.Title != item.FileName {
		t.Errorf("filename = %q, title = %q, want %q", knowledge.FileName, knowledge.Title, item.FileName)
	}
	if task.calls != 1 {
		t.Errorf("processing tasks = %d, want 1", task.calls)
	}
	file, err := fileSvc.GetFile(ctx, knowledge.FilePath)
	if err != nil {
		t.Fatalf("open ingested file: %v", err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close ingested file: %v", err)
		}
	})
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read ingested file: %v", err)
	}
	if !bytes.Equal(got, item.Content) {
		t.Errorf("ingested content = %q, want %q", got, item.Content)
	}
}

type filenameKnowledgeRepo struct {
	createKnowledgeFileRepoStub
}

func (*filenameKnowledgeRepo) FindByDataSourceExternalID(
	context.Context, uint64, string, string, string,
) (*types.Knowledge, error) {
	return nil, nil
}
