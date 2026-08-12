package ima

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// DefaultBaseURL is the IMA OpenAPI base URL. The Path prefix is baked into
// the client (see client.go); users only configure the host.
const DefaultBaseURL = "https://ima.qq.com"

// Base path for every OpenAPI request.
const apiBasePath = "/openapi/wiki/v1"

// Config holds IMA-specific configuration decoded from
// DataSourceConfig.Credentials. Both credentials are opaque strings and
// stored encrypted at rest by DataSourceConfig.ToJSON (see internal/types/datasource.go).
type Config struct {
	// ClientID: value of the `ima-openapi-clientid` request header,
	// obtained from https://ima.qq.com/agent-interface.
	ClientID string `json:"client_id"`

	// APIKey: value of the `ima-openapi-apikey` request header.
	APIKey string `json:"api_key"`

	// BaseURL lets users point at an on-prem / test IMA endpoint.
	// Defaults to https://ima.qq.com when empty.
	BaseURL string `json:"base_url,omitempty"`
}

// GetBaseURL returns the normalized base URL (empty → default, no trailing slash).
func (c *Config) GetBaseURL() string {
	url := strings.TrimSpace(c.BaseURL)
	if url == "" {
		return DefaultBaseURL
	}
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	return strings.TrimRight(url, "/")
}

// parseIMAConfig extracts and validates IMA-specific configuration.
// Uses JSON marshal/unmarshal roundtrip so extra fields are ignored gracefully.
func parseIMAConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(credBytes, &cfg); err != nil {
		return nil, fmt.Errorf("parse ima credentials: %w", err)
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, fmt.Errorf("%w: client_id is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("%w: api_key is required", datasource.ErrInvalidCredentials)
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// apiEnvelope is the uniform IMA response wrapper: `{ code, msg, data }`.
// A non-zero code is a business-level error and MUST be surfaced to the user
type apiEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// KnowledgeBaseInfo mirrors IMA's KnowledgeBaseInfo (get_knowledge_base).
type knowledgeBaseInfo struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	CoverURL             string   `json:"cover_url"`
	Description          string   `json:"description"`
	RecommendedQuestions []string `json:"recommended_questions"`
}

// searchedKnowledgeBaseInfo — search_knowledge_base returns fewer fields.
type searchedKnowledgeBaseInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	CoverURL string `json:"cover_url"`
}

// searchKnowledgeBaseResp — search_knowledge_base response.
type searchKnowledgeBaseResp struct {
	InfoList   []searchedKnowledgeBaseInfo `json:"info_list"`
	IsEnd      bool                        `json:"is_end"`
	NextCursor string                      `json:"next_cursor"`
}

// addableKnowledgeBaseInfo — get_addable_knowledge_base_list item.
// Contains only id + name (no cover_url), see api.md §7.
type addableKnowledgeBaseInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// getAddableKnowledgeBaseListResp — get_addable_knowledge_base_list response.
// NOTE: the JSON field is `addable_knowledge_base_list`, not `info_list`.
type getAddableKnowledgeBaseListResp struct {
	AddableKnowledgeBaseList []addableKnowledgeBaseInfo `json:"addable_knowledge_base_list"`
	IsEnd                    bool                       `json:"is_end"`
	NextCursor               string                     `json:"next_cursor"`
}

// getKnowledgeBaseResp — get_knowledge_base response (map keyed by id).
type getKnowledgeBaseResp struct {
	Infos map[string]knowledgeBaseInfo `json:"infos"`
}

// knowledgeInfo mirrors IMA's KnowledgeInfo (files, notes, etc.).
// IMPORTANT: IMA does not expose media_type in the list response, we must call
// get_media_info to discover it. However, folders are distinguished by presence
// of a non-empty folder_id — see folderInfo below.
type knowledgeInfo struct {
	MediaID        string `json:"media_id"`
	Title          string `json:"title"`
	ParentFolderID string `json:"parent_folder_id"`
	MediaType      int32  `json:"media_type,omitempty"`
}

