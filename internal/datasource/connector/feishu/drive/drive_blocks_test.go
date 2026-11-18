package drive

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

// makeDriveConfig builds a DataSourceConfig for the Drive connector, mirroring
// makeConfig but with the drive connector type.
func makeDriveConfig(cfg *core.Config, resourceIDs []string) *types.DataSourceConfig {
	c := makeConfig(cfg, resourceIDs)
	c.Type = types.ConnectorTypeFeishuDrive
	return c
}

// fakeFeishuDriveDocx serves the Drive list endpoint plus the docx blocks /
// media / export endpoints. blocksMode controls the blocks API behaviour:
//   - "ok":    serve the given blocks
//   - "fail":  HTTP 500 (missing scope) → exercises the export fallback
//   - "empty": serve only a page block → empty Markdown → export fallback
//
// The export trio is always registered so both fallback modes can complete.
func fakeFeishuDriveDocx(t *testing.T, files []core.DriveFile, docToken string,
	blocks []core.DocxBlock, blocksMode string, mediaContent []byte,
) (*httptest.Server, *core.Config) {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, core.TokenResponse{ApiResponse: core.ApiResponse{Code: 0}, TenantAccessToken: "fake-token", Expire: 7200})
	})

	// Drive file listing (single page).
	mux.HandleFunc("/open-apis/drive/v1/files", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, core.DriveFileListResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.DriveFileListData{Files: files},
		})
	})

	// Blocks API for the given docx document.
	blocksPath := "/open-apis/docx/v1/documents/" + docToken + "/blocks"
	switch blocksMode {
	case "fail":
		mux.HandleFunc(blocksPath, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":99991400,"msg":"insufficient scope"}`))
		})
	case "empty":
		mux.HandleFunc(blocksPath, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, core.DocxBlocksResponse{
				ApiResponse: core.ApiResponse{Code: 0},
				Data:        core.DocxBlocksData{Items: []core.DocxBlock{{BlockID: "b1", BlockType: core.BlockTypePage}}},
			})
		})
	default: // "ok"
		mux.HandleFunc(blocksPath, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, core.DocxBlocksResponse{
				ApiResponse: core.ApiResponse{Code: 0},
				Data:        core.DocxBlocksData{Items: blocks},
			})
		})
	}

	// Media download for File/Image block tokens.
	mux.HandleFunc("/open-apis/drive/v1/medias/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(mediaContent)
			return
		}
		http.NotFound(w, r)
	})

	// Export trio for the fallback path.
	mux.HandleFunc("/open-apis/drive/v1/export_tasks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.ExportTaskCreateResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.ExportTaskCreateData{Ticket: "ticket-drv"},
		})
	})
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/ticket-drv", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.ExportTaskStatusResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data: core.ExportTaskStatusData{
				Result: core.ExportTaskResult{FileToken: "ft-export-drv", FileSize: 512, JobStatus: 0, FileName: "drive-fallback.docx"},
			},
		})
	})
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/file/ft-export-drv/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("fake-drive-export-binary"))
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &core.Config{AppID: "test-app-id", AppSecret: "test-app-secret", BaseURL: ts.URL}
}

func driveDocxBlocks(attToken, attName string) []core.DocxBlock {
	return []core.DocxBlock{
		{BlockID: "b1", BlockType: core.BlockTypePage},
		{BlockID: "b2", BlockType: core.BlockTypeText, Text: &core.BlockText{
			Elements: []core.TextElement{{TextRun: &core.TextRun{Content: "Hello drive"}}},
		}},
		{BlockID: "b3", BlockType: core.BlockTypeFile, File: &core.BlockFileRef{Token: attToken, Name: attName}},
	}
}

const driveDocxFileToken = "fdoc1"

func driveDocxFile() core.DriveFile {
	return core.DriveFile{
		Token:        driveDocxFileToken,
		Type:         "docx",
		Name:         "Drive Doc",
		ModifiedTime: "500",
		ParentToken:  "folder1",
		URL:          "https://example.feishu.cn/file/" + driveDocxFileToken,
	}
}

