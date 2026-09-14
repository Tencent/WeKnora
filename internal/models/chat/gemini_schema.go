package chat

import "strings"

// normalizeGeminiToolSchemas adapts the JSON Schema emitted by MCP tools to
// the subset accepted by Google's OpenAPI Schema. In particular, a JSON
// Schema union such as {"type":["string","null"]} cannot be decoded by
// Gemini's proto field, which expects one scalar type string.
func normalizeGeminiToolSchemas(body map[string]any) {
	tools, ok := body["tools"].([]any)
	if !ok {
		return
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		if parameters, ok := function["parameters"].(map[string]any); ok {
			function["parameters"] = normalizeGeminiSchema(parameters)
		}
	}
}

func normalizeGeminiSchema(value any) any {
	switch schema := value.(type) {
	case map[string]any:
		if rawType, ok := schema["type"].([]any); ok {
			for _, candidate := range rawType {
				typeName, ok := candidate.(string)
				if !ok || strings.EqualFold(typeName, "null") {
					continue
				}
				schema["type"] = typeName
				break
			}
			if _, ok := schema["type"].([]any); ok {
				// A null-only union has no useful Gemini type. Omitting the
				// field lets the gateway infer it from the remaining schema.
				delete(schema, "type")
			}
		}
		for key, child := range schema {
			schema[key] = normalizeGeminiSchema(child)
		}
		return schema
	case []any:
		for i, child := range schema {
			schema[i] = normalizeGeminiSchema(child)
		}
	}
	return value
}