// folderInfo mirrors IMA's FolderInfo.
type folderInfo struct {
	FolderID       string      `json:"folder_id"`
	Name           string      `json:"name"`
	FileNumber     json.Number `json:"file_number"`
	FolderNumber   json.Number `json:"folder_number"`
	ParentFolderID string      `json:"parent_folder_id"`
	IsTop          bool        `json:"is_top"`
}

// FileCount safely converts FileNumber to int64 (0 on empty/invalid).
func (f folderInfo) FileCount() int64 {
	if f.FileNumber == "" {
		return 0
	}
	n, err := f.FileNumber.Int64()
	if err != nil {
		return 0
	}
	return n
}

// FolderCount safely converts FolderNumber to int64 (0 on empty/invalid).
func (f folderInfo) FolderCount() int64 {
	if f.FolderNumber == "" {
		return 0
	}
	n, err := f.FolderNumber.Int64()
	if err != nil {
		return 0
	}
	return n
}

// getKnowledgeListResp — get_knowledge_list response.
// According to `references/api.md`, the response returns knowledge_list mixed
// with folders; we decode a loose JSON view so entries without a folder_id are
// treated as knowledge and those with folder_id are treated as folders.
type getKnowledgeListResp struct {
	// KnowledgeList is a loose slice of raw JSON objects because IMA returns
	// files and folders in the same array. We iterate and inspect fields.
	KnowledgeList []json.RawMessage `json:"knowledge_list"`
	IsEnd         bool              `json:"is_end"`
	NextCursor    string            `json:"next_cursor"`
	CurrentPath   []folderInfo      `json:"current_path"`
}

// urlInfo mirrors IMA's URLInfo (present on get_media_info for URL-backed media).
type urlInfo struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// notebookExtInfo — set on notes (media_type=11).
type notebookExtInfo struct {
	NotebookID string `json:"notebook_id"`
}

// getMediaInfoResp — get_media_info response.
type getMediaInfoResp struct {
	MediaType       int32           `json:"media_type"`
	URLInfo         urlInfo         `json:"url_info"`
	NotebookExtInfo notebookExtInfo `json:"notebook_ext_info"`
}

// imaCursor tracks per-knowledge-base state seen during the last sync.
//
// KBLogical: { logical_key: media_id } — the authoritative source for both
// delete detection AND "replacement" detection. IMA reassigns media_id
// whenever a same-named file is replaced in place, so a logical key (see
// logicalKey) is what we treat as a document's stable identity.
// A logical_key present last sync but absent this sync ⇒ delete.
// A logical_key whose media_id changed ⇒ replacement (re-fetch content; the
// emitted FetchedItem carries the same ExternalID so the ingest layer's
// "existing external_id → delete-and-recreate" path (see
// datasource_service.go applyFetchedItem) treats it as an update.
//
// Only items that were successfully synced (or deterministically skipped)
// this cycle are recorded, so a transient download failure leaves the key
// absent and the next run retries it instead of treating it as unchanged.
//
// KBMedia is a legacy field retained only so cursors persisted by earlier
// builds still deserialize; it is never written or consulted.
type imaCursor struct {
	LastSyncTime time.Time                    `json:"last_sync_time"`
	KBLogical    map[string]map[string]string `json:"kb_logical,omitempty"`
	KBMedia      map[string]map[string]string `json:"kb_media,omitempty"`
}

