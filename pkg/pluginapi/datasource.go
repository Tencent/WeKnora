package pluginapi

import (
	"encoding/json"
	"fmt"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const ProtocolVersionV1 = "v1"

// Extension type constants for the SDK control plane. They mirror the values
// used by the host manifest but stay independent of the main repository.
const (
	ExtensionTypeDataSource = "datasource"
	ExtensionTypeParser     = "parser"
	ExtensionTypeSearch     = "search"
	ExtensionTypeModel      = "model"
	ExtensionTypeRetriever  = "retriever"
)

type Resource struct {
	ExternalID string            `json:"external_id"`
	Name       string            `json:"name"`
	ParentID   string            `json:"parent_id,omitempty"`
	Type       string            `json:"type,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}
type FetchedItem struct {
	ExternalID      string            `json:"external_id"`
	Title           string            `json:"title,omitempty"`
	FileName        string            `json:"file_name,omitempty"`
	Content         []byte            `json:"content,omitempty"`
	URL             string            `json:"url,omitempty"`
	MIMEType        string            `json:"mime_type,omitempty"`
	UpdatedAt       string            `json:"updated_at,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	IsDeleted       bool              `json:"is_deleted,omitempty"`
	ReplacesSubtree bool              `json:"replaces_subtree,omitempty"`
	SubtreeKeep     []string          `json:"subtree_keep,omitempty"`
}

// Metadata keys the host reads on a FetchedItem. They are part of the wire
// contract: build failure placeholders with the helpers below instead of
// assembling the map by hand, so the host's classification cannot be missed.
const (
	// MetadataKeyError marks a per-item failure placeholder. The host counts the
	// item as Failed, records a user-facing error sample, and keeps the previous
	// cursor so the next run retries the same batch instead of silently skipping
	// the document. The item MUST carry neither Content nor URL — when either is
	// set the host ingests it as an ordinary document (see
	// docs/plugin-development-datasource.md §3.6).
	MetadataKeyError = "error"
	// MetadataKeyErrorReasonCode is an optional stable i18n code. When present
	// the host records code + params and the frontend localises the message.
	MetadataKeyErrorReasonCode = "error_reason_code"
	// MetadataKeyErrorReasonCodeValue is the single interpolation param for that
	// code (e.g. an upstream status code).
	MetadataKeyErrorReasonCodeValue = "error_reason_code_value"
	// MetadataKeyErrorReason is the human-readable fallback shown when the
	// frontend does not know the code.
	MetadataKeyErrorReason = "error_reason"
)

// FailedItem builds a per-item failure placeholder. Use it when a single
// resource could not be fetched but the rest of the batch should still sync:
// the host counts this item as failed, keeps the batch going, and retries it on
// the next run. Content and URL are deliberately left empty — that emptiness is
// what tells the host this is a failure rather than a document. Return
// Response.Error only when nothing could be fetched at all (see §3.6).
func FailedItem(externalID, title, message string) FetchedItem {
	return FetchedItem{
		ExternalID: externalID,
		Title:      title,
		Metadata:   map[string]string{MetadataKeyError: message},
	}
}

// FailedItemWithReason is FailedItem plus a stable i18n reason code, so the
// host records a structured, localisable error instead of the raw upstream
// message. Empty reasonCode/reasonValue are omitted rather than stored blank.
func FailedItemWithReason(externalID, title, message, reasonCode, reasonValue string) FetchedItem {
	item := FailedItem(externalID, title, message)
	item.Metadata[MetadataKeyErrorReason] = message
	if reasonCode != "" {
		item.Metadata[MetadataKeyErrorReasonCode] = reasonCode
	}
	if reasonValue != "" {
		item.Metadata[MetadataKeyErrorReasonCodeValue] = reasonValue
	}
	return item
}

type Request struct {
	Config      map[string]any `json:"config,omitempty"`
	ResourceIDs []string       `json:"resource_ids,omitempty"`
	ParentID    string         `json:"parent_id,omitempty"`
	Cursor      map[string]any `json:"cursor,omitempty"`
}
type HandshakeResponse struct {
	ProtocolVersion string   `json:"protocol_version"`
	PluginID        string   `json:"plugin_id"`
	Capabilities    []string `json:"capabilities,omitempty"`
	ExtensionType   string   `json:"extension_type,omitempty"`
	Services        []string `json:"services,omitempty"`
}
type HealthResponse struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}
type Response struct {
	Error     string         `json:"error,omitempty"`
	Resources []Resource     `json:"resources,omitempty"`
	Ancestors []string       `json:"ancestors,omitempty"`
	Items     []FetchedItem  `json:"items,omitempty"`
	Cursor    map[string]any `json:"cursor,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

func structConfig(v map[string]any) (*structpb.Struct, error) {
	if v == nil {
		return nil, nil
	}
	return structpb.NewStruct(v)
}
func mapConfig(v *structpb.Struct) map[string]any {
	if v == nil {
		return nil
	}
	return v.AsMap()
}
func encodeCursor(v map[string]any) (*pluginproto.Cursor, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &pluginproto.Cursor{Data: b, ContentType: "application/json"}, nil
}
func decodeCursor(v *pluginproto.Cursor) (map[string]any, error) {
	if v == nil || len(v.Data) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(v.Data, &out); err != nil {
		return nil, fmt.Errorf("decode cursor: %w", err)
	}
	return out, nil
}
func toProtoResource(v Resource) *pluginproto.Resource {
	return &pluginproto.Resource{ExternalId: v.ExternalID, Name: v.Name, ParentId: v.ParentID, Type: v.Type, Metadata: v.Metadata}
}
func fromProtoResource(v *pluginproto.Resource) Resource {
	if v == nil {
		return Resource{}
	}
	return Resource{ExternalID: v.ExternalId, Name: v.Name, ParentID: v.ParentId, Type: v.Type, Metadata: v.Metadata}
}
func toProtoItem(v FetchedItem) *pluginproto.FetchedItem {
	return &pluginproto.FetchedItem{ExternalId: v.ExternalID, Title: v.Title, FileName: v.FileName, Content: v.Content, Url: v.URL, MimeType: v.MIMEType, UpdatedAt: v.UpdatedAt, Metadata: v.Metadata, IsDeleted: v.IsDeleted, ReplacesSubtree: v.ReplacesSubtree, SubtreeKeep: v.SubtreeKeep}
}
func fromProtoItem(v *pluginproto.FetchedItem) FetchedItem {
	if v == nil {
		return FetchedItem{}
	}
	return FetchedItem{ExternalID: v.ExternalId, Title: v.Title, FileName: v.FileName, Content: v.Content, URL: v.Url, MIMEType: v.MimeType, UpdatedAt: v.UpdatedAt, Metadata: v.Metadata, IsDeleted: v.IsDeleted, ReplacesSubtree: v.ReplacesSubtree, SubtreeKeep: v.SubtreeKeep}
}
func EncodeRequest(v Request) (*pluginproto.DataSourceRequest, error) {
	cfg, err := structConfig(v.Config)
	if err != nil {
		return nil, err
	}
	cur, err := encodeCursor(v.Cursor)
	if err != nil {
		return nil, err
	}
	return &pluginproto.DataSourceRequest{Config: cfg, ResourceIds: v.ResourceIDs, ParentId: v.ParentID, Cursor: cur}, nil
}
func DecodeRequest(v *pluginproto.DataSourceRequest, out *Request) error {
	if v == nil {
		return fmt.Errorf("nil datasource request")
	}
	cur, err := decodeCursor(v.Cursor)
	if err != nil {
		return err
	}
	out.Config = mapConfig(v.Config)
	out.ResourceIDs = v.ResourceIds
	out.ParentID = v.ParentId
	out.Cursor = cur
	return nil
}
func EncodeResponse(v Response) (*pluginproto.DataSourceResponse, error) {
	cur, err := encodeCursor(v.Cursor)
	if err != nil {
		return nil, err
	}
	out := &pluginproto.DataSourceResponse{Error: v.Error, Ancestors: v.Ancestors, Cursor: cur, Warnings: v.Warnings}
	for _, r := range v.Resources {
		out.Resources = append(out.Resources, toProtoResource(r))
	}
	for _, i := range v.Items {
		out.Items = append(out.Items, toProtoItem(i))
	}
	return out, nil
}
func DecodeResponse(v *pluginproto.DataSourceResponse, out *Response) error {
	if v == nil {
		return fmt.Errorf("nil datasource response")
	}
	cur, err := decodeCursor(v.Cursor)
	if err != nil {
		return err
	}
	out.Error = v.Error
	out.Ancestors = v.Ancestors
	out.Cursor = cur
	out.Warnings = v.Warnings
	for _, r := range v.Resources {
		out.Resources = append(out.Resources, fromProtoResource(r))
	}
	for _, i := range v.Items {
		out.Items = append(out.Items, fromProtoItem(i))
	}
	return nil
}
func EncodeHandshake(v HandshakeResponse) *pluginproto.HandshakeResponse {
	return &pluginproto.HandshakeResponse{ProtocolVersion: v.ProtocolVersion, PluginId: v.PluginID, Capabilities: v.Capabilities, ExtensionType: v.ExtensionType, Services: v.Services}
}
func DecodeHandshake(v *pluginproto.HandshakeResponse, out *HandshakeResponse) error {
	if v == nil {
		return fmt.Errorf("nil handshake response")
	}
	out.ProtocolVersion = v.ProtocolVersion
	out.PluginID = v.PluginId
	out.Capabilities = v.Capabilities
	out.ExtensionType = v.ExtensionType
	out.Services = v.Services
	return nil
}
func EncodeHealth(v HealthResponse) *pluginproto.HealthResponse {
	return &pluginproto.HealthResponse{State: v.State, Message: v.Message}
}
func DecodeHealth(v *pluginproto.HealthResponse, out *HealthResponse) error {
	if v == nil {
		return fmt.Errorf("nil health response")
	}
	out.State = v.State
	out.Message = v.Message
	return nil
}

var _ DataSourcePluginServer = (*DataSourceHandler)(nil)
