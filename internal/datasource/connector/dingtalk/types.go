// Package dingtalk implements a DingTalk document datasource connector.
//
// The connector uses DingTalk's native OpenAPI flow:
//   - app credentials -> tenant access token
//   - operator user ID -> union ID
//   - wiki workspace/node APIs -> selectable resources
//   - drive download API -> original uploaded files
//   - document block API -> best-effort Markdown content
package dingtalk

import (
	"bytes"
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
	DentryUUID   string `json:"dentryUuid"`
	WorkspaceID  string `json:"workspaceId"`
	SpaceID      string `json:"spaceId"`
	DentryID     string `json:"dentryId"`
	Extension    string `json:"extension"`
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
	return n.isOnlineDocument() || n.isDownloadableFile()
}

func (n wikiNode) isOnlineDocument() bool {
	t := strings.ToUpper(strings.TrimSpace(n.Type))
	category := strings.ToUpper(strings.TrimSpace(n.Category))
	return (t == "FILE" || t == "DOCUMENT" || t == "") && (category == "ALIDOC" || category == "DOC" || category == "")
}

func (n wikiNode) isDownloadableFile() bool {
	t := strings.ToUpper(strings.TrimSpace(n.Type))
	category := strings.ToUpper(strings.TrimSpace(n.Category))
	if t != "FILE" && t != "DOCUMENT" && t != "" {
		return false
	}
	switch category {
	case "ALIDOC", "DOC", "AXLS", "ASHEET", "ASLIDE", "ADRAW", "AMIND", "":
		return false
	default:
		return true
	}
}

func (n wikiNode) downloadSpaceID(workspaceID string) string {
	return firstNonEmpty(n.SpaceID, n.WorkspaceID, workspaceID)
}

func (n wikiNode) downloadDentryID() string {
	return firstNonEmpty(n.DentryID, n.NodeID)
}

func (n wikiNode) downloadDentryUUID() string {
	return firstNonEmpty(n.DentryUUID, n.NodeID)
}

func (n wikiNode) downloadFileName() string {
	name := n.displayName()
	ext := strings.TrimSpace(n.Extension)
	if ext == "" {
		ext = strings.ToLower(strings.TrimSpace(n.Category))
	}
	if ext == "" || strings.Contains(strings.ToLower(name), "."+strings.ToLower(strings.TrimPrefix(ext, "."))) {
		return name
	}
	return name + "." + strings.TrimPrefix(ext, ".")
}

type blockListResponse struct {
	Blocks []docBlock `json:"blocks"`
	Data   struct {
		Blocks []docBlock `json:"blocks"`
	} `json:"data"`
	Result struct {
		Data   []docBlock `json:"data"`
		Blocks []docBlock `json:"blocks"`
	} `json:"result"`
}

func (r blockListResponse) blocks() []docBlock {
	if len(r.Blocks) > 0 {
		return r.Blocks
	}
	if len(r.Result.Data) > 0 {
		return r.Result.Data
	}
	if len(r.Result.Blocks) > 0 {
		return r.Result.Blocks
	}
	return r.Data.Blocks
}

type exportTaskResponse struct {
	TaskID interface{} `json:"taskId"`
	Data   struct {
		TaskID interface{} `json:"taskId"`
	} `json:"data"`
	Result struct {
		TaskID interface{} `json:"taskId"`
	} `json:"result"`
}

func (r exportTaskResponse) taskID() string {
	for _, raw := range []interface{}{r.TaskID, r.Data.TaskID, r.Result.TaskID} {
		if s := exportTaskIDString(raw); s != "" {
			return s
		}
	}
	return ""
}

