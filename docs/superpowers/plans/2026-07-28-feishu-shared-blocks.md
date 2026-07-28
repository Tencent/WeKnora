# 飞书 connector shared.go 抽取 + 云盘 docx Blocks 路径实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 wiki/drive 公共逻辑抽入 `shared.go`，并让飞书云盘的 docx 走「Blocks API 优先、导出回退」路径（与 wiki 一致）。

**Architecture:** 纯搬运重构（`connector.go` → `shared.go`）+ `fetchDocxWithBlocks` 包级化（入参 `docxFetchInput` 承载 wiki/drive 差异）+ drive 侧 docx 分支改调公共函数。

**Tech Stack:** Go 1.26，httptest mock server，table-driven tests。

**Spec:** `docs/superpowers/specs/2026-07-28-feishu-shared-blocks-design.md`

## Global Constraints

- 构建必须用 `make build` / `go build` 带 proto 冲突标志；验证统一用 `make fmt && make lint && make test`。
- lint：golangci-lint v2，lll 行宽 120。
- Commit message：Conventional Commits + scope，如 `refactor(datasource):` / `feat(datasource):`。
- wiki 行为零变化：现有测试（golden/stream/convergence 等）必须全绿，且**不得修改**这些测试。
- 子项 metadata 父级 key 保守沿用 `parent_node_token`（drive 值 = file.Token）。
- 不写存量数据迁移代码。
- 测试基线注意：`connector_realapi_test.go` 在本机因 SSRF 环境限制失败，属已知问题，与本次改动无关；判断回归时排除该文件。

---

### Task 1: 创建 shared.go，纯搬运公共符号

**Files:**
- Create: `internal/datasource/connector/feishu/shared.go`
- Modify: `internal/datasource/connector/feishu/connector.go`（删除被搬走的符号）

**Interfaces:**
- Consumes: 无（纯搬运）。
- Produces（后续 Task 依赖这些符号存在于 shared.go，包内可见性不变）:
  - `feishuStreamCheckpointInterval` (var int)、`feishuStreamCheckpointMaxInterval` (var time.Duration)
  - `fetchTally` struct + `newFetchTally(discovered int) *fetchTally` + 方法 `fetch/fail/skip/skipped/summary`
  - `reFeishuErrorCode`、`feishuErrorCode(raw string) string`
  - `feishuFailure(err error) (code, codeValue, fallback string)`
  - `feishuErrorItemMeta(err error, extra map[string]string) map[string]string`
  - `isSupportedDocType(objType string) bool`
  - `parseFeishuConfig(config *types.DataSourceConfig, region Region) (*Config, error)`
  - `parseFeishuTimestamp(ts string) time.Time`
  - `sanitizeFileName(name string) string`、`truncateUTF8(s string, maxBytes int) string`
  - `feishuWikiNodeResourceSeparator` (const string)
  - `parseableAttachmentExts` (var map[string]bool)、`minAttachmentBytes` (const)
  - `supportedImageExt(data []byte) (ext, contentType string, ok bool)`

- [ ] **Step 1: 创建 shared.go 并搬入符号**

创建 `internal/datasource/connector/feishu/shared.go`，文件头：

```go
package feishu

// shared.go holds the helpers used by BOTH the wiki Connector (connector.go)
// and the Drive DriveConnector (drive_connector.go): error classification,
// config parsing, stream-checkpoint tuning, the fetch tally, filename/time
// utilities, attachment rules, and the docx blocks fetch path. Anything that
// is specific to one connector stays in that connector's own file.
```

从 `connector.go` **逐字搬出**（不修改实现与注释）以下符号，粘贴到 shared.go：

1. `feishuStreamCheckpointInterval` var + 注释（connector.go:45-49）
2. `feishuStreamCheckpointMaxInterval` var + 注释（connector.go:51-56）
3. `fetchTally` struct + `newFetchTally` + 全部方法 + 注释（connector.go:58-88）
4. `reFeishuErrorCode` + `feishuErrorCode`（connector.go:572-581）
5. `feishuFailure`（connector.go:583-621）
6. `feishuErrorItemMeta`（connector.go:623-640）
7. `parseableAttachmentExts` + `minAttachmentBytes`（connector.go:664-672）
8. `supportedImageExt`（connector.go:977-994）
9. `feishuWikiNodeResourceSeparator` const（connector.go:43）
10. `parseFeishuConfig`（connector.go:1040-1082）
11. `isSupportedDocType`（connector.go:1084-1094）
12. `parseFeishuTimestamp`（connector.go:1096-1106）
13. `sanitizeFileName` + `truncateUTF8`（connector.go:1108-1152）

