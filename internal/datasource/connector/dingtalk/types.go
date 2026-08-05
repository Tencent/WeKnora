package dingtalk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// config holds validated connector configuration after parsing from DataSourceConfig.
type config struct {
	AppKey       string
	AppSecret    string
	BaseURL      string
	OperatorID   string
	TenantID     string
	DataSourceID string
}

const officialAPIBaseURL = "https://api.dingtalk.com"

// parseConfig extracts and validates DingTalk credentials from the raw config.
func parseConfig(raw *types.DataSourceConfig) (*config, error) {
	if raw == nil {
		return nil, fmt.Errorf("%w: DingTalk configuration is required", datasource.ErrInvalidConfig)
	}
	creds := raw.Credentials
	appKey, _ := creds["app_key"].(string)
	appSecret, _ := creds["app_secret"].(string)
	operatorID, _ := creds["operator_id"].(string)

	if appKey == "" {
		return nil, fmt.Errorf("app_key is required: %w", datasource.ErrInvalidCredentials)
	}
	if appSecret == "" {
		return nil, fmt.Errorf("app_secret is required: %w", datasource.ErrInvalidCredentials)
	}
	if operatorID == "" {
		return nil, fmt.Errorf("operator_id is required: %w", datasource.ErrInvalidCredentials)
	}
	if value, exists := creds["base_url"]; exists {
		baseURL, isString := value.(string)
		if !isString || strings.TrimSpace(baseURL) != "" {
			return nil, fmt.Errorf(
				"%w: DingTalk API endpoint overrides are not supported",
				datasource.ErrInvalidConfig,
			)
		}
	}

	settings := raw.Settings
	tenantID, _ := settings["tenant_id"].(string)
	dataSourceID, _ := settings["data_source_id"].(string)

	return &config{
		AppKey:       appKey,
		AppSecret:    appSecret,
		BaseURL:      officialAPIBaseURL,
		OperatorID:   operatorID,
		TenantID:     tenantID,
		DataSourceID: dataSourceID,
	}, nil
}

// tokenCacheKey returns a per-tenant, per-datasource cache key that isolates tokens
// across different tenants even when they share the same DingTalk app credentials.
// D3: this prevents cross-tenant token sharing. The app_secret and token issuer
// fixed issuer are included in a SHA-256 fingerprint so rotating any identity
// component invalidates the cached token without tenant IDs, app keys, or
// private material entering the process-global map key (and therefore a
// diagnostic dump). Tests inject a private client explicitly rather than
// exposing an endpoint setting to tenants.
func (c *config) tokenCacheKey() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		c.TenantID,
		c.DataSourceID,
		c.AppKey,
		c.AppSecret,
		c.BaseURL,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// cursorIdentityFingerprint binds incremental revisions to the authenticated
// external identity without storing any credential material. It protects a
// credential switch from a late-finishing old sync that writes its stale
// cursor after the service reset.
func (c *config) cursorIdentityFingerprint() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		c.TenantID,
		c.DataSourceID,
		c.AppKey,
		c.AppSecret,
		c.OperatorID,
		c.BaseURL,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// classification identifies how a DingTalk wiki node should be processed.
type classification int

const (
	classUnsupported classification = iota // binary, spreadsheet, or unknown leaf
	classContainer                         // folder or workspace root
	classDocument                          // online document (adoc)
)

// isSupportedDocument reports whether this node has a readable DingTalk online
// document body. A document may also have children, so this predicate must stay
// independent from canDescend.
func (n *node) isSupportedDocument() bool {
	typ := strings.ToLower(n.Type)
	if typ == "file" {
		// The current API identifies online documents as FILE/ALIDOC/adoc and
		// accepts nodeId as the blocks API document key. Keep the legacy key
		// fields as compatibility hints for older responses.
		if strings.EqualFold(n.Category, "ALIDOC") ||
			strings.EqualFold(n.Extension, "adoc") ||
			n.DocKey != "" ||
			n.DocumentID != "" {
			return true
		}
	}
	return false
}

// canDescend reports whether the node can contain child nodes. DingTalk allows
// ALIDOC nodes to have children, so this can be true at the same time as
// isSupportedDocument.
func (n *node) canDescend() bool {
	return strings.EqualFold(n.Type, "folder") || n.HasChildren
}

// classify returns the node's primary picker type. D2: unknown leaf types are
// unsupported, never treated as empty documents or inferred deletions.
func (n *node) classify() classification {
	if n.isSupportedDocument() {
		return classDocument
	}
	if n.canDescend() {
		return classContainer
	}
	return classUnsupported
}

// documentKey returns the stable document identifier for content fetching.
// Prefers DocKey when present; falls back to NodeID for older API responses.
func (n *node) documentKey() string {
	if n.DocKey != "" {
		return n.DocKey
	}
	if n.DocumentID != "" {
		return n.DocumentID
	}
	return n.NodeID
}

// redactIdentifier returns a sanitized version of an ID suitable for logging.
// Keeps a short prefix for correlation; masks the rest. D4: never logs secrets,
// tokens, or the full operator UnionID.
func redactIdentifier(id string) string {
	if id == "" {
		return ""
	}
	if len(id) <= 4 {
		return "***"
	}
	return id[:4] + "***"
}

// workspace represents a DingTalk wiki workspace (知识库).
type workspace struct {
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	RootNodeID  string `json:"rootNodeId"`
	URL         string `json:"url"`
}