// A drive docx goes through the blocks path: main Markdown item (external_id =
// file token, channel = feishu_drive) plus the attachment sub-item.
func TestDriveFetchStream_DocxBlocksMultiItem(t *testing.T) {
	const (
		attToken = "ft-drv-att"
		attName  = "report.pdf"
	)
	attContent := bytes.Repeat([]byte("x"), core.MinAttachmentBytes+1)

	_, cfg := fakeFeishuDriveDocx(t, []core.DriveFile{driveDocxFile()}, driveDocxFileToken,
		driveDocxBlocks(attToken, attName), "ok", attContent)

	c := NewDriveConnector(core.RegionFeishuDrive)
	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeDriveConfig(cfg, []string{"folder1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.emitted) != 2 {
		t.Fatalf("expected 2 emitted items (attachment + main doc), got %d: %+v", len(h.emitted), h.emitted)
	}

	// P3 contract: sub-items are emitted BEFORE the parent document.
	att := h.emitted[0]
	wantAttID := driveDocxFileToken + "#file#" + attToken
	if att.ExternalID != wantAttID {
		t.Errorf("items[0].ExternalID = %q, want %q", att.ExternalID, wantAttID)
	}
	if att.Metadata["attachment"] != "true" {
		t.Errorf("items[0].Metadata[attachment] = %q, want \"true\"", att.Metadata["attachment"])
	}
	if att.Metadata["parent_doc_id"] != driveDocxFileToken {
		t.Errorf("items[0].Metadata[parent_doc_id] = %q, want %q", att.Metadata["parent_doc_id"], driveDocxFileToken)
	}

	main := h.emitted[1]
	if main.ExternalID != driveDocxFileToken {
		t.Errorf("items[1].ExternalID = %q, want %q", main.ExternalID, driveDocxFileToken)
	}
	if main.ContentType != "text/markdown" {
		t.Errorf("items[1].ContentType = %q, want text/markdown", main.ContentType)
	}
	if !main.ReplacesSubtree {
		t.Errorf("items[1].ReplacesSubtree = false, want true")
	}
	if main.Metadata["channel"] != types.ChannelFeishuDrive {
		t.Errorf("items[1].Metadata[channel] = %q, want %q", main.Metadata["channel"], types.ChannelFeishuDrive)
	}
	if main.URL != "https://example.feishu.cn/file/"+driveDocxFileToken {
		t.Errorf("items[1].URL = %q, want drive file URL passthrough", main.URL)
	}
	if !strings.Contains(string(main.Content), "Hello drive") {
		t.Errorf("items[1].Content missing expected text; got %q", string(main.Content))
	}
}

// Blocks API failure falls back to export: exactly one octet-stream item, no
// ReplacesSubtree (must not sweep good prior children on a transient failure).
func TestDriveFetchStream_DocxBlocksFailFallsBackToExport(t *testing.T) {
	_, cfg := fakeFeishuDriveDocx(t, []core.DriveFile{driveDocxFile()}, driveDocxFileToken, nil, "fail", nil)

	c := NewDriveConnector(core.RegionFeishuDrive)
	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeDriveConfig(cfg, []string{"folder1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.emitted) != 1 {
		t.Fatalf("expected 1 emitted item (export fallback), got %d: %+v", len(h.emitted), h.emitted)
	}
	item := h.emitted[0]
	if item.ExternalID != driveDocxFileToken {
		t.Errorf("item.ExternalID = %q, want %q", item.ExternalID, driveDocxFileToken)
	}
	if item.ContentType != "application/octet-stream" {
		t.Errorf("item.ContentType = %q, want application/octet-stream", item.ContentType)
	}
	if !strings.HasSuffix(item.FileName, ".docx") {
		t.Errorf("item.FileName = %q, want .docx suffix", item.FileName)
	}
	if item.ReplacesSubtree {
		t.Error("export-fallback item must not set ReplacesSubtree")
	}
}

// Blocks rendering to empty Markdown also falls back to export (a blank page
// would otherwise ingest as a login-gated URL core.Fetch and fail).
func TestDriveFetchStream_DocxBlocksEmptyFallsBackToExport(t *testing.T) {
	_, cfg := fakeFeishuDriveDocx(t, []core.DriveFile{driveDocxFile()}, driveDocxFileToken, nil, "empty", nil)

	c := NewDriveConnector(core.RegionFeishuDrive)
	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeDriveConfig(cfg, []string{"folder1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.emitted) != 1 {
		t.Fatalf("expected 1 emitted item (export fallback), got %d: %+v", len(h.emitted), h.emitted)
	}
	item := h.emitted[0]
	if item.ContentType != "application/octet-stream" {
		t.Errorf("item.ContentType = %q, want application/octet-stream", item.ContentType)
	}
	if item.ReplacesSubtree {
		t.Error("export-fallback item must not set ReplacesSubtree")
	}
}

// Image items are unconditional (not gated by the KB multimodal switch): the
// download produces a PNG sub-item, the parent's Markdown carries the numbered
// marker, and the external_id stays in SubtreeKeep.
func TestDriveFetchStream_DocxImageEmitted(t *testing.T) {
	const imgToken = "img-drv-1"
	blocks := []core.DocxBlock{
		{BlockID: "b1", BlockType: core.BlockTypePage},
		{BlockID: "b2", BlockType: core.BlockTypeText, Text: &core.BlockText{
			Elements: []core.TextElement{{TextRun: &core.TextRun{Content: "has image"}}},
		}},
		{BlockID: "b3", BlockType: core.BlockTypeImage, Image: &core.BlockTokenRef{Token: imgToken}},
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), core.MinAttachmentBytes)...)
	_, cfg := fakeFeishuDriveDocx(t, []core.DriveFile{driveDocxFile()}, driveDocxFileToken, blocks, "ok", png)

	c := NewDriveConnector(core.RegionFeishuDrive)
	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeDriveConfig(cfg, []string{"folder1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	// Orthodox flow: the image rides inline in the parent markdown as a base64
	// data URI — one emitted item, no image sub-item, no image_map.
	if len(h.emitted) != 1 {
		t.Fatalf("expected 1 emitted item (main only), got %d: %+v", len(h.emitted), h.emitted)
	}
	main := h.emitted[0]
	if main.ExternalID != driveDocxFileToken {
		t.Errorf("main ExternalID = %q, want %q", main.ExternalID, driveDocxFileToken)
	}
	wantURI := "![图片](data:image/png;base64," + base64.StdEncoding.EncodeToString(png) + ")"
	if !strings.Contains(string(main.Content), wantURI) {
		t.Errorf("image not inlined as base64 data URI:\n%s", main.Content)
	}
	if main.Metadata["image_map"] != "" {
		t.Errorf("image_map must be gone, got %q", main.Metadata["image_map"])
	}
}

func TestDriveParseModeChangeRefetchesExistingDocument(t *testing.T) {
	ts, cfg := fakeFeishuDriveDocx(t, []core.DriveFile{driveDocxFile()}, driveDocxFileToken,
		driveDocxBlocks("", ""), "ok", nil)
	defer ts.Close()
	conf := makeDriveConfig(cfg, []string{"folder1"})
	conf.Settings = map[string]interface{}{"parse_mode": "export"}
	c := NewDriveConnector(core.RegionFeishu)
	items, cursor, err := c.FetchIncremental(context.Background(), conf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("initial sync: %d items", len(items))
	}
	for _, mode := range []string{"blocks", "export"} {
		conf.Settings["parse_mode"] = mode
		items, cursor, err = c.FetchIncremental(context.Background(), conf, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("mode %s: want one refreshed document, got %d", mode, len(items))
		}
		items, _, err = c.FetchIncremental(context.Background(), conf, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 {
			t.Fatalf("unchanged mode %s unexpectedly refetched", mode)
		}
	}
}

// Image-heavy documents must take the native export path, whose ImageRefs do
// not have the Markdown data-URI count limit. In particular, no excess base64
// may be persisted as searchable document text.
func TestDriveImageBudgetFallsBackToNativeExport(t *testing.T) {
	for _, count := range []int{types.MaxInlineDocumentImages, types.MaxInlineDocumentImages + 1, 61} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			blocks := []core.DocxBlock{{BlockID: "page", BlockType: core.BlockTypePage}}
			for i := 0; i < count; i++ {
				blocks = append(blocks, core.DocxBlock{
					BlockID: fmt.Sprintf("img%d", i), BlockType: core.BlockTypeImage,
					Image: &core.BlockTokenRef{Token: "img"},
				})
			}
			data := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), core.MinAttachmentBytes)...)
			_, cfg := fakeFeishuDriveDocx(t, []core.DriveFile{driveDocxFile()}, driveDocxFileToken, blocks, "ok", data)
			items, err := core.FetchDocxWithBlocks(context.Background(), core.NewClient(cfg), core.DocxFetchInput{
				DocToken: driveDocxFileToken, ObjToken: driveDocxFileToken, Title: "Images",
				BaseMeta: map[string]string{}, ParseMode: core.ParseModeBlocks,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 {
				t.Fatalf("want one document, got %d", len(items))
			}
			if count <= types.MaxInlineDocumentImages {
				if n := strings.Count(string(items[0].Content), "data:image/png;base64,"); n != count {
					t.Fatalf("inline images=%d, want %d", n, count)
				}
			} else {
				if !strings.HasSuffix(items[0].FileName, ".docx") ||
					string(items[0].Content) != "fake-drive-export-binary" {
					t.Fatalf("image-heavy document was not exported: %s", items[0].FileName)
				}
				if !items[0].ReplacesSubtree {
					t.Fatal("complete blocks listing must retain attachment reconciliation")
				}
			}
		})
	}
}