shared.go 所需 imports（编译器报错时再微调）：

```go
import (
    "encoding/json"
    "fmt"
    "net/http"
    "path/filepath"
    "regexp"
    "strconv"
    "strings"
    "time"
    "unicode/utf8"

    "github.com/Tencent/WeKnora/internal/datasource"
    "github.com/Tencent/WeKnora/internal/types"
)
```

- [ ] **Step 2: 从 connector.go 删除已搬走的符号，并清理 imports**

connector.go 删除上面 13 项。剩余代码仍需要的 imports 保留；不再需要的删除（预计可删 `net/http`、`regexp`、`unicode/utf8`；`strconv`/`path/filepath`/`strings` 以编译器为准）。

- [ ] **Step 3: 编译验证**

Run: `make build`
Expected: 编译通过。

- [ ] **Step 4: 回归测试**

Run: `go test ./internal/datasource/connector/feishu/... 2>&1 | tail -20`
Expected: 除已知 SSRF 环境限制的 `connector_realapi_test.go` 外全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/datasource/connector/feishu/shared.go internal/datasource/connector/feishu/connector.go
git commit -m "refactor(datasource): extract shared feishu connector helpers into shared.go

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: fetchDocxWithBlocks 包级化，wiki 调用点更新

**Files:**
- Modify: `internal/datasource/connector/feishu/shared.go`（新增 `docxFetchInput`、`fetchDocxWithBlocks`、`exportDocxFallback`）
- Modify: `internal/datasource/connector/feishu/connector.go:674-975`（`fetchNodeContent` 的 docx 分支改调用；删除旧方法 `(*Connector).fetchDocxWithBlocks`）

**Interfaces:**
- Consumes: Task 1 搬入 shared.go 的全部符号。
- Produces:
  - `docxFetchInput` struct（字段：`docToken, objToken, title, url, resourceID string; editTime time.Time; baseMeta map[string]string; multimodalEnabled bool`）
  - `fetchDocxWithBlocks(ctx context.Context, client *Client, in docxFetchInput) ([]*types.FetchedItem, error)`
  - `exportDocxFallback(ctx context.Context, client *Client, in docxFetchInput) (*types.FetchedItem, error)`
  - connector.go 保留 `(*Connector).fetchViaExport`（doc/sheet/bitable 专用，签名不变）。

- [ ] **Step 1: 在 shared.go 新增 docxFetchInput 与两个函数**

逻辑与 connector.go:778-975 的现实现**逐行等价**，仅把 `node.ObjToken`→`in.objToken`、`node.Title`→`in.title`、`node.NodeToken`→`in.docToken`、`c.region.wikiURL(node.NodeToken)`→`in.url`、`baseMeta`→`in.baseMeta`、`resourceID`→`in.resourceID`、`editTime`→`in.editTime`、`multimodalEnabled`→`in.multimodalEnabled`；回退导出改调 `exportDocxFallback`：