// logicalKey derives a stable identity for an IMA item that survives
// same-name replacement (which mints a new media_id server-side).
//
// The key is a short SHA-256 hex over (kb_id, parent_folder_id, title).
// Rationale:
//
//   - kb_id keeps identities isolated across knowledge bases (same file
//     copied into two KBs is two distinct docs from WeKnora's perspective).
//   - parent_folder_id + title anchor the doc to its folder location and
//     display name — the tuple IMA's own check_repeated_names uses to gate
//     uploads (see api.md), so IMA's own uniqueness constraint enforces
//     this key's uniqueness inside a KB.
//
// media_type is deliberately NOT part of the key: get_knowledge_list omits it
// (it only arrives later from get_media_info), so folding it in would mean
// every external_id shifts the day IMA starts populating the field in list
// responses — which reads as "every document deleted and re-added".
//
// Emitted as "ima_" + first 32 hex chars (128 bits) — small enough to fit
// well within Postgres varchar(64) external_id columns, still >> collision
// resistant for any realistic KB size.
func logicalKey(kbID, parentFolderID, title string) string {
	h := sha256.New()
	// Delimiter can't appear in any component because it's neither a valid
	// IMA id character nor allowed in titles once we've folded to bytes.
	_, _ = fmt.Fprintf(h, "%s\x1f%s\x1f%s", kbID, parentFolderID, title)
	return "ima_" + hex.EncodeToString(h.Sum(nil))[:32]
}

// IMA MediaType enum (see api.md §MediaType).
const (
	mediaTypePDF       int32 = 1
	mediaTypeWeb       int32 = 2
	mediaTypeWord      int32 = 3
	mediaTypePPT       int32 = 4
	mediaTypeExcel     int32 = 5
	mediaTypeMPArticle int32 = 6
	mediaTypeMarkdown  int32 = 7
	mediaTypeImage     int32 = 9
	mediaTypeNote      int32 = 11
	mediaTypeAISession int32 = 12
	mediaTypeTXT       int32 = 13
	mediaTypeXmind     int32 = 14
	mediaTypeAudio     int32 = 15
	mediaTypeVideo     int32 = 16
	mediaTypeHTML      int32 = 20
	mediaTypeEPUB      int32 = 21
)

// extensionForMediaType maps IMA media types to a file extension.
// Returns "" when the media type has no downloadable body (web page, note, AI
// session, video-parse) — callers should skip those or treat them as URL-only.
func extensionForMediaType(t int32) string {
	switch t {
	case mediaTypePDF:
		return "pdf"
	case mediaTypeWord:
		return "docx"
	case mediaTypePPT:
		return "pptx"
	case mediaTypeExcel:
		return "xlsx"
	case mediaTypeMarkdown:
		return "md"
	case mediaTypeImage:
		return "png"
	case mediaTypeTXT:
		return "txt"
	case mediaTypeXmind:
		return "xmind"
	case mediaTypeAudio:
		return "mp3"
	case mediaTypeHTML:
		return "html"
	case mediaTypeEPUB:
		return "epub"
	default:
		return ""
	}
}

// extensionForContentType maps a response Content-Type back to a file
// extension, ignoring any "; charset=..." suffix. Returns "" for types we
// have no better guess for than the media_type-derived default.
//
// IMA reports every image as media_type=9 regardless of the real encoding, so
// the download's Content-Type is the only signal that distinguishes a JPEG
// from a PNG. Naming a JPEG ".png" breaks the extension-based file-type checks
// downstream in the ingest pipeline.
func extensionForContentType(contentType string) string {
	mediaType := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.Index(mediaType, ";"); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	switch mediaType {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "image/bmp":
		return "bmp"
	default:
		return ""
	}
}

// mimeForExtension maps a file extension to a canonical MIME type.
// Used when the downloaded URL response omits (or misreports) Content-Type.
func mimeForExtension(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "pdf":
		return "application/pdf"
	case "doc":
		return "application/msword"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "ppt":
		return "application/vnd.ms-powerpoint"
	case "pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case "xls":
		return "application/vnd.ms-excel"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "md":
		return "text/markdown"
	case "txt":
		return "text/plain"
	case "html", "htm":
		return "text/html"
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "bmp":
		return "image/bmp"
	case "epub":
		return "application/epub+zip"
	case "xmind":
		return "application/x-xmind"
	case "mp3":
		return "audio/mpeg"
	case "m4a":
		return "audio/x-m4a"
	case "wav":
		return "audio/wav"
	case "aac":
		return "audio/aac"
	default:
		return "application/octet-stream"
	}
}

func isSkippableMediaType(t int32) bool {
	return t == mediaTypeNote || t == mediaTypeAISession || t == mediaTypeVideo
}
