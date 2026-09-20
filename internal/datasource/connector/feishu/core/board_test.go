package core

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// P3 embedded-object pipeline tests: board blocks (block_type 43) exported via
// download_as_image, oversize attachments degraded inline, and the marker →
// image_map contract. These run against the real Client + Feishu-shaped fake
// endpoints, exercising FetchDocxWithBlocks end to end.

func fakeFeishuForBoard(t *testing.T, blocks []DocxBlock, boardStatus int, boardBody []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, TokenResponse{ApiResponse: ApiResponse{Code: 0}, TenantAccessToken: "fake-token", Expire: 7200})
	})
	mux.HandleFunc("/open-apis/docx/v1/documents/obj-board/blocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, DocxBlocksResponse{ApiResponse: ApiResponse{Code: 0},
			Data: DocxBlocksData{Items: blocks}})
	})
	mux.HandleFunc("/open-apis/board/v1/whiteboards/brd-1/download_as_image", func(w http.ResponseWriter, r *http.Request) {
		if boardStatus != http.StatusOK {
			http.Error(w, string(boardBody), boardStatus)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(boardBody)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func boardBlocks() []DocxBlock {
	return []DocxBlock{
		{BlockID: "b1", BlockType: BlockTypePage},
		{BlockID: "b2", BlockType: BlockTypeText, Text: &BlockText{
			Elements: []TextElement{{TextRun: &TextRun{Content: "见画板"}}}},
		},
		{BlockID: "b3", BlockType: BlockTypeBoard, Board: &BlockBoard{Token: "brd-1"}},
	}
}

func boardFetchInput() DocxFetchInput {
	return DocxFetchInput{
		DocToken:   "nt-board",
		ObjToken:   "obj-board",
		Title:      "Board Doc",
		URL:        "https://example.feishu.cn/wiki/nt-board",
		ResourceID: "space1:nt-board",
		BaseMeta:   map[string]string{"channel": types.ChannelFeishu},
	}
}

func TestFetchDocxWithBlocks_BoardExportSuccess(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), MinAttachmentBytes)...)
	ts := fakeFeishuForBoard(t, boardBlocks(), http.StatusOK, png)
	client := NewClient(&Config{AppID: "a", AppSecret: "b", BaseURL: ts.URL, ParseMode: ParseModeBlocks})

	items, err := FetchDocxWithBlocks(context.Background(), client, boardFetchInput())
	if err != nil {
		t.Fatalf("FetchDocxWithBlocks: %v", err)
	}
	// Orthodox flow: the board rides inline in the parent markdown as a
	// base64 data URI — no image sub-item, no image_map.
	if len(items) != 1 {
		t.Fatalf("want 1 item (main only), got %d: %+v", len(items), items)
	}
	main := items[0]
	if !strings.Contains(string(main.Content), "![图片](data:image/png;base64,") {
		t.Errorf("board not inlined as base64 data URI:\n%s", main.Content)
	}
	if strings.Contains(string(main.Content), "weknora-img://") {
		t.Errorf("internal marker leaked into stored markdown:\n%s", main.Content)
	}
	if main.Metadata["image_map"] != "" {
		t.Errorf("image_map must be gone, got %q", main.Metadata["image_map"])
	}
	if !strings.Contains(string(main.Content), "见画板") {
		t.Errorf("main markdown lost body text:\n%s", main.Content)
	}
}

func TestFetchDocxWithBlocks_BoardExportForbiddenDegrades(t *testing.T) {
	// 403 = app lacks board:whiteboard:node:read. No board item, no failure:
	// the marker degrades to an inline note and the document still syncs.
	ts := fakeFeishuForBoard(t, boardBlocks(), http.StatusForbidden, []byte(`{"code":2890005,"msg":"forbidden"}`))
	client := NewClient(&Config{AppID: "a", AppSecret: "b", BaseURL: ts.URL, ParseMode: ParseModeBlocks})

	items, err := FetchDocxWithBlocks(context.Background(), client, boardFetchInput())
	if err != nil {
		t.Fatalf("board export failure must not fail the document: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item (main only), got %d: %+v", len(items), items)
	}
	main := items[0]
	if !strings.Contains(string(main.Content), "![图片]()") {
		t.Errorf("markdown missing board degrade note:\n%s", main.Content)
	}
	if strings.Contains(string(main.Content), "weknora-img") {
		t.Errorf("unresolved board marker left in markdown:\n%s", main.Content)
	}
	if main.Metadata["image_map"] != "" {
		t.Errorf("failed board must not appear in image_map, got %q", main.Metadata["image_map"])
	}
	if !strings.Contains(string(main.Content), "见画板") {
		t.Errorf("main markdown lost body text:\n%s", main.Content)
	}
}