```go
// docxFetchInput is the unified description of one docx document from either
// source (wiki node or Drive file) that fetchDocxWithBlocks needs.
type docxFetchInput struct {
	docToken          string // WeKnora external_id: wiki=node.NodeToken, drive=file.Token
	objToken          string // Feishu docx document token
	title             string
	url               string
	resourceID        string
	editTime          time.Time
	baseMeta          map[string]string
	multimodalEnabled bool
}

// fetchDocxWithBlocks retrieves a docx document via the blocks API, converts it
// to Markdown, and returns a main item plus any parseable attachment/image
// sub-items. Falls back to the export API if the blocks API errors or renders
// empty. Shared by the wiki Connector and the Drive DriveConnector.
func fetchDocxWithBlocks(ctx context.Context, client *Client, in docxFetchInput) ([]*types.FetchedItem, error) {
	blocks, err := client.ListDocumentBlocks(ctx, in.objToken)
	if err != nil {
		logger.Warnf(ctx, "[Feishu] blocks API failed for %s (%s), falling back to export: %v",
			in.title, in.objToken, err)
		item, ferr := exportDocxFallback(ctx, client, in)
		if ferr != nil {
			return nil, ferr
		}
		// Do NOT set ReplacesSubtree here (see the wiki history: a transient
		// blocks failure must not sweep good attachment children from the prior
		// blocks-path sync with nothing to replace them).
		return []*types.FetchedItem{item}, nil
	}

	md, atts, err := blocksToMarkdown(ctx, client, blocks)
	if err != nil {
		return nil, fmt.Errorf("convert blocks %s: %w", in.title, err)
	}

	if len(strings.TrimSpace(string(md))) == 0 {
		logger.Infof(ctx, "[Feishu] doc %s (%s): blocks rendered empty Markdown, falling back to export",
			in.title, in.objToken)
		item, ferr := exportDocxFallback(ctx, client, in)
		if ferr != nil {
			return nil, ferr
		}
		return []*types.FetchedItem{item}, nil
	}

	main := &types.FetchedItem{
		ExternalID:       in.docToken,
		Title:            in.title,
		Content:          md,
		ContentType:      "text/markdown",
		FileName:         sanitizeFileName(in.title) + ".md",
		URL:              in.url,
		UpdatedAt:        in.editTime,
		SourceResourceID: in.resourceID,
		Metadata:         in.baseMeta,
		ReplacesSubtree:  true, // sweep stale attachment sub-items on re-sync
	}
	items := []*types.FetchedItem{main}

	keep := make([]string, 0, len(atts))
	childMeta := func() map[string]string {
		m := maps.Clone(in.baseMeta)
		m["parent_node_token"] = in.docToken
		m["attachment"] = "true"
		return m
	}
	for _, a := range atts {
		childID := types.SubtreeChildID(in.docToken, "file", a.FileToken)
		keep = append(keep, childID) // present in the doc → never sweep as stale
		ext := strings.ToLower(filepath.Ext(a.Name))
		if ext == "" {
			logger.Warnf(ctx, "[Feishu] doc %s: skipping attachment with no usable filename (token=%s name=%q)",
				in.objToken, a.FileToken, a.Name)
			continue
		}
		if !parseableAttachmentExts[ext] {
			continue
		}
		data, derr := client.DownloadMediaFile(ctx, a.FileToken)
		if derr != nil {
			logger.Warnf(ctx, "[Feishu] doc %s: attachment %q (token=%s) download failed: %v",
				in.objToken, a.Name, a.FileToken, derr)
			items = append(items, &types.FetchedItem{
				ExternalID:       childID,
				Title:            a.Name,
				SourceResourceID: in.resourceID,
				Metadata:         feishuErrorItemMeta(derr, childMeta()),
			})
			continue
		}
		if len(data) < minAttachmentBytes {
			logger.Infof(ctx, "[Feishu] doc %s: skipping tiny attachment %q (token=%s, %d bytes < %d)",
				in.objToken, a.Name, a.FileToken, len(data), minAttachmentBytes)
			continue
		}
		items = append(items, &types.FetchedItem{
			ExternalID:       childID,
			Title:            a.Name,
			Content:          data,
			ContentType:      "application/octet-stream",
			FileName:         sanitizeFileName(a.Name),
			URL:              in.url,
			UpdatedAt:        in.editTime,
			SourceResourceID: in.resourceID,
			Metadata:         childMeta(),
		})
	}

	imgMeta := func() map[string]string {
		m := maps.Clone(in.baseMeta)
		m["parent_node_token"] = in.docToken
		m["embedded_image"] = "true"
		return m
	}
	for _, b := range blocks {
		if b.BlockType != blockTypeImage || b.Image == nil || b.Image.Token == "" {
			continue
		}
		childID := types.SubtreeChildID(in.docToken, "image", b.Image.Token)
		keep = append(keep, childID) // present in the doc → never sweep as stale
		if !in.multimodalEnabled {
			continue // KB can't OCR images; the inline placeholder is all we keep
		}
		data, derr := client.DownloadMediaFile(ctx, b.Image.Token)
		if derr != nil {
			logger.Warnf(ctx, "[Feishu] doc %s: image (token=%s) download failed: %v",
				in.objToken, b.Image.Token, derr)
			items = append(items, &types.FetchedItem{
				ExternalID:       childID,
				Title:            fmt.Sprintf("%s（内嵌图片）", in.title),
				SourceResourceID: in.resourceID,
				Metadata:         feishuErrorItemMeta(derr, imgMeta()),
			})
			continue
		}
		if len(data) < minAttachmentBytes {
			continue // decorative micro-image (icon/spacer)
		}
		ext, contentType, ok := supportedImageExt(data)
		if !ok {
			logger.Warnf(ctx, "[Feishu] doc %s: skipping image (token=%s) of unsupported type %q",
				in.objToken, b.Image.Token, contentType)
			continue
		}
		items = append(items, &types.FetchedItem{
			ExternalID:       childID,
			Title:            fmt.Sprintf("%s（内嵌图片）", in.title),
			Content:          data,
			ContentType:      contentType,
			FileName:         "image-" + b.Image.Token + ext,
			URL:              in.url,
			UpdatedAt:        in.editTime,
			SourceResourceID: in.resourceID,
			Metadata:         imgMeta(),
		})
	}

	main.SubtreeKeep = keep
	return items, nil
}

// exportDocxFallback exports a docx document via the async export API and
// returns a single FetchedItem containing the exported .docx binary. Used by
// fetchDocxWithBlocks when the blocks API is unavailable or renders empty.
func exportDocxFallback(ctx context.Context, client *Client, in docxFetchInput) (*types.FetchedItem, error) {
	data, fileName, err := client.ExportAndDownload(ctx, in.objToken, "docx")
	if err != nil {
		return nil, fmt.Errorf("export %s (docx): %w", in.title, err)
	}

	ext := exportFileExtToSuffix[objTypeToExportFileExtension["docx"]]
	if fileName == "" {
		fileName = sanitizeFileName(in.title) + ext
	} else if !strings.HasSuffix(strings.ToLower(fileName), ext) {
		fileName = sanitizeFileName(fileName) + ext
	}

	return &types.FetchedItem{
		ExternalID:       in.docToken,
		Title:            in.title,
		Content:          data,
		ContentType:      "application/octet-stream",
		FileName:         fileName,
		URL:              in.url,
		UpdatedAt:        in.editTime,
		SourceResourceID: in.resourceID,
		Metadata:         in.baseMeta,
	}, nil
}
```

