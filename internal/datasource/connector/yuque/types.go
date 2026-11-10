// Package yuque implements the Yuque (语雀) data source connector for WeKnora.
//
// It syncs documents from personal and group knowledge bases (books/repos) into WeKnora
// knowledge bases, preserving Markdown formatting.
//
// Yuque API docs:
//   - Authentication: X-Auth-Token header, personal token from https://www.yuque.com/settings/tokens
//   - User:           GET /api/v2/user
//   - Groups:         GET /api/v2/users/{id}/groups
//   - Repos:          GET /api/v2/users/{login}/repos, GET /api/v2/groups/{login}/repos
//   - Docs:           GET /api/v2/repos/{book_id}/docs (list), GET /api/v2/repos/docs/{id} (detail)
//
// Known limitations (v1):
//   - Only syncs type=Doc (Sheet/Thread/Board/Table skipped)
//   - Only syncs status="1" (published), drafts skipped
//   - Private-book images (CDN URLs with auth) may fail to load
//   - Lake editor may leave non-standard markdown (anchors, sized image attrs)
package yuque

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// DefaultBaseURL is the Yuque public cloud API base URL.
const DefaultBaseURL = "https://www.yuque.com"

// Config holds Yuque-specific configuration.
type Config struct {
	// APIToken is a personal token from Yuque settings → tokens.
	APIToken string `json:"api_token"`

	// BaseURL is the Yuque deployment base URL (default: https://www.yuque.com).
	// For enterprise/private deployments, use the company's yuque domain.
	BaseURL string `json:"base_url,omitempty"`
}

// GetBaseURL returns the normalized base URL:
//   - empty → DefaultBaseURL
//   - missing scheme → prepend "https://"
//   - trailing slash → stripped
func (c *Config) GetBaseURL() string {
	url := strings.TrimSpace(c.BaseURL)
	if url == "" {
		return DefaultBaseURL
	}
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	url = strings.TrimRight(url, "/")
	return url
}

// parseYuqueConfig extracts and validates Yuque-specific configuration.
// Uses JSON marshal/unmarshal roundtrip (consistent with Feishu's parseFeishuConfig)
// rather than single-field type assertion, because we have multiple fields with
// optional defaults.
func parseYuqueConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(credBytes, &cfg); err != nil {
		return nil, fmt.Errorf("parse yuque credentials: %w", err)
	}
	if strings.TrimSpace(cfg.APIToken) == "" {
		return nil, fmt.Errorf("%w: api_token is required", datasource.ErrInvalidCredentials)
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// --- Yuque API response types ---

// flexibleStatus accepts either a string ("1") or a number (1) for the doc
// `status` field. Yuque's OpenAPI spec declares `status` as string, but the
// runtime API returns it as an integer, so unmarshaling into a plain string
// fails with "cannot unmarshal number into Go struct field ... of type string".
// Normalizing to the textual form lets existing comparisons (e.g. != "1") keep
// working for both response shapes.
type flexibleStatus string

func (s *flexibleStatus) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*s = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*s = flexibleStatus(str)
		return nil
	}
	// Integer form. We decode to int64 so that floats, booleans, arrays, and
	// objects fail loudly instead of being silently stringified — if Yuque
	// changes the shape again, we'd rather surface a clear error than feed
	// garbage to the Status == "1" comparison.
	var i int64
	if err := json.Unmarshal(b, &i); err != nil {
		return fmt.Errorf("flexibleStatus: expected string or integer, got %s: %w", b, err)
	}
	*s = flexibleStatus(strconv.FormatInt(i, 10))
	return nil
}

// apiErrorBody is the error body shape Yuque sometimes returns on non-2xx.
type apiErrorBody struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// v2UserResponse wraps GET /api/v2/user.
type v2UserResponse struct {
	Data v2User `json:"data"`
}

type v2User struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"` // "User" for personal token, "Group" for team token
	Login string `json:"login"`
	Name  string `json:"name"`
}

// v2GroupListResponse wraps GET /api/v2/users/{id}/groups.
type v2GroupListResponse struct {
	Data []v2Group `json:"data"`
}

type v2Group struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

// v2RepoListResponse wraps GET /api/v2/users/{login}/repos and /groups/{login}/repos.
type v2RepoListResponse struct {
	Data []v2Repo `json:"data"`
}

type v2Repo struct {
	ID          int64  `json:"id"`
	Type        string `json:"type"` // "Book" | "Design" (listing filter enum; connector requests type=Book)
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	UserID      int64  `json:"user_id"`
	Namespace   string `json:"namespace"` // e.g. "group_login/book_slug"
	Public      int    `json:"public"`    // 0:private, 1:public, 2:internal
	Description string `json:"description"`
	UpdatedAt   string `json:"updated_at"` // RFC3339 string
}

