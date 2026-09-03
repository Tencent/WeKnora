package types

import (
	"fmt"
	"strings"
)

const (
	// MCPRequestMetaNamespace is the extension key placed inside MCP's _meta object.
	// A namespaced key prevents collisions with protocol-defined metadata.
	MCPRequestMetaNamespace = "io.weknora/request"

	// MCPRequestMetaMaxSelectors bounds persisted agent configuration and the
	// amount of caller-controlled data considered for one MCP request.
	MCPRequestMetaMaxSelectors  = 16
	MCPRequestMetaMaxValueBytes = 8 * 1024
	MCPRequestMetaMaxTotalBytes = 32 * 1024
)

// MCPRequestMetaConfig is an explicit allowlist for copying selected values
// from an Agent HTTP request into MCP tool-call metadata. Only names are stored
// on the agent; request values remain scoped to a single Agent run.
type MCPRequestMetaConfig struct {
	Headers    []string `yaml:"headers,omitempty" json:"headers,omitempty"`
	BodyFields []string `yaml:"body_fields,omitempty" json:"body_fields,omitempty"`
}

// Validate rejects configurations that could create ambiguous or unbounded
// forwarding rules. Runtime extraction applies the same limits defensively for
// legacy rows and built-in YAML configurations.
func (c *MCPRequestMetaConfig) Validate() error {
	if c == nil {
		return nil
	}
	if len(c.Headers) > MCPRequestMetaMaxSelectors {
		return fmt.Errorf("mcp request metadata supports at most %d header names", MCPRequestMetaMaxSelectors)
	}
	if len(c.BodyFields) > MCPRequestMetaMaxSelectors {
		return fmt.Errorf("mcp request metadata supports at most %d body fields", MCPRequestMetaMaxSelectors)
	}

	for _, name := range c.Headers {
		if !validHTTPHeaderName(strings.TrimSpace(name)) {
			return fmt.Errorf("invalid MCP request metadata header name %q", name)
		}
	}
	for _, field := range c.BodyFields {
		field = strings.TrimSpace(field)
		if !validMCPRequestBodyField(field) {
			return fmt.Errorf("unsupported MCP request metadata body field %q", field)
		}
	}
	return nil
}

// MCPRequestMeta contains the values captured for one Agent request. It is a
// runtime-only value and must never be persisted or logged.
type MCPRequestMeta struct {
	Headers map[string]string `json:"headers,omitempty"`
	Body    map[string]any    `json:"body,omitempty"`
}

func (m *MCPRequestMeta) Empty() bool {
	return m == nil || (len(m.Headers) == 0 && len(m.Body) == 0)
}

func validHTTPHeaderName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", r) {
			continue
		}
		return false
	}
	return true
}

// IsAllowedMCPRequestHeader reports whether name is a syntactically valid HTTP
// field name accepted by the MCP request-metadata allowlist.
func IsAllowedMCPRequestHeader(name string) bool {
	return validHTTPHeaderName(strings.TrimSpace(name))
}

var mcpRequestBodyScalarFields = map[string]struct{}{
	"query":                  {},
	"agent_enabled":          {},
	"agent_id":               {},
	"agent_source_tenant_id": {},
	"web_search_enabled":     {},
	"summary_model_id":       {},
	"disable_title":          {},
	"channel":                {},
}

var mcpRequestBodyListFields = map[string]struct{}{
	"knowledge_base_ids": {},
	"knowledge_ids":      {},
	"mcp_service_ids":    {},
	"skill_names":        {},
	"tag_ids":            {},
}

// IsAllowedMCPRequestBodyField reports whether a selector names a bounded,
// non-binary field in CreateKnowledgeQARequest. Custom API metadata lives under
// mcp_metadata.<key>; images and attachments are deliberately not selectable.
func IsAllowedMCPRequestBodyField(field string) bool {
	return validMCPRequestBodyField(strings.TrimSpace(field))
}

// ValidateMCPRequestMetadata validates the caller-supplied scalar metadata
// object before it is considered for forwarding to an MCP server.
func ValidateMCPRequestMetadata(metadata map[string]string) error {
	if len(metadata) > MCPRequestMetaMaxSelectors {
		return fmt.Errorf("mcp request metadata supports at most %d values", MCPRequestMetaMaxSelectors)
	}

	totalBytes := 0
	for key, value := range metadata {
		if !validMCPRequestMetadataKey(key) {
			return fmt.Errorf("invalid MCP request metadata key %q", key)
		}
		if len(value) > MCPRequestMetaMaxValueBytes {
			return fmt.Errorf("MCP request metadata value for %q exceeds %d bytes", key, MCPRequestMetaMaxValueBytes)
		}
		entryBytes := len(key) + len(value)
		if totalBytes+entryBytes > MCPRequestMetaMaxTotalBytes {
			return fmt.Errorf("MCP request metadata exceeds %d bytes", MCPRequestMetaMaxTotalBytes)
		}
		totalBytes += entryBytes
	}
	return nil
}

func validMCPRequestBodyField(field string) bool {
	if _, ok := mcpRequestBodyScalarFields[field]; ok {
		return true
	}
	if _, ok := mcpRequestBodyListFields[field]; ok {
		return true
	}
	const prefix = "mcp_metadata."
	if !strings.HasPrefix(field, prefix) {
		return false
	}
	key := strings.TrimPrefix(field, prefix)
	return validMCPRequestMetadataKey(key)
}

func validMCPRequestMetadataKey(key string) bool {
	if key == "" || len(key) > 128 {
		return false
	}
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}
