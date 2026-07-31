package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const FeishuWikiNodeResourceSeparator = ":"

// shared.go holds the helpers used by BOTH the wiki Connector (connector.go)
// and the Drive DriveConnector (connector.go): error classification,
// config parsing, stream-Checkpoint tuning, the fetch tally, filename/time
// utilities, attachment rules, and the docx blocks fetch path. Anything that
// is specific to one connector stays in that connector's own file.

// FeishuStreamCheckpointInterval is how many processed nodes pass between
// cursor checkpoints during a streaming fetch. Small enough that a timed-out
// sync loses little work on resume, large enough that Checkpoint persistence
// (a DB write) does not dominate. Overridable in tests. See FetchStream.
var FeishuStreamCheckpointInterval = 50

// FeishuStreamCheckpointMaxInterval bounds checkpointing by wall-clock time as
// well as node count. Without it, a sync of fewer than
// FeishuStreamCheckpointInterval very slow (rate-limited) exports could reach
// the 2h task timeout having never checkpointed, and resume from scratch every
// retry — the #2136 "never fully syncs" case. Overridable in tests.
var FeishuStreamCheckpointMaxInterval = 30 * time.Second

// fetchTally accumulates the outcome of fetching a wiki node subtree so the
// connector can Emit a single actionable summary. Without it, unsupported nodes
// (mindnote/slides/etc.) vanish with no item, no error and no log, leaving users
// unable to explain why "13 documents synced only 3" (Tencent/WeKnora#2136).
type fetchTally struct {
	discovered    int
	fetched       int
	failed        int
	skippedByType map[string]int
}

func newFetchTally(discovered int) *fetchTally {
	return &fetchTally{discovered: discovered, skippedByType: map[string]int{}}
}

func (t *fetchTally) fetch()              { t.fetched++ }
func (t *fetchTally) fail()               { t.failed++ }
func (t *fetchTally) Skip(objType string) { t.skippedByType[objType]++ }

func (t *fetchTally) skipped() int {
	n := 0
	for _, c := range t.skippedByType {
		n += c
	}
	return n
}

func (t *fetchTally) summary() string {
	return fmt.Sprintf("discovered=%d fetched=%d failed=%d skipped_unsupported=%d by_type=%v",
		t.discovered, t.fetched, t.failed, t.skipped(), t.skippedByType)
}

var reFeishuErrorCode = regexp.MustCompile(`code["\s]*[:=]\s*(\d+)`)

// feishuErrorCode extracts the numeric Feishu error code from a raw error string
// (e.g. `body={"code":1663,...}` or `code=1663`), best-effort.
func feishuErrorCode(raw string) string {
	if m := reFeishuErrorCode.FindStringSubmatch(raw); len(m) == 2 {
		return m[1]
	}
	return ""
}

// feishuFailure classifies a raw connector/API error into a stable i18n code
// (mapped to a localized string on the frontend), an optional numeric Feishu
// error code for interpolation, and an English fallback message for clients
// without the i18n key. The raw status/JSON body/log_id is never returned here —
// it stays in the server logs. Dumping it in the UI is the anti-pattern
// Airbyte/Fivetran/Onyx warn against. Transient errors are retried next sync
// (the cursor is retained); auth/permission errors point at the fix instead.
func feishuFailure(err error) (code, codeValue, fallback string) {
	if err == nil {
		return "sync_failed", "", "Sync failed; will retry on the next sync"
	}
	s := strings.ToLower(err.Error())

	switch {
	case strings.Contains(s, "auth error"),
		strings.Contains(s, "invalid access token"),
		strings.Contains(s, "permission"),
		strings.Contains(s, "forbidden"),
		strings.Contains(s, "status=403"):
		return "feishu_auth_or_permission", "", "Authentication or permission error; check credentials and app scopes"
	case strings.Contains(s, "rate limited"), strings.Contains(s, "status=429"):
		return "feishu_rate_limited", "", "Feishu API rate limited; will retry on the next sync"
	case strings.Contains(s, "timed out"),
		strings.Contains(s, "timeout"),
		strings.Contains(s, "deadline exceeded"):
		return "feishu_timeout", "", "Export or request timed out; will retry on the next sync"
	case strings.Contains(s, "server error"):
		return "feishu_server_unavailable", "", "Feishu service temporarily unavailable; will retry on the next sync"
	case strings.Contains(s, "api error"),
		strings.Contains(s, "export task failed"),
		strings.Contains(s, "download failed"):
		if v := feishuErrorCode(err.Error()); v != "" {
			return "feishu_api_error", v, fmt.Sprintf("Feishu API error (code=%s); will retry on the next sync", v)
		}
		return "feishu_api_error_generic", "", "Feishu API error; will retry on the next sync"
	default:
		return "sync_failed", "", "Sync failed; will retry on the next sync"
	}
}

