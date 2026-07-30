// Package dingtalk implements the DingTalk Docs data source connector.
//
// It uses a DingTalk internal application's AppKey/AppSecret to obtain an
// application access token, lists the knowledge spaces visible to an operator,
// walks their directory trees, and reads DingTalk document blocks.
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

const defaultBaseURL = "https://api.dingtalk.com"

// Config contains the credentials required by DingTalk's organization APIs.
// baseURL is deliberately not user-configurable: keeping the API host fixed
// prevents application access tokens from being sent to an arbitrary host.
type Config struct {
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret"`
	OperatorUnionID string `json:"operator_union_id"`
	baseURL         string
}

func (c *Config) getBaseURL() string {
	if c.baseURL != "" {
		return strings.TrimRight(c.baseURL, "/")
	}
	return defaultBaseURL
}

func parseConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	b, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal dingtalk credentials: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse dingtalk credentials: %w", err)
	}
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.OperatorUnionID = strings.TrimSpace(cfg.OperatorUnionID)
	switch {
	case cfg.ClientID == "":
		return nil, fmt.Errorf("%w: client_id is required", datasource.ErrInvalidCredentials)
	case cfg.ClientSecret == "":
		return nil, fmt.Errorf("%w: client_secret is required", datasource.ErrInvalidCredentials)
	case cfg.OperatorUnionID == "":
		return nil, fmt.Errorf("%w: operator_union_id is required", datasource.ErrInvalidCredentials)
	}
	return &cfg, nil
}

type accessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"`
}

type apiErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestid"`
}

type relatedSpacesResponse struct {
	HasMore   bool            `json:"hasMore"`
	NextToken string          `json:"nextToken"`
	Items     []dingtalkSpace `json:"items"`
}

type dingtalkSpace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Type        int32  `json:"type"`
}

type directoriesResponse struct {
	Children  []dentry `json:"children"`
	HasMore   bool     `json:"hasMore"`
	NextToken string   `json:"nextToken"`
}

type dentry struct {
	ContentType string `json:"contentType"`
	CreatedTime int64  `json:"createdTime"`
	DentryID    string `json:"dentryId"`
	DentryType  string `json:"dentryType"`
	DentryUUID  string `json:"dentryUuid"`
	DocKey      string `json:"docKey"`
	Extension   string `json:"extension"`
	HasChildren bool   `json:"hasChildren"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	SpaceID     string `json:"spaceId"`
	UpdatedTime int64  `json:"updatedTime"`
	URL         string `json:"url"`
}

func (d dentry) externalID() string {
	if d.DentryUUID != "" {
		return d.DentryUUID
	}
	return d.DentryID
}

func (d dentry) contentID() string {
	// DingTalk's block API accepts docKey. Some directory responses omit it,
	// while dentryUuid is also accepted for DingTalk Docs.
	if d.DocKey != "" {
		return d.DocKey
	}
	return d.DentryUUID
}

func (d dentry) isFolder() bool {
	t := strings.ToLower(strings.TrimSpace(d.DentryType))
	return t == "folder" || t == "directory"
}

func (d dentry) isDocument() bool {
	contentType := strings.ToLower(strings.TrimSpace(d.ContentType))
	extension := strings.ToLower(strings.TrimSpace(d.Extension))
	if extension != "" {
		return extension == "alidoc" || extension == "adoc"
	}
	return contentType == "alidoc"
}

func (d dentry) updatedAt() time.Time {
	if d.UpdatedTime <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(d.UpdatedTime)
}

type blocksResponse struct {
	Success bool `json:"success"`
	Result  struct {
		Data []json.RawMessage `json:"data"`
	} `json:"result"`
}

// cursor stores the last known update time for each document in each selected
// space. Millisecond timestamps come directly from DingTalk, avoiding lossy
// time formatting during change detection.
type cursor struct {
	Version       int                         `json:"version"`
	LastSyncTime  time.Time                   `json:"last_sync_time"`
	SpaceDocTimes map[string]map[string]int64 `json:"space_doc_times,omitempty"`
}

func sanitizeFileName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "untitled"
	}
	result := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	).Replace(name)
	const maxBytes = 200
	if len(result) <= maxBytes {
		return result
	}
	result = result[:maxBytes]
	for len(result) > 0 {
		r, size := utf8.DecodeLastRuneInString(result)
		if r != utf8.RuneError || size != 1 {
			break
		}
		result = result[:len(result)-1]
	}
	return result
}
