// Package dingtalk implements the DingTalk document data source connector.
//
// The connector uses DingTalk app credentials to list document workspaces,
// traverse document/folder resources, and ingest documents as Markdown.
package dingtalk

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const DefaultBaseURL = "https://api.dingtalk.com"

type Config struct {
	AppKey     string `json:"app_key"`
	AppSecret  string `json:"app_secret"`
	OperatorID string `json:"operator_id"`
	BaseURL    string `json:"base_url,omitempty"`
}

func (c *Config) GetBaseURL() string {
	baseURL := strings.TrimSpace(c.BaseURL)
	if baseURL == "" {
		return DefaultBaseURL
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	return strings.TrimRight(baseURL, "/")
}

func parseConfig(config *types.DataSourceConfig) (*Config, error) {
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
	if strings.TrimSpace(cfg.OperatorID) == "" {
		return nil, fmt.Errorf("%w: operator_id is required", datasource.ErrInvalidCredentials)
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type accessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"`
	ErrCode     int    `json:"errcode"`
	Code        string `json:"code"`
	Message     string `json:"message"`
}

type apiErrorBody struct {
	Message string `json:"message"`
	Code    string `json:"code"`
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

type workspace struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspaceId"`
	SpaceID      string `json:"spaceId"`
	RootNodeID   string `json:"rootNodeId"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	URL          string `json:"url"`
	ModifiedAt   string `json:"modifiedAt"`
	UpdatedAt    string `json:"updatedAt"`
	ModifiedTime string `json:"modifiedTime"`
	HasChildren  bool   `json:"hasChildren"`
}

func (w workspace) normalizedID() string {
	return firstNonEmpty(w.ID, w.WorkspaceID, w.SpaceID)
}

func (w workspace) displayName() string {
	return firstNonEmpty(w.Name, w.Title, w.normalizedID())
}

func (w workspace) modifiedAtText() string {
	return firstNonEmpty(w.ModifiedAt, w.UpdatedAt, w.ModifiedTime)
}

type node struct {
	ID           string `json:"id"`
	NodeID       string `json:"nodeId"`
	DocID        string `json:"docId"`
	DentryID     string `json:"dentryId"`
	FileID       string `json:"fileId"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	Category     string `json:"category"`
	Extension    string `json:"extension"`
	ParentID     string `json:"parentId"`
	URL          string `json:"url"`
	ModifiedAt   string `json:"modifiedAt"`
	UpdatedAt    string `json:"updatedAt"`
	ModifiedTime string `json:"modifiedTime"`
	CreateTime   string `json:"createTime"`
	HasChildren  bool   `json:"hasChildren"`
}

func (n node) normalizedID() string {
	return firstNonEmpty(n.ID, n.NodeID, n.DocID, n.DentryID, n.FileID)
}

func (n node) displayName() string {
	return firstNonEmpty(n.Name, n.Title, n.normalizedID())
}

func (n node) modifiedAtText() string {
	return firstNonEmpty(n.ModifiedAt, n.UpdatedAt, n.ModifiedTime, n.CreateTime)
}

func (n node) normalizedType() string {
	t := strings.ToLower(strings.TrimSpace(n.Type))
	switch t {
	case "folder", "directory", "dir":
		return "folder"
	case "document", "doc", "wiki", "file":
		return "document"
	case "":
		if n.HasChildren {
			return "folder"
		}
		return "document"
	default:
		if n.HasChildren {
			return "folder"
		}
		return t
	}
}

func (n node) isFolder() bool {
	return n.normalizedType() == "folder"
}

func (n node) isDocument() bool {
	t := n.normalizedType()
	return t == "document" || t == "doc" || t == "wiki" || t == "file"
}

type documentDetail struct {
	ID           string `json:"id"`
	DocID        string `json:"docId"`
	NodeID       string `json:"nodeId"`
	WorkspaceID  string `json:"workspaceId"`
	Title        string `json:"title"`
	Name         string `json:"name"`
	Markdown     string `json:"markdown"`
	Content      string `json:"content"`
	Body         string `json:"body"`
	URL          string `json:"url"`
	UpdatedAt    string `json:"updatedAt"`
	ModifiedAt   string `json:"modifiedAt"`
	ModifiedTime string `json:"modifiedTime"`
	CreateTime   string `json:"createTime"`
}

func (d documentDetail) normalizedID() string {
	return firstNonEmpty(d.ID, d.DocID, d.NodeID)
}

func (d documentDetail) displayTitle(fallback string) string {
	return firstNonEmpty(d.Title, d.Name, fallback, d.normalizedID())
}

func (d documentDetail) markdown() string {
	return firstNonEmpty(d.Markdown, d.Content, d.Body)
}

func (d documentDetail) updatedAtText() string {
	return firstNonEmpty(d.UpdatedAt, d.ModifiedAt, d.ModifiedTime, d.CreateTime)
}

type dingtalkCursor struct {
	LastSyncTime time.Time         `json:"last_sync_time"`
	DocTimes     map[string]string `json:"doc_times,omitempty"`
}

type resourceRef struct {
	Kind        string
	WorkspaceID string
	NodeID      string
}

func workspaceExternalID(workspaceID string) string {
	return "workspace:" + workspaceID
}

func workspaceExternalIDWithRoot(workspaceID, rootNodeID string) string {
	if strings.TrimSpace(rootNodeID) == "" {
		return workspaceExternalID(workspaceID)
	}
	return "workspace:" + workspaceID + ":" + rootNodeID
}

func folderExternalID(workspaceID, nodeID string) string {
	return "folder:" + workspaceID + ":" + nodeID
}

func docExternalID(workspaceID, docID string) string {
	return "doc:" + workspaceID + ":" + docID
}

func parseResourceRef(externalID string) (resourceRef, error) {
	parts := strings.SplitN(strings.TrimSpace(externalID), ":", 3)
	if len(parts) == 1 {
		// Backward/defensive fallback: a raw ID is treated as a workspace.
		return resourceRef{Kind: "workspace", WorkspaceID: parts[0]}, nil
	}
	switch parts[0] {
	case "workspace":
		if len(parts) < 2 || parts[1] == "" {
			return resourceRef{}, fmt.Errorf("invalid workspace resource id %q", externalID)
		}
		ref := resourceRef{Kind: "workspace", WorkspaceID: parts[1]}
		if len(parts) == 3 {
			ref.NodeID = parts[2]
		}
		return ref, nil
	case "folder", "doc":
		if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
			return resourceRef{}, fmt.Errorf("invalid %s resource id %q", parts[0], externalID)
		}
		return resourceRef{Kind: parts[0], WorkspaceID: parts[1], NodeID: parts[2]}, nil
	default:
		return resourceRef{}, fmt.Errorf("invalid dingtalk resource id %q", externalID)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func parseTime(ts string) time.Time {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", ts); err == nil {
		return t
	}
	if ms, err := strconv.ParseInt(ts, 10, 64); err == nil {
		if ms > 1_000_000_000_000 {
			return time.UnixMilli(ms).UTC()
		}
		if ms > 0 {
			return time.Unix(ms, 0).UTC()
		}
	}
	return time.Time{}
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
		"\n", " ", "\r", " ", "\t", " ",
	)
	result := strings.TrimSpace(replacer.Replace(name))
	if result == "" {
		return "untitled"
	}
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