func exportTaskIDString(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return strings.TrimSpace(v.String())
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

type ExportFinishEvent struct {
	EventType    string
	EventID      string
	TaskID       string
	DentryUUID   string
	Name         string
	URL          string
	Format       string
	Extension    string
	Success      bool
	ErrorCode    string
	ErrorMessage string
}

type exportFinishEnvelope struct {
	HTTPEventType   string          `json:"EventType"`
	StreamEventType string          `json:"eventType"`
	EventID         string          `json:"eventId"`
	BizData         json.RawMessage `json:"biz_data"`
	Data            json.RawMessage `json:"data"`
}

type exportFinishStreamData struct {
	BizData json.RawMessage `json:"bizData"`
}

type exportFinishBizData struct {
	EventID      string `json:"eventId"`
	Extension    string `json:"extension"`
	Format       string `json:"format"`
	ErrorCode    string `json:"errorCode"`
	URL          string `json:"url"`
	ErrorMessage string `json:"errorMsg"`
	Success      bool   `json:"success"`
	DentryUUID   string `json:"dentryUuid"`
	Name         string `json:"name"`
	TaskID       string `json:"taskId"`
}

func ParseExportFinishEvent(payload []byte) (*ExportFinishEvent, error) {
	var envelope exportFinishEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode dingtalk export event: %w", err)
	}

	eventType := firstNonEmpty(envelope.HTTPEventType, envelope.StreamEventType)
	if !isExportFinishEventType(eventType) {
		return nil, fmt.Errorf("unsupported dingtalk event type %q", eventType)
	}

	rawBizData, err := exportFinishBizDataRaw(envelope)
	if err != nil {
		return nil, err
	}
	if len(rawBizData) == 0 {
		return nil, fmt.Errorf("dingtalk export event missing biz data")
	}

	var biz exportFinishBizData
	if err := json.Unmarshal(rawBizData, &biz); err != nil {
		return nil, fmt.Errorf("decode dingtalk export biz data: %w", err)
	}
	event := &ExportFinishEvent{
		EventType:    eventType,
		EventID:      firstNonEmpty(biz.EventID, envelope.EventID),
		TaskID:       strings.TrimSpace(biz.TaskID),
		DentryUUID:   strings.TrimSpace(biz.DentryUUID),
		Name:         strings.TrimSpace(biz.Name),
		URL:          strings.TrimSpace(biz.URL),
		Format:       strings.TrimSpace(biz.Format),
		Extension:    strings.TrimSpace(biz.Extension),
		Success:      biz.Success,
		ErrorCode:    strings.TrimSpace(biz.ErrorCode),
		ErrorMessage: strings.TrimSpace(biz.ErrorMessage),
	}
	if event.TaskID == "" {
		return nil, fmt.Errorf("dingtalk export event missing task id")
	}
	if event.DentryUUID == "" {
		return nil, fmt.Errorf("dingtalk export event missing dentry uuid")
	}
	return event, nil
}

func isExportFinishEventType(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "dingdoc_export_finish", "doc_content_export_result":
		return true
	default:
		return false
	}
}

func exportFinishBizDataRaw(envelope exportFinishEnvelope) (json.RawMessage, error) {
	if len(envelope.BizData) > 0 {
		return normalizeExportBizData(envelope.BizData)
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}

	var streamData exportFinishStreamData
	if err := json.Unmarshal(envelope.Data, &streamData); err == nil && len(streamData.BizData) > 0 {
		return normalizeExportBizData(streamData.BizData)
	}
	return normalizeExportBizData(envelope.Data)
}

func normalizeExportBizData(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return trimmed, nil
	}
	var encoded string
	if err := json.Unmarshal(trimmed, &encoded); err != nil {
		return nil, fmt.Errorf("decode dingtalk export biz data string: %w", err)
	}
	return bytes.TrimSpace([]byte(encoded)), nil
}

type downloadInfoResponse struct {
	DownloadInfo downloadInfo `json:"downloadInfo"`
	Data         struct {
		DownloadInfo downloadInfo `json:"downloadInfo"`
	} `json:"data"`
	Result struct {
		DownloadInfo downloadInfo `json:"downloadInfo"`
	} `json:"result"`
}

