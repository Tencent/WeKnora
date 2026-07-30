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
package core

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

	// Timezone is the IANA name (e.g. "Asia/Shanghai", "America/New_York") used to
	// render bitable date cells. Feishu stores a date as a UTC instant, but the
	// calendar date a user sees is that instant in the table's timezone, so
	// rendering in UTC shifts the date. Empty defaults to GMT+8, which matches
	// Feishu (mainland) tenants; set it for Lark tenants in other zones.
	Timezone string `json:"timezone,omitempty"`
}

// defaultTimezoneOffsetSeconds is GMT+8 (the Feishu mainland default), used when
// no timezone is configured. A fixed zone avoids any dependency on system tzdata,
// which minimal container images may omit.
const defaultTimezoneOffsetSeconds = 8 * 3600

// resolveLocation returns the *time.Location used to render bitable date cells.
// An empty name yields a fixed GMT+8 zone (no tzdata dependency); a named zone is
// loaded from the system tz database, falling back to GMT+8 if it is unavailable.
func resolveLocation(name string) *time.Location {
	if name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.FixedZone("GMT+8", defaultTimezoneOffsetSeconds)
}

// defaultBaseURL is the default Feishu Open Platform API base URL.
const defaultBaseURL = "https://open.feishu.cn"

// larkBaseURL is the Lark (international) API base URL.
const larkBaseURL = "https://open.larksuite.com"

// GetBaseURL returns the effective base URL, defaulting to Feishu if not set.
func (c *Config) GetBaseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultBaseURL
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

// ObjTypeToExportFileExtension maps Feishu obj_type to the best export file_extension.
var ObjTypeToExportFileExtension = map[string]string{
	"docx":    ExportTypeDocx,
	"doc":     ExportTypeDocx,
	"sheet":   ExportTypeXlsx,
	"bitable": ExportTypeXlsx,
}

// ObjTypeToExportType maps Feishu obj_type to the export API "type" parameter.
// See: https://open.feishu.cn/document/server-docs/docs/drive-v1/export_task/create
var ObjTypeToExportType = map[string]string{
	"docx":    "docx",
	"doc":     "doc",
	"sheet":   "sheet",
	"bitable": "bitable",
}

// ExportFileExtToSuffix maps export file_extension to the file suffix for FileName.
var ExportFileExtToSuffix = map[string]string{
	ExportTypeDocx: ".docx",
	ExportTypeXlsx: ".xlsx",
	ExportTypePDF:  ".pdf",
}

// --- Feishu API response structures ---

// ApiResponse is the common Feishu API response wrapper.
type ApiResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// TokenResponse is the response for tenant_access_token API.
type TokenResponse struct {
	ApiResponse
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"` // seconds
}

// WikiSpaceListData is the data payload of WikiSpaceListResponse.
type WikiSpaceListData struct {
	Items     []WikiSpace `json:"items"`
	HasMore   bool        `json:"has_more"`
	PageToken string      `json:"page_token"`
}

// WikiSpaceListResponse is the response for GET /open-apis/wiki/v2/spaces.
type WikiSpaceListResponse struct {
	ApiResponse
	Data WikiSpaceListData `json:"data"`
}

// WikiSpace represents a Feishu Wiki space.
type WikiSpace struct {
	SpaceID     string `json:"space_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"` // "public" or "private"
}

// WikiNodeListData is the data payload of WikiNodeListResponse.
type WikiNodeListData struct {
	Items     []WikiNode `json:"items"`
	HasMore   bool       `json:"has_more"`
	PageToken string     `json:"page_token"`
}

// WikiNodeListResponse is the response for GET /open-apis/wiki/v2/spaces/:space_id/nodes.
type WikiNodeListResponse struct {
	ApiResponse
	Data WikiNodeListData `json:"data"`
}

