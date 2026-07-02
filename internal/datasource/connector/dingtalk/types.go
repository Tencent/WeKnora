// Package dingtalk implements a DingTalk document datasource connector.
//
// The connector uses DingTalk's native OpenAPI flow:
//   - app credentials -> tenant access token
//   - operator user ID -> union ID
//   - wiki workspace/node APIs -> selectable resources
//   - document block API -> Markdown content
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

const (
	DefaultBaseURL     = "https://api.dingtalk.com"
	DefaultOAPIBaseURL = "https://oapi.dingtalk.com"
)

type Config struct {
	AppKey          string `json:"app_key"`
	AppSecret       string `json:"app_secret"`
	OperatorUserID  string `json:"operator_user_id,omitempty"`
	OperatorUnionID string `json:"operator_union_id,omitempty"`
	BaseURL         string `json:"base_url,omitempty"`
	OAPIBaseURL     string `json:"oapi_base_url,omitempty"`
}

func (c *Config) GetBaseURL() string {
	return normalizeBaseURL(c.BaseURL, DefaultBaseURL)
}

func (c *Config) GetOAPIBaseURL() string {
	if strings.TrimSpace(c.OAPIBaseURL) != "" {
		return normalizeBaseURL(c.OAPIBaseURL, DefaultOAPIBaseURL)
	}
	if strings.TrimSpace(c.BaseURL) != "" {
		return c.GetBaseURL()
	}
	return DefaultOAPIBaseURL
}

func parseDingTalkConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	if len(config.Credentials) == 0 {
		return nil, fmt.Errorf("%w: credentials are required", datasource.ErrInvalidCredentials)
	}

	cfg := &Config{
		AppKey:          credentialString(config.Credentials, "client_id", "app_key", "app_id", "appKey"),
		AppSecret:       credentialString(config.Credentials, "client_secret", "app_secret", "appSecret"),
		OperatorUserID:  credentialString(config.Credentials, "operator_user_id", "operator_id", "user_id", "userid", "userId"),
		OperatorUnionID: credentialString(config.Credentials, "operator_union_id", "union_id", "unionid", "unionId"),
		BaseURL:         credentialString(config.Credentials, "base_url"),
		OAPIBaseURL:     credentialString(config.Credentials, "oapi_base_url"),
	}

	if strings.TrimSpace(cfg.AppKey) == "" {
		return nil, fmt.Errorf("%w: client_id/app_key is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(cfg.AppSecret) == "" {
		return nil, fmt.Errorf("%w: client_secret/app_secret is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(cfg.OperatorUserID) == "" && strings.TrimSpace(cfg.OperatorUnionID) == "" {
		return nil, fmt.Errorf("%w: operator_user_id or operator_union_id is required", datasource.ErrInvalidCredentials)
	}
	return cfg, nil
}

func credentialString(credentials map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		raw, ok := credentials[key]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case fmt.Stringer:
			if s := strings.TrimSpace(v.String()); s != "" {
				return s
			}
		default:
			b, err := json.Marshal(v)
			if err == nil {
				var s string
				if json.Unmarshal(b, &s) == nil && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
	}
	return ""
}

func normalizeBaseURL(raw, fallback string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		base = fallback
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return strings.TrimRight(base, "/")
}

type accessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int    `json:"expireIn"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

type userDetailResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	Result  struct {
		UnionID string `json:"unionid"`
	} `json:"result"`
}

type workspaceListResponse struct {
	Workspaces []workspace `json:"workspaces"`
	NextToken  string      `json:"nextToken"`
	HasMore    bool        `json:"hasMore"`
	Data       struct {
		Workspaces []workspace `json:"workspaces"`
		NextToken  string      `json:"nextToken"`
		HasMore    bool        `json:"hasMore"`
	} `json:"data"`
}

func (r workspaceListResponse) items() ([]workspace, string, bool) {
	if len(r.Workspaces) > 0 || r.NextToken != "" || r.HasMore {
		return r.Workspaces, r.NextToken, r.HasMore
	}
	return r.Data.Workspaces, r.Data.NextToken, r.Data.HasMore
}

type workspace struct {
	WorkspaceID  string `json:"workspaceId"`
	RootNodeID   string `json:"rootNodeId"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	URL          string `json:"url"`
	ModifiedTime string `json:"modifiedTime"`
}

type nodeListResponse struct {
	Nodes     []wikiNode `json:"nodes"`
	NextToken string     `json:"nextToken"`
	HasMore   bool       `json:"hasMore"`
	Data      struct {
		Nodes     []wikiNode `json:"nodes"`
		NextToken string     `json:"nextToken"`
		HasMore   bool       `json:"hasMore"`
	} `json:"data"`
}

func (r nodeListResponse) items() ([]wikiNode, string, bool) {
	if len(r.Nodes) > 0 || r.NextToken != "" || r.HasMore {
		return r.Nodes, r.NextToken, r.HasMore
	}
	return r.Data.Nodes, r.Data.NextToken, r.Data.HasMore
}

type nodeDetailResponse struct {
	Node wikiNode `json:"node"`
	Data struct {
		Node wikiNode `json:"node"`
	} `json:"data"`
}

func (r nodeDetailResponse) item() wikiNode {
	if r.Node.NodeID != "" {
		return r.Node
	}
	return r.Data.Node
}

