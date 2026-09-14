package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFetchDocxWithBlocks_EmbeddedBoard(t *testing.T) {
	t.Setenv("FEISHU_DOCX_PARSE_MODE", "blocks")
	const (
		nodeToken = "nt-docx-board"
		objToken  = "obj-docx-board"
		wid       = "wb-topo-1"
	)
	png := squareWhiteWithTopBar()

	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, TokenResponse{ApiResponse: ApiResponse{Code: 0}, TenantAccessToken: "fake-token", Expire: 7200})
	})
	mux.HandleFunc("/open-apis/docx/v1/documents/"+objToken+"/blocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, DocxBlocksResponse{
			ApiResponse: ApiResponse{Code: 0},
			Data: DocxBlocksData{Items: []DocxBlock{
				{BlockID: "b1", BlockType: BlockTypePage},
				{BlockID: "b2", BlockType: BlockTypeText, Text: &BlockText{
					Elements: []TextElement{{TextRun: &TextRun{Content: "拓扑如下"}}},
				}},
				{BlockID: "b3", BlockType: BlockTypeBoard, Board: &BlockTokenRef{Token: wid}},
			}},
		})
	})
	mux.HandleFunc("/open-apis/board/v1/whiteboards/"+wid+"/download_as_image", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := NewClient(&Config{AppID: "id", AppSecret: "sec", BaseURL: ts.URL})
	ctx := context.Background()
	in := DocxFetchInput{
		DocToken:          nodeToken,
		ObjToken:          objToken,
		Title:             "方案拓扑",
		ResourceID:        "space1",
		BaseMeta:          map[string]string{"channel": types.ChannelFeishu},
		MultimodalEnabled: true,
	}
	items, err := FetchDocxWithBlocks(ctx, client, in)
	if err != nil {
		t.Fatalf("FetchDocxWithBlocks: %v", err)
	}
	childID := types.SubtreeChildID(nodeToken, "board", wid)
	var main, board *types.FetchedItem
	for _, it := range items {
		switch it.ExternalID {
		case nodeToken:
			main = it
		case childID:
			board = it
		}
	}
	if main == nil {
		t.Fatal("main item missing")
	}
	if !strings.Contains(string(main.Content), "![画板](board-"+wid+".png)") {
		t.Errorf("markdown missing board placeholder:\n%s", main.Content)
	}
	if main.FileName != "方案拓扑/方案拓扑.md" {
		t.Errorf("main FileName = %q, want folder-prefixed", main.FileName)
	}
	if board == nil {
		t.Fatalf("board sub-item missing; got %+v", items)
	}
	if board.Metadata["whiteboard"] != "true" || board.Metadata["embedded_image"] != "true" {
		t.Errorf("board metadata = %+v", board.Metadata)
	}
	if !strings.HasPrefix(board.FileName, "方案拓扑/board-") {
		t.Errorf("board FileName = %q, want under doc folder", board.FileName)
	}
	if len(board.Content) < 100 {
		t.Errorf("board content too small: %d", len(board.Content))
	}

	off, err := FetchDocxWithBlocks(ctx, client, DocxFetchInput{
		DocToken: nodeToken, ObjToken: objToken, Title: "方案拓扑",
		ResourceID: "space1", BaseMeta: map[string]string{}, MultimodalEnabled: false,
	})
	if err != nil {
		t.Fatalf("multimodal off: %v", err)
	}
	for _, it := range off {
		if it.ExternalID == childID {
			t.Fatalf("multimodal off must not emit board bytes, got %+v", it)
		}
		if it.ExternalID == nodeToken {
			found := false
			for _, k := range it.SubtreeKeep {
				if k == childID {
					found = true
				}
			}
			if !found {
				t.Errorf("SubtreeKeep must retain board id when multimodal off: %+v", it.SubtreeKeep)
			}
		}
	}
}

func TestDownloadWhiteboardAsImage_RejectsJSONErrorBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, TokenResponse{ApiResponse: ApiResponse{Code: 0}, TenantAccessToken: "t", Expire: 7200})
	})
	mux.HandleFunc("/open-apis/board/v1/whiteboards/wb1/download_as_image", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":99991663,"msg":"forbidden","extra":"` + strings.Repeat("x", 80) + `"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c := NewClient(&Config{AppID: "id", AppSecret: "sec", BaseURL: ts.URL})
	_, err := c.downloadWhiteboardAsImage(context.Background(), "wb1")
	if err == nil {
		t.Fatal("want error for JSON error body")
	}
}
