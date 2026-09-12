// Package outline implements the Outline data source connector for WeKnora.
//
// It syncs documents from Outline collections (cloud at app.getoutline.com or a
// self-hosted instance) into WeKnora knowledge bases, preserving the source
// Markdown and inlining attachment images so they survive ingestion.
//
// Outline API (https://www.getoutline.com/developers) — every RPC endpoint is a
// POST with a JSON body, authenticated with an API token:
//   - Authentication: Authorization: Bearer <api_token>
//   - Identity:       POST /api/auth.info
//   - Collections:    POST /api/collections.list      {limit, offset}
//   - Documents:      POST /api/documents.list        {collectionId, limit, offset}
//   - Attachments:    GET  /api/attachments.redirect?id=<uuid>  (302 to storage)
//
// documents.list returns each document's full Markdown in `text`, so one
// paginated pass both enumerates and fetches: no per-document detail call.
//
// Images: Outline attachment URLs require the Bearer token, while WeKnora's
// ingestion downloads http(s) images anonymously (ImageResolver.ResolveRemoteImages
// sends no auth header), so a passed-through Outline URL always 401s and the
// image is silently lost. This connector downloads each attachment with the token
// and inlines it as a base64 data URI, which ImageResolver.ResolveDataURIImages
// stores and rewrites to a resource:// handle. Inlining (rather than fanning
// images out into separate knowledge items) is what keeps an image bound to its
// parent document via parent_chunk_id — see the FEISHU_DOCX_PARSE_MODE comment in
// connector/feishu/core/shared.go for why the fan-out path was abandoned there.
//
// Known limitations (v1):
//   - At most 30 images per document are inlined (ImageResolver.maxRemoteImages);
//     the rest keep their original URL and will not render.
//   - Images larger than 9MB are left as URLs rather than downscaled.
//   - Templates and documents in the trash are not synced.
package outline

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// DefaultBaseURL is the Outline cloud API base URL. Self-hosted instances set
// base_url to their own origin.
const DefaultBaseURL = "https://app.getoutline.com"

// Config holds Outline-specific configuration.
type Config struct {
	// APIToken is an Outline API token (Settings -> API Tokens -> New token).
	APIToken string `json:"api_token"`

	// BaseURL is the Outline origin. Empty means the public cloud.
	BaseURL string `json:"base_url,omitempty"`
}

// GetBaseURL returns the normalized base URL:
//   - empty -> DefaultBaseURL
//   - missing scheme -> prepend "https://"
//   - trailing slashes -> stripped
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

// parseOutlineConfig extracts and validates Outline-specific configuration.
// Uses a JSON marshal/unmarshal roundtrip (consistent with parseYuqueConfig)
// rather than per-field type assertions, because base_url is optional.
func parseOutlineConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(credBytes, &cfg); err != nil {
		return nil, fmt.Errorf("parse outline credentials: %w", err)
	}
	if strings.TrimSpace(cfg.APIToken) == "" {
		return nil, fmt.Errorf("%w: api_token is required", datasource.ErrInvalidCredentials)
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// --- Outline API response types ---

// pagination is the envelope Outline attaches to every list endpoint.
type pagination struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
	Total  int `json:"total"`
}

// collection is one Outline collection (the syncable resource).
type collection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"` // path only, e.g. "/collection/handbook-abc123"
	UpdatedAt   string `json:"updatedAt"`
}

// document is one Outline document. documents.list returns the full Markdown in
// Text, so this single shape covers both listing and content fetching.
type document struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	Text             string `json:"text"`
	URL              string `json:"url"` // path only, e.g. "/doc/title-abc123"
	CollectionID     string `json:"collectionId"`
	ParentDocumentID string `json:"parentDocumentId"`
	TemplateID       string `json:"templateId"`
	// Revision increments only when the document's content or title changes,
	// making it a tighter change signal than updatedAt.
	Revision   int    `json:"revision"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	DeletedAt  string `json:"deletedAt"`
	ArchivedAt string `json:"archivedAt"`
}

// isGone reports whether Outline considers the document removed from normal
// view. documents.list does not return such documents, so this is a defensive
// check for API variations rather than the primary deletion signal.
func (d document) isGone() bool {
	return strings.TrimSpace(d.DeletedAt) != "" || strings.TrimSpace(d.ArchivedAt) != ""
}

type collectionsListResponse struct {
	Data       []collection `json:"data"`
	Pagination pagination   `json:"pagination"`
}

type documentsListResponse struct {
	Data       []document `json:"data"`
	Pagination pagination `json:"pagination"`
}

// authInfoResponse wraps POST /api/auth.info. Only the team name is read: the
// user object carries an email address, which must never reach the logs.
type authInfoResponse struct {
	Data struct {
		Team struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
	} `json:"data"`
}

// apiErrorBody is the error shape Outline returns on non-2xx.
type apiErrorBody struct {
	Ok      bool   `json:"ok"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

// outlineCursor stores incremental sync state.
// CollectionDocRevisions: collectionID -> documentID -> revision.
type outlineCursor struct {
	LastSyncTime           time.Time                 `json:"last_sync_time"`
	CollectionDocRevisions map[string]map[string]int `json:"collection_doc_revisions,omitempty"`
}

// parseOutlineTime parses an Outline timestamp, returning the zero time on
// failure. Outline emits RFC3339 with milliseconds ("2026-09-11T07:11:10.199Z"),
// which time.RFC3339 accepts.
func parseOutlineTime(ts string) time.Time {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

// sanitizeFileName removes characters that are invalid in filenames and
// truncates to a safe length at a UTF-8 rune boundary. Raw byte truncation
// would split a multi-byte codepoint and produce an invalid UTF-8 string, which
// downstream filename validation rejects.
func sanitizeFileName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "untitled"
	}
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	result := replacer.Replace(name)
	const maxBytes = 200
	if len(result) > maxBytes {
		result = result[:maxBytes]
		// Peel trailing bytes that no longer form a complete rune.
		for len(result) > 0 {
			r, size := utf8.DecodeLastRuneInString(result)
			if r != utf8.RuneError || size != 1 {
				break
			}
			result = result[:len(result)-1]
		}
	}
	return result
}

// redactToken returns a masked form of the token for logging.
func redactToken(t string) string {
	if len(t) < 12 {
		return "***"
	}
	return t[:6] + "..." + t[len(t)-4:]
}