type wikiNode struct {
	NodeID       string `json:"nodeId"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	Category     string `json:"category"`
	DocKey       string `json:"docKey"`
	URL          string `json:"url"`
	ModifiedTime string `json:"modifiedTime"`
	UpdatedTime  string `json:"updatedTime"`
	ParentNodeID string `json:"parentNodeId"`
	ParentID     string `json:"parentId"`
	HasChildren  bool   `json:"hasChildren"`
}

func (n wikiNode) displayName() string {
	if strings.TrimSpace(n.Name) != "" {
		return strings.TrimSpace(n.Name)
	}
	if strings.TrimSpace(n.Title) != "" {
		return strings.TrimSpace(n.Title)
	}
	return "Untitled"
}

func (n wikiNode) parentNodeID() string {
	if strings.TrimSpace(n.ParentNodeID) != "" {
		return strings.TrimSpace(n.ParentNodeID)
	}
	return strings.TrimSpace(n.ParentID)
}

func (n wikiNode) docKey() string {
	if strings.TrimSpace(n.DocKey) != "" {
		return strings.TrimSpace(n.DocKey)
	}
	return strings.TrimSpace(n.NodeID)
}

func (n wikiNode) modifiedTime() string {
	if strings.TrimSpace(n.ModifiedTime) != "" {
		return strings.TrimSpace(n.ModifiedTime)
	}
	return strings.TrimSpace(n.UpdatedTime)
}

func (n wikiNode) isFolder() bool {
	t := strings.ToUpper(strings.TrimSpace(n.Type))
	return t == "FOLDER" || t == "DIR" || n.HasChildren
}

func (n wikiNode) isSupportedDocument() bool {
	t := strings.ToUpper(strings.TrimSpace(n.Type))
	category := strings.ToUpper(strings.TrimSpace(n.Category))
	return (t == "FILE" || t == "DOCUMENT" || t == "") && (category == "ALIDOC" || category == "DOC" || category == "")
}

type blockListResponse struct {
	Blocks    []docBlock `json:"blocks"`
	NextToken string     `json:"nextToken"`
	HasMore   bool       `json:"hasMore"`
	Data      struct {
		Blocks    []docBlock `json:"blocks"`
		NextToken string     `json:"nextToken"`
		HasMore   bool       `json:"hasMore"`
	} `json:"data"`
	// DingTalk's block API may wrap the actual block list in result.data.
	Result struct {
		Data      []docBlock `json:"data"`
		Blocks    []docBlock `json:"blocks"`
		NextToken string     `json:"nextToken"`
		HasMore   bool       `json:"hasMore"`
	} `json:"result"`
}

func (r blockListResponse) items() ([]docBlock, string, bool) {
	if len(r.Blocks) > 0 || r.NextToken != "" || r.HasMore {
		return r.Blocks, r.NextToken, r.HasMore
	}
	if len(r.Result.Data) > 0 || r.Result.NextToken != "" || r.Result.HasMore {
		return r.Result.Data, r.Result.NextToken, r.Result.HasMore
	}
	if len(r.Result.Blocks) > 0 {
		return r.Result.Blocks, r.Result.NextToken, r.Result.HasMore
	}
	return r.Data.Blocks, r.Data.NextToken, r.Data.HasMore
}

type docBlock struct {
	Type       string     `json:"type"`
	BlockType  string     `json:"blockType,omitempty"`
	Text       string     `json:"text,omitempty"`
	Heading    blockText  `json:"heading,omitempty"`
	Paragraph  blockText  `json:"paragraph,omitempty"`
	Bullet     blockText  `json:"bullet,omitempty"`
	Ordered    blockText  `json:"ordered,omitempty"`
	Blockquote blockText  `json:"blockquote,omitempty"`
	Callout    blockText  `json:"callout,omitempty"`
	Children   []docBlock `json:"children,omitempty"`
}

func (b docBlock) kind() string {
	if strings.TrimSpace(b.Type) != "" {
		return strings.TrimSpace(b.Type)
	}
	return strings.TrimSpace(b.BlockType)
}

type blockText struct {
	Text             string            `json:"text,omitempty"`
	Content          string            `json:"content,omitempty"`
	Level            interface{}       `json:"level,omitempty"`
	Elements         []richTextElement `json:"elements,omitempty"`
	RichTextElements []richTextElement `json:"richTextElements,omitempty"`
}

type richTextElement struct {
	Text    string `json:"text,omitempty"`
	Content string `json:"content,omitempty"`
	TextRun struct {
		Content string `json:"content,omitempty"`
	} `json:"textRun,omitempty"`
}

type dingtalkCursor struct {
	LastSyncTime      time.Time                    `json:"last_sync_time"`
	ResourceDocTimes  map[string]map[string]string `json:"resource_doc_times,omitempty"`
	ResourceDocHashes map[string]map[string]string `json:"resource_doc_hashes,omitempty"`
}

func makeResourceID(workspaceID, nodeID string) string {
	return workspaceID + ":" + nodeID
}

func parseResourceID(resourceID string) (string, string, error) {
	parts := strings.SplitN(resourceID, ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("%w: invalid DingTalk resource id %q", datasource.ErrInvalidConfig, resourceID)
	}
	return parts[0], parts[1], nil
}

func parseDingTalkTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if ms > 1_000_000_000_000 {
			return time.UnixMilli(ms)
		}
		return time.Unix(ms, 0)
	}
	return time.Time{}
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "untitled"
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

func markdownFileName(title string) string {
	name := sanitizeFileName(title)
	lower := strings.ToLower(name)
	for _, ext := range []string{".md", ".markdown", ".adoc", ".asciidoc", ".txt"} {
		if strings.HasSuffix(lower, ext) {
			name = strings.TrimSpace(name[:len(name)-len(ext)])
			break
		}
	}
	if name == "" {
		name = "untitled"
	}
	return name + ".md"
}
