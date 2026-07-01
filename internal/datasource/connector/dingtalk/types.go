package dingtalk

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// Config holds DingTalk-specific configuration credentials.
type Config struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	UserID       string `json:"user_id"`
	BaseURL      string `json:"base_url,omitempty"`
	// MCPServerURL is the DingTalk MCP gateway URL (includes key auth param),
	// e.g. https://mcp-gw.dingtalk.com/server/{hash}?key={apikey}
	MCPServerURL string `json:"mcp_server_url,omitempty"`
}

// parseDingTalkConfig parses and validates DingTalk credentials.
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

	// Backward compatibility fallbacks
	if cfg.ClientID == "" && config.Credentials != nil {
		if val, ok := config.Credentials["app_key"].(string); ok {
			cfg.ClientID = val
		}
	}
	if cfg.ClientSecret == "" && config.Credentials != nil {
		if val, ok := config.Credentials["app_secret"].(string); ok {
			cfg.ClientSecret = val
		}
	}
	if cfg.UserID == "" && config.Credentials != nil {
		if val, ok := config.Credentials["operator_id"].(string); ok {
			cfg.UserID = val
		}
	}

	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, fmt.Errorf("%w: client_id is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, fmt.Errorf("%w: client_secret is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(cfg.UserID) == "" {
		return nil, fmt.Errorf("%w: user_id is required", datasource.ErrInvalidCredentials)
	}
	return &cfg, nil
}

// DingTalkTime is a custom time type that handles both RFC3339 and layout without seconds.
type DingTalkTime time.Time

func (t *DingTalkTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), "\"")
	if s == "" || s == "null" {
		return nil
	}

	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04Z", // DingTalk's format without seconds
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999Z07:00",
	}

	var parsed time.Time
	var err error
	for _, layout := range layouts {
		parsed, err = time.Parse(layout, s)
		if err == nil {
			*t = DingTalkTime(parsed)
			return nil
		}
	}
	return fmt.Errorf("cannot parse %q: %w", s, err)
}

func (t DingTalkTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t))
}

func (t DingTalkTime) Time() time.Time {
	return time.Time(t)
}

func (t DingTalkTime) Format(layout string) string {
	return time.Time(t).Format(layout)
}

// Workspace represents a DingTalk Doc workspace (knowledge base).
type Workspace struct {
	WorkspaceID string       `json:"workspaceId"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	URL         string       `json:"url"`
	RootNodeID  string       `json:"rootNodeId"`
	CreateTime  DingTalkTime `json:"createTime"`
	UpdateTime  DingTalkTime `json:"modifiedTime"`
}

// Node represents a document node in DingTalk Wiki/Workspace.
type Node struct {
	NodeID     string       `json:"nodeId"`
	ParentID   string       `json:"parentId"`
	Name       string       `json:"name"`
	Type       string       `json:"type"`      // e.g. "file" / "folder"
	Extension  string       `json:"extension"` // file extension, e.g. "pdf", "docx"
	URL        string       `json:"url"`
	DentryUUID string       `json:"dentryUuid"`
	CreateTime DingTalkTime `json:"createTime"`
	UpdateTime DingTalkTime `json:"modifiedTime"` // DingTalk returns "modifiedTime", not "updateTime"
}