shared.go imports 需追加：`"context"`、`"maps"`、`"github.com/Tencent/WeKnora/internal/logger"`（原注释里 connector.go:806-814 关于空渲染回退的长注释、:792-797 关于不设置 ReplacesSubtree 的注释、:839-846 关于 keep 语义的注释、:908-915 关于内嵌图片的注释，一并随对应代码块搬入）。

- [ ] **Step 2: 更新 wiki 调用点并删除旧方法**

connector.go 的 `fetchNodeContent` 中 `case "docx":` 改为：

```go
	case "docx":
		return fetchDocxWithBlocks(ctx, client, docxFetchInput{
			docToken:          node.NodeToken,
			objToken:          node.ObjToken,
			title:             node.Title,
			url:               c.region.wikiURL(node.NodeToken),
			resourceID:        resourceID,
			editTime:          editTime,
			baseMeta:          baseMeta,
			multimodalEnabled: multimodalEnabled,
		})
```

删除 connector.go:778-975 的旧方法 `func (c *Connector) fetchDocxWithBlocks(...)` 及其头部注释（connector.go:778-780）。`fetchViaExport`（connector.go:719-748）保留不动。

- [ ] **Step 3: 编译验证**

Run: `make build`
Expected: 编译通过。

- [ ] **Step 4: 回归测试**

Run: `go test ./internal/datasource/connector/feishu/... -run 'TestFetchDocxWithBlocks|TestFetchStream' -v 2>&1 | tail -30`
Expected: `TestFetchDocxWithBlocks_MultiItem`、`TestFetchStream_DocxMultiItem`、`TestFetchStream_DocxBlocksFallback` 等全部 PASS（wiki 行为未变）。

