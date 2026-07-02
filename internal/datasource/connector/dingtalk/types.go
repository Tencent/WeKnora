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

	// OperatorID is the unionId of the operator (used for API calls).
	// This is optional and can be extracted from access_token response.
	OperatorID string `json:"operator_id,omitempty"`
}

// GetBaseURL returns the normalized base URL (always uses default for DingTalk).
func (c *Config) GetBaseURL() string {
	return DefaultBaseURL
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
	ExpireIn   int    `json:"expireIn"`
}

// wikiWorkspacesResponse wraps GET /v2.0/wiki/workspaces.
type wikiWorkspacesResponse struct {
	Workspaces []WikiWorkspace `json:"workspaces,omitempty"`
}

// WikiWorkspace represents a DingTalk knowledge base (workspace).
type WikiWorkspace struct {
	WorkspaceID   string `json:"workspaceId"`  // spaceUuid
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

// WikiNode represents a node (file or folder) in DingTalk wiki.
type WikiNode struct {
	NodeID       string `json:"nodeId"`        // dentryUuid
	WorkspaceID  string `json:"workspaceId"`   // spaceUuid
	Name         string `json:"name"`
	Size         int64  `json:"size,omitempty"`
	NodeType     string `json:"type"`   // "FILE" or "FOLDER"
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
}

// dingtalkAPIError represents a DingTalk API error.
type dingtalkAPIError struct {
	Code int
	Msg  string
}

func (e *dingtalkAPIError) Error() string {
	return fmt.Sprintf("dingtalk api error: code=%d msg=%s", e.Code, e.Msg)
}

// dingtalkCursor stores incremental sync state.
type dingtalkCursor struct {
	LastSyncTime    time.Time                               `json:"last_sync_time"`
	WorkspaceTimes  map[string]map[string]time.Time        `json:"workspace_node_times,omitempty"` // workspaceId -> nodeId -> modifiedTime
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
