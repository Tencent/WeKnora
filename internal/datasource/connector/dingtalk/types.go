// Package dingtalk implements the DingTalk knowledge-base data source connector.
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

const (
	defaultBaseURL    = "https://api.dingtalk.com"
	resourceSeparator = ":"
)

// Config holds DingTalk-specific credentials.
type Config struct {
	AppKey          string `json:"app_key"`
	AppSecret       string `json:"app_secret"`
	OperatorUnionID string `json:"operator_union_id"`
	BaseURL         string `json:"base_url,omitempty"`
}

func (c *Config) GetBaseURL() string {
	baseURL := strings.TrimSpace(c.BaseURL)
	if baseURL == "" {
		return defaultBaseURL
	}
	return strings.TrimRight(baseURL, "/")
}

func parseConfig(config *types.DataSourceConfig) (*Config, error) {
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

	// Accept the names users see in DingTalk's console and the field names used
	// by earlier drafts, while persisting the canonical app_key/app_secret pair.
	if cfg.AppKey == "" {
		cfg.AppKey = stringCredential(config.Credentials, "app_id", "client_id")
	}
	if cfg.AppSecret == "" {
		cfg.AppSecret = stringCredential(config.Credentials, "client_secret")
	}
	if cfg.OperatorUnionID == "" {
		cfg.OperatorUnionID = stringCredential(config.Credentials, "operator_id", "union_id")
	}

	if strings.TrimSpace(cfg.AppKey) == "" || strings.TrimSpace(cfg.AppSecret) == "" {
		return nil, fmt.Errorf("%w: app_key and app_secret are required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(cfg.OperatorUnionID) == "" {
		return nil, fmt.Errorf("%w: operator_union_id is required", datasource.ErrInvalidCredentials)
	}
	cfg.AppKey = strings.TrimSpace(cfg.AppKey)
	cfg.AppSecret = strings.TrimSpace(cfg.AppSecret)
	cfg.OperatorUnionID = strings.TrimSpace(cfg.OperatorUnionID)
	return &cfg, nil
}

func stringCredential(credentials map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		raw, ok := credentials[key]
		if !ok {
			continue
		}
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

type accessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Request string `json:"requestid"`
}

type workspaceListResponse struct {
	Workspaces []workspace `json:"workspaces"`
	NextToken  string      `json:"nextToken"`
}

type workspace struct {
	WorkspaceID    string `json:"workspaceId"`
	CorpID         string `json:"corpId"`
	TeamID         string `json:"teamId"`
	RootNodeID     string `json:"rootNodeId"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Description    string `json:"description"`
	URL            string `json:"url"`
	CreatorID      string `json:"creatorId"`
	ModifierID     string `json:"modifierId"`
	CreateTime     string `json:"createTime"`
	ModifiedTime   string `json:"modifiedTime"`
	PermissionRole string `json:"permissionRole"`
}

type nodeListResponse struct {
	Nodes     []node `json:"nodes"`
	NextToken string `json:"nextToken"`
}

type nodeInfoResponse struct {
	Node node `json:"node"`
}

type node struct {
	NodeID         string          `json:"nodeId"`
	WorkspaceID    string          `json:"workspaceId"`
	Name           string          `json:"name"`
	Size           int64           `json:"size"`
	Type           string          `json:"type"`     // FILE / FOLDER
	Category       string          `json:"category"` // ALIDOC / DOCUMENT / ...
	Extension      string          `json:"extension"`
	URL            string          `json:"url"`
	CreatorID      string          `json:"creatorId"`
	ModifierID     string          `json:"modifierId"`
	CreateTime     string          `json:"createTime"`
	ModifiedTime   string          `json:"modifiedTime"`
	HasChildren    bool            `json:"hasChildren"`
	PermissionRole string          `json:"permissionRole"`
	Stats          statisticalInfo `json:"statisticalInfo"`
}

type statisticalInfo struct {
	WordCount int64 `json:"wordCount"`
}

type docBlocksResponse struct {
	Success bool `json:"success"`
	Result  struct {
		Data []docBlock `json:"data"`
	} `json:"result"`
}

type docBlock struct {
	BlockType     string          `json:"blockType"`
	ID            string          `json:"id"`
	Index         int             `json:"index"`
	Paragraph     *textBlock      `json:"paragraph,omitempty"`
	Heading       *headingBlock   `json:"heading,omitempty"`
	Blockquote    *textBlock      `json:"blockquote,omitempty"`
	OrderedList   *textBlock      `json:"orderedList,omitempty"`
	UnorderedList *textBlock      `json:"unorderedList,omitempty"`
	Callout       *textBlock      `json:"callout,omitempty"`
	Children      json.RawMessage `json:"children,omitempty"`
}

type textBlock struct {
	Text string `json:"text"`
}

type headingBlock struct {
	Text  string `json:"text"`
	Level int    `json:"level"`
}

type dingtalkCursor struct {
	LastSyncTime     time.Time                    `json:"last_sync_time"`
	ResourceNodeTime map[string]map[string]string `json:"resource_node_time,omitempty"`
}

func makeResourceID(workspaceID, nodeID string) string {
	return workspaceID + resourceSeparator + nodeID
}

func parseResourceID(resourceID string) (workspaceID string, nodeID string) {
	workspaceID, nodeID, _ = strings.Cut(resourceID, resourceSeparator)
	return workspaceID, nodeID
}

func parseDingTalkTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04Z",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	if ms, err := strconv.ParseInt(value, 10, 64); err == nil {
		if ms > 1_000_000_000_000 {
			return time.UnixMilli(ms)
		}
		return time.Unix(ms, 0)
	}
	return time.Time{}
}

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