func (r downloadInfoResponse) info() downloadInfo {
	if strings.TrimSpace(r.DownloadInfo.ResourceURL) != "" ||
		strings.TrimSpace(r.DownloadInfo.InternalResourceURL) != "" {
		return r.DownloadInfo
	}
	if strings.TrimSpace(r.Data.DownloadInfo.ResourceURL) != "" ||
		strings.TrimSpace(r.Data.DownloadInfo.InternalResourceURL) != "" {
		return r.Data.DownloadInfo
	}
	return r.Result.DownloadInfo
}

type dentryIdentityResponse struct {
	SpaceID  string `json:"spaceId"`
	DentryID string `json:"dentryId"`
	Data     struct {
		SpaceID  string `json:"spaceId"`
		DentryID string `json:"dentryId"`
	} `json:"data"`
	Result struct {
		SpaceID  string `json:"spaceId"`
		DentryID string `json:"dentryId"`
	} `json:"result"`
}

func (r dentryIdentityResponse) identity() dentryIdentity {
	if strings.TrimSpace(r.SpaceID) != "" || strings.TrimSpace(r.DentryID) != "" {
		return dentryIdentity{SpaceID: r.SpaceID, DentryID: r.DentryID}
	}
	if strings.TrimSpace(r.Data.SpaceID) != "" || strings.TrimSpace(r.Data.DentryID) != "" {
		return dentryIdentity{SpaceID: r.Data.SpaceID, DentryID: r.Data.DentryID}
	}
	return dentryIdentity{SpaceID: r.Result.SpaceID, DentryID: r.Result.DentryID}
}

type dentryIdentity struct {
	SpaceID  string
	DentryID string
}

type downloadInfo struct {
	ResourceURL         string                 `json:"resourceUrl"`
	InternalResourceURL string                 `json:"internalResourceUrl"`
	Headers             map[string]interface{} `json:"headers"`
}

func (i downloadInfo) downloadURL() string {
	return firstNonEmpty(i.ResourceURL, i.InternalResourceURL)
}

type docBlock struct {
	Type          string                 `json:"type"`
	BlockType     string                 `json:"blockType,omitempty"`
	Text          string                 `json:"text,omitempty"`
	Heading       blockText              `json:"heading,omitempty"`
	Paragraph     blockText              `json:"paragraph,omitempty"`
	Bullet        blockText              `json:"bullet,omitempty"`
	Ordered       blockText              `json:"ordered,omitempty"`
	Blockquote    blockText              `json:"blockquote,omitempty"`
	Callout       blockText              `json:"callout,omitempty"`
	Code          blockText              `json:"code,omitempty"`
	Image         mediaBlock             `json:"image,omitempty"`
	OrderedList   blockText              `json:"orderedList,omitempty"`
	UnorderedList blockText              `json:"unorderedList,omitempty"`
	Table         tableBlock             `json:"table,omitempty"`
	Children      []json.RawMessage      `json:"children,omitempty"`
	Blocks        []docBlock             `json:"blocks,omitempty"`
	Properties    map[string]interface{} `json:"properties,omitempty"`
}

func (b docBlock) kind() string {
	if strings.TrimSpace(b.Type) != "" {
		return strings.TrimSpace(b.Type)
	}
	return strings.TrimSpace(b.BlockType)
}

func (b docBlock) childBlocks() []docBlock {
	if len(b.Children) == 0 {
		return b.Blocks
	}
	blocks := make([]docBlock, 0, len(b.Children))
	for _, raw := range b.Children {
		var child docBlock
		if err := json.Unmarshal(raw, &child); err != nil {
			continue
		}
		if child.isBlockElement() {
			blocks = append(blocks, child)
		}
	}
	return blocks
}

func (b docBlock) inlineChildren() []richTextElement {
	if len(b.Children) == 0 {
		return nil
	}
	elements := make([]richTextElement, 0, len(b.Children))
	for _, raw := range b.Children {
		var child richTextElement
		if err := json.Unmarshal(raw, &child); err != nil {
			continue
		}
		if child.isInlineElement() {
			elements = append(elements, child)
		}
	}
	return elements
}