// v2DocListResponse wraps GET /api/v2/repos/{book_id}/docs.
type v2DocListResponse struct {
	Meta struct {
		Total int `json:"total"`
	} `json:"meta"`
	Data []v2Doc `json:"data"`
}

// v2Doc is the document summary returned by the list endpoint (no body).
type v2Doc struct {
	ID               int64          `json:"id"`
	Type             string         `json:"type"` // Doc / Sheet / Thread / Board / Table
	Slug             string         `json:"slug"`
	Title            string         `json:"title"`
	BookID           int64          `json:"book_id"`
	UserID           int64          `json:"user_id"`
	Status           flexibleStatus `json:"status"`             // "0" draft, "1" published — API may return int or string
	ContentUpdatedAt string         `json:"content_updated_at"` // RFC3339 string — use for change detection
	UpdatedAt        string         `json:"updated_at"`
	WordCount        int            `json:"word_count"`
}

// v2DocDetailResponse wraps GET /api/v2/repos/docs/{id}.
type v2DocDetailResponse struct {
	Data v2DocDetail `json:"data"`
}

type v2DocDetail struct {
	ID               int64          `json:"id"`
	Type             string         `json:"type"`
	Slug             string         `json:"slug"`
	Title            string         `json:"title"`
	BookID           int64          `json:"book_id"`
	Format           string         `json:"format"` // "markdown" / "lake" / "html"
	Body             string         `json:"body"`   // Markdown content
	Status           flexibleStatus `json:"status"` // API may return int or string
	ContentUpdatedAt string         `json:"content_updated_at"`
	UpdatedAt        string         `json:"updated_at"`
	WordCount        int            `json:"word_count"`
	Book             v2Repo         `json:"book"`
}

// yuqueCursor stores incremental sync state.
// Key1: book_id (string), Key2: doc_id (string), Value: content_updated_at (raw RFC3339 string)
type yuqueCursor struct {
	LastSyncTime time.Time                    `json:"last_sync_time"`
	BookDocTimes map[string]map[string]string `json:"book_doc_times,omitempty"`
}

// parseContentUpdatedAt parses Yuque ISO 8601 timestamp (returns zero time on parse failure).
func parseContentUpdatedAt(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

// redactToken returns a masked form of the token for logging (never log the full token).
func redactToken(t string) string {
	if len(t) < 12 {
		return "***"
	}
	return t[:6] + "..." + t[len(t)-4:]
}

// --- Table of contents (TOC) ---

// v2TOCResponse wraps GET /api/v2/repos/{book_id}/toc.
type v2TOCResponse struct {
	Data []v2TOCNode `json:"data"`
}

// v2TOCNode is one entry in a book's table of contents.
//
// The endpoint returns a flat list; the hierarchy is expressed by parent_uuid.
// TITLE nodes are grouping headers (their doc_id is empty), DOC nodes are the
// actual documents. Both carry a uuid that other nodes reference as parent_uuid.
type v2TOCNode struct {
	UUID       string        `json:"uuid"`
	Type       string        `json:"type"`
	Title      string        `json:"title"`
	ParentUUID string        `json:"parent_uuid"`
	DocID      flexibleDocID `json:"doc_id"`
}

const (
	tocNodeTypeTitle = "TITLE"
	tocNodeTypeDoc   = "DOC"
)

// flexibleDocID accepts the TOC `doc_id` field, which the API serializes as a
// number on DOC nodes but as an empty string on TITLE nodes. Decoding straight
// into int64 fails on "" ("cannot unmarshal string into Go struct field").
// Absent or empty IDs normalise to 0, which is never a real Yuque document ID.
type flexibleDocID int64

func (d *flexibleDocID) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*d = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*d = 0
			return nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("flexibleDocID: %q is not a numeric id: %w", s, err)
		}
		*d = flexibleDocID(n)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("flexibleDocID: expected string or integer, got %s: %w", b, err)
	}
	*d = flexibleDocID(n)
	return nil
}

// maxTOCDepth bounds the ancestor walk in buildTOCPaths. Real nesting is only a
// few levels deep; the bound is a backstop against malformed input.
const maxTOCDepth = 32

