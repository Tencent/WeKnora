package ima

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFetchedFolderPathsReachKnowledgeIngestion(t *testing.T) {
	for _, incremental := range []bool{false, true} {
		for _, mediaType := range []int32{mediaTypeMarkdown, mediaTypeNote} {
			t.Run(fmt.Sprintf("incremental=%v/type=%d", incremental, mediaType), func(t *testing.T) {
				f := newFakeIMA(t)
				f.setKB("kb1", []fakeFile{
					{
						MediaID: "nested", Title: "Guide", ParentFolderID: "folder_inner",
						MediaType: mediaType, Body: "guide", NotebookID: "777", NoteBody: "guide",
					},
					{
						MediaID: "root", Title: "Root", MediaType: mediaType,
						Body: "root", NotebookID: "888", NoteBody: "root",
					},
				}, fakeFolder{FolderID: "folder_outer", Name: " 文档. "},
					fakeFolder{FolderID: "folder_inner", Name: "Inner", ParentFolderID: "folder_outer"})
				c := NewConnector()
				cfg := f.config("kb1")
				var items []types.FetchedItem
				var err error
				var cursor *types.SyncCursor
				if incremental {
					items, cursor, err = c.FetchIncremental(context.Background(), cfg, nil)
				} else {
					items, err = c.FetchAll(context.Background(), cfg, []string{"kb1"})
				}
				if err != nil {
					t.Fatal(err)
				}
				nested := mustFindItem(t, items, logicalKey("kb1", "folder_inner", "Guide"))
				folder, name := types.SplitKnowledgeRelativePath(nested.FileName)
				if folder != "文档/Inner" || name != "Guide.md" {
					t.Fatalf("ingestion path=(%q,%q), want (文档/Inner,Guide.md)", folder, name)
				}
				if incremental {
					unchanged, _, nextErr := c.FetchIncremental(context.Background(), cfg, cursor)
					if nextErr != nil || len(unchanged) != 0 {
						t.Fatalf("unchanged round: items=%d err=%v", len(unchanged), nextErr)
					}
				}
				root := mustFindItem(t, items, logicalKey("kb1", "", "Root"))
				folder, name = types.SplitKnowledgeRelativePath(root.FileName)
				if folder != "" || name != "Root.md" {
					t.Fatalf("root=(%q,%q)", folder, name)
				}
			})
		}
	}
}

func TestNestedURLOnlyKeepsBareURLHandling(t *testing.T) {
	f := newFakeIMA(t)
	f.setKB("kb1", []fakeFile{
		{MediaID: "web", Title: "Website", ParentFolderID: "folder_docs", MediaType: mediaTypeWeb},
	}, fakeFolder{FolderID: "folder_docs", Name: "Docs"})
	items, err := NewConnector().FetchAll(context.Background(), f.config("kb1"), []string{"kb1"})
	if err != nil {
		t.Fatal(err)
	}
	item := mustFindItem(t, items, logicalKey("kb1", "folder_docs", "Website"))
	if item.FileName != "" || item.URL == "" {
		t.Fatalf("URL-only item changed routing: %#v", item)
	}
}