// WikiNode represents a node (document or folder) in a Feishu Wiki space.
type WikiNode struct {
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

// WikiNodeInfoData is the data payload of WikiNodeInfoResponse.
type WikiNodeInfoData struct {
	Node WikiNode `json:"node"`
}

// WikiNodeInfoResponse is the response for GET /open-apis/wiki/v2/spaces/get_node.
type WikiNodeInfoResponse struct {
	ApiResponse
	Data WikiNodeInfoData `json:"data"`
}

// --- Export task API responses ---

// docRawContentData is the data payload of docRawContentResponse.
type docRawContentData struct {
	Content string `json:"content"`
}

// docRawContentResponse is the response for GET /open-apis/docx/v1/documents/:document_id/raw_content.
// Deprecated: prefer export API for full-fidelity document export.
type docRawContentResponse struct {
	ApiResponse
	Data docRawContentData `json:"data"`
}

// ExportTaskCreateData is the data payload of ExportTaskCreateResponse.
type ExportTaskCreateData struct {
	Ticket string `json:"ticket"`
}

// ExportTaskCreateResponse is the response for POST /drive/v1/export_tasks.
type ExportTaskCreateResponse struct {
	ApiResponse
	Data ExportTaskCreateData `json:"data"`
}

// ExportTaskResult is the per-task result inside ExportTaskStatusData.
type ExportTaskResult struct {
	FileToken string `json:"file_token"`
	FileSize  int64  `json:"file_size"`
	// JobStatus: 0=success, 1=initializing, 2=processing
	JobStatus   int    `json:"job_status"`
	JobErrorMsg string `json:"job_error_msg"`
	FileName    string `json:"file_name"`
}

// ExportTaskStatusData is the data payload of ExportTaskStatusResponse.
type ExportTaskStatusData struct {
	Result ExportTaskResult `json:"result"`
}

// ExportTaskStatusResponse is the response for GET /drive/v1/export_tasks/{ticket}.
type ExportTaskStatusResponse struct {
	ApiResponse
	Data ExportTaskStatusData `json:"data"`
}

// --- File download response ---

// driveFileMeta is one entry of driveFileMetaData.Metas.
type driveFileMeta struct {
	DocToken string `json:"doc_token"`
	DocType  string `json:"doc_type"`
	Title    string `json:"title"`
}

// driveFileMetaData is the data payload of driveFileMetaResponse.
type driveFileMetaData struct {
	Metas []driveFileMeta `json:"metas"`
}

// driveFileMetaResponse is the response for GET /drive/v1/metas for file type nodes.
type driveFileMetaResponse struct {
	ApiResponse
	Data driveFileMetaData `json:"data"`
}

// FeishuCursor stores incremental sync state for Feishu.
type FeishuCursor struct {
	// LastSyncTime is the timestamp of the last successful sync.
	LastSyncTime time.Time `json:"last_sync_time"`

	// SpaceNodeTimes maps space_id -> node_token -> last known edit time.
	// Used to detect which nodes have changed since last sync.
	SpaceNodeTimes map[string]map[string]string `json:"space_node_times,omitempty"`
}

// --- Drive (云盘) file listing types (feishu_drive / lark_drive connectors) ---
// Added by feat/datasource-feishu-drive. These are independent of the wiki
// types above and do not affect the wiki connector.

// DriveFile represents a file/folder in Feishu Drive (云空间). Returned by
// GET /open-apis/drive/v1/files?folder_token=xxx. The list API returns
// modified_time directly (verified), so no batch_query/metas call is needed for
// incremental detection - see ADR-0002.
type DriveFile struct {
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

// DriveFileListData is the data payload of DriveFileListResponse.
type DriveFileListData struct {
	Files         []DriveFile `json:"files"`
	HasMore       bool        `json:"has_more"`
	NextPageToken string      `json:"next_page_token"`
}

// DriveFileListResponse is the response for GET /open-apis/drive/v1/files.
type DriveFileListResponse struct {
	ApiResponse
	Data DriveFileListData `json:"data"`
}

// driveFolderMetaData is the data payload of driveFolderMetaResponse.
type driveFolderMetaData struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Token     string `json:"token"`
	CreateUid string `json:"createUid"`
	EditUid   string `json:"editUid"`
	ParentID  string `json:"parentId"`
	OwnUid    string `json:"ownUid"`
}

// driveFolderMetaResponse is the response for GET /open-apis/drive/explorer/v2/folder/:folderToken/meta.
// Used to resolve a root folder's human-readable name (the list API only returns
// the folder's children, not the folder itself).
type driveFolderMetaResponse struct {
	ApiResponse
	Data driveFolderMetaData `json:"data"`
}

// DriveFileListFailure records a single sub-folder listing that failed during a
// recursive walk. Mirrors WikiNodeListFailure.
type DriveFileListFailure struct {
	FolderToken string
	Err         error
}

// PartialDriveFileListError aggregates per-folder listing failures so the walk
// can continue and the caller can still surface the partial result. Mirrors
// PartialWikiNodeListError.
type PartialDriveFileListError struct {
	Failures []DriveFileListFailure
}

func (e *PartialDriveFileListError) Error() string {
	if e == nil || len(e.Failures) == 0 {
		return "partial drive file listing failed"
	}
	parts := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		parts = append(parts, failure.Err.Error())
	}
	return strings.Join(parts, "; ")
}

// FeishuDriveCursor stores incremental sync state for Feishu Drive (云盘).
// Structurally symmetric with FeishuCursor: outer key = resourceID
// ("folderToken" or "folderToken:fileToken"), inner key = file_token,
// value = modified_time. See ADR-0001.
type FeishuDriveCursor struct {
	// LastSyncTime is the timestamp of the last successful sync.
	LastSyncTime time.Time `json:"last_sync_time"`

	// FileTimes maps resourceID -> file_token -> last known modified_time.
	// Used to detect which files have changed since last sync.
	FileTimes map[string]map[string]string `json:"file_times,omitempty"`
}