// buildTOCPaths folds a flat TOC node list into two per-document lookup maps:
//
//	segs  — doc_id → ancestor titles, root-first, each already passed through
//	        sanitizeFileName. Absent when the document sits directly under the
//	        book root.
//	inTOC — doc_id → whether the document appears in the TOC node list at all.
//
// The two maps are deliberately separate and must stay that way. A document can
// legitimately appear in the TOC without belonging to any group, in which case
// inTOC is true and segs is empty. A caller filtering out "documents that are
// not in the TOC" must test inTOC — testing segs[id] instead would also drop
// those group-less documents.
//
// Sanitising per segment here (rather than on the joined path) is what keeps a
// "/" inside a group title from silently creating an extra nesting level.
func buildTOCPaths(nodes []v2TOCNode) (map[int64][]string, map[int64]bool) {
	byUUID := make(map[string]v2TOCNode, len(nodes))
	for _, n := range nodes {
		if n.UUID != "" {
			byUUID[n.UUID] = n
		}
	}

	segs := make(map[int64][]string)
	inTOC := make(map[int64]bool)

	for _, n := range nodes {
		if n.Type != tocNodeTypeDoc {
			continue
		}
		id := int64(n.DocID)
		if id == 0 {
			continue
		}
		inTOC[id] = true

		var reverse []string
		visited := map[string]bool{n.UUID: true}
		cur := n
		for len(reverse) < maxTOCDepth {
			parent, ok := byUUID[cur.ParentUUID]
			if !ok || parent.UUID == "" || visited[parent.UUID] {
				break // reached the root, hit a dangling parent, or a cycle
			}
			visited[parent.UUID] = true
			if title := strings.TrimSpace(parent.Title); title != "" {
				reverse = append(reverse, datasource.SanitizeFileName(title))
			}
			cur = parent
		}
		// Walked child → root; flip to root → child for path order.
		out := make([]string, len(reverse))
		for i, s := range reverse {
			out[len(reverse)-1-i] = s
		}
		if len(out) > 0 {
			segs[id] = out
		}
	}
	return segs, inTOC
}

// --- Folder path settings ---

const (
	// folderModeNone is the historical behaviour: a bare "<title>.md" file name,
	// so every synced document lands at the knowledge base root.
	folderModeNone = "none"
	// folderModeTOC derives the folder path from the book's table of contents.
	folderModeTOC = "toc"
)

// folderSettings controls how folder paths are derived for synced documents.
// Configured under DataSourceConfig.Settings, never under Credentials.
type folderSettings struct {
	// FolderMode is folderModeNone or folderModeTOC.
	FolderMode string
	// TOCOnly admits only documents that appear in the book's TOC. It is an
	// admission filter, not a reaper: a filtered document is simply not
	// ingested, and one already in the knowledge base is left untouched. Only
	// meaningful when FolderMode is folderModeTOC.
	TOCOnly bool
}

// defaultFolderSettings keeps the emitted FileName byte-for-byte identical to
// previous releases, so enabling the new behaviour is strictly opt-in and an
// existing data source keeps working unchanged after an upgrade.
func defaultFolderSettings() folderSettings {
	return folderSettings{
		FolderMode: folderModeNone,
		TOCOnly:    false,
	}
}

// parseFolderSettings reads the folder-path settings, falling back to the
// default for any key that is absent or unrecognised. A typo in the settings
// map must not fail a sync.
func parseFolderSettings(ctx context.Context, ds *types.DataSourceConfig) folderSettings {
	s := defaultFolderSettings()
	if ds == nil || ds.Settings == nil {
		return s
	}
	if raw, ok := ds.Settings["folder_mode"]; ok {
		mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
		switch mode {
		case folderModeNone, folderModeTOC:
			s.FolderMode = mode
		default:
			logger.Warnf(ctx, "[Yuque] unknown folder_mode %q, keeping %q", mode, s.FolderMode)
		}
	}
	s.TOCOnly = settingBool(ds.Settings["toc_only"], s.TOCOnly)
	return s
}

// settingBool interprets a settings value as a boolean, returning def when the
// value is absent or of an unrecognised shape. Settings arrive from JSON, so a
// hand-edited config may carry either a real bool or its string form.
func settingBool(v interface{}, def bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return def
}

// buildFolderFileName assembles the FileName for a synced document.
//
// folder_path downstream is derived from FileName alone
// (SplitKnowledgeRelativePath → NormalizeKnowledgeFolderPath), so the folder
// hierarchy has to be encoded here as a relative path.
//
// With the default settings — or whenever the book's TOC could not be loaded —
// this returns exactly "<title>.md", which is the byte-for-byte behaviour of
// previous releases.
//
// When the TOC is in play the book name is always the first segment. It is the
// only carrier of the "which book" dimension — the automatic tag granularity is
// per data source, not per book — and omitting it would silently merge
// same-named top-level groups from different books into one folder.
func buildFolderFileName(
	settings folderSettings, tocOK bool, bookName string, tocSegs []string, title string,
) string {
	fileName := datasource.SanitizeFileName(title) + ".md"
	if settings.FolderMode != folderModeTOC || !tocOK {
		return fileName
	}
	segments := make([]string, 0, len(tocSegs)+1)
	if bookName != "" {
		segments = append(segments, datasource.SanitizeFileName(bookName))
	}
	// tocSegs are already sanitised by buildTOCPaths.
	segments = append(segments, tocSegs...)
	if len(segments) == 0 {
		return fileName
	}
	return strings.Join(segments, "/") + "/" + fileName
}
