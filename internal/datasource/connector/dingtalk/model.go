// Package dingtalk synchronizes DingTalk knowledge-base documents.
package dingtalk

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	defaultAPIBaseURL = "https://api.dingtalk.com"
	resourcePrefix    = "dingtalk:v1?"
	maxResourceBytes  = 8 * 1024
	maxResourceDepth  = 64
	cursorVersion     = 1
)

type Config struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	OperatorID   string `json:"operator_id"`
	BaseURL      string `json:"base_url,omitempty"`
}

func (c Config) baseURL() string {
	raw := strings.TrimSpace(c.BaseURL)
	if raw == "" {
		return defaultAPIBaseURL
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	return strings.TrimRight(raw, "/")
}

func parseConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	data, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal dingtalk credentials: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode dingtalk credentials: %w", err)
	}
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.OperatorID = strings.TrimSpace(cfg.OperatorID)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)

	switch {
	case cfg.ClientID == "":
		return nil, fmt.Errorf("%w: client_id is required", datasource.ErrInvalidCredentials)
	case cfg.ClientSecret == "":
		return nil, fmt.Errorf("%w: client_secret is required", datasource.ErrInvalidCredentials)
	case cfg.OperatorID == "":
		return nil, fmt.Errorf("%w: operator_id is required", datasource.ErrInvalidCredentials)
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.baseURL()); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// resourceReference is serialized into the picker resource ID. Ancestors are
// node IDs from the workspace root down to the selected node's direct parent.
type resourceReference struct {
	WorkspaceID string
	NodeID      string
	Ancestors   []string
}

func (r resourceReference) child(nodeID string) resourceReference {
	path := append([]string(nil), r.Ancestors...)
	if r.NodeID != "" {
		path = append(path, r.NodeID)
	}
	return resourceReference{
		WorkspaceID: r.WorkspaceID,
		NodeID:      strings.TrimSpace(nodeID),
		Ancestors:   path,
	}
}

func encodeResourceReference(ref resourceReference) (string, error) {
	if err := validateResourceReference(ref); err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("workspace", ref.WorkspaceID)
	if ref.NodeID != "" {
		query.Set("node", ref.NodeID)
		for _, ancestor := range ref.Ancestors {
			query.Add("ancestor", ancestor)
		}
	}
	encoded := resourcePrefix + query.Encode()
	if len(encoded) > maxResourceBytes {
		return "", fmt.Errorf("dingtalk resource ID exceeds %d bytes", maxResourceBytes)
	}
	return encoded, nil
}

func decodeResourceReference(raw string) (resourceReference, error) {
	if len(raw) == 0 || len(raw) > maxResourceBytes || !strings.HasPrefix(raw, resourcePrefix) {
		return resourceReference{}, fmt.Errorf("invalid dingtalk resource ID")
	}
	values, err := url.ParseQuery(strings.TrimPrefix(raw, resourcePrefix))
	if err != nil {
		return resourceReference{}, fmt.Errorf("parse dingtalk resource ID: %w", err)
	}
	ref := resourceReference{
		WorkspaceID: values.Get("workspace"),
		NodeID:      values.Get("node"),
		Ancestors:   append([]string(nil), values["ancestor"]...),
	}
	if err := validateResourceReference(ref); err != nil {
		return resourceReference{}, err
	}
	return ref, nil
}

func validateResourceReference(ref resourceReference) error {
	if ref.WorkspaceID == "" || ref.WorkspaceID != strings.TrimSpace(ref.WorkspaceID) {
		return fmt.Errorf("dingtalk resource has invalid workspace ID")
	}
	if ref.NodeID != strings.TrimSpace(ref.NodeID) {
		return fmt.Errorf("dingtalk resource has invalid node ID")
	}
	if ref.NodeID == "" && len(ref.Ancestors) > 0 {
		return fmt.Errorf("workspace resource cannot have node ancestors")
	}
	if len(ref.Ancestors) > maxResourceDepth {
		return fmt.Errorf("dingtalk resource depth exceeds %d", maxResourceDepth)
	}
	seen := make(map[string]struct{}, len(ref.Ancestors)+1)
	for _, id := range ref.Ancestors {
		if id == "" || id != strings.TrimSpace(id) {
			return fmt.Errorf("dingtalk resource has invalid ancestor")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("dingtalk resource contains an ancestor cycle")
		}
		seen[id] = struct{}{}
	}
	if ref.NodeID != "" {
		if _, exists := seen[ref.NodeID]; exists {
			return fmt.Errorf("dingtalk resource contains a node cycle")
		}
	}
	return nil
}