func TestFetchDocxWithBlocks_AttachmentOverCapDegrades(t *testing.T) {
	// The fake declares a Content-Length above maxFeishuDownloadBytes with a
	// tiny body: downloadRawBytes must reject early on the header, without
	// needing to serve half a gigabyte.
	bigPDF := bytes.Repeat([]byte("A"), 1024)
	blocks := []DocxBlock{
		{BlockID: "b1", BlockType: BlockTypePage},
		{BlockID: "b2", BlockType: BlockTypeFile, File: &BlockFileRef{Token: "ft-huge", Name: "report.pdf"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, TokenResponse{ApiResponse: ApiResponse{Code: 0}, TenantAccessToken: "fake-token", Expire: 7200})
	})
	mux.HandleFunc("/open-apis/docx/v1/documents/obj-board/blocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, DocxBlocksResponse{ApiResponse: ApiResponse{Code: 0},
			Data: DocxBlocksData{Items: blocks}})
	})
	mux.HandleFunc("/open-apis/drive/v1/medias/ft-huge/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxFeishuDownloadBytes+1, 10))
		w.Write(bigPDF)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	client := NewClient(&Config{AppID: "a", AppSecret: "b", BaseURL: ts.URL, ParseMode: ParseModeBlocks})

	in := boardFetchInput()
	in.ObjToken = "obj-board"
	items, err := FetchDocxWithBlocks(context.Background(), client, in)
	if err != nil {
		t.Fatalf("over-cap attachment must not fail the document: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item (main only), got %d: %+v", len(items), items)
	}
	main := items[0]
	// The over-cap file degrades to an inline reference carrying the source link.
	if !strings.Contains(string(main.Content), "> [附件: report.pdf](") {
		t.Errorf("markdown missing over-cap attachment placeholder:\n%s", main.Content)
	}
	if strings.Contains(string(main.Content), "- report.pdf") {
		t.Errorf("over-cap attachment must not stay in the file-name list:\n%s", main.Content)
	}
	if main.Metadata["attachment_ids"] != "" {
		t.Errorf("over-cap attachment must not appear in attachment_ids, got %q", main.Metadata["attachment_ids"])
	}
}

// Regression for the line-text patch bug: an earlier bullet that happens to
// carry the attachment's display name must survive the over-cap degrade, and
// the attachment's own marker line is the one replaced.
func TestFetchDocxWithBlocks_OverCapPatchHitsMarkerNotEarlierBullet(t *testing.T) {
	bigPDF := bytes.Repeat([]byte("A"), 1024)
	blocks := []DocxBlock{
		{BlockID: "b1", BlockType: BlockTypePage},

		{BlockID: "b3", BlockType: BlockTypeBullet, Bullet: &BlockText{Elements: []TextElement{
			{TextRun: &TextRun{Content: "report.pdf"}},
		}}},
		{BlockID: "b4", BlockType: BlockTypeFile, File: &BlockFileRef{Token: "ft-huge", Name: "report.pdf"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, TokenResponse{ApiResponse: ApiResponse{Code: 0}, TenantAccessToken: "fake-token", Expire: 7200})
	})
	mux.HandleFunc("/open-apis/docx/v1/documents/obj-board/blocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, DocxBlocksResponse{ApiResponse: ApiResponse{Code: 0},
			Data: DocxBlocksData{Items: blocks}})
	})
	mux.HandleFunc("/open-apis/drive/v1/medias/ft-huge/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxFeishuDownloadBytes+1, 10))
		w.Write(bigPDF)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	client := NewClient(&Config{AppID: "a", AppSecret: "b", BaseURL: ts.URL, ParseMode: ParseModeBlocks})

	in := boardFetchInput()
	in.ObjToken = "obj-board"
	items, err := FetchDocxWithBlocks(context.Background(), client, in)
	if err != nil {
		t.Fatalf("over-cap attachment must not fail the document: %v", err)
	}
	main := items[len(items)-1]
	if n := strings.Count(string(main.Content), "- report.pdf"); n != 1 {
		t.Errorf("earlier bullet must survive exactly once, got %d:\n%s", n, main.Content)
	}
	if !strings.Contains(string(main.Content), "> [附件: report.pdf]") {
		t.Errorf("over-cap degrade note missing:\n%s", main.Content)
	}
}
