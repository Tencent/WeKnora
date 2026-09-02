package session

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

func buildMCPRequestMeta(
	headers http.Header,
	request *CreateKnowledgeQARequest,
	config *types.MCPRequestMetaConfig,
	sharedAgentReadOnly bool,
) *types.MCPRequestMeta {
	// A shared Agent can point at MCP servers controlled by another workspace.
	// Forwarding the caller's credentials there would turn the share into a
	// credential-exfiltration primitive, even when the selector was explicit.
	if request == nil || config == nil || sharedAgentReadOnly {
		return nil
	}

	meta := &types.MCPRequestMeta{}
	totalBytes := 0
	seenHeaders := make(map[string]struct{}, len(config.Headers))
	for _, configuredName := range config.Headers {
		if len(seenHeaders) >= types.MCPRequestMetaMaxSelectors {
			break
		}
		configuredName = strings.TrimSpace(configuredName)
		if !types.IsAllowedMCPRequestHeader(configuredName) {
			continue
		}
		name := http.CanonicalHeaderKey(configuredName)
		lowerName := strings.ToLower(name)
		if _, seen := seenHeaders[lowerName]; seen {
			continue
		}
		seenHeaders[lowerName] = struct{}{}

		values := headers.Values(configuredName)
		if len(values) == 0 {
			continue
		}
		value := strings.Join(values, ", ")
		entryBytes := len(name) + len(value)
		if len(value) > types.MCPRequestMetaMaxValueBytes ||
			totalBytes+entryBytes > types.MCPRequestMetaMaxTotalBytes {
			continue
		}
		if meta.Headers == nil {
			meta.Headers = make(map[string]string)
		}
		meta.Headers[name] = value
		totalBytes += entryBytes
	}

	seenBodyFields := make(map[string]struct{}, len(config.BodyFields))
	for _, configuredField := range config.BodyFields {
		if len(seenBodyFields) >= types.MCPRequestMetaMaxSelectors {
			break
		}
		field := strings.TrimSpace(configuredField)
		if !types.IsAllowedMCPRequestBodyField(field) {
			continue
		}
		if _, seen := seenBodyFields[field]; seen {
			continue
		}
		seenBodyFields[field] = struct{}{}

		value, present := mcpRequestBodyValue(request, field)
		if !present {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		entryBytes := len(field) + len(encoded)
		if len(encoded) > types.MCPRequestMetaMaxValueBytes ||
			totalBytes+entryBytes > types.MCPRequestMetaMaxTotalBytes {
			continue
		}
		if meta.Body == nil {
			meta.Body = make(map[string]any)
		}
		if strings.HasPrefix(field, "mcp_metadata.") {
			key := strings.TrimPrefix(field, "mcp_metadata.")
			nested, _ := meta.Body["mcp_metadata"].(map[string]string)
			if nested == nil {
				nested = make(map[string]string)
				meta.Body["mcp_metadata"] = nested
			}
			nested[key] = value.(string)
		} else {
			meta.Body[field] = value
		}
		totalBytes += entryBytes
	}

	if meta.Empty() {
		return nil
	}
	return meta
}

func mcpRequestBodyValue(request *CreateKnowledgeQARequest, field string) (any, bool) {
	switch field {
	case "query":
		return request.Query, true
	case "agent_enabled":
		return request.AgentEnabled, true
	case "agent_id":
		return request.AgentID, true
	case "agent_source_tenant_id":
		return request.AgentSourceTenantID, true
	case "web_search_enabled":
		return request.WebSearchEnabled, true
	case "summary_model_id":
		return request.SummaryModelID, true
	case "disable_title":
		return request.DisableTitle, true
	case "channel":
		return request.Channel, true
	case "knowledge_base_ids":
		return append([]string{}, request.KnowledgeBaseIDs...), true
	case "knowledge_ids":
		return append([]string{}, request.KnowledgeIds...), true
	case "mcp_service_ids":
		return append([]string{}, request.MCPServiceIDs...), true
	case "skill_names":
		return append([]string{}, request.SkillNames...), true
	case "tag_ids":
		return append([]string{}, request.TagIDs...), true
	}

	const prefix = "mcp_metadata."
	if strings.HasPrefix(field, prefix) {
		value, ok := request.MCPMetadata[strings.TrimPrefix(field, prefix)]
		return value, ok
	}
	return nil, false
}
