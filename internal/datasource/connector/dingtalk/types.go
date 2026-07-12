// Package dingtalk implements the DingTalk (钉钉) document / wiki data source
// connector for WeKnora.
//
// It syncs knowledge-base workspaces and documents from DingTalk into WeKnora,
// converting block content into Markdown.
//
// DingTalk OpenAPI docs:
//   - Auth:        POST /v1.0/oauth2/accessToken
//   - Workspaces:  GET  /v2.0/wiki/workspaces
//   - Nodes:       GET  /v2.0/wiki/nodes
//   - Doc blocks:  GET  /v1.0/doc/suites/documents/{docKey}/blocks
//
// Credentials (stored encrypted):
//   - app_key       Client ID / AppKey from the DingTalk developer console
//   - app_secret    Client Secret / AppSecret
//   - operator_id   Operator unionId (required by wiki/doc APIs for permission)
//
// Known limitations (v1):
//   - Only FILE nodes with document-like categories are content-synced
//   - Complex blocks (images, code, advanced tables) degrade to plain text
//   - Hierarchical listing is lazy (one level per ListResources call)
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

// DefaultBaseURL is the DingTalk OpenAPI host.
const DefaultBaseURL = "https://api.dingtalk.com"

// Resource ID encoding: workspace resources use the bare workspaceId;
// nested nodes use "workspaceId:nodeId" so FetchAll can resolve both the
// workspace root and a specific subtree without extra API round-trips.
const resourceIDSeparator = ":"

// Config holds DingTalk-specific configuration for the data source connector.
type Config struct {
	// AppKey is the application Client ID (AppKey) from the developer console.
	AppKey string `json:"app_key"`

	// AppSecret is the application Client Secret (AppSecret).
	AppSecret string `json:"app_secret"`

	// OperatorID is the operator's unionId. DingTalk wiki/doc APIs require a
	// real user identity for permission checks (not a bare application token).
	OperatorID string `json:"operator_id"`

	// BaseURL overrides the OpenAPI host (default: https://api.dingtalk.com).
	// Useful for private / dedicated DingTalk deployments.
	BaseURL string `json:"base_url,omitempty"`
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
	return strings.TrimRight(url, "/")
}

// parseDingTalkConfig extracts and validates DingTalk-specific credentials.
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
	if strings.TrimSpace(cfg.OperatorID) == "" {
		return nil, fmt.Errorf("%w: operator_id (unionId) is required", datasource.ErrInvalidCredentials)
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// --- DingTalk API response types ---

type accessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"`
	Code        string `json:"code"`
	Message     string `json:"message"`
}

// workspace is a DingTalk knowledge-base workspace.
type workspace struct {
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RootNodeID  string `json:"rootNodeId"`
	URL         string `json:"url"`
	// ModifiedTime is best-effort; not always populated by the list API.
	ModifiedTime string `json:"modifiedTime"`
	CreateTime   string `json:"createTime"`
}

type workspaceListResponse struct {
	Workspaces []workspace `json:"workspaces"`
	NextToken  string      `json:"nextToken"`
	// Some responses wrap under "result"
	Result *struct {
		Workspaces []workspace `json:"workspaces"`
		NextToken  string      `json:"nextToken"`
	} `json:"result"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r *workspaceListResponse) items() ([]workspace, string) {
	if r.Result != nil && len(r.Result.Workspaces) > 0 {
		return r.Result.Workspaces, r.Result.NextToken
	}
	return r.Workspaces, r.NextToken
}

// wikiNode is a node (document or folder) in a DingTalk knowledge base.
type wikiNode struct {
	NodeID      string `json:"nodeId"`
	Name        string `json:"name"`
	// Type is "FILE" or "FOLDER".
	Type string `json:"type"`
	// Category distinguishes document kinds (e.g. ALIDOC, DOC, FOLDER).
	Category     string `json:"category"`
	WorkspaceID  string `json:"workspaceId"`
	ParentNodeID string `json:"parentNodeId"`
	URL          string `json:"url"`
	// HasChildren may be absent; treat missing as false and discover via listing.
	HasChildren  bool   `json:"hasChildren"`
	ModifiedTime string `json:"modifiedTime"`
	CreateTime   string `json:"createTime"`
	// DocKey is the content key used by the blocks API. For wiki nodes it is
	// typically equal to NodeID; create-doc responses may return a separate value.
	DocKey string `json:"docKey"`
}

type nodeListResponse struct {
	Nodes     []wikiNode `json:"nodes"`
	NextToken string     `json:"nextToken"`
	Result    *struct {
		Nodes     []wikiNode `json:"nodes"`
		NextToken string     `json:"nextToken"`
	} `json:"result"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r *nodeListResponse) items() ([]wikiNode, string) {
	if r.Result != nil && len(r.Result.Nodes) > 0 {
		return r.Result.Nodes, r.Result.NextToken
	}
	return r.Nodes, r.NextToken
}

