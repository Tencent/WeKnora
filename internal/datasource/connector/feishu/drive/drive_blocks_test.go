package drive

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

// makeDriveConfig builds a DataSourceConfig for the Drive connector, mirroring
// makeConfig but with the drive connector type and a multimodal toggle.
func makeDriveConfig(cfg *core.Config, resourceIDs []string, multimodal bool) *types.DataSourceConfig {
	c := makeConfig(cfg, resourceIDs)
	c.Type = types.ConnectorTypeFeishuDrive
	c.MultimodalEnabled = multimodal
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
	_, err := c.FetchStream(context.Background(), makeDriveConfig(cfg, []string{"folder1"}, false), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.emitted) != 2 {
		t.Fatalf("expected 2 emitted items (main doc + attachment), got %d: %+v", len(h.emitted), h.emitted)
	}

	main := h.emitted[0]
	if main.ExternalID != driveDocxFileToken {
		t.Errorf("items[0].ExternalID = %q, want %q", main.ExternalID, driveDocxFileToken)
	}
	if main.ContentType != "text/markdown" {
		t.Errorf("items[0].ContentType = %q, want text/markdown", main.ContentType)
	}
	if !main.ReplacesSubtree {
		t.Errorf("items[0].ReplacesSubtree = false, want true")
	}
	if main.Metadata["channel"] != types.ChannelFeishuDrive {
		t.Errorf("items[0].Metadata[channel] = %q, want %q", main.Metadata["channel"], types.ChannelFeishuDrive)
	}
	if main.URL != "https://example.feishu.cn/file/"+driveDocxFileToken {
		t.Errorf("items[0].URL = %q, want drive file URL passthrough", main.URL)
	}
	if !strings.Contains(string(main.Content), "Hello drive") {
		t.Errorf("items[0].Content missing expected text; got %q", string(main.Content))
	}

	att := h.emitted[1]
	wantAttID := driveDocxFileToken + "#file#" + attToken
	if att.ExternalID != wantAttID {
		t.Errorf("items[1].ExternalID = %q, want %q", att.ExternalID, wantAttID)
	}
	if att.Metadata["attachment"] != "true" {
		t.Errorf("items[1].Metadata[attachment] = %q, want \"true\"", att.Metadata["attachment"])
	}
}

// Blocks API failure falls back to export: exactly one octet-stream item, no
// ReplacesSubtree (must not sweep good prior children on a transient failure).
func TestDriveFetchStream_DocxBlocksFailFallsBackToExport(t *testing.T) {
	_, cfg := fakeFeishuDriveDocx(t, []core.DriveFile{driveDocxFile()}, driveDocxFileToken, nil, "fail", nil)

	c := NewDriveConnector(core.RegionFeishuDrive)
	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeDriveConfig(cfg, []string{"folder1"}, false), nil, h)
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
	_, err := c.FetchStream(context.Background(), makeDriveConfig(cfg, []string{"folder1"}, false), nil, h)
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

// With multimodal disabled, an embedded image is not downloaded or emitted,
// but its external_id is still in SubtreeKeep so a later toggle-on does not
// sweep it, and toggling VLM off later does not delete previously OCR'd images.
func TestDriveFetchStream_DocxImageMultimodalOff(t *testing.T) {
	const imgToken = "img-drv-1"
	blocks := []core.DocxBlock{
		{BlockID: "b1", BlockType: core.BlockTypePage},
		{BlockID: "b2", BlockType: core.BlockTypeText, Text: &core.BlockText{
			Elements: []core.TextElement{{TextRun: &core.TextRun{Content: "has image"}}},
		}},
		{BlockID: "b3", BlockType: core.BlockTypeImage, Image: &core.BlockTokenRef{Token: imgToken}},
	}
	_, cfg := fakeFeishuDriveDocx(t, []core.DriveFile{driveDocxFile()}, driveDocxFileToken, blocks, "ok", nil)

	c := NewDriveConnector(core.RegionFeishuDrive)
	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeDriveConfig(cfg, []string{"folder1"}, false), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.emitted) != 1 {
		t.Fatalf("expected 1 emitted item (main only, image core.Skipped), got %d: %+v", len(h.emitted), h.emitted)
	}
	main := h.emitted[0]
	wantKeep := driveDocxFileToken + "#image#" + imgToken
	found := false
	for _, k := range main.SubtreeKeep {
		if k == wantKeep {
			found = true
		}
	}
	if !found {
		t.Errorf("SubtreeKeep = %v, want it to contain %q", main.SubtreeKeep, wantKeep)
	}
}
