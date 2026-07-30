// Package dingtalk implements the DingTalk (钉钉) document data source connector for WeKnora.
//
// It syncs documents from DingTalk document spaces into WeKnora knowledge bases,
// converting HTML content to Markdown format.
//
// DingTalk Open API docs:
//   - Authentication: GET https://oapi.dingtalk.com/gettoken (AppKey + AppSecret)
//   - Spaces:         GET https://oapi.dingtalk.com/v1.0/doc/spaces
//   - Space Docs:     GET https://oapi.dingtalk.com/v1.0/doc/spaces/{spaceId}/docs
//   - Doc Content:    GET https://oapi.dingtalk.com/v1.0/doc/spaces/{spaceId}/docs/{docId}
//
// Known limitations (v1):
//   - Only syncs DingTalk native documents (not attachments or spreadsheets)
//   - Document content returned as HTML, converted to Markdown
//   - Space list is flat with no hierarchical nesting
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

// DefaultBaseURL is the DingTalk Open API base URL.
const DefaultBaseURL = "https://oapi.dingtalk.com"

// Config holds DingTalk-specific configuration.
type Config struct {
	AppKey    string `json:"app_key"`
	AppSecret string `json:"app_secret"`
	BaseURL   string `json:"base_url,omitempty"`
}

// GetBaseURL returns the normalized base URL.
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

// tokenResponse wraps GET /gettoken response.
type tokenResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func (r *tokenResponse) IsSuccess() bool { return r.ErrCode == 0 }

type apiErrorBody struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// spaceListResponse wraps the list spaces response.
type spaceListResponse struct {
	HasMore   bool    `json:"hasMore"`
	NextToken string  `json:"nextToken"`
	Items     []space `json:"items"`
}

type space struct {
	SpaceID      string `json:"spaceId"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	CreatedTime  int64  `json:"createdTime,omitempty"`
	ModifiedTime int64  `json:"modifiedTime,omitempty"`
	OwnerID      string `json:"ownerId,omitempty"`
	OwnerName    string `json:"ownerName,omitempty"`
	URL          string `json:"url,omitempty"`
}

// docListResponse wraps list documents response.
type docListResponse struct {
	HasMore   bool         `json:"hasMore"`
	NextToken string       `json:"nextToken"`
	Items     []docSummary `json:"items"`
}

type docSummary struct {
	DocID               string `json:"docId"`
	SpaceID             string `json:"spaceId"`
	Name                string `json:"name"`
	DocType             string `json:"docType"`
	URL                 string `json:"url"`
	CreatorID           string `json:"creatorId,omitempty"`
	CreatorName         string `json:"creatorName,omitempty"`
	CreatedTime         int64  `json:"createdTime,omitempty"`
	ModifiedTime        int64  `json:"modifiedTime,omitempty"`
	ContentModifiedTime int64  `json:"contentModifiedTime,omitempty"`
}

type docDetailResponse struct {
	DocID       string `json:"docId"`
	SpaceID     string `json:"spaceId"`
	Name        string `json:"name"`
	DocType     string `json:"docType"`
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
	URL         string `json:"url"`
	CreatorID   string `json:"creatorId,omitempty"`
	CreatorName string `json:"creatorName,omitempty"`
	CreatedTime int64  `json:"createdTime,omitempty"`
	ModifiedTime int64  `json:"modifiedTime,omitempty"`
}

// dingtalkCursor stores incremental sync state.
type dingtalkCursor struct {
	LastSyncTime  time.Time                    `json:"last_sync_time"`
	SpaceDocTimes map[string]map[string]int64  `json:"space_doc_times,omitempty"`
}

func parseMsTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func redactToken(t string) string {
	if len(t) < 12 {
		return "***"
	}
	return t[:6] + "..." + t[len(t)-4:]
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
