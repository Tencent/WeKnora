package dingtalk

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const defaultBaseURL = "https://api.dingtalk.com"

type Config struct {
	AppKey     string `json:"app_key"`
	AppSecret  string `json:"app_secret"`
	OperatorID string `json:"operator_id"`
	BaseURL    string `json:"base_url,omitempty"`
}

func (c *Config) baseURL() string {
	u := strings.TrimSpace(c.BaseURL)
	if u == "" {
		return defaultBaseURL
	}
	return strings.TrimRight(u, "/")
}

func parseConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	b, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse dingtalk credentials: %w", err)
	}
	if strings.TrimSpace(cfg.AppKey) == "" || strings.TrimSpace(cfg.AppSecret) == "" || strings.TrimSpace(cfg.OperatorID) == "" {
		return nil, fmt.Errorf("%w: app_key, app_secret and operator_id are required", datasource.ErrInvalidCredentials)
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.baseURL()); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type workspace struct{ WorkspaceID, WorkspaceName, RootDentryUUID, URL, CreateTime string }
type node struct {
	NodeID, Name, Type, Category, Extension, URL, ModifiedTime, WorkspaceID string
	HasChildren                                                             bool
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000Z07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