func resourceAncestorIDs(ref resourceReference) ([]string, error) {
	root, err := encodeResourceReference(resourceReference{WorkspaceID: ref.WorkspaceID})
	if err != nil {
		return nil, err
	}
	out := []string{root}
	for i, nodeID := range ref.Ancestors {
		id, err := encodeResourceReference(resourceReference{
			WorkspaceID: ref.WorkspaceID,
			NodeID:      nodeID,
			Ancestors:   append([]string(nil), ref.Ancestors[:i]...),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

type accessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"`
}

type workspaceListResponse struct {
	Workspaces []workspace `json:"workspaces"`
	NextToken  string      `json:"nextToken"`
}

type workspaceResponse struct {
	Workspace workspace `json:"workspace"`
}

type workspace struct {
	WorkspaceID  string `json:"workspaceId"`
	RootNodeID   string `json:"rootNodeId"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Description  string `json:"description"`
	URL          string `json:"url"`
	ModifiedTime string `json:"modifiedTime"`
}

type nodeListResponse struct {
	Nodes     []node `json:"nodes"`
	NextToken string `json:"nextToken"`
}

type nodeResponse struct {
	Node node `json:"node"`
}

type node struct {
	NodeID          string `json:"nodeId"`
	WorkspaceID     string `json:"workspaceId"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Category        string `json:"category"`
	Extension       string `json:"extension"`
	URL             string `json:"url"`
	CreatorID       string `json:"creatorId"`
	ModifierID      string `json:"modifierId"`
	ModifiedTime    string `json:"modifiedTime"`
	HasChildren     bool   `json:"hasChildren"`
	PermissionRole  string `json:"permissionRole"`
	ParentNodeID    string `json:"-"`
	ModifiedUnixMS  int64  `json:"modifiedTimestamp"`
	CreateTimestamp int64  `json:"createTimestamp"`
}

func (n node) isFolder() bool {
	return strings.EqualFold(n.Type, "FOLDER")
}

func (n node) isOnlineDocument() bool {
	return strings.EqualFold(n.Type, "FILE") &&
		strings.EqualFold(n.Category, "ALIDOC") &&
		strings.EqualFold(n.Extension, "adoc")
}

func (n node) displayName() string {
	if name := strings.TrimSpace(n.Name); name != "" {
		return name
	}
	return n.NodeID
}

type documentBlocksResponse struct {
	Success bool `json:"success"`
	Result  struct {
		Data []json.RawMessage `json:"data"`
	} `json:"result"`
}

type apiErrorPayload struct {
	Code      json.RawMessage `json:"code"`
	ErrCode   json.RawMessage `json:"errcode"`
	Message   string          `json:"message"`
	ErrMsg    string          `json:"errmsg"`
	RequestID string          `json:"requestId"`
}

type connectorCursor struct {
	Version   int                       `json:"version"`
	SyncedAt  time.Time                 `json:"synced_at"`
	Resources map[string]resourceCursor `json:"resources"`
}

type resourceCursor struct {
	Documents map[string]string `json:"documents"`
}

func parseTimestamp(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n > 100_000_000_000 {
			return time.UnixMilli(n)
		}
		if n > 1_000_000_000 {
			return time.Unix(n, 0)
		}
	}
	return time.Time{}
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_",
	).Replace(name)
	name = strings.Trim(name, " ._")
	if name == "" {
		return "untitled"
	}
	const maxBytes = 200
	if len(name) > maxBytes {
		name = name[:maxBytes]
		for len(name) > 0 {
			r, size := utf8.DecodeLastRuneInString(name)
			if r != utf8.RuneError || size != 1 {
				break
			}
			name = name[:len(name)-1]
		}
		name = strings.Trim(name, " ._")
	}
	if name == "" {
		return "untitled"
	}
	return name
}
