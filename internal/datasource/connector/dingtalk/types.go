// Package dingtalk implements the DingTalk (钉钉) data source connector for WeKnora.
//
// It syncs documents from DingTalk knowledge bases (知识库) into WeKnora knowledge bases,
// converting DingTalk document blocks to Markdown format.
//
// DingTalk API docs:
//   - Knowledge base overview: https://open.dingtalk.com/document/development/knowledge-base-overview
//   - List workspaces:        https://open.dingtalk.com/document/development/get-knowledge-base-list
//   - List nodes:             https://open.dingtalk.com/document/development/get-node-list
//   - Get node:               https://open.dingtalk.com/document/development/obtain-node-details
//   - Document blocks:        https://open.dingtalk.com/document/development/api-docblocksquery
//   - Auth:                   https://open.dingtalk.com/document/development/obtain-the-access-token-of-an-internal-app
package dingtalk

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

// DefaultBaseURL is the default DingTalk Open Platform API base URL.
const DefaultBaseURL = "https://api.dingtalk.com"

// Config holds DingTalk-specific configuration for the data source connector.
// Uses the enterprise internal app (企业内部应用) authentication model.
type Config struct {
	// ClientID is the application's Client ID (appKey) from the DingTalk developer console.
	ClientID string `json:"client_id"`

	// ClientSecret is the application's Client Secret (appSecret) from the DingTalk developer console.
	ClientSecret string `json:"client_secret"`

	// OperatorID is the unionId of the user who operates the API.
	// DingTalk knowledge base APIs require an operatorId for permission checking.
	// Find it in: DingTalk admin console → 通讯录 → member details → unionId
	OperatorID string `json:"operator_id"`

	// BaseURL is the DingTalk API base URL (default: https://api.dingtalk.com).
	BaseURL string `json:"base_url,omitempty"`
}

// GetBaseURL returns the effective base URL, defaulting to DingTalk if not set.
// Normalizes: trims whitespace, removes trailing slash, and auto-prefixes https:// if missing.
func (c *Config) GetBaseURL() string {
	raw := strings.TrimSpace(c.BaseURL)
	if raw == "" {
		return DefaultBaseURL
	}
	// Auto-prefix scheme if missing.
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}
	return strings.TrimRight(raw, "/")
}