func (b docBlock) isBlockElement() bool {
	if strings.TrimSpace(b.kind()) != "" {
		return true
	}
	return blockTextContent(b.Paragraph) != "" ||
		blockTextContent(b.Heading) != "" ||
		blockTextContent(b.Bullet) != "" ||
		blockTextContent(b.Ordered) != "" ||
		blockTextContent(b.OrderedList) != "" ||
		blockTextContent(b.UnorderedList) != "" ||
		blockTextContent(b.Blockquote) != "" ||
		blockTextContent(b.Callout) != "" ||
		blockTextContent(b.Code) != "" ||
		imageURL(b.Image) != "" ||
		len(b.Table.Cells) > 0 ||
		len(b.Blocks) > 0
}

type blockText struct {
	Text             string            `json:"text,omitempty"`
	Content          string            `json:"content,omitempty"`
	Level            interface{}       `json:"level,omitempty"`
	Elements         []richTextElement `json:"elements,omitempty"`
	RichTextElements []richTextElement `json:"richTextElements,omitempty"`
}

type richTextElement struct {
	Text        string `json:"text,omitempty"`
	Content     string `json:"content,omitempty"`
	Type        string `json:"type,omitempty"`
	ElementType string `json:"elementType,omitempty"`
	URL         string `json:"url,omitempty"`
	TextRun     struct {
		Content string `json:"content,omitempty"`
	} `json:"textRun,omitempty"`
	Link struct {
		Text string `json:"text,omitempty"`
		URL  string `json:"url,omitempty"`
	} `json:"link,omitempty"`
	Image      mediaBlock             `json:"image,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Children   []richTextElement      `json:"children,omitempty"`
	Bold       bool                   `json:"bold,omitempty"`
	Italic     bool                   `json:"italic,omitempty"`
	Strike     bool                   `json:"strike,omitempty"`
	Stike      bool                   `json:"stike,omitempty"`
	Underline  bool                   `json:"underline,omitempty"`
	Fonts      string                 `json:"fonts,omitempty"`
}

func (el richTextElement) isInlineElement() bool {
	return strings.TrimSpace(el.Text) != "" ||
		strings.TrimSpace(el.Content) != "" ||
		strings.TrimSpace(el.TextRun.Content) != "" ||
		strings.TrimSpace(el.Type) != "" ||
		strings.TrimSpace(el.ElementType) != "" ||
		strings.TrimSpace(el.URL) != "" ||
		strings.TrimSpace(el.Link.URL) != "" ||
		imageURL(el.Image) != "" ||
		len(el.Properties) > 0 ||
		len(el.Children) > 0
}

type mediaBlock struct {
	Alt         string                 `json:"alt,omitempty"`
	URL         string                 `json:"url,omitempty"`
	SourceURL   string                 `json:"sourceUrl,omitempty"`
	ResourceURL string                 `json:"resourceUrl,omitempty"`
	ResourceID  string                 `json:"resourceId,omitempty"`
	Token       string                 `json:"token,omitempty"`
	Properties  map[string]interface{} `json:"properties,omitempty"`
}

type tableBlock struct {
	Cells [][]string `json:"cells,omitempty"`
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
	return fileNameWithExtension(title, ".md")
}

func exportedDocxFileName(title string) string {
	return fileNameWithExtension(title, ".docx")
}

func fileNameWithExtension(title, ext string) string {
	name := sanitizeFileName(title)
	lower := strings.ToLower(name)
	for _, currentExt := range []string{".md", ".markdown", ".adoc", ".asciidoc", ".txt", ".doc", ".docx"} {
		if strings.HasSuffix(lower, currentExt) {
			name = strings.TrimSpace(name[:len(name)-len(currentExt)])
			break
		}
	}
	if name == "" {
		name = "untitled"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return name + ext
}