type nodeInfoResponse struct {
	Node   wikiNode `json:"node"`
	Result *struct {
		Node wikiNode `json:"node"`
	} `json:"result"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r *nodeInfoResponse) item() wikiNode {
	if r.Result != nil && r.Result.Node.NodeID != "" {
		return r.Result.Node
	}
	return r.Node
}

// docBlock is one element returned by the document blocks API.
type docBlock struct {
	ID        string `json:"id"`
	Index     int    `json:"index"`
	BlockType string `json:"blockType"`
	Heading   *struct {
		Level string `json:"level"`
		Text  string `json:"text"`
	} `json:"heading"`
	Paragraph *struct {
		Text string `json:"text"`
	} `json:"paragraph"`
	UnorderedList *struct {
		Text string `json:"text"`
	} `json:"unorderedList"`
	OrderedList *struct {
		Text string `json:"text"`
	} `json:"orderedList"`
	Blockquote *struct {
		Text string `json:"text"`
	} `json:"blockquote"`
	Table *struct {
		// Best-effort; structure varies. We stringify cell text when present.
		Rows [][]struct {
			Text string `json:"text"`
		} `json:"rows"`
	} `json:"table"`
}

type blocksResponse struct {
	Result *struct {
		Data []docBlock `json:"data"`
	} `json:"result"`
	Data    []docBlock `json:"data"`
	Success bool       `json:"success"`
	Code    string     `json:"code"`
	Message string     `json:"message"`
}

func (r *blocksResponse) items() []docBlock {
	if r.Result != nil && len(r.Result.Data) > 0 {
		return r.Result.Data
	}
	return r.Data
}

// dingtalkCursor stores incremental sync state.
// Key1: source resource ID, Key2: node ID, Value: modifiedTime raw string.
type dingtalkCursor struct {
	LastSyncTime  time.Time                       `json:"last_sync_time"`
	NodeModTimes  map[string]map[string]string    `json:"node_mod_times,omitempty"`
}

// encodeResourceID builds the composite external id for a nested node.
func encodeResourceID(workspaceID, nodeID string) string {
	if nodeID == "" {
		return workspaceID
	}
	return workspaceID + resourceIDSeparator + nodeID
}

// parseResourceID splits a composite external id into workspace + optional node.
func parseResourceID(id string) (workspaceID, nodeID string) {
	if i := strings.Index(id, resourceIDSeparator); i >= 0 {
		return id[:i], id[i+1:]
	}
	return id, ""
}

// isSyncableFile reports whether a wiki node should have its body content fetched.
// Folders are traversed but not content-synced; unknown FILE categories are kept
// so future document types still land in the knowledge base as best-effort text.
func isSyncableFile(n wikiNode) bool {
	t := strings.ToUpper(strings.TrimSpace(n.Type))
	if t == "FOLDER" {
		return false
	}
	// Empty type is treated as file (forward-compat).
	if t != "" && t != "FILE" {
		return false
	}
	cat := strings.ToUpper(strings.TrimSpace(n.Category))
	switch cat {
	case "FOLDER":
		return false
	default:
		return true
	}
}

// isFolder reports whether a node is a folder (has expandable children).
func isFolder(n wikiNode) bool {
	if strings.EqualFold(n.Type, "FOLDER") {
		return true
	}
	if strings.EqualFold(n.Category, "FOLDER") {
		return true
	}
	return n.HasChildren
}

// parseDingTalkTime parses common DingTalk timestamp formats.
func parseDingTalkTime(ts string) time.Time {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return time.Time{}
	}
	// RFC3339 / ISO 8601
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t
	}
	// Millisecond epoch as string
	if len(ts) >= 10 {
		var ms int64
		if _, err := fmt.Sscan(ts, &ms); err == nil && ms > 1_000_000_000_000 {
			return time.UnixMilli(ms)
		}
		if _, err := fmt.Sscan(ts, &ms); err == nil && ms > 1_000_000_000 {
			return time.Unix(ms, 0)
		}
	}
	return time.Time{}
}

// sanitizeFileName removes characters that are invalid in filenames and
// truncates to a safe length at a UTF-8 rune boundary.
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

// blocksToMarkdown converts DingTalk document blocks into a Markdown string.
func blocksToMarkdown(blocks []docBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	// Sort is not strictly required (API usually returns by index) but be safe.
	// Use a simple insertion sort by Index to avoid importing sort for tiny lists
	// when already ordered — still O(n log n) with sort.Slice is fine.
	type indexed struct {
		idx int
		b   docBlock
	}
	items := make([]indexed, len(blocks))
	for i, b := range blocks {
		items[i] = indexed{idx: b.Index, b: b}
	}
	// Stable bubble for small n — keep deps minimal.
	for i := 1; i < len(items); i++ {
		key := items[i]
		j := i - 1
		for j >= 0 && items[j].idx > key.idx {
			items[j+1] = items[j]
			j--
		}
		items[j+1] = key
	}

	var b strings.Builder
	for i, it := range items {
		line := blockToMarkdown(it.b)
		if line == "" {
			continue
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func blockToMarkdown(blk docBlock) string {
	switch strings.ToLower(blk.BlockType) {
	case "heading":
		if blk.Heading == nil {
			return ""
		}
		level := 2
		switch strings.ToLower(blk.Heading.Level) {
		case "heading-1", "h1", "1":
			level = 1
		case "heading-2", "h2", "2":
			level = 2
		case "heading-3", "h3", "3":
			level = 3
		case "heading-4", "h4", "4":
			level = 4
		case "heading-5", "h5", "5":
			level = 5
		case "heading-6", "h6", "6":
			level = 6
		}
		return strings.Repeat("#", level) + " " + blk.Heading.Text
	case "paragraph":
		if blk.Paragraph == nil {
			return ""
		}
		return blk.Paragraph.Text
	case "unorderedlist", "unordered_list", "bullet":
		if blk.UnorderedList == nil {
			return ""
		}
		return "- " + blk.UnorderedList.Text
	case "orderedlist", "ordered_list", "number":
		if blk.OrderedList == nil {
			return ""
		}
		return "1. " + blk.OrderedList.Text
	case "blockquote", "quote":
		if blk.Blockquote == nil {
			return ""
		}
		return "> " + blk.Blockquote.Text
	case "table":
		if blk.Table == nil || len(blk.Table.Rows) == 0 {
			return ""
		}
		var sb strings.Builder
		for ri, row := range blk.Table.Rows {
			sb.WriteString("|")
			for _, cell := range row {
				sb.WriteString(" ")
				sb.WriteString(strings.ReplaceAll(cell.Text, "|", "\\|"))
				sb.WriteString(" |")
			}
			sb.WriteString("\n")
			if ri == 0 {
				sb.WriteString("|")
				for range row {
					sb.WriteString(" --- |")
				}
				sb.WriteString("\n")
			}
		}
		return sb.String()
	default:
		// unknown / code / image — skip silently
		return ""
	}
}

// redactSecret returns a masked form of a secret for logging.
func redactSecret(t string) string {
	if len(t) < 12 {
		return "***"
	}
	return t[:4] + "..." + t[len(t)-4:]
}
