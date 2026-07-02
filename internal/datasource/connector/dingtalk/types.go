// Package dingtalk implements the DingTalk data source connector for WeKnora.
//
// It syncs DingTalk wiki spaces and online documents into WeKnora knowledge
// bases, rendering supported document blocks as Markdown.
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
	// DefaultBaseURL is the DingTalk OpenAPI base URL.
	DefaultBaseURL = "https://api.dingtalk.com"

	// DefaultOAPIBaseURL is the legacy OAPI base URL used for userid lookup.
	DefaultOAPIBaseURL = "https://oapi.dingtalk.com"

	resourceSeparator = ":"
)

// Config holds DingTalk-specific configuration.
type Config struct {
	// AppKey is the app key from the DingTalk developer console.
	AppKey string `json:"app_key"`

	// AppSecret is the app secret from the DingTalk developer console.
	AppSecret string `json:"app_secret"`

	// OperatorUserID is a DingTalk userid. It is used to look up the unionid
	// required by wiki APIs when OperatorUnionID is not provided directly.
	OperatorUserID string `json:"operator_user_id"`

	// OperatorUnionID is the operator unionid accepted by DingTalk wiki APIs.
	OperatorUnionID string `json:"operator_union_id,omitempty"`

	// BaseURL is the DingTalk OpenAPI base URL. Empty uses DefaultBaseURL.
	BaseURL string `json:"base_url,omitempty"`
}

// GetBaseURL returns the normalized OpenAPI base URL.
func (c *Config) GetBaseURL() string {
	baseURL := strings.TrimSpace(c.BaseURL)
	if baseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(baseURL, "/")
}

// GetOAPIBaseURL returns the OAPI base URL used for userid-to-unionid lookup.
func (c *Config) GetOAPIBaseURL() string {
	baseURL := c.GetBaseURL()
	if baseURL == DefaultBaseURL {
		return DefaultOAPIBaseURL
	}
	return baseURL
}

// parseConfig extracts and validates DingTalk-specific configuration.
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
	if cfg.OperatorUserID == "" {
		cfg.OperatorUserID = stringCredential(
			config.Credentials,
			"operator_user_id",
			"operatorUserId",
			"userid",
			"user_id",
			"userId",
		)
	}

	if strings.TrimSpace(cfg.AppKey) == "" || strings.TrimSpace(cfg.AppSecret) == "" {
		return nil, fmt.Errorf("%w: app_key and app_secret are required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(cfg.OperatorUserID) == "" && strings.TrimSpace(cfg.OperatorUnionID) == "" {
		return nil, fmt.Errorf("%w: operator_user_id is required", datasource.ErrInvalidCredentials)
	}
	cfg.AppKey = strings.TrimSpace(cfg.AppKey)
	cfg.AppSecret = strings.TrimSpace(cfg.AppSecret)
	cfg.OperatorUserID = strings.TrimSpace(cfg.OperatorUserID)
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

// --- DingTalk API response types ---

type accessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Request string `json:"requestid"`
}

type userGetResponse struct {
	RequestID string      `json:"request_id"`
	ErrCode   interface{} `json:"errcode"`
	ErrMsg    string      `json:"errmsg"`
	Result    struct {
		UserID  string `json:"userid"`
		UnionID string `json:"unionid"`
	} `json:"result"`
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
	Text  string       `json:"text"`
	Level headingLevel `json:"level"`
}

// headingLevel accepts both the numeric and string forms DingTalk may return.
type headingLevel int

func (l *headingLevel) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*l = 0
		return nil
	}

	var number int
	if err := json.Unmarshal(data, &number); err == nil {
		*l = headingLevel(number)
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		if number, ok := parseHeadingLevel(text); ok {
			*l = headingLevel(number)
		} else {
			*l = 0
		}
		return nil
	}

	*l = 0
	return nil
}

func parseHeadingLevel(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if number, err := strconv.Atoi(value); err == nil {
		return number, true
	}

	start := -1
	for i, r := range value {
		if r >= '0' && r <= '9' {
			if start == -1 {
				start = i
			}
			continue
		}
		if start >= 0 {
			number, err := strconv.Atoi(value[start:i])
			return number, err == nil
		}
	}
	if start >= 0 {
		number, err := strconv.Atoi(value[start:])
		return number, err == nil
	}
	return 0, false
}

// dingtalkCursor stores incremental sync state.
// Key1: resource id (workspace_id:node_id), Key2: node_id, Value: modifiedTime.
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