func TestDriveImageBudgetPreservesAttachmentsAndExportFailures(t *testing.T) {
	for _, failExport := range []bool{false, true} {
		t.Run(strconv.FormatBool(failExport), func(t *testing.T) {
			blocks := driveDocxBlocks("attachment", "report.pdf")
			for i := 0; i < 61; i++ {
				blocks = append(blocks, core.DocxBlock{
					BlockID: fmt.Sprintf("img%d", i), BlockType: core.BlockTypeImage,
					Image: &core.BlockTokenRef{Token: "img"},
				})
			}
			ts, cfg := fakeFeishuDriveDocx(t, []core.DriveFile{driveDocxFile()}, driveDocxFileToken,
				blocks, "ok", bytes.Repeat([]byte("pdf"), 1024))
			handler := ts.Config.Handler
			var mediaDownloads atomic.Int32
			ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/medias/") {
					mediaDownloads.Add(1)
				}
				if failExport && r.URL.Path == "/open-apis/drive/v1/export_tasks" {
					writeJSON(w, core.ApiResponse{Code: 1, Msg: "export unavailable"})
					return
				}
				handler.ServeHTTP(w, r)
			})
			items, err := core.FetchDocxWithBlocks(context.Background(), core.NewClient(cfg), core.DocxFetchInput{
				DocToken: driveDocxFileToken, ObjToken: driveDocxFileToken, Title: "Images",
				BaseMeta: map[string]string{}, ParseMode: core.ParseModeBlocks,
			})
			if failExport {
				if err == nil || len(items) != 0 {
					t.Fatalf("failed export must remain retryable: err=%v items=%d", err, len(items))
				}
				if mediaDownloads.Load() != 0 {
					t.Fatal("media downloaded despite failed parent export")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 2 {
				t.Fatalf("want attachment and exported parent, got %d", len(items))
			}
			parent := items[1]
			childID := types.SubtreeChildID(driveDocxFileToken, "file", "attachment")
			if items[0].ExternalID != childID || items[0].Metadata["parent_doc_id"] != parent.ExternalID {
				t.Fatal("lost attachment association")
			}
			if !parent.ReplacesSubtree || len(parent.SubtreeKeep) != 1 || parent.SubtreeKeep[0] != childID ||
				!strings.Contains(parent.Metadata["attachment_ids"], childID) {
				t.Fatalf("lost attachment keep list: %+v", parent)
			}
			if mediaDownloads.Load() != 1 {
				t.Fatalf("want only attachment download, got %d", mediaDownloads.Load())
			}
		})
	}
}
