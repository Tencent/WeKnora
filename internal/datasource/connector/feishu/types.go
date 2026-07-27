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

import (
	"strings"
	"time"
)

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
	ParentNodeID   string `json:"parent_node_token"`
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

// --- Drive (云盘) file listing types ---

// driveFile represents a file/folder in Feishu Drive (云空间). Returned by
// GET /open-apis/drive/v1/files?folder_token=xxx. The list API returns
// modified_time directly (verified), so no batch_query/metas call is needed for
// incremental detection - see ADR-0002.
type driveFile struct {
	Token        string `json:"token"`
	Name         string `json:"name"`
	Type         string `json:"type"` // doc/docx/sheet/bitable/file/folder/shortcut/mindnote/slides/board
	ParentToken  string `json:"parent_token"`
	URL          string `json:"url"`
	CreatedTime  string `json:"created_time"`  // unix seconds string
	ModifiedTime string `json:"modified_time"` // unix seconds string - 等价知识库 obj_edit_time
	OwnerID      string `json:"owner_id"`
	// ShortcutInfo is populated only for type=="shortcut". target_type can only
	// be doc/sheet/mindnote/bitable/file/docx (Feishu does not allow shortcuts to
	// folders, verified) - see ADR-0002 / glossary shortcut entry.
	ShortcutInfo *driveShortcutInfo `json:"shortcut_info,omitempty"`
}

// driveShortcutInfo is the target metadata of a Drive shortcut.
type driveShortcutInfo struct {
	TargetToken string `json:"target_token"`
	TargetType  string `json:"target_type"`
}

// driveFileListResponse is the response for GET /open-apis/drive/v1/files.
type driveFileListResponse struct {
	apiResponse
	Data struct {
		Files         []driveFile `json:"files"`
		HasMore       bool        `json:"has_more"`
		NextPageToken string      `json:"next_page_token"`
	} `json:"data"`
}

// driveFolderMetaResponse is the response for GET /open-apis/drive/explorer/v2/folder/:folderToken/meta.
// Used to resolve a root folder's human-readable name (the list API only returns
// the folder's children, not the folder itself).
type driveFolderMetaResponse struct {
	apiResponse
	Data struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Token     string `json:"token"`
		CreateUid string `json:"createUid"`
		EditUid   string `json:"editUid"`
		ParentID  string `json:"parentId"`
		OwnUid    string `json:"ownUid"`
	} `json:"data"`
}

// driveFileListFailure records a single sub-folder listing that failed during a
// recursive walk. Mirrors wikiNodeListFailure.
type driveFileListFailure struct {
	FolderToken string
	Err         error
}

// partialDriveFileListError aggregates per-folder listing failures so the walk
// can continue and the caller can still surface the partial result. Mirrors
// partialWikiNodeListError.
type partialDriveFileListError struct {
	Failures []driveFileListFailure
}

func (e *partialDriveFileListError) Error() string {
	if e == nil || len(e.Failures) == 0 {
		return "partial drive file listing failed"
	}
	parts := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		parts = append(parts, failure.Err.Error())
	}
	return strings.Join(parts, "; ")
}

// feishuDriveCursor stores incremental sync state for Feishu Drive (云盘).
// Structurally symmetric with feishuCursor: outer key = resourceID
// ("folderToken" or "folderToken:fileToken"), inner key = file_token,
// value = modified_time. See ADR-0001.
type feishuDriveCursor struct {
	// LastSyncTime is the timestamp of the last successful sync.
	LastSyncTime time.Time `json:"last_sync_time"`

	// FileTimes maps resourceID -> file_token -> last known modified_time.
	// Used to detect which files have changed since last sync.
	FileTimes map[string]map[string]string `json:"file_times,omitempty"`
}

// feishuCursor stores incremental sync state for Feishu.
type feishuCursor struct {
	// LastSyncTime is the timestamp of the last successful sync.
	LastSyncTime time.Time `json:"last_sync_time"`

	// SpaceNodeTimes maps space_id -> node_token -> last known edit time.
	// Used to detect which nodes have changed since last sync.
	SpaceNodeTimes map[string]map[string]string `json:"space_node_times,omitempty"`
}
