// Package dingtalk implements the DingTalk (钉钉) data source connector for WeKnora.
//
// It syncs documents from DingTalk knowledge bases (知识库) into WeKnora knowledge bases,
// preserving document content as Markdown.
//
// DingTalk API docs:
//   - Authentication:   POST /v1.0/oauth2/accessToken (appKey + appSecret → accessToken)
//   - Knowledge bases:  GET /v1.0/doc/spaces
//   - Space nodes:      GET /v1.0/doc/spaces/{spaceId}/nodes
//   - Node detail:      GET /v1.0/doc/nodes/{nodeId}
//
// Known limitations (v1):
//   - Only syncs type="doc" (Sheet/Mindmap/Folder skipped as content)
//   - Folder nodes are traversed for children but have no content themselves
//   - AccessToken expires ~2h; cached and refreshed 5 min before expiry
package dingtalk

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// DefaultBaseURL is the DingTalk OpenAPI base URL.
const DefaultBaseURL = "https://api.dingtalk.com"

// WebBaseURL is the DingTalk app origin for user-facing links.
const WebBaseURL = "https://www.dingtalk.com"

// Config holds DingTalk-specific configuration.
type Config struct {
	// AppKey is the Client ID from DingTalk developer console.
	AppKey string `json:"app_key"`

	// AppSecret is the Client Secret from DingTalk developer console.
	AppSecret string `json:"app_secret"`

	// BaseURL is the DingTalk API base URL (default: https://api.dingtalk.com).
	// For private/enterprise deployments, use the company's DingTalk API domain.
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

	if strings.TrimSpace(cfg.AppKey) == "" {
		return nil, fmt.Errorf("%w: app_key is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(cfg.AppSecret) == "" {
		return nil, fmt.Errorf("%w: app_secret is required", datasource.ErrInvalidCredentials)
	}

	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// --- DingTalk API response types ---

// tokenResponse is the response for POST /v1.0/oauth2/accessToken.
type tokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"` // seconds
}

// spaceListResponse wraps GET /v1.0/doc/spaces.
type spaceListResponse struct {
	Result struct {
		Spaces    []docSpace `json:"spaces"`
		NextToken string     `json:"nextToken"`
	} `json:"result"`
}

// docSpace represents a DingTalk knowledge base (space).
type docSpace struct {
	SpaceID string `json:"spaceId"`
	Name    string `json:"name"`
	Desc    string `json:"desc"`
}

// nodeListResponse wraps GET /v1.0/doc/spaces/{spaceId}/nodes.
type nodeListResponse struct {
	Result struct {
		Nodes     []docNode `json:"nodes"`
		NextToken string    `json:"nextToken"`
	} `json:"result"`
}

// docNode represents a node (document or folder) in a DingTalk knowledge base space.
type docNode struct {
	NodeID     string `json:"nodeId"`
	SpaceID    string `json:"spaceId"`
	Name       string `json:"name"`
	Type       string `json:"type"`      // "doc" | "sheet" | "mindmap" | "folder" | "file"
	ParentID   string `json:"parentId"`
	EditTime   int64  `json:"editTime"`  // unix timestamp in milliseconds
	CreateTime int64  `json:"createTime"`
	Creator    string `json:"creator"`
}

// nodeDetailResponse wraps GET /v1.0/doc/nodes/{nodeId}.
type nodeDetailResponse struct {
	Result docNodeDetail `json:"result"`
}

// docNodeDetail is the detailed node info including content.
type docNodeDetail struct {
	NodeID     string `json:"nodeId"`
	SpaceID    string `json:"spaceId"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Content    string `json:"content"`  // Markdown content for type="doc"
	EditTime   int64  `json:"editTime"`
	CreateTime int64  `json:"createTime"`
	Creator    string `json:"creator"`
}

// dingtalkCursor stores incremental sync state.
// Key1: resource_id (string), Key2: node_id (string), Value: edit_time (ms timestamp string)
type dingtalkCursor struct {
	LastSyncTime   time.Time                    `json:"last_sync_time"`
	SpaceNodeTimes map[string]map[string]string `json:"space_node_times,omitempty"`
}

// parseEditTime parses a DingTalk unix timestamp (milliseconds) into time.Time.
func parseEditTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