// workspacesPage is the response envelope for GET /v2.0/wiki/workspaces.
type workspacesPage struct {
	Workspaces []workspace `json:"workspaces"`
	NextToken  string      `json:"nextToken"`
}

// node represents a DingTalk wiki node (folder or document).
type node struct {
	NodeID            string `json:"nodeId"`
	WorkspaceID       string `json:"workspaceId"`
	Name              string `json:"name"`
	Type              string `json:"type"` // "FOLDER" or "FILE"; online docs are FILE/ALIDOC/adoc
	Category          string `json:"category"`
	Extension         string `json:"extension"`
	URL               string `json:"url"`
	HasChildren       bool   `json:"hasChildren"`
	CreateTime        string `json:"createTime"`        // RFC3339 timestamp
	ModifiedTime      string `json:"modifiedTime"`      // RFC3339 timestamp
	CreateTimestamp   int64  `json:"createTimestamp"`   // Unix seconds or milliseconds
	ModifiedTimestamp int64  `json:"modifiedTimestamp"` // Unix seconds or milliseconds
	DocumentID        string `json:"documentId"`
	DocKey            string `json:"docKey"`
}

// revision returns a stable change-detection token. The official API exposes
// both numeric timestamp and formatted-time fields; numeric values preserve the
// most precise change token and therefore take precedence.
func (n *node) revision() string {
	if n.ModifiedTimestamp > 0 {
		return strconv.FormatInt(n.ModifiedTimestamp, 10)
	}
	if n.ModifiedTime != "" {
		return n.ModifiedTime
	}
	if n.CreateTimestamp > 0 {
		return strconv.FormatInt(n.CreateTimestamp, 10)
	}
	return n.CreateTime
}

// metadataRevision detects rename/source-URL/document-key changes independently
// of DingTalk's content timestamps. Some tenants may receive metadata-only node
// updates without a modifiedTimestamp change; keeping this digest beside the
// content revision makes those updates observable without storing raw metadata
// in the cursor.
func (n *node) metadataRevision() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		n.Name,
		safeSourceURL(n.URL),
		n.WorkspaceID,
		n.DocumentID,
		n.DocKey,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// lastModified normalizes official numeric timestamps (seconds or milliseconds)
// and the formatted-time compatibility fields.
func (n *node) lastModified() time.Time {
	if n.ModifiedTimestamp > 0 {
		return timeFromUnixAuto(n.ModifiedTimestamp)
	}
	ts := n.ModifiedTime
	if ts == "" {
		if n.CreateTimestamp > 0 {
			return timeFromUnixAuto(n.CreateTimestamp)
		}
		ts = n.CreateTime
	}
	if ts == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
		return parsed
	}
	msec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return timeFromUnixAuto(msec)
}

func timeFromUnixAuto(value int64) time.Time {
	switch {
	case value > 1_000_000_000_000:
		return time.UnixMilli(value)
	case value > 0:
		return time.Unix(value, 0)
	default:
		return time.Time{}
	}
}

// nodesPage is the response envelope for GET /v2.0/wiki/nodes.
type nodesPage struct {
	Nodes     []node `json:"nodes"`
	NextToken string `json:"nextToken"`
}

// nodeDetail is the response for GET /v2.0/wiki/nodes/{nodeId}.
type nodeDetail struct {
	Node node `json:"node"`
}

// block represents a content block in a DingTalk online document.
type block struct {
	BlockType     string                 `json:"blockType"`
	InlineType    string                 `json:"inlineType"`
	ElementType   string                 `json:"elementType"`
	Text          string                 `json:"text"`
	Bold          bool                   `json:"bold"`
	Italic        bool                   `json:"italic"`
	Code          bool                   `json:"code"`
	Stike         bool                   `json:"stike"`
	Strikethrough bool                   `json:"strikethrough"`
	Paragraph     map[string]interface{} `json:"paragraph,omitempty"`
	Heading       map[string]interface{} `json:"heading,omitempty"`
	Blockquote    map[string]interface{} `json:"blockquote,omitempty"`
	Callout       map[string]interface{} `json:"callout,omitempty"`
	Columns       map[string]interface{} `json:"columns,omitempty"`
	OrderedList   map[string]interface{} `json:"orderedList,omitempty"`
	UnorderedList map[string]interface{} `json:"unorderedList,omitempty"`
	Table         map[string]interface{} `json:"table,omitempty"`
	Properties    map[string]interface{} `json:"properties,omitempty"`
	Children      []block                `json:"children,omitempty"`

	// Value preserves compatibility with early/fixture response shapes while
	// official responses use the type-specific objects above.
	Value map[string]interface{} `json:"value,omitempty"`
}

// blocksResponse is the response envelope for
// GET /v1.0/doc/suites/documents/{docKey}/blocks.
type blocksResponse struct {
	Success bool `json:"success"`
	Result  struct {
		Data []block `json:"data"`
	} `json:"result"`
}

// tokenResponse is the response envelope for POST /v1.0/oauth2/accessToken.
type tokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"` // seconds until expiry
}

// apiError wraps a DingTalk API error response.
type apiError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	RequestID  string `json:"requestId"`
	StatusCode int    `json:"-"`
}

func (e *apiError) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("dingtalk: %s: %s (request=%s)", e.Code, e.Message, e.RequestID)
	}
	return fmt.Sprintf("dingtalk: %s: %s", e.Code, e.Message)
}
