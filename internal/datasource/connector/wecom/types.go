// Package wecom implements the WeCom (企业微信) document data source connector for WeKnora.
//
// It syncs smart documents and WeDrive files from WeCom into WeKnora knowledge bases.
//
// WeCom API docs:
//   - Auth:              https://developer.work.weixin.qq.com/document/path/91039
//   - Smart doc list:    https://developer.work.weixin.qq.com/document/path/97460
//   - Doc base info:     https://developer.work.weixin.qq.com/document/path/97461
//   - WeDrive file list: https://developer.work.weixin.qq.com/document/path/95859
//   - WeDrive download:  https://developer.work.weixin.qq.com/document/path/98021
package wecom

import (
	"fmt"
	"time"
)

// Config holds WeCom-specific configuration for the data source connector.
type Config struct {
	CorpID     string `json:"corp_id"`
	CorpSecret string `json:"corp_secret"`
}

// BaseURL is the WeCom API base URL.
const BaseURL = "https://qyapi.weixin.qq.com"

// Smart document type filter values for POST /cgi-bin/wedoc/doc_list.
const (
	DocFilterAll   = 0 // All types
	DocFilterDoc   = 3 // Documents (文档)
	DocFilterSheet = 4 // Spreadsheets (表格)
	DocFilterForm  = 10 // Collect forms (收集表)
)

// WeDrive file_type values returned by file_list / file_info.
const (
	FileTypeFolder  = 1 // Folder
	FileTypeFile    = 2 // Uploaded file (binary)
	FileTypeDoc     = 3 // Smart doc (文档)
	FileTypeSheet   = 4 // Smart sheet (表格)
	FileTypeForm    = 5 // Collect form (收集表)
)

// Resource type identifiers used by ListResources.
const (
	ResourceAllDocs = "all_documents"
	ResourceDocs    = "documents"
	ResourceSheets  = "sheets"
)

// docFilterForResource maps resource type IDs to doc/list type parameters.
var docFilterForResource = map[string]int{
	ResourceAllDocs: DocFilterAll,
	ResourceDocs:    DocFilterDoc,
	ResourceSheets:  DocFilterSheet,
}

// --- WeCom API response structures ---

type apiResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

func (r *apiResponse) ok() bool { return r.ErrCode == 0 }

func (r *apiResponse) wrapError(action string) error {
	hint := ""
	switch r.ErrCode {
	case 48002:
		hint = " (应用未被授权调用此接口，请在管理后台「协作→文档→API」中将应用添加为「可调用接口的应用」，并检查「企业可信IP」配置)"
	case 60020:
		hint = " (IP 不在企业可信 IP 白名单中，请在应用详情页底部添加服务器 IP)"
	case 40014, 42001:
		hint = " (access_token 无效或已过期)"
	case 301002:
		hint = " (应用无文档访问权限，请检查可见范围设置)"
	}
	return fmt.Errorf("%s: errcode=%d errmsg=%s%s", action, r.ErrCode, r.ErrMsg, hint)
}

type tokenResponse struct {
	apiResponse
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"` // seconds, typically 7200
}

// docListItem is a single item returned by the doc list API.
type docListItem struct {
	DocID   string `json:"docid"`
	DocName string `json:"doc_name"`
	DocType int    `json:"doc_type,omitempty"`
}

// docListResponse is the response for POST /cgi-bin/wedoc/doc_list.
type docListResponse struct {
	apiResponse
	DocInfoList []docListItem `json:"doc_info_list"`
	HasMore     bool          `json:"has_more"`
	NextCursor  string        `json:"next_cursor"`
}

// docBaseInfo is the detailed metadata of a single document.
type docBaseInfo struct {
	DocID      string `json:"docid"`
	DocName    string `json:"doc_name"`
	CreateTime int64  `json:"create_time"` // unix timestamp
	ModifyTime int64  `json:"modify_time"` // unix timestamp
	DocType    int    `json:"doc_type"`    // 3=doc, 4=sheet
}

// docBaseInfoResponse is the response for POST /cgi-bin/wedoc/get_doc_base_info.
type docBaseInfoResponse struct {
	apiResponse
	DocBaseInfo docBaseInfo `json:"doc_base_info"`
}

// driveFileItem represents a file/folder in WeDrive.
type driveFileItem struct {
	FileID       string `json:"fileid"`
	FileName     string `json:"file_name"`
	SpaceID      string `json:"spaceid"`
	FatherID     string `json:"fatherid"`
	FileSize     int64  `json:"file_size"`
	CreateTime   int64  `json:"ctime"`
	ModifyTime   int64  `json:"mtime"`
	FileType     int    `json:"file_type"` // 1=folder, 2=file, 3=doc, 4=sheet, 5=form
	FileStatus   int    `json:"file_status"`
	CreateUserID string `json:"create_userid"`
	UpdateUserID string `json:"update_userid"`
	URL          string `json:"url"` // non-empty for smart doc types
}

// driveFileListResponse is the response for POST /cgi-bin/wedrive/file_list.
type driveFileListResponse struct {
	apiResponse
	FileList struct {
		Item    []driveFileItem `json:"item"`
		HasMore bool            `json:"has_more"`
	} `json:"file_list"`
}

// driveFileDownloadResponse is the response for POST /cgi-bin/wedrive/file_download.
type driveFileDownloadResponse struct {
	apiResponse
	DownloadURL string `json:"download_url"`
	CookieName  string `json:"cookie_name"`
	CookieValue string `json:"cookie_value"`
}

// wecomCursor stores incremental sync state for WeCom.
type wecomCursor struct {
	LastSyncTime   time.Time        `json:"last_sync_time"`
	DocModifyTimes map[string]int64 `json:"doc_modify_times,omitempty"` // docid -> modify_time
}