// parseDingtalkConfig extracts and validates DingTalk-specific configuration.
// Uses JSON marshal/unmarshal roundtrip (consistent with Feishu's parseFeishuConfig)
// rather than single-field type assertion, because we have multiple fields with
// optional defaults.
func parseDingtalkConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(credBytes, &cfg); err != nil {
		return nil, fmt.Errorf("parse dingtalk credentials: %w", err)
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, fmt.Errorf("%w: client_id is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, fmt.Errorf("%w: client_secret is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(cfg.OperatorID) == "" {
		return nil, fmt.Errorf("%w: operator_id is required", datasource.ErrInvalidCredentials)
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// --- DingTalk API response structures ---

// tokenResponse is the response for POST /v1.0/oauth2/accessToken.
type tokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"` // seconds
}

// workspaceListResponse is the response for GET /v2.0/wiki/workspaces.
type workspaceListResponse struct {
	Workspaces []workspace `json:"workspaces"`
	NextToken  string      `json:"nextToken"`
}

// workspace represents a DingTalk knowledge base (知识库/知识空间).
type workspace struct {
	WorkspaceID  string `json:"workspaceId"` // knowledge base ID (spaceUuid)
	CorpID       string `json:"corpId"`
	TeamID       string `json:"teamId"`     // knowledge group ID
	RootNodeID   string `json:"rootNodeId"` // root node ID (dentryUuid) — entry point for traversal
	Name         string `json:"name"`
	Type         string `json:"type"` // "TEAM" = knowledge base, "PERSONAL" = personal docs
	Description  string `json:"description"`
	URL          string `json:"url"`
	CreatorID    string `json:"creatorId"`
	ModifierID   string `json:"modifierId"`
	CreateTime   string `json:"createTime"`   // ISO 8601, e.g. "2023-05-15T11:29Z"
	ModifiedTime string `json:"modifiedTime"` // ISO 8601
}

// nodeListResponse is the response for GET /v2.0/wiki/nodes.
type nodeListResponse struct {
	Nodes     []node `json:"nodes"`
	NextToken string `json:"nextToken"`
}

// node represents a node (document or folder) in a DingTalk knowledge base.
type node struct {
	NodeID       string `json:"nodeId"` // node ID (dentryUuid)
	WorkspaceID  string `json:"workspaceId"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Type         string `json:"type"`      // "FILE" or "FOLDER"
	Category     string `json:"category"`  // "ALIDOC" = DingTalk doc, "DOCUMENT" = local doc, "IMAGE", "VIDEO", etc.
	Extension    string `json:"extension"` // e.g. "adoc" for ALIDOC
	URL          string `json:"url"`
	CreatorID    string `json:"creatorId"`
	ModifierID   string `json:"modifierId"`
	CreateTime   string `json:"createTime"`   // ISO 8601
	ModifiedTime string `json:"modifiedTime"` // ISO 8601 — used for incremental sync
	HasChildren  bool   `json:"hasChildren"`
	ParentID     string `json:"parentId"` // parent node ID
}

// nodeDetailResponse is the response for GET /v2.0/wiki/nodes/{nodeId}.
type nodeDetailResponse struct {
	Node node `json:"node"`
}

// flexibleInt accepts either a number (1), a numeric string ("1"), or
// DingTalk's heading style string ("heading-1") for integer JSON fields.
type flexibleInt int

func (f *flexibleInt) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = 0
		return nil
	}
	// String form — parse either "1" or DingTalk's "heading-1" format.
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		level := s
		if strings.HasPrefix(s, "heading-") {
			level = strings.TrimPrefix(s, "heading-")
		}
		i, err := strconv.Atoi(level)
		if err != nil {
			return fmt.Errorf("flexibleInt: cannot parse %q as int: %w", s, err)
		}
		*f = flexibleInt(i)
		return nil
	}
	// Numeric form.
	var i int
	if err := json.Unmarshal(b, &i); err != nil {
		return fmt.Errorf("flexibleInt: expected string or integer, got %s: %w", b, err)
	}
	*f = flexibleInt(i)
	return nil
}

// Int returns the flexibleInt as a regular int.
func (f flexibleInt) Int() int {
	return int(f)
}

// blocksResponse is the response for GET /v1.0/doc/suites/documents/{docKey}/blocks.
type blocksResponse struct {
	Success bool         `json:"success"`
	Result  blocksResult `json:"result"`
}

type blocksResult struct {
	Data []block `json:"data"`
}

// block represents a single block element in a DingTalk document.
type block struct {
	BlockType string          `json:"blockType"`
	ID        string          `json:"id"`
	Index     int             `json:"index"`
	Paragraph *blockParagraph `json:"paragraph,omitempty"`
	Heading   *blockHeading   `json:"heading,omitempty"`
	CodeBlock *blockCode      `json:"codeBlock,omitempty"`
	Table     *blockTable     `json:"table,omitempty"`
	List      *blockList      `json:"list,omitempty"`
	Quote     *blockQuote     `json:"quote,omitempty"`
	Divider   *blockDivider   `json:"divider,omitempty"`
	Image     *blockImage     `json:"image,omitempty"`
}

type blockParagraph struct {
	Text string `json:"text"`
}

type blockHeading struct {
	Level flexibleInt `json:"level"` // 1-6; DingTalk API may return string "1" or integer 1
	Text  string      `json:"text"`
}

type blockCode struct {
	Language string `json:"language"`
	Text     string `json:"text"`
}

type blockTable struct {
	RowSize flexibleInt `json:"rolSize"` // DingTalk API uses "rolSize" (typo in official docs)
	ColSize flexibleInt `json:"colSize"` // column count
	Cells   [][]string  `json:"cells"`   // String[][] per DingTalk docs — 2D array of cell text
}

type blockList struct {
	Style string   `json:"style"` // "ordered" or "unordered"
	Items []string `json:"items"`
}

type blockQuote struct {
	Text string `json:"text"`
}

type blockDivider struct{}

type blockImage struct {
	URL     string `json:"url"`
	AltText string `json:"altText"`
}

// dingtalkCursor stores incremental sync state for DingTalk.
type dingtalkCursor struct {
	// LastSyncTime is the timestamp of the last successful sync.
	LastSyncTime time.Time `json:"last_sync_time"`

	// NodeTimes maps workspaceId → nodeId → modifiedTime.
	// Used to detect which nodes have changed since last sync.
	NodeTimes map[string]map[string]string `json:"node_times,omitempty"`
}

// --- Utility functions ---

// sanitizeFileName removes characters that are invalid in filenames and
// truncates to a safe length at a UTF-8 rune boundary. Raw byte truncation
// would split a multi-byte codepoint (Chinese characters are 3 bytes in UTF-8)
// and produce an invalid UTF-8 string, which downstream filename validation
// (utf8.ValidString) rejects.
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
		for len(result) > 0 {
			r, size := utf8.DecodeLastRuneInString(result)
			if r != utf8.RuneError || size != 1 {
				break
			}
			result = result[:len(result)-1]
		}
	}
	if result == "" {
		return "untitled"
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

// parseDingtalkTime parses an ISO 8601 timestamp string (returns zero time on parse failure).
func parseDingtalkTime(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	// Try common ISO 8601 formats
	formats := []string{
		time.RFC3339,     // "2006-01-02T15:04:05Z07:00"
		time.RFC3339Nano, // "2006-01-02T15:04:05.999999999Z07:00"
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04Z",
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
	}
	for _, format := range formats {
		t, err := time.Parse(format, ts)
		if err == nil {
			return t
		}
	}
	return time.Time{}
}
