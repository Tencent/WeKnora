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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
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

// sanitizeFileName removes characters that are invalid in filenames and
// truncates to a safe length at a UTF-8 rune boundary. Raw byte truncation
// would split a multi-byte codepoint (Chinese characters are 3 bytes in UTF-8)
// and produce an invalid UTF-8 string, which downstream filename validation
// (utf8.ValidString) rejects with "文件名包含非法字符".
func sanitizeFileName(name string) string {
	if name == "" {
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
		// Peel any trailing bytes that no longer form a complete rune.
		// DecodeLastRuneInString returns (RuneError, 1) for a partial UTF-8
		// sequence — at most 3 bytes need peeling since a rune is ≤ 4 bytes.
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

// redactToken returns a masked form of the token for logging (never log the full token).
func redactToken(t string) string {
	if len(t) < 12 {
		return "***"
	}
	return t[:6] + "..." + t[len(t)-4:]
}
