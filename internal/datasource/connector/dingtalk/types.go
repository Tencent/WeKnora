// Package dingtalk implements the DingTalk (钉钉) data source connector for WeKnora.
//
// It syncs documents from DingTalk knowledge bases (知识库) into WeKnora knowledge bases.
//
// DingTalk API docs:
//   - Auth:            https://open.dingtalk.com/document/development/obtain-the-access_token-of-an-internal-application
//   - Wiki workspaces: GET  /v2.0/wiki/workspaces          (scope: Wiki.Workspace.Read)
//   - Wiki nodes:      GET  /v2.0/wiki/nodes               (scope: Wiki.Node.Read)
//   - Doc export:      POST /v2.0/doc/me/export/submit +
//     GET  /v2.0/doc/me/export/task/query (scope: Storage.File.Read)
//
// Wiki APIs require an operator (unionId) for permission checks. The operator
// is either configured explicitly (operator_id) or auto-resolved from the org
// admin list via the legacy contact API (scope: qyapi_get_member).
package dingtalk

import "time"

// Config holds DingTalk-specific configuration for the data source connector.
// Uses the self-built app (企业内部应用) authentication model.
type Config struct {
	// AppKey (Client ID) from DingTalk developer console
	AppKey string `json:"app_key"`

	// AppSecret (Client Secret) from DingTalk developer console
	AppSecret string `json:"app_secret"`

	// OperatorID is the unionId of the user on whose behalf wiki APIs are
	// called. Optional: when empty, the connector resolves the unionId of
	// the org's first admin via the legacy contact API.
	OperatorID string `json:"operator_id,omitempty"`

	// BaseURL for the new-style DingTalk API (default: https://api.dingtalk.com)
	BaseURL string `json:"base_url,omitempty"`

	// LegacyBaseURL for the legacy DingTalk API used to resolve the operator
	// unionId (default: https://oapi.dingtalk.com)
	LegacyBaseURL string `json:"legacy_base_url,omitempty"`
}

// DefaultBaseURL is the default DingTalk Open Platform API base URL.
const DefaultBaseURL = "https://api.dingtalk.com"

// DefaultLegacyBaseURL is the default legacy (topapi) API base URL.
const DefaultLegacyBaseURL = "https://oapi.dingtalk.com"

// GetBaseURL returns the effective new-style API base URL.
func (c *Config) GetBaseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

// GetLegacyBaseURL returns the effective legacy API base URL.
func (c *Config) GetLegacyBaseURL() string {
	if c.LegacyBaseURL != "" {
		return c.LegacyBaseURL
	}
	return DefaultLegacyBaseURL
}

// --- Node type constants (values returned by the wiki node API) ---

const (
	nodeTypeFolder = "FOLDER"
	nodeTypeFile   = "FILE"

	// categoryAlidoc marks native DingTalk online documents (在线文档),
	// the only category the export API can convert to markdown.
	categoryAlidoc = "ALIDOC"

	// extensionAdoc is the extension of DingTalk text documents. Other
	// ALIDOC extensions (e.g. spreadsheets) cannot be exported to markdown.
	extensionAdoc = "adoc"
)

// --- New-style API (api.dingtalk.com) response structures ---

// apiError is the error body returned by api.dingtalk.com on non-2xx status.
type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestid"`
}

// tokenResponse is the response of POST /v1.0/oauth2/accessToken.
type tokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int    `json:"expireIn"` // seconds
}

// workspace represents a DingTalk knowledge base (知识库).
type workspace struct {
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RootNodeID  string `json:"rootNodeId"`
	URL         string `json:"url"`
	Type        string `json:"type"` // e.g. "TEAM"
}

// workspaceListResponse is the response of GET /v2.0/wiki/workspaces.
type workspaceListResponse struct {
	Workspaces []workspace `json:"workspaces"`
	NextToken  string      `json:"nextToken"`
}

// wikiNode represents a node (folder or document) in a DingTalk knowledge base.
type wikiNode struct {
	NodeID            string `json:"nodeId"`
	WorkspaceID       string `json:"workspaceId"`
	Name              string `json:"name"`
	Type              string `json:"type"`      // "FOLDER" or "FILE"
	Category          string `json:"category"`  // "ALIDOC" (online doc), "OTHER", ...
	Extension         string `json:"extension"` // "adoc" for DingTalk text documents
	HasChildren       bool   `json:"hasChildren"`
	URL               string `json:"url"`
	Size              int64  `json:"size"`
	CreateTimestamp   int64  `json:"createTimestamp"`   // unix ms
	ModifiedTimestamp int64  `json:"modifiedTimestamp"` // unix ms
	CreatorID         string `json:"creatorId"`
	ModifierID        string `json:"modifierId"`
}

// nodeListResponse is the response of GET /v2.0/wiki/nodes.
type nodeListResponse struct {
	Nodes     []wikiNode `json:"nodes"`
	NextToken string     `json:"nextToken"`
}

// nodeDetailResponse is the response of GET /v2.0/wiki/nodes/{nodeId}.
type nodeDetailResponse struct {
	Node wikiNode `json:"node"`
}

// workspaceDetailResponse is the response of GET /v2.0/wiki/workspaces/{workspaceId}.
type workspaceDetailResponse struct {
	Workspace workspace `json:"workspace"`
}

// exportSubmitResponse is the response of POST /v2.0/doc/me/export/submit.
type exportSubmitResponse struct {
	TaskID      string `json:"taskId"`
	DownloadURL string `json:"downloadUrl"`
}

// exportQueryResponse is the response of GET /v2.0/doc/me/export/task/query.
type exportQueryResponse struct {
	Status      string `json:"status"`
	DownloadURL string `json:"downloadUrl"`
}

// --- Legacy API (oapi.dingtalk.com) response structures ---
// Used only to auto-resolve the operator unionId from the org admin list.

// legacyTokenResponse is the response of GET /gettoken.
type legacyTokenResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
}

// adminListResponse is the response of POST /topapi/user/listadmin.
type adminListResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	Result  []struct {
		UserID   string `json:"userid"`
		SysLevel int    `json:"sys_level"`
	} `json:"result"`
}

// userGetResponse is the response of POST /topapi/v2/user/get.
type userGetResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	Result  struct {
		UnionID string `json:"unionid"`
		Name    string `json:"name"`
	} `json:"result"`
}

// dingtalkCursor stores incremental sync state for DingTalk.
type dingtalkCursor struct {
	// LastSyncTime is the timestamp of the last successful sync.
	LastSyncTime time.Time `json:"last_sync_time"`

	// ResourceNodeTimes maps resource ID -> node ID -> last known modified
	// timestamp (unix ms, decimal string). Used to detect changed nodes.
	ResourceNodeTimes map[string]map[string]string `json:"resource_node_times,omitempty"`
}
