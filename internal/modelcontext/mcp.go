package modelcontext

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// normalizeMCPCallArguments tolerates one extra JSON encoding of the bridge's
// arguments envelope. Run after preserving ModelArguments and before decoding
// resource handles so audit/UI/execution all use the same canonical object.
// This is not JSON repair: incomplete JSON, null, arrays and further string
// layers remain invalid, and remote business fields are never coerced here.
func normalizeMCPCallArguments(calls []types.LLMToolCall) {
	for i := range calls {
		if calls[i].Function.Name != "call_mcp_tool" {
			continue
		}
		var envelope map[string]json.RawMessage
		if json.Unmarshal([]byte(calls[i].Function.Arguments), &envelope) != nil {
			continue
		}
		var wrapped string
		if json.Unmarshal(envelope["arguments"], &wrapped) != nil {
			continue
		}
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(wrapped), &object) != nil || object == nil {
			continue
		}
		// Retain raw number literals and nested values instead of round-tripping
		// through float64 or recursively decoding business strings.
		envelope["arguments"] = json.RawMessage(wrapped)
		encoded, err := json.Marshal(envelope)
		if err == nil {
			calls[i].Function.Arguments = string(encoded)
		}
	}
}

// MCP routing identities belong to our bridge, not to the remote tool schema.
// Rewrite only explicit envelope fields; never walk an external input_schema,
// call_mcp_tool.arguments, or a dynamic MCP tool's arguments/results.
func (r *Registry) mcpArgumentTable(toolName string) (string, *HandleTable) {
	switch toolHandlePolicies[toolName].mcpRoutingKey {
	case "server_id":
		return "server_id", r.mcpServers
	case "tool_ref":
		return "tool_ref", r.mcpTools
	default:
		return "", nil
	}
}

func rewriteMCPField(raw, key string, rewrite func(string) string) string {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) != nil || object == nil {
		return raw
	}
	var value string
	if json.Unmarshal(object[key], &value) != nil {
		return raw
	}
	rewritten := rewrite(value)
	if rewritten == value {
		return raw
	}
	object[key], _ = json.Marshal(rewritten)
	result, _ := json.Marshal(object)
	return string(result)
}

func mcpHandleShape(value string, table *HandleTable) bool {
	return table != nil && strings.HasPrefix(value, table.table.prefix) &&
		allDigits(strings.TrimPrefix(value, table.table.prefix))
}

func registerMCPIdentity(table *HandleTable, value string) string {
	// Replayed in-turn results are already encoded. Never allocate aliases for
	// aliases, including unknown ones from compacted history.
	if value == "" || mcpHandleShape(value, table) {
		return value
	}
	return table.Register(value)
}

func (r *Registry) encodeMCPArguments(toolName, raw string) string {
	key, table := r.mcpArgumentTable(toolName)
	if table == nil {
		return raw
	}
	return rewriteMCPField(raw, key, func(value string) string {
		if handle, ok := table.Handle(value); ok {
			return handle
		}
		return value
	})
}

func (r *Registry) decodeMCPArguments(toolName, raw string) (string, []string) {
	key, table := r.mcpArgumentTable(toolName)
	if table == nil {
		return raw, nil
	}
	var unresolved []string
	decoded := rewriteMCPField(raw, key, func(value string) string {
		if !mcpHandleShape(value, table) {
			return value
		}
		if durable, ok := table.Resolve(value); ok {
			return durable
		}
		unresolved = append(unresolved, value)
		return value
	})
	return decoded, unresolved
}

func (r *Registry) encodeMCPDirectory(output string) string {
	encodeRow := func(raw string) string {
		raw = rewriteMCPField(raw, "server_id", func(value string) string {
			return registerMCPIdentity(r.mcpServers, value)
		})
		return rewriteMCPField(raw, "tool_ref", func(value string) string {
			return registerMCPIdentity(r.mcpTools, value)
		})
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(output), &object) != nil || object == nil {
		// Validation failures contain our known durable identifiers in prose.
		return r.mcpTools.EncodeKnownText(r.mcpServers.EncodeKnownText(output))
	}
	for _, key := range []string{"servers", "tools"} {
		var rows []json.RawMessage
		if json.Unmarshal(object[key], &rows) != nil {
			continue
		}
		for i := range rows {
			rows[i] = json.RawMessage(encodeRow(string(rows[i])))
		}
		object[key], _ = json.Marshal(rows)
	}
	encoded, _ := json.Marshal(object)
	return encodeRow(string(encoded))
}

// EncodeTools returns a model-facing copy. The registry/executor retains the
// original UUID enum and validates only decoded durable identities.
func (r *Registry) EncodeTools(tools []chat.Tool) []chat.Tool {
	if r == nil {
		return tools
	}
	encoded := append([]chat.Tool(nil), tools...)
	// Register routing enums first, independently of tool description order.
	for i := range encoded {
		def := &encoded[i].Function
		key, table := r.mcpArgumentTable(def.Name)
		if table == nil {
			continue
		}
		var schema map[string]json.RawMessage
		if json.Unmarshal(def.Parameters, &schema) != nil {
			continue
		}
		var properties map[string]json.RawMessage
		if json.Unmarshal(schema["properties"], &properties) != nil {
			continue
		}
		var field map[string]json.RawMessage
		if json.Unmarshal(properties[key], &field) != nil {
			continue
		}
		var ids []string
		if json.Unmarshal(field["enum"], &ids) != nil {
			continue
		}
		for j := range ids {
			ids[j] = registerMCPIdentity(table, ids[j])
		}
		field["enum"], _ = json.Marshal(ids)
		properties[key], _ = json.Marshal(field)
		schema["properties"], _ = json.Marshal(properties)
		def.Parameters, _ = json.Marshal(schema)
	}
	for i := range encoded {
		def := &encoded[i].Function
		if def.Name == "discover_mcp_tools" {
			lines := strings.Split(def.Description, "\n")
			for j := range lines {
				lines[j] = rewriteMCPField(lines[j], "server_id", func(value string) string {
					return registerMCPIdentity(r.mcpServers, value)
				})
			}
			def.Description = strings.Join(lines, "\n")
		} else if strings.HasPrefix(def.Name, "mcp_") && strings.HasPrefix(def.Description, "[MCP service ") {
			prefix, body, ok := strings.Cut(def.Description, " (external)] ")
			if !ok {
				continue
			}
			def.Description = r.encodeMCPRoutingText(prefix) + " (external)] " + body
		}
	}
	return encoded
}

// Only system-owned routing syntax is compacted in runtime-context text. Do
// not replace arbitrary matching UUIDs in user prose or external tool payloads.
func (r *Registry) encodeMCPRoutingText(text string) string {
	for _, pair := range r.mcpServers.table.pairs() {
		text = strings.ReplaceAll(text, "server_id="+strconv.Quote(pair.value), "server_id="+strconv.Quote(pair.handle))
	}
	return text
}
