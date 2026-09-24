package core

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
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
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, TokenResponse{
			ApiResponse:       ApiResponse{Code: 0},
			TenantAccessToken: "fake-token", Expire: 7200,
		})
	})
	mux.HandleFunc("/open-apis/docx/v1/documents/obj-board/blocks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, DocxBlocksResponse{
			ApiResponse: ApiResponse{Code: 0},
			Data:        DocxBlocksData{Items: blocks},
		})
	})
	mux.HandleFunc("/open-apis/board/v1/whiteboards/brd-1/download_as_image",
		func(w http.ResponseWriter, _ *http.Request) {
			if boardStatus != http.StatusOK {
				http.Error(w, string(boardBody), boardStatus)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(boardBody)
		})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func boardBlocks() []DocxBlock {
	return []DocxBlock{
		{BlockID: "b1", BlockType: BlockTypePage},
		{
			BlockID: "b2", BlockType: BlockTypeText, Text: &BlockText{
				Elements: []TextElement{{TextRun: &TextRun{Content: "见画板"}}},
			},
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
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, TokenResponse{
			ApiResponse:       ApiResponse{Code: 0},
			TenantAccessToken: "fake-token", Expire: 7200,
		})
	})
	mux.HandleFunc("/open-apis/docx/v1/documents/obj-board/blocks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, DocxBlocksResponse{
			ApiResponse: ApiResponse{Code: 0},
			Data:        DocxBlocksData{Items: blocks},
		})
	})
	mux.HandleFunc("/open-apis/drive/v1/medias/ft-huge/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxFeishuDownloadBytes+1, 10))
		_, _ = w.Write(bigPDF)
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
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, TokenResponse{
			ApiResponse:       ApiResponse{Code: 0},
			TenantAccessToken: "fake-token", Expire: 7200,
		})
	})
	mux.HandleFunc("/open-apis/docx/v1/documents/obj-board/blocks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, DocxBlocksResponse{
			ApiResponse: ApiResponse{Code: 0},
			Data:        DocxBlocksData{Items: blocks},
		})
	})
	mux.HandleFunc("/open-apis/drive/v1/medias/ft-huge/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxFeishuDownloadBytes+1, 10))
		_, _ = w.Write(bigPDF)
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

// TestSupportedImageExtSVG pins the svg recovery: Go's sniffer reports SVG
// markup as text/xml, but a board export carrying an <svg> document must be
// treated as an inlinable image, not degraded to a placeholder.
func TestSupportedImageExtSVG(t *testing.T) {
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`)
	ext, ct, ok := SupportedImageExt(svg)
	if !ok || ext != ".svg" || ct != "image/svg+xml" {
		t.Fatalf("SupportedImageExt(svg) = %q %q %v, want .svg image/svg+xml true", ext, ct, ok)
	}
	if _, _, ok := SupportedImageExt([]byte("<?xml version=\"1.0\"?><not-svg/>")); ok {
		t.Fatal("non-svg xml must not be treated as an image")
	}
}

// trimBoardImage tests: the board API renders the whole canvas, so exports
// carry large blank borders (mostly below the content) — they must be cropped.

func encodeTestImage(t *testing.T, img image.Image, format string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", format, err)
	}
	return buf.Bytes()
}

func TestTrimBoardImage_CropsBlankBottomAndRight(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		src := image.NewRGBA(image.Rect(0, 0, 120, 200))
		content := color.RGBA{R: 30, G: 60, B: 200, A: 255}
		for y := 0; y < 40; y++ {
			for x := 0; x < 80; x++ {
				src.Set(x, y, content)
			}
		}
		trimmed := trimBoardImage(encodeTestImage(t, src, format))
		out, gotFormat, err := image.Decode(bytes.NewReader(trimmed))
		if err != nil {
			t.Fatalf("%s: decode trimmed: %v", format, err)
		}
		if gotFormat != format {
			t.Fatalf("%s: format changed to %s", format, gotFormat)
		}
		b := out.Bounds()
		if b.Dx() > 80+2*trimMargin || b.Dy() > 40+2*trimMargin {
			t.Fatalf("%s: blank canvas not trimmed, got %dx%d", format, b.Dx(), b.Dy())
		}
		if b.Dx() < 40 || b.Dy() < 20 {
			t.Fatalf("%s: content lost, got %dx%d", format, b.Dx(), b.Dy())
		}
		if r, _, _, _ := out.At(b.Min.X+4, b.Min.Y+4).RGBA(); uint8(r>>8) != 30 {
			t.Fatalf("%s: content pixel lost at margin", format)
		}
	}
}

func TestTrimBoardImage_NoBlankBorderReturnsOriginal(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 50, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			src.Set(x, y, color.RGBA{R: uint8(x * 5), G: uint8(y * 5), B: 90, A: 255})
		}
	}
	data := encodeTestImage(t, src, "png")
	if got := trimBoardImage(data); !bytes.Equal(got, data) {
		t.Fatal("fully painted image must pass through byte-identical")
	}
}

func TestTrimBoardImage_FullyBlankAndGarbagePassThrough(t *testing.T) {
	blank := image.NewRGBA(image.Rect(0, 0, 60, 60))
	data := encodeTestImage(t, blank, "png")
	if got := trimBoardImage(data); !bytes.Equal(got, data) {
		t.Fatal("fully blank export must pass through unchanged")
	}
	if got := trimBoardImage([]byte("not an image")); !bytes.Equal(got, []byte("not an image")) {
		t.Fatal("undecodable bytes must pass through unchanged")
	}
}

// Content spanning two opposite corners makes the corner vote 2:2 — trimming
// must bail entirely rather than trust either color as the background.
func TestTrimBoardImage_SplitCornerVoteSkipsTrimming(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 120, 200))
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	red := color.RGBA{R: 200, G: 30, B: 30, A: 255}
	for y := 0; y < 200; y++ {
		for x := 0; x < 120; x++ {
			src.Set(x, y, white)
		}
	}
	// Red band down the full left side: touches top-left and bottom-left.
	for y := 0; y < 200; y++ {
		for x := 0; x < 50; x++ {
			src.Set(x, y, red)
		}
	}
	data := encodeTestImage(t, src, "png")
	if got := trimBoardImage(data); !bytes.Equal(got, data) {
		t.Fatal("split corner vote (2:2) must pass through untrimmed")
	}
}

// Content touching exactly one corner keeps the majority vote at 3 — the
// blank borders away from that corner are still trimmed.
func TestTrimBoardImage_ContentAtOneCornerStillTrims(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 120, 200))
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	red := color.RGBA{R: 200, G: 30, B: 30, A: 255}
	for y := 0; y < 200; y++ {
		for x := 0; x < 120; x++ {
			src.Set(x, y, white)
		}
	}
	// Content only in the top-left corner region.
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			src.Set(x, y, red)
		}
	}
	trimmed := trimBoardImage(encodeTestImage(t, src, "png"))
	out, _, err := image.Decode(bytes.NewReader(trimmed))
	if err != nil {
		t.Fatalf("decode trimmed: %v", err)
	}
	b := out.Bounds()
	if b.Dy() > 40+2*trimMargin || b.Dx() > 40+2*trimMargin {
		t.Fatalf("blank canvas not trimmed, got %dx%d", b.Dx(), b.Dy())
	}
	if r, _, _, _ := out.At(2, 2).RGBA(); uint8(r>>8) != 200 {
		t.Fatal("corner-touching content must survive at its position")
	}
}

// 云文档 mention links: FetchDocxWithBlocks backfills link text with the
// referenced document's title via the drive metadata batch API; unresolved
// refs (no permission, 403) degrade to the URL-as-text form.
func TestFetchDocxWithBlocks_MentionDocTitleBackfill(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "r", BlockType: BlockTypePage},
		{BlockID: "p", BlockType: BlockTypeText, Text: &BlockText{
			Elements: []TextElement{{MentionDoc: &MentionDoc{
				URL: "https://my.feishu.cn/docx/RefDoc001",
			}}},
		}},
	}
	run := func(metasStatus int, metasBody string, _ string) *types.FetchedItem {
		t.Helper()
		mux := http.NewServeMux()
		mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, TokenResponse{
				ApiResponse:       ApiResponse{Code: 0},
				TenantAccessToken: "fake-token", Expire: 7200,
			})
		})
		mux.HandleFunc("/open-apis/docx/v1/documents/obj-doc/blocks", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, DocxBlocksResponse{
				ApiResponse: ApiResponse{Code: 0},
				Data:        DocxBlocksData{Items: blocks},
			})
		})
		mux.HandleFunc("/open-apis/drive/v1/metas/batch_query", func(w http.ResponseWriter, _ *http.Request) {
			if metasStatus != http.StatusOK {
				http.Error(w, metasBody, metasStatus)
				return
			}
			_, _ = w.Write([]byte(metasBody))
		})
		ts := httptest.NewServer(mux)
		t.Cleanup(ts.Close)

		client := NewClient(&Config{AppID: "a", AppSecret: "b", BaseURL: ts.URL})
		items, err := FetchDocxWithBlocks(context.Background(), client, DocxFetchInput{
			DocToken: "nt-doc", ObjToken: "obj-doc", Title: "父文档",
			URL: "https://my.feishu.cn/docx/obj-doc",
		})
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		return items[len(items)-1]
	}

	main := run(http.StatusOK,
		`{"code":0,"data":{"metas":[{"doc_token":"RefDoc001","doc_type":"docx","title":"引用的文档"}]}}`,
		"引用的文档")
	if !strings.Contains(string(main.Content), "[引用的文档](https://my.feishu.cn/docx/RefDoc001)") {
		t.Errorf("title not backfilled, got:\n%s", main.Content)
	}

	// Permission denied on the metadata API → falls back to URL-as-text.
	main = run(http.StatusForbidden, `forbidden`, "")
	if !strings.Contains(string(main.Content),
		"[https://my.feishu.cn/docx/RefDoc001](https://my.feishu.cn/docx/RefDoc001)") {
		t.Errorf("fallback form missing, got:\n%s", main.Content)
	}
}
