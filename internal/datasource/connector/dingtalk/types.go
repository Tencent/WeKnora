// Package dingtalk implements the DingTalk (钉钉) data source connector for WeKnora.
//
// It syncs documents from DingTalk knowledge bases (wiki) into WeKnora
// knowledge bases, preserving Markdown formatting.
//
// DingTalk OpenAPI docs:
//   - Authentication: POST /v1.0/oauth2/accessToken (client_id + client_secret)
//   - Workspaces:     GET /v2.0/wiki/workspaces (list knowledge bases)
//   - Nodes:          GET /v2.0/wiki/nodes (list nodes within a workspace)
//   - Token:          https://open.dingtalk.com/document/orgapp-server/dingtalk-openapi-overview
//
// Known limitations (v1):
//   - Only syncs type=FILE and category=ALIDOC/DOCUMENT (document nodes)
//   - Folders are listed as resources but not synced as content
//   - Incremental sync based on node modifiedTime
package dingtalk

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// DefaultBaseURL is the DingTalk OpenAPI base URL.
const DefaultBaseURL = "https://api.dingtalk.com"

// Config holds DingTalk-specific configuration.
type Config struct {
	// ClientID is the AppKey from DingTalk application credentials.
	ClientID string `json:"client_id"`

	// ClientSecret is the AppSecret from DingTalk application credentials.
	ClientSecret string `json:"client_secret"`

	// OperatorID is the unionId of the operator used for DingTalk Wiki API
	// calls. DingTalk requires it on workspace and node listing requests.
	OperatorID string `json:"operator_id,omitempty"`

	// BaseURL is only used by tests and private deployments. The frontend does
	// not expose it, so production still uses DingTalk's public OpenAPI host.
	BaseURL string `json:"base_url,omitempty"`
}

// GetBaseURL returns the normalized base URL.
func (c *Config) GetBaseURL() string {
	baseURL := strings.TrimSpace(c.BaseURL)
	if baseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(baseURL, "/")
}

// parseDingTalkConfig extracts and validates DingTalk-specific configuration.
func parseDingTalkConfig(config *types.DataSourceConfig) (*Config, error) {
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
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.OperatorID = strings.TrimSpace(cfg.OperatorID)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	return &cfg, nil
}

// --- DingTalk API request/response types ---

// accessTokenRequest is the request body for getting access token.
type accessTokenRequest struct {
	AppKey    string `json:"appKey"`
	AppSecret string `json:"appSecret"`
}

// accessTokenResponse is the response from getting access token.
type accessTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpireIn    int    `json:"expireIn"`
}

// wikiWorkspacesResponse wraps GET /v2.0/wiki/workspaces.
type wikiWorkspacesResponse struct {
	Workspaces []WikiWorkspace `json:"workspaces,omitempty"`
	NextToken  string          `json:"nextToken,omitempty"`
}

// WikiWorkspace represents a DingTalk knowledge base (workspace).
type WikiWorkspace struct {
	WorkspaceID  string `json:"workspaceId"` // spaceUuid
	CorpID       string `json:"corpId"`
	TeamID       string `json:"teamId,omitempty"`
	RootNodeID   string `json:"rootNodeId"` // dentryUuid of root node
	Name         string `json:"name"`
	Type         string `json:"type"` // "TEAM" or "PERSONAL"
	Description  string `json:"description,omitempty"`
	URL          string `json:"url"`
	CreatorID    string `json:"creatorId,omitempty"`
	CreateTime   string `json:"createTime,omitempty"`
	ModifiedTime string `json:"modifiedTime,omitempty"`
}

// wikiNodesResponse wraps GET /v2.0/wiki/nodes.
type wikiNodesResponse struct {
	Nodes     []WikiNode `json:"nodes,omitempty"`
	NextToken string     `json:"nextToken,omitempty"`
}

// docBlocksResponse wraps GET /v1.0/doc/suites/documents/{docKey}/blocks.
// DingTalk has returned both top-level and data-wrapped shapes across docs and
// SDKs, so the client accepts both to keep the connector forward-compatible.
type docBlocksResponse struct {
	Blocks []docBlock `json:"blocks,omitempty"`
	Data   struct {
		Blocks []docBlock `json:"blocks,omitempty"`
	} `json:"data,omitempty"`
	Result struct {
		Blocks []docBlock `json:"blocks,omitempty"`
		Data   []docBlock `json:"data,omitempty"`
		List   []docBlock `json:"list,omitempty"`
	} `json:"result,omitempty"`
}

