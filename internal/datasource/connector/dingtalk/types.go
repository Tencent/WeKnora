// Package dingtalk implements the DingTalk document data source connector.
package dingtalk

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const DefaultBaseURL = "https://api.dingtalk.com"

type Config struct {
	AppKey    string `json:"app_key"`
	AppSecret string `json:"app_secret"`
	BaseURL   string `json:"base_url,omitempty"`
}

func (c *Config) GetBaseURL() string {
	raw := strings.TrimSpace(c.BaseURL)
	if raw == "" {
		return DefaultBaseURL
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	return strings.TrimRight(raw, "/")
}

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

	if cfg.AppKey == "" {
		cfg.AppKey = stringCredential(config.Credentials, "app_id", "client_id")
	}
	if cfg.AppSecret == "" {
		cfg.AppSecret = stringCredential(config.Credentials, "client_secret")
	}
	if cfg.BaseURL == "" && config.Settings != nil {
		cfg.BaseURL = stringSetting(config.Settings, "base_url")
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

func stringCredential(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func stringSetting(m map[string]interface{}, key string) string {
	if s, ok := m[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

type tokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"`
}

type spaceListResponse struct {
	Spaces []docSpace `json:"spaces"`
	Result struct {
		Spaces []docSpace `json:"spaces"`
	} `json:"result"`
	Data []docSpace `json:"data"`
}

func (r spaceListResponse) items() []docSpace {
	if len(r.Spaces) > 0 {
		return r.Spaces
	}
	if len(r.Result.Spaces) > 0 {
		return r.Result.Spaces
	}
	return r.Data
}

type docSpace struct {
	SpaceID     string `json:"spaceId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ModifiedAt  string `json:"modifiedAt"`
	URL         string `json:"url"`
}

type nodeListResponse struct {
	Nodes  []docNode `json:"nodes"`
	Result struct {
		Nodes []docNode `json:"nodes"`
	} `json:"result"`
	Data []docNode `json:"data"`
}

func (r nodeListResponse) items() []docNode {
	if len(r.Nodes) > 0 {
		return r.Nodes
	}
	if len(r.Result.Nodes) > 0 {
		return r.Result.Nodes
	}
	return r.Data
}

type docNode struct {
	NodeID      string `json:"nodeId"`
	DocumentID  string `json:"documentId"`
	Title       string `json:"title"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	UpdatedAt   string `json:"updatedAt"`
	ModifiedAt  string `json:"modifiedAt"`
	HasChildren bool   `json:"hasChildren"`
}

func (n docNode) id() string {
	if n.NodeID != "" {
		return n.NodeID
	}
	return n.DocumentID
}

func (n docNode) docID() string {
	if n.DocumentID != "" {
		return n.DocumentID
	}
	return n.NodeID
}

func (n docNode) displayName() string {
	if n.Title != "" {
		return n.Title
	}
	return n.Name
}

func (n docNode) updated() string {
	if n.UpdatedAt != "" {
		return n.UpdatedAt
	}
	return n.ModifiedAt
}

type contentResponse struct {
	DocumentID string `json:"documentId"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Markdown   string `json:"markdown"`
	Format     string `json:"format"`
	UpdatedAt  string `json:"updatedAt"`
	URL        string `json:"url"`
	Result     struct {
		DocumentID string `json:"documentId"`
		Title      string `json:"title"`
		Content    string `json:"content"`
		Markdown   string `json:"markdown"`
		Format     string `json:"format"`
		UpdatedAt  string `json:"updatedAt"`
		URL        string `json:"url"`
	} `json:"result"`
}

func (r contentResponse) normalized() contentResponse {
	if r.Content == "" && r.Markdown == "" && (r.Result.Content != "" || r.Result.Markdown != "") {
		r.DocumentID = firstNonEmpty(r.DocumentID, r.Result.DocumentID)
		r.Title = firstNonEmpty(r.Title, r.Result.Title)
		r.Content = r.Result.Content
		r.Markdown = r.Result.Markdown
		r.Format = firstNonEmpty(r.Format, r.Result.Format)
		r.UpdatedAt = firstNonEmpty(r.UpdatedAt, r.Result.UpdatedAt)
		r.URL = firstNonEmpty(r.URL, r.Result.URL)
	}
	return r
}

func (r contentResponse) body() string {
	if r.Content != "" {
		return r.Content
	}
	return r.Markdown
}

type dingtalkCursor struct {
	LastSyncTime   time.Time                    `json:"last_sync_time"`
	SpaceNodeTimes map[string]map[string]string `json:"space_node_times,omitempty"`
}

func parseDingTalkTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.TrimSpace(filepath.Base(name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "untitled"
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
