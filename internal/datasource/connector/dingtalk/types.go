// Package dingtalk implements the DingTalk (钉钉) data source connector for WeKnora.
//
// It syncs documents from DingTalk workspaces into WeKnora knowledge bases.
//
// DingTalk API docs:
//   - Auth:           POST /v1.0/oauth2/accessToken
//   - Workspaces:     GET /v1.0/doc/workspaces (list)
//   - Documents:      GET /v1.0/doc/workspaces/{workspaceId}/documents (list)
//   - Document:       GET /v1.0/doc/documents/{documentId} (detail with content)
//
// Authentication:
//   - AppKey + AppSecret (自建应用) → access_token (expires in 7200s)
package dingtalk

import "time"

// DefaultBaseURL is the default DingTalk Open API base URL.
const DefaultBaseURL = "https://api.dingtalk.com"

// Config holds DingTalk-specific configuration for the data source connector.
type Config struct {
	AppKey    string `json:"app_key"`
	AppSecret string `json:"app_secret"`

	// Base URL for DingTalk API (default: https://api.dingtalk.com)
	BaseURL string `json:"base_url,omitempty"`
}

// GetBaseURL returns the effective base URL, defaulting to DefaultBaseURL if not set.
func (c *Config) GetBaseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

// --- DingTalk API response structures ---

// tokenResponse is the response for POST /v1.0/oauth2/accessToken.
type tokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int    `json:"expireIn"` // seconds
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

// workspaceListResponse is the response for GET /v1.0/doc/workspaces.
type workspaceListResponse struct {
	Workspaces []workspace `json:"workspaces"`
	NextCursor int64       `json:"nextCursor"`
	HasMore    bool        `json:"hasMore"`
}

// workspace represents a DingTalk document workspace.
type workspace struct {
	WorkspaceID   string `json:"workspaceId"`
	WorkspaceName string `json:"workspaceName"`
	WorkspaceType string `json:"workspaceType"` // "personal" or "organization"
	Description   string `json:"description"`
	Creator       string `json:"creator"`
	CreateTime    int64  `json:"createTime"`
	UpdateTime    int64  `json:"updateTime"`
}

// docListResponse is the response for GET /v1.0/doc/workspaces/{workspaceId}/documents.
type docListResponse struct {
	Documents  []document `json:"documents"`
	NextCursor int64      `json:"nextCursor"`
	HasMore    bool       `json:"hasMore"`
}

// document is a summary of a DingTalk document (list endpoint, no body).
type document struct {
	DocumentID   string `json:"documentId"`
	DocumentName string `json:"documentName"`
	DocumentType string `json:"documentType"` // "doc", "sheet", "slide", etc.
	WorkspaceID  string `json:"workspaceId"`
	Creator      string `json:"creator"`
	CreatorName  string `json:"creatorName"`
	CreateTime   int64  `json:"createTime"`
	UpdateTime   int64  `json:"updateTime"` // unix millis — use for change detection
	URL          string `json:"url"`
}

// docDetailResponse is the response for GET /v1.0/doc/documents/{documentId}.
type docDetailResponse struct {
	DocumentID   string `json:"documentId"`
	DocumentName string `json:"documentName"`
	DocumentType string `json:"documentType"`
	Content      string `json:"content"`     // Markdown content
	ContentType  string `json:"contentType"` // "markdown" or "html"
	CreateTime   int64  `json:"createTime"`
	UpdateTime   int64  `json:"updateTime"`
	WorkspaceID  string `json:"workspaceId"`
	Creator      string `json:"creator"`
	CreatorName  string `json:"creatorName"`
	URL          string `json:"url"`
}

// apiErrorBody is the generic error response from DingTalk API.
type apiErrorBody struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// dingtalkCursor stores incremental sync state.
// Key1: workspace_id, Key2: document_id, Value: update_time (unix millis as string)
type dingtalkCursor struct {
	LastSyncTime      time.Time                    `json:"last_sync_time"`
	WorkspaceDocTimes map[string]map[string]string `json:"workspace_doc_times,omitempty"`
}

// parseDingtalkTimestamp parses a DingTalk unix millisecond timestamp into time.Time.
func parseDingtalkTimestamp(ts int64) time.Time {
	if ts <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ts)
}