// FeishuErrorItemMeta builds the metadata for a failed item: the raw error (for
// server logs) plus the classified i18n code / param / fallback (for a
// localisable SyncItemError in the UI), merged with any caller-supplied extras.
func FeishuErrorItemMeta(err error, extra map[string]string) map[string]string {
	code, codeValue, fallback := feishuFailure(err)
	m := map[string]string{
		"error":             err.Error(),
		"error_reason_code": code,
		"error_reason":      fallback,
	}
	if codeValue != "" {
		m["error_reason_code_value"] = codeValue
	}
	maps.Copy(m, extra)
	return m
}

// parseableAttachmentExts are attachment extensions worth ingesting as their
// own knowledge entries; other files (icons, tiny decor) are skipped.
var parseableAttachmentExts = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true, ".txt": true, ".md": true, ".csv": true,
}

// MinAttachmentBytes filters out decorative micro-files.
const MinAttachmentBytes = 2 * 1024

// SupportedImageExt sniffs image bytes and returns the filename extension and
// content type WeKnora accepts for a standalone image knowledge item (png/jpg/
// gif — the image set isValidFileType admits). ok is false for non-image or
// unsupported formats (e.g. webp/bmp), which the caller skips rather than
// mislabel — a wrong extension would fail parsing. The detected content type is
// returned even when ok is false so the caller can log it without re-sniffing.
func SupportedImageExt(data []byte) (ext, contentType string, ok bool) {
	switch ct := http.DetectContentType(data); ct {
	case "image/png":
		return ".png", ct, true
	case "image/jpeg":
		return ".jpg", ct, true
	case "image/gif":
		return ".gif", ct, true
	default:
		return "", ct, false
	}
}

// ParseFeishuConfig extracts and validates Feishu/Lark-specific configuration.
//
// base_url stays an explicit override so existing data sources that pointed a
// "feishu" connector at open.larksuite.com keep working; when it is unset the
// region's own host is filled in, making the resolved Config.BaseURL concrete
// for everything downstream.
func ParseFeishuConfig(config *types.DataSourceConfig, region Region) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}

	var feishuConfig Config
	if err := json.Unmarshal(credBytes, &feishuConfig); err != nil {
		return nil, fmt.Errorf("parse %s credentials: %w", region.ConnectorType, err)
	}

	if feishuConfig.AppID == "" || feishuConfig.AppSecret == "" {
		return nil, fmt.Errorf("%s app_id and app_secret are required", region.ConnectorType)
	}

	if feishuConfig.BaseURL == "" {
		feishuConfig.BaseURL = region.OpenBaseURL
	}

	// Timezone is a display setting (bitable date rendering), not a credential, so
	// it lives in Settings. Empty falls back to GMT+8 in resolveLocation.
	if feishuConfig.Timezone == "" && config.Settings != nil {
		if tz, ok := config.Settings["timezone"].(string); ok {
			feishuConfig.Timezone = strings.TrimSpace(tz)
		}
	}

	if err := datasource.ValidateConnectorBaseURL(feishuConfig.GetBaseURL()); err != nil {
		return nil, err
	}

	return &feishuConfig, nil
}

// IsSupportedDocType checks if a Feishu document type can be synced.
// mindnote and slides have no content read API and are skipped.
func IsSupportedDocType(objType string) bool {
	switch objType {
	case "docx", "doc", "sheet", "bitable", "file":
		return true
	default:
		// mindnote, slides — no content retrieval API available
		return false
	}
}

// ParseFeishuTimestamp parses a Feishu unix timestamp string (seconds) into time.Time.
func ParseFeishuTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// SanitizeFileName removes characters that are invalid in filenames and
// truncates at a UTF-8 rune boundary. Raw byte truncation would split a
// multi-byte codepoint (Chinese chars are 3 bytes) and produce invalid UTF-8
// that downstream validation (utf8.ValidString) rejects.
//
// The extension is preserved across truncation: only the base name is trimmed,
// so a long attachment name like "很长的名字….pdf" keeps its ".pdf" suffix that
// downstream file-type classification depends on.
func SanitizeFileName(name string) string {
	if name == "" {
		return "untitled"
	}
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	result := replacer.Replace(name)
	const maxBytes = 200
	if len(result) <= maxBytes {
		return result
	}
	ext := filepath.Ext(result)
	if len(ext) >= maxBytes {
		// pathological: extension alone overflows the budget → drop it
		ext = ""
	}
	base := truncateUTF8(result[:len(result)-len(ext)], maxBytes-len(ext))
	return base + ext
}