Run: `go test ./internal/datasource/connector/feishu/... 2>&1 | tail -5`
Expected: 除已知 SSRF 环境限制的 realapi 测试外全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/datasource/connector/feishu/shared.go internal/datasource/connector/feishu/connector.go
git commit -m "refactor(datasource): make fetchDocxWithBlocks a package-level function

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: drive docx 接入 Blocks 路径（TDD）

**Files:**
- Test: `internal/datasource/connector/feishu/drive_blocks_test.go`（新建）
- Modify: `internal/datasource/connector/feishu/drive_connector.go:251-275, 350-363, 471-502, 555-638`（`fetchDriveFileContent` 签名改返回 slice + 新增 multimodalEnabled 参数；docx 分支改调 `fetchDocxWithBlocks`；三处调用点更新）

**Interfaces:**
- Consumes: Task 2 的 `fetchDocxWithBlocks(ctx, client, in docxFetchInput) ([]*types.FetchedItem, error)`、`docxFetchInput`。
- Produces:
  - `(*DriveConnector).fetchDriveFileContent(ctx context.Context, client *Client, file driveFile, resourceID string, multimodalEnabled bool) ([]*types.FetchedItem, error)`（**签名变化**：返回值由 `*types.FetchedItem` 改为 `[]*types.FetchedItem`，新增 `multimodalEnabled` 参数）
  - 测试辅助：`fakeFeishuDriveDocx(t, files, docToken, blocks, blocksMode, mediaContent)`、`makeDriveConfig(cfg, resourceIDs, multimodal)`

- [ ] **Step 1: 写失败测试 — 新建 drive_blocks_test.go**

```go
package feishu

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// makeDriveConfig builds a DataSourceConfig for the Drive connector, mirroring
// makeConfig but with the drive connector type and a multimodal toggle.
func makeDriveConfig(cfg *Config, resourceIDs []string, multimodal bool) *types.DataSourceConfig {
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
func fakeFeishuDriveDocx(t *testing.T, files []driveFile, docToken string, blocks []docxBlock, blocksMode string, mediaContent []byte) (*httptest.Server, *Config) {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{apiResponse: apiResponse{Code: 0}, TenantAccessToken: "fake-token", Expire: 7200})
	})

	// Drive file listing (single page).
	mux.HandleFunc("/open-apis/drive/v1/files", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, driveFileListResponse{
			apiResponse: apiResponse{Code: 0},
			Data: struct {
				Files         []driveFile `json:"files"`
				HasMore       bool        `json:"has_more"`
				NextPageToken string      `json:"next_page_token"`
			}{Files: files},
		})
	})

	// Blocks API for the given docx document.
	blocksPath := "/open-apis/docx/v1/documents/" + docToken + "/blocks"
	switch blocksMode {
	case "fail":
		mux.HandleFunc(blocksPath, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":99991400,"msg":"insufficient scope"}`))
		})
	case "empty":
		mux.HandleFunc(blocksPath, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, docxBlocksResponse{
				apiResponse: apiResponse{Code: 0},
				Data: struct {
					Items     []docxBlock `json:"items"`
					HasMore   bool        `json:"has_more"`
					PageToken string      `json:"page_token"`
				}{Items: []docxBlock{{BlockID: "b1", BlockType: blockTypePage}}},
			})
		})
	default: // "ok"
		mux.HandleFunc(blocksPath, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, docxBlocksResponse{
				apiResponse: apiResponse{Code: 0},
				Data: struct {
					Items     []docxBlock `json:"items"`
					HasMore   bool        `json:"has_more"`
					PageToken string      `json:"page_token"`
				}{Items: blocks},
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
		writeJSON(w, exportTaskCreateResponse{
			apiResponse: apiResponse{Code: 0},
			Data:        struct{ Ticket string `json:"ticket"` }{Ticket: "ticket-drv"},
		})
	})
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/ticket-drv", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, exportTaskStatusResponse{
			apiResponse: apiResponse{Code: 0},
			Data: struct {
				Result struct {
					FileToken   string `json:"file_token"`
					FileSize    int64  `json:"file_size"`
					JobStatus   int    `json:"job_status"`
					JobErrorMsg string `json:"job_error_msg"`
					FileName    string `json:"file_name"`
				} `json:"result"`
			}{
				Result: struct {
					FileToken   string `json:"file_token"`
					FileSize    int64  `json:"file_size"`
					JobStatus   int    `json:"job_status"`
					JobErrorMsg string `json:"job_error_msg"`
					FileName    string `json:"file_name"`
				}{FileToken: "ft-export-drv", FileSize: 512, JobStatus: 0, FileName: "drive-fallback.docx"},
			},
		})
	})
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/file/ft-export-drv/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("fake-drive-export-binary"))
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &Config{AppID: "test-app-id", AppSecret: "test-app-secret", BaseURL: ts.URL}
}