func (r docBlocksResponse) allBlocks() []docBlock {
	if len(r.Blocks) > 0 {
		return r.Blocks
	}
	if len(r.Data.Blocks) > 0 {
		return r.Data.Blocks
	}
	if len(r.Result.Blocks) > 0 {
		return r.Result.Blocks
	}
	if len(r.Result.List) > 0 {
		return r.Result.List
	}
	return r.Result.Data
}

// docBlock intentionally models only the portable text-bearing fields we need
// for ingestion. Raw keeps the complete block so we can extract common text
// keys without coupling the connector to every DingTalk block variant.
type docBlock struct {
	BlockID   string          `json:"blockId,omitempty"`
	BlockType string          `json:"blockType,omitempty"`
	Type      string          `json:"type,omitempty"`
	Text      string          `json:"text,omitempty"`
	Content   string          `json:"content,omitempty"`
	Children  []docBlock      `json:"children,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

func (b *docBlock) UnmarshalJSON(data []byte) error {
	type alias docBlock
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*b = docBlock(a)
	b.Raw = append(b.Raw[:0], data...)
	return nil
}

// WikiNode represents a node (file or folder) in DingTalk wiki.
type WikiNode struct {
	NodeID       string `json:"nodeId"` // dentryUuid
	DocKey       string `json:"docKey,omitempty"`
	WorkspaceID  string `json:"workspaceId"` // spaceUuid
	Name         string `json:"name"`
	Size         int64  `json:"size,omitempty"`
	NodeType     string `json:"type"`     // "FILE" or "FOLDER"
	Category     string `json:"category"` // "ALIDOC", "DOCUMENT", "IMAGE", etc.
	Extension    string `json:"extension,omitempty"`
	URL          string `json:"url,omitempty"`
	CreatorID    string `json:"creatorId,omitempty"`
	ModifierID   string `json:"modifierId,omitempty"`
	CreateTime   string `json:"createTime,omitempty"`
	ModifiedTime string `json:"modifiedTime,omitempty"`
	HasChildren  bool   `json:"hasChildren,omitempty"`
	WordCount    int64  `json:"wordCount,omitempty"`
}

// dingtalkErrorResponse is the error body shape DingTalk returns on non-2xx.
type dingtalkErrorResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r dingtalkErrorResponse) errorCode() string {
	if r.Code != "" {
		return r.Code
	}
	if r.ErrCode != 0 {
		return fmt.Sprintf("%d", r.ErrCode)
	}
	return ""
}

func (r dingtalkErrorResponse) errorMessage() string {
	if r.Message != "" {
		return r.Message
	}
	return r.ErrMsg
}

// dingtalkAPIError represents a DingTalk API error.
type dingtalkAPIError struct {
	Code string
	Msg  string
}

func (e *dingtalkAPIError) Error() string {
	return fmt.Sprintf("dingtalk api error: code=%s msg=%s", e.Code, e.Msg)
}

// dingtalkCursor stores incremental sync state.
type dingtalkCursor struct {
	LastSyncTime   time.Time                       `json:"last_sync_time"`
	WorkspaceTimes map[string]map[string]time.Time `json:"workspace_node_times,omitempty"` // workspaceId -> nodeId -> modifiedTime
}

// parseTime parses DingTalk timestamp (returns zero time on parse failure).
func parseTime(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	// DingTalk uses RFC3339 format: "2024-01-01T00:00:00+08:00"
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		// Try alternative format without timezone
		t, err = time.Parse("2006-01-02T15:04:05Z", ts)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

// sanitizeFileName removes characters that are invalid in filenames and
// truncates to a safe length at a UTF-8 rune boundary.
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

// redactClientID returns a masked form for logging (never log the full ID).
func redactClientID(id string) string {
	if len(id) < 8 {
		return "***"
	}
	return id[:4] + "..." + id[len(id)-4:]
}
