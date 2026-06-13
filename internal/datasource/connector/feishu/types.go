// Package feishu implements the Feishu (飞书/Lark) data source connector for WeKnora.
//
// It syncs documents from Feishu Wiki spaces and cloud documents into WeKnora knowledge bases.
//
// Feishu API docs:
//   - Wiki spaces:      https://open.feishu.cn/document/server-docs/docs/wiki-v2/space/list
//   - Wiki nodes:       https://open.feishu.cn/document/server-docs/docs/wiki-v2/space-node/list
//   - Export tasks:     https://open.feishu.cn/document/server-docs/docs/drive-v1/export_task/export-user-guide
//   - File download:    https://open.feishu.cn/document/server-docs/docs/drive-v1/file/download
//   - Auth:             https://open.feishu.cn/document/server-docs/authentication-management/access-token/tenant_access_token_internal
package feishu

import "time"

// Config holds Feishu-specific configuration for the data source connector.
// Uses the self-built app (企业自建应用) authentication model.
type Config struct {
	// App ID from Feishu developer console
	AppID string `json:"app_id"`

	// App Secret from Feishu developer console
	AppSecret string `json:"app_secret"`

	// Base URL for Feishu API (default: https://open.feishu.cn)
	// Use https://open.larksuite.com for Lark (international) deployments
	BaseURL string `json:"base_url,omitempty"`
}

// DefaultBaseURL is the default Feishu Open Platform API base URL.
const DefaultBaseURL = "https://open.feishu.cn"

// LarkBaseURL is the Lark (international) API base URL.
const LarkBaseURL = "https://open.larksuite.com"

// GetBaseURL returns the effective base URL, defaulting to Feishu if not set.
func (c *Config) GetBaseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

// --- Export format constants ---
// Used by the export task API: POST /drive/v1/export_tasks

const (
	// ExportTypeDocx exports Feishu documents to .docx format.
	ExportTypeDocx = "docx"
	// ExportTypeXlsx exports spreadsheets / bitable to .xlsx format.
	ExportTypeXlsx = "xlsx"
	// ExportTypePDF exports documents to .pdf format (fallback).
	ExportTypePDF = "pdf"
)

// objTypeToExportFileExtension maps Feishu obj_type to the best export file_extension.
var objTypeToExportFileExtension = map[string]string{
	"docx":    ExportTypeDocx,
	"doc":     ExportTypeDocx,
	"sheet":   ExportTypeXlsx,
	"bitable": ExportTypeXlsx,
}

// objTypeToExportType maps Feishu obj_type to the export API "type" parameter.
// See: https://open.feishu.cn/document/server-docs/docs/drive-v1/export_task/create
var objTypeToExportType = map[string]string{
	"docx":    "docx",
	"doc":     "doc",
	"sheet":   "sheet",
	"bitable": "bitable",
}

// exportFileExtToSuffix maps export file_extension to the file suffix for FileName.
var exportFileExtToSuffix = map[string]string{
	ExportTypeDocx: ".docx",
	ExportTypeXlsx: ".xlsx",
	ExportTypePDF:  ".pdf",
}

// --- Feishu API response structures ---

// apiResponse is the common Feishu API response wrapper.
type apiResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// tokenResponse is the response for tenant_access_token API.
type tokenResponse struct {
	apiResponse
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"` // seconds
}

// wikiSpaceListResponse is the response for GET /open-apis/wiki/v2/spaces.
type wikiSpaceListResponse struct {
	apiResponse
	Data struct {
		Items     []wikiSpace `json:"items"`
		HasMore   bool        `json:"has_more"`
		PageToken string      `json:"page_token"`
	} `json:"data"`
}

// wikiSpace represents a Feishu Wiki space.
type wikiSpace struct {
	SpaceID     string `json:"space_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"` // "public" or "private"
}

// wikiNodeListResponse is the response for GET /open-apis/wiki/v2/spaces/:space_id/nodes.
type wikiNodeListResponse struct {
	apiResponse
	Data struct {
		Items     []wikiNode `json:"items"`
		HasMore   bool       `json:"has_more"`
		PageToken string     `json:"page_token"`
	} `json:"data"`
}

// wikiNode represents a node (document or folder) in a Feishu Wiki space.
type wikiNode struct {
	SpaceID        string `json:"space_id"`
	NodeToken      string `json:"node_token"`
	ObjToken       string `json:"obj_token"` // document token
	ObjType        string `json:"obj_type"`  // "doc", "sheet", "mindnote", "bitable", "file", "docx", "slides"
	ParentNodeID   string `json:"parent_node_id"`
	NodeType       string `json:"node_type"` // "origin" or "shortcut"
	OriginNodeID   string `json:"origin_node_id"`
	OriginSpaceID  string `json:"origin_space_id"`
	HasChild       bool   `json:"has_child"`
	Title          string `json:"title"`
	Creator        string `json:"creator"`
	Owner          string `json:"owner"`
	ObjCreateTime  string `json:"obj_create_time"`  // document creation time (unix timestamp string)
	ObjEditTime    string `json:"obj_edit_time"`    // document last edit time (unix timestamp string) — tracks content changes
	NodeCreateTime string `json:"node_create_time"` // node creation time (unix timestamp string)
	NodeEditTime   string `json:"node_edit_time"`   // node edit time (unix timestamp string) — only tracks node attribute changes
}

// wikiNodeInfoResponse is the response for GET /open-apis/wiki/v2/spaces/get_node.
type wikiNodeInfoResponse struct {
	apiResponse
	Data struct {
		Node wikiNode `json:"node"`
	} `json:"data"`
}

// --- Export task API responses ---

// docRawContentResponse is the response for GET /open-apis/docx/v1/documents/:document_id/raw_content.
// Deprecated: prefer export API for full-fidelity document export.
type docRawContentResponse struct {
	apiResponse
	Data struct {
		Content string `json:"content"`
	} `json:"data"`
}

// exportTaskCreateResponse is the response for POST /drive/v1/export_tasks.
type exportTaskCreateResponse struct {
	apiResponse
	Data struct {
		Ticket string `json:"ticket"`
	} `json:"data"`
}

// exportTaskStatusResponse is the response for GET /drive/v1/export_tasks/{ticket}.
type exportTaskStatusResponse struct {
	apiResponse
	Data struct {
		Result struct {
			FileToken string `json:"file_token"`
			FileSize  int64  `json:"file_size"`
			// JobStatus: 0=success, 1=initializing, 2=processing
			JobStatus   int    `json:"job_status"`
			JobErrorMsg string `json:"job_error_msg"`
			FileName    string `json:"file_name"`
		} `json:"result"`
	} `json:"data"`
}

// --- File download response ---

// driveFileMetaResponse is the response for GET /drive/v1/metas for file type nodes.
type driveFileMetaResponse struct {
	apiResponse
	Data struct {
		Metas []struct {
			DocToken string `json:"doc_token"`
			DocType  string `json:"doc_type"`
			Title    string `json:"title"`
		} `json:"metas"`
	} `json:"data"`
}

// feishuCursor stores incremental sync state for Feishu.
type feishuCursor struct {
	// LastSyncTime is the timestamp of the last successful sync.
	LastSyncTime time.Time `json:"last_sync_time"`

	// SpaceNodeTimes maps space_id -> node_token -> last known edit time.
	// Used to detect which nodes have changed since last sync.
	SpaceNodeTimes map[string]map[string]string `json:"space_node_times,omitempty"`
}