func driveDocxBlocks(attToken, attName string) []docxBlock {
	return []docxBlock{
		{BlockID: "b1", BlockType: blockTypePage},
		{BlockID: "b2", BlockType: blockTypeText, Text: &blockText{
			Elements: []textElement{{TextRun: &struct {
				Content string `json:"content"`
			}{Content: "Hello drive"}}},
		}},
		{BlockID: "b3", BlockType: blockTypeFile, File: &struct {
			Token string `json:"token"`
			Name  string `json:"name"`
		}{Token: attToken, Name: attName}},
	}
}

const driveDocxFileToken = "fdoc1"

func driveDocxFile() driveFile {
	return driveFile{
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
	attContent := bytes.Repeat([]byte("x"), minAttachmentBytes+1)

	_, cfg := fakeFeishuDriveDocx(t, []driveFile{driveDocxFile()}, driveDocxFileToken,
		driveDocxBlocks(attToken, attName), "ok", attContent)

	c := NewDriveConnector(RegionFeishuDrive)
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
	_, cfg := fakeFeishuDriveDocx(t, []driveFile{driveDocxFile()}, driveDocxFileToken, nil, "fail", nil)

	c := NewDriveConnector(RegionFeishuDrive)
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
// would otherwise ingest as a login-gated URL fetch and fail).
func TestDriveFetchStream_DocxBlocksEmptyFallsBackToExport(t *testing.T) {
	_, cfg := fakeFeishuDriveDocx(t, []driveFile{driveDocxFile()}, driveDocxFileToken, nil, "empty", nil)

	c := NewDriveConnector(RegionFeishuDrive)
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
	blocks := []docxBlock{
		{BlockID: "b1", BlockType: blockTypePage},
		{BlockID: "b2", BlockType: blockTypeText, Text: &blockText{
			Elements: []textElement{{TextRun: &struct {
				Content string `json:"content"`
			}{Content: "has image"}}},
		}},
		{BlockID: "b3", BlockType: blockTypeImage, Image: &struct {
			Token string `json:"token"`
		}{Token: imgToken}},
	}
	_, cfg := fakeFeishuDriveDocx(t, []driveFile{driveDocxFile()}, driveDocxFileToken, blocks, "ok", nil)

	c := NewDriveConnector(RegionFeishuDrive)
	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeDriveConfig(cfg, []string{"folder1"}, false), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.emitted) != 1 {
		t.Fatalf("expected 1 emitted item (main only, image skipped), got %d: %+v", len(h.emitted), h.emitted)
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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/datasource/connector/feishu/ -run 'TestDriveFetchStream' -v 2>&1 | tail -20`
Expected:
- `TestDriveFetchStream_DocxBlocksMultiItem` FAIL —— 现状 docx 走导出路径只产出 1 个 octet-stream item，`len(h.emitted) != 2`；
- `TestDriveFetchStream_DocxImageMultimodalOff` FAIL —— `SubtreeKeep` 为空，找不到 `fdoc1#image#img-drv-1`；
- `TestDriveFetchStream_DocxBlocksFailFallsBackToExport` / `...EmptyFallsBackToExport` 在改动前**可能已 PASS**（现状本就是导出路径，可观察结果相同）——它们是回退语义的回归保护，确认不 FAIL 即可。

确认失败原因是「行为不符」而非编译错误。若编译失败先修测试代码本身。

注意：`recordingHandler.Checkpoint` 会把 drive 的 cursor 反序列化成 `feishuCursor`（字段不匹配但 `json.Unmarshal` 容错，不会 panic）；单文件同步不触发 checkpoint（间隔 50），完全无影响。

- [ ] **Step 3: 修改 fetchDriveFileContent 签名与 docx 分支**

drive_connector.go 中，`fetchDriveFileContent` 整体替换为：

```go
// fetchDriveFileContent fetches the content of a single Drive file and converts
// it to FetchedItems. Dispatches by file.Type, mirroring the wiki
// fetchNodeContent. Shortcuts have already been expanded to their target by
// ListDriveFilesRecursiveFrom, so this only sees the target type.
//
//   - docx                   -> blocks API (Markdown) with export fallback; may return attachments/images
//   - doc/sheet/bitable      -> ExportAndDownload -> docx/xlsx
//   - file                   -> DownloadDriveFile -> original file
//   - mindnote/slides/board  -> skip (no API), returns (nil, nil)
func (c *DriveConnector) fetchDriveFileContent(
	ctx context.Context, client *Client, file driveFile, resourceID string, multimodalEnabled bool,
) ([]*types.FetchedItem, error) {
	if !isSupportedDocType(file.Type) {
		return nil, nil
	}

	editTime := parseFeishuTimestamp(file.ModifiedTime)
	// Channel marks the knowledge "source" label. Drive uses its own channel
	// (feishu_drive / lark_drive) so Drive docs show "飞书云盘" / "Lark 云盘"
	// distinct from the wiki connector's "飞书".
	channel := types.ChannelFeishuDrive
	if c.region.ConnectorType == types.ConnectorTypeLarkDrive {
		channel = types.ChannelLarkDrive
	}
	baseMeta := map[string]string{
		"obj_token":    file.Token,
		"obj_type":     file.Type,
		"file_token":   file.Token,
		"folder_token": file.ParentToken,
		"channel":      channel,
	}

	switch file.Type {
	case "docx":
		return fetchDocxWithBlocks(ctx, client, docxFetchInput{
			docToken:          file.Token,
			objToken:          file.Token,
			title:             file.Name,
			url:               file.URL,
			resourceID:        resourceID,
			editTime:          editTime,
			baseMeta:          baseMeta,
			multimodalEnabled: multimodalEnabled,
		})

	case "doc", "sheet", "bitable":
		data, fileName, err := client.ExportAndDownload(ctx, file.Token, file.Type)
		if err != nil {
			return nil, fmt.Errorf("export %s (%s): %w", file.Name, file.Type, err)
		}

		ext := exportFileExtToSuffix[objTypeToExportFileExtension[file.Type]]
		if fileName == "" {
			fileName = sanitizeFileName(file.Name) + ext
		} else if !strings.HasSuffix(strings.ToLower(fileName), ext) {
			fileName = sanitizeFileName(fileName) + ext
		}

		return []*types.FetchedItem{{
			ExternalID:       file.Token, // Drive uses file token as external_id
			Title:            file.Name,
			Content:          data,
			ContentType:      "application/octet-stream",
			FileName:         fileName,
			URL:              file.URL, // list API returns the absolute url
			UpdatedAt:        editTime,
			SourceResourceID: resourceID,
			Metadata:         baseMeta,
		}}, nil

	case "file":
		data, err := client.DownloadDriveFile(ctx, file.Token)
		if err != nil {
			return nil, fmt.Errorf("download file %s (%s): %w", file.Name, file.Token, err)
		}

		fileName := file.Name
		if fileName == "" {
			fileName = file.Token
		}

		return []*types.FetchedItem{{
			ExternalID:       file.Token,
			Title:            file.Name,
			Content:          data,
			ContentType:      "application/octet-stream",
			FileName:         fileName,
			URL:              file.URL,
			UpdatedAt:        editTime,
			SourceResourceID: resourceID,
			Metadata:         baseMeta,
		}}, nil

	default:
		return nil, nil
	}
}
```

- [ ] **Step 4: 更新三处调用点**

`FetchAll`（drive_connector.go:251-269 附近）：

```go
		tally := newFetchTally(len(files))
		for i, file := range files {
			items, ferr := c.fetchDriveFileContent(ctx, client, file, resourceID, config.MultimodalEnabled)
			if ferr != nil {
				tally.fail()
				allItems = append(allItems, types.FetchedItem{
					ExternalID:       file.Token,
					Title:            file.Name,
					SourceResourceID: resourceID,
					Metadata:         feishuErrorItemMeta(ferr, nil),
				})
				continue
			}
			if len(items) > 0 {
				tally.fetch()
				for _, it := range items {
					allItems = append(allItems, *it)
				}
			} else {
				tally.skip(file.Type)
			}
```

`FetchIncremental`（drive_connector.go:350-363 附近）：

```go
			items, ferr := c.fetchDriveFileContent(ctx, client, file, resourceID, config.MultimodalEnabled)
			if ferr != nil {
				changedItems = append(changedItems, types.FetchedItem{
					ExternalID:       file.Token,
					Title:            file.Name,
					SourceResourceID: resourceID,
					Metadata:         feishuErrorItemMeta(ferr, nil),
				})
				continue
			}
			for _, it := range items {
				changedItems = append(changedItems, *it)
			}
```

`FetchStream`（drive_connector.go:471-502 附近）：

```go
			items, ferr := c.fetchDriveFileContent(ctx, client, file, resourceID, config.MultimodalEnabled)
			if ferr != nil {
				tally.fail()
				// Do NOT advance the cursor: the content was never fetched.
				// Retain the prior modify time (if any) so prev != current next
				// run and the file is retried, instead of being permanently
				// skipped on a transient export failure (Tencent/WeKnora#2136).
				if hadPrev {
					newCursor.FileTimes[resourceID][file.Token] = prevModify
				}
				if eerr := h.Emit(ctx, types.FetchedItem{
					ExternalID:       file.Token,
					Title:            file.Name,
					SourceResourceID: resourceID,
					Metadata:         feishuErrorItemMeta(ferr, nil),
				}); eerr != nil {
					return nil, eerr
				}
			} else {
				// Fetched, or an unsupported type (nothing to fetch): record the
				// current modify time so the file is not re-processed next run.
				newCursor.FileTimes[resourceID][file.Token] = modifyTimeStr
				if len(items) > 0 {
					tally.fetch()
					for _, it := range items {
						if eerr := h.Emit(ctx, *it); eerr != nil {
							return nil, eerr
						}
					}
				} else {
					// Unsupported type (mindnote/slides/…): no item.
					tally.skip(file.Type)
				}
			}
```

- [ ] **Step 5: 运行新测试确认通过**

Run: `go test ./internal/datasource/connector/feishu/ -run 'TestDriveFetchStream' -v 2>&1 | tail -20`
Expected: 4 个用例全部 PASS。

- [ ] **Step 6: 全量回归**

Run: `go test ./internal/datasource/connector/feishu/... 2>&1 | tail -5`
Expected: 除已知 SSRF 环境限制的 realapi 测试外全部 PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/datasource/connector/feishu/drive_blocks_test.go internal/datasource/connector/feishu/drive_connector.go
git commit -m "feat(datasource): sync feishu drive docx via blocks API with export fallback

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: 全量检查收尾

**Files:**
- Modify: 仅当 fmt/lint 报告问题时触及对应文件。

**Interfaces:**
- Consumes: Task 1-3 的全部产物。
- Produces: 无新接口。

- [ ] **Step 1: fmt**

Run: `make fmt`
Expected: 无输出或仅列出被格式化的文件；若有文件被改动，`git diff` 确认仅为格式化。

- [ ] **Step 2: lint**

Run: `make lint`
Expected: 0 issues（重点：lll 120 行宽、govet、revive）。若 shared.go 或新测试超行宽，折行修复。

- [ ] **Step 3: 全量测试**

Run: `make test 2>&1 | tail -30`
Expected: 除已知 SSRF 环境限制的 `connector_realapi_test.go` 外全部 PASS。

- [ ] **Step 4: 若有 fmt/lint 修复则提交**

```bash
git add -A internal/datasource/connector/feishu/
git commit -m "style(datasource): apply fmt and lint fixes

Co-Authored-By: Claude <noreply@anthropic.com>"
```
