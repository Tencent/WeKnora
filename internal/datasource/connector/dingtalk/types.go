// Package dingtalk implements DingTalk Docs as a WeKnora data source.
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
	DefaultBaseURL = "https://api.dingtalk.com"
	cursorVersion  = 1
)

// Config contains the credentials required by DingTalk Wiki and Doc APIs.
type Config struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	OperatorID   string `json:"operator_id"`
	BaseURL      string `json:"base_url,omitempty"`
}

func (c *Config) GetBaseURL() string {
	baseURL := strings.TrimSpace(c.BaseURL)
	if baseURL == "" {
		return DefaultBaseURL
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	return strings.TrimRight(baseURL, "/")
}

func parseDingTalkConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}

	encoded, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal dingtalk credentials: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(encoded, &cfg); err != nil {
		return nil, fmt.Errorf("parse dingtalk credentials: %w", err)
	}

	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.OperatorID = strings.TrimSpace(cfg.OperatorID)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)

	if cfg.ClientID == "" {
		return nil, fmt.Errorf("%w: client_id is required", datasource.ErrInvalidCredentials)
	}
	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("%w: client_secret is required", datasource.ErrInvalidCredentials)
	}
	if cfg.OperatorID == "" {
		return nil, fmt.Errorf("%w: operator_id (unionId) is required", datasource.ErrInvalidCredentials)
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// The official OAuth2 API uses accessToken. LegacyAccessToken is accepted only
// as a defensive fallback for proxies that rewrite the response to snake_case.
type accessTokenResponse struct {
	AccessToken       string `json:"accessToken"`
	LegacyAccessToken string `json:"access_token"`
	ExpireIn          int64  `json:"expireIn"`
}

func (r accessTokenResponse) token() string {
	if r.AccessToken != "" {
		return r.AccessToken
	}
	return r.LegacyAccessToken
}

type workspaceListResponse struct {
	NextToken  string      `json:"nextToken,omitempty"`
	Workspaces []workspace `json:"workspaces,omitempty"`
}

type workspaceResponse struct {
	Workspace workspace `json:"workspace"`
}

type workspace struct {
	WorkspaceID    string `json:"workspaceId"`
	CorpID         string `json:"corpId,omitempty"`
	TeamID         string `json:"teamId,omitempty"`
	RootNodeID     string `json:"rootNodeId"`
	Name           string `json:"name"`
	Type           string `json:"type,omitempty"`
	Description    string `json:"description,omitempty"`
	URL            string `json:"url,omitempty"`
	PermissionRole string `json:"permissionRole,omitempty"`
	CreateTime     string `json:"createTime,omitempty"`
	ModifiedTime   string `json:"modifiedTime,omitempty"`
}

type nodeListResponse struct {
	NextToken string     `json:"nextToken,omitempty"`
	Nodes     []wikiNode `json:"nodes,omitempty"`
}

type nodeResponse struct {
	Node wikiNode `json:"node"`
}

type nodeStatisticalInfo struct {
	WordCount int64 `json:"wordCount,omitempty"`
}

type wikiNode struct {
	NodeID          string              `json:"nodeId"`
	WorkspaceID     string              `json:"workspaceId,omitempty"`
	Name            string              `json:"name,omitempty"`
	NodeType        string              `json:"type,omitempty"`
	Category        string              `json:"category,omitempty"`
	Extension       string              `json:"extension,omitempty"`
	URL             string              `json:"url,omitempty"`
	PermissionRole  string              `json:"permissionRole,omitempty"`
	CreateTime      string              `json:"createTime,omitempty"`
	ModifiedTime    string              `json:"modifiedTime,omitempty"`
	HasChildren     bool                `json:"hasChildren,omitempty"`
	Size            int64               `json:"size,omitempty"`
	StatisticalInfo nodeStatisticalInfo `json:"statisticalInfo,omitempty"`

	// ParentNodeID is supplied by the traversal, because DingTalk's list/get
	// node responses do not include the parent id in the official SDK model.
	ParentNodeID string `json:"-"`
}

type docBlocksResponse struct {
	Success *bool `json:"success,omitempty"`
	Result  struct {
		Data []json.RawMessage `json:"data,omitempty"`
	} `json:"result,omitempty"`
}

type apiErrorBody struct {
	Code      json.RawMessage `json:"code,omitempty"`
	ErrCode   json.RawMessage `json:"errcode,omitempty"`
	Message   string          `json:"message,omitempty"`
	ErrMsg    string          `json:"errmsg,omitempty"`
	RequestID string          `json:"requestId,omitempty"`
}

func (e apiErrorBody) code() string {
	if code := rawScalarString(e.Code); code != "" {
		return code
	}
	return rawScalarString(e.ErrCode)
}

func (e apiErrorBody) message() string {
	if strings.TrimSpace(e.Message) != "" {
		return strings.TrimSpace(e.Message)
	}
	return strings.TrimSpace(e.ErrMsg)
}

func rawScalarString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

type dingTalkCursor struct {
	Version   int                              `json:"version"`
	SyncedAt  time.Time                        `json:"synced_at"`
	Resources map[string]dingTalkResourceState `json:"resources,omitempty"`
}

type dingTalkResourceState struct {
	Nodes map[string]dingTalkNodeState `json:"nodes,omitempty"`
}

type dingTalkNodeState struct {
	ModifiedTime string `json:"modified_time,omitempty"`
}

func parseDingTalkTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		if n > 100_000_000_000 {
			return time.UnixMilli(n)
		}
		if n > 1_000_000_000 {
			return time.Unix(n, 0)
		}
	}
	return time.Time{}
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}
	name = strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_",
	).Replace(name)
	name = strings.Trim(name, " ._")
	if name == "" {
		return "untitled"
	}
	const maxBytes = 200
	if len(name) > maxBytes {
		name = name[:maxBytes]
		for len(name) > 0 {
			r, size := utf8.DecodeLastRuneInString(name)
			if r != utf8.RuneError || size != 1 {
				break
			}
			name = name[:len(name)-1]
		}
		name = strings.Trim(name, " ._")
	}
	if name == "" {
		return "untitled"
	}
	return name
}

func redact(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 9 {
		return "***"
	}
	return value[:4] + "..." + value[len(value)-4:]
}