// truncateUTF8 shortens s to at most maxBytes bytes without splitting a
// multi-byte rune: after a hard byte cut it trims any trailing partial codepoint.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

// DocxFetchInput is the unified description of one docx document from either
// source (wiki node or Drive file) that FetchDocxWithBlocks needs.
type DocxFetchInput struct {
	// WeKnora external_id: wiki=node.NodeToken, drive=file.Token
	DocToken string
	// Feishu docx document token
	ObjToken          string
	Title             string
	URL               string
	ResourceID        string
	EditTime          time.Time
	BaseMeta          map[string]string
	MultimodalEnabled bool
}

// FetchDocxWithBlocks retrieves a docx document via the blocks API, converts it
// to Markdown, and returns a main item plus any parseable attachment/image
// sub-items. Falls back to the export API if the blocks API errors or renders
// empty. Shared by the wiki Connector and the Drive DriveConnector.
func FetchDocxWithBlocks(ctx context.Context, client *Client, in DocxFetchInput) ([]*types.FetchedItem, error) {
	// FEISHU_DOCX_PARSE_MODE=export forces the async export API (docx binary ->
	// docreader) instead of the blocks API. The blocks path renders image blocks
	// as empty `![图片]()` placeholders and fans images out into separate knowledge
	// items, which breaks image↔document association in retrieval/wiki/agent. The
	// export path yields a .docx that docreader parses inline, so images are bound
	// to the parent document via parent_chunk_id (same as a regular docx upload).
	// Default (unset / "blocks") keeps the existing blocks-first behaviour.
	if strings.EqualFold(strings.TrimSpace(os.Getenv("FEISHU_DOCX_PARSE_MODE")), "export") {
		item, err := exportDocxFallback(ctx, client, in)
		if err != nil {
			return nil, err
		}
		return []*types.FetchedItem{item}, nil
	}

	blocks, err := client.listDocumentBlocks(ctx, in.ObjToken)
	if err != nil {
		logger.Warnf(ctx, "[Feishu] blocks API failed for %s (%s), falling back to export: %v",
			in.Title, in.ObjToken, err)
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
		return nil, fmt.Errorf("convert blocks %s: %w", in.Title, err)
	}

	if len(strings.TrimSpace(string(md))) == 0 {
		logger.Infof(ctx, "[Feishu] doc %s (%s): blocks rendered empty Markdown, falling back to export",
			in.Title, in.ObjToken)
		item, ferr := exportDocxFallback(ctx, client, in)
		if ferr != nil {
			return nil, ferr
		}
		return []*types.FetchedItem{item}, nil
	}

	main := &types.FetchedItem{
		ExternalID:       in.DocToken,
		Title:            in.Title,
		Content:          md,
		ContentType:      "text/markdown",
		FileName:         SanitizeFileName(in.Title) + ".md",
		URL:              in.URL,
		UpdatedAt:        in.EditTime,
		SourceResourceID: in.ResourceID,
		Metadata:         in.BaseMeta,
		ReplacesSubtree:  true, // sweep stale attachment sub-items on re-sync
	}
	items := []*types.FetchedItem{main}

	keep := make([]string, 0, len(atts))
	childMeta := func() map[string]string {
		m := maps.Clone(in.BaseMeta)
		m["parent_node_token"] = in.DocToken
		m["attachment"] = "true"
		return m
	}
	for _, a := range atts {
		childID := types.SubtreeChildID(in.DocToken, "file", a.FileToken)
		keep = append(keep, childID) // present in the doc → never sweep as stale
		ext := strings.ToLower(filepath.Ext(a.Name))
		if ext == "" {
			logger.Warnf(ctx, "[Feishu] doc %s: skipping attachment with no usable filename (token=%s name=%q)",
				in.ObjToken, a.FileToken, a.Name)
			continue
		}
		if !parseableAttachmentExts[ext] {
			continue
		}
		data, derr := client.downloadMediaFile(ctx, a.FileToken)
		if derr != nil {
			logger.Warnf(ctx, "[Feishu] doc %s: attachment %q (token=%s) download failed: %v",
				in.ObjToken, a.Name, a.FileToken, derr)
			items = append(items, &types.FetchedItem{
				ExternalID:       childID,
				Title:            a.Name,
				SourceResourceID: in.ResourceID,
				Metadata:         FeishuErrorItemMeta(derr, childMeta()),
			})
			continue
		}
		if len(data) < MinAttachmentBytes {
			logger.Infof(ctx, "[Feishu] doc %s: skipping tiny attachment %q (token=%s, %d bytes < %d)",
				in.ObjToken, a.Name, a.FileToken, len(data), MinAttachmentBytes)
			continue
		}
		items = append(items, &types.FetchedItem{
			ExternalID:       childID,
			Title:            a.Name,
			Content:          data,
			ContentType:      "application/octet-stream",
			FileName:         SanitizeFileName(a.Name),
			URL:              in.URL,
			UpdatedAt:        in.EditTime,
			SourceResourceID: in.ResourceID,
			Metadata:         childMeta(),
		})
	}

	imgMeta := func() map[string]string {
		m := maps.Clone(in.BaseMeta)
		m["parent_node_token"] = in.DocToken
		m["embedded_image"] = "true"
		return m
	}
	for _, b := range blocks {
		if b.BlockType != BlockTypeImage || b.Image == nil || b.Image.Token == "" {
			continue
		}
		childID := types.SubtreeChildID(in.DocToken, "image", b.Image.Token)
		keep = append(keep, childID) // present in the doc → never sweep as stale
		if !in.MultimodalEnabled {
			continue // KB can't OCR images; the inline placeholder is all we keep
		}
		data, derr := client.downloadMediaFile(ctx, b.Image.Token)
		if derr != nil {
			logger.Warnf(ctx, "[Feishu] doc %s: image (token=%s) download failed: %v",
				in.ObjToken, b.Image.Token, derr)
			items = append(items, &types.FetchedItem{
				ExternalID:       childID,
				Title:            fmt.Sprintf("%s（内嵌图片）", in.Title),
				SourceResourceID: in.ResourceID,
				Metadata:         FeishuErrorItemMeta(derr, imgMeta()),
			})
			continue
		}
		if len(data) < MinAttachmentBytes {
			continue // decorative micro-image (icon/spacer)
		}
		ext, contentType, ok := SupportedImageExt(data)
		if !ok {
			logger.Warnf(ctx, "[Feishu] doc %s: skipping image (token=%s) of unsupported type %q",
				in.ObjToken, b.Image.Token, contentType)
			continue
		}
		items = append(items, &types.FetchedItem{
			ExternalID:       childID,
			Title:            fmt.Sprintf("%s（内嵌图片）", in.Title),
			Content:          data,
			ContentType:      contentType,
			FileName:         "image-" + b.Image.Token + ext,
			URL:              in.URL,
			UpdatedAt:        in.EditTime,
			SourceResourceID: in.ResourceID,
			Metadata:         imgMeta(),
		})
	}

	main.SubtreeKeep = keep
	return items, nil
}

// exportDocxFallback exports a docx document via the async export API and
// returns a single FetchedItem containing the exported .docx binary. Used by
// FetchDocxWithBlocks when the blocks API is unavailable or renders empty.
func exportDocxFallback(ctx context.Context, client *Client, in DocxFetchInput) (*types.FetchedItem, error) {
	data, fileName, err := client.ExportAndDownload(ctx, in.ObjToken, "docx")
	if err != nil {
		return nil, fmt.Errorf("export %s (docx): %w", in.Title, err)
	}

	ext := ExportFileExtToSuffix[ObjTypeToExportFileExtension["docx"]]
	if fileName == "" {
		fileName = SanitizeFileName(in.Title) + ext
	} else if !strings.HasSuffix(strings.ToLower(fileName), ext) {
		fileName = SanitizeFileName(fileName) + ext
	}

	return &types.FetchedItem{
		ExternalID:       in.DocToken,
		Title:            in.Title,
		Content:          data,
		ContentType:      "application/octet-stream",
		FileName:         fileName,
		URL:              in.URL,
		UpdatedAt:        in.EditTime,
		SourceResourceID: in.ResourceID,
		Metadata:         in.BaseMeta,
	}, nil
}
