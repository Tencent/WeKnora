package tools

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// CastParams performs schema-driven type casting on tool arguments.
// LLMs sometimes return incorrect types (e.g., "true" instead of true, "123" instead of 123).
// This function attempts safe conversions based on the JSON Schema definition of the tool's parameters.
//
// If the schema is nil or cannot be parsed, the original args are returned unchanged.
func CastParams(args json.RawMessage, schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 || len(args) == 0 {
		return args
	}

	var schemaDef map[string]interface{}
	if err := json.Unmarshal(schema, &schemaDef); err != nil {
		return args
	}

	properties, ok := schemaDef["properties"].(map[string]interface{})
	if !ok || len(properties) == 0 {
		return args
	}

	var argsMap map[string]interface{}
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return args
	}

	changed := false
	for key, val := range argsMap {
		propDef, exists := properties[key]
		if !exists {
			continue
		}
		prop, ok := propDef.(map[string]interface{})
		if !ok {
			continue
		}
		targetType, _ := prop["type"].(string)
		if targetType == "" {
			continue
		}

		newVal, didCast := castValue(val, targetType)
		if didCast {
			argsMap[key] = newVal
			changed = true
		}
	}

	if !changed {
		return args
	}

	result, err := json.Marshal(argsMap)
	if err != nil {
		return args
	}
	return result
}

// NormalizeToolParams applies narrowly-scoped repairs for model argument
// shapes observed in production. Dynamic tools remain opaque even when their
// schemas happen to use the same field names.
func NormalizeToolParams(name string, args json.RawMessage, schema json.RawMessage) json.RawMessage {
	if name != ToolWikiWritePage || len(schema) == 0 || len(args) == 0 {
		return args
	}
	var schemaDef map[string]interface{}
	if err := json.Unmarshal(schema, &schemaDef); err != nil {
		return args
	}
	properties, ok := schemaDef["properties"].(map[string]interface{})
	if !ok {
		return args
	}
	var argsMap map[string]interface{}
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return args
	}
	if !normalizeWikiWritePageArgs(argsMap, properties) {
		return args
	}
	result, err := json.Marshal(argsMap)
	if err != nil {
		return args
	}
	return result
}

// castValue attempts to convert val to the expected targetType.
// Returns (newValue, true) if a conversion was made, (val, false) otherwise.
func castValue(val interface{}, targetType string) (interface{}, bool) {
	switch targetType {
	case "array":
		if s, ok := val.(string); ok {
			// Try JSON parsing first (handles "[{...}]" → []interface{})
			var parsed []interface{}
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				return parsed, true
			}
			// Fall back: single string → string array
			return []string{s}, true
		}

	case "boolean":
		if s, ok := val.(string); ok {
			lower := strings.ToLower(s)
			switch lower {
			case "true", "1", "yes":
				return true, true
			case "false", "0", "no":
				return false, true
			}
		}
		// JSON number 0/1 -> bool
		if n, ok := val.(float64); ok {
			if n == 0 {
				return false, true
			}
			if n == 1 {
				return true, true
			}
		}

	case "integer":
		if s, ok := val.(string); ok {
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return i, true
			}
		}
		// JSON numbers are float64 in Go; convert to int if it's a whole number
		if f, ok := val.(float64); ok {
			if f == float64(int64(f)) {
				return int64(f), true
			}
		}

	case "number":
		if s, ok := val.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, true
			}
		}

	case "string":
		// Non-string values -> string (e.g., number or bool passed as non-string)
		switch v := val.(type) {
		case bool:
			if v {
				return "true", true
			}
			return "false", true
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64), true
		case int64:
			return strconv.FormatInt(v, 10), true
		}
	}

	return val, false
}

// normalizeWikiWritePageArgs repairs the two shapes observed in graph runs:
// a complete page payload nested under summary, and semantic page_type values
// emitted for knowledge objects even though the pipeline contract requires
// index. The detection is schema-based so unrelated tools are untouched.
func normalizeWikiWritePageArgs(args map[string]interface{}, properties map[string]interface{}) bool {
	if args == nil || len(properties) == 0 {
		return false
	}
	if _, ok := properties["content"]; !ok {
		return false
	}
	if _, ok := properties["page_type"]; !ok {
		return false
	}
	if _, ok := properties["summary"]; !ok {
		return false
	}

	changed := false
	if nested, ok := args["summary"].(map[string]interface{}); ok {
		for _, key := range []string{"content", "page_type", "source_refs"} {
			if _, exists := args[key]; exists {
				continue
			}
			if value, exists := nested[key]; exists {
				args[key] = value
				changed = true
			}
		}
		if value, ok := nested["summary"].(string); ok && strings.TrimSpace(value) != "" {
			args["summary"] = value
		} else if content, ok := nested["content"].(string); ok {
			args["summary"] = summarizeWikiContent(content)
		} else {
			delete(args, "summary")
		}
		changed = true
	}

	// Knowledge candidate pages carry their business type in frontmatter.type;
	// the storage page type remains the single legal value `index`.
	if pageType, ok := args["page_type"].(string); ok && pageType != "index" {
		if content, ok := args["content"].(string); ok && isKnowledgeObjectFrontmatter(content) {
			args["page_type"] = "index"
			changed = true
		}
	}
	return changed
}

func isKnowledgeObjectFrontmatter(content string) bool {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---") {
		return false
	}
	for _, marker := range []string{"\nknowledge_object_id:", "\nsource_video_id:", "\ntranscript_generation:"} {
		if !strings.Contains(trimmed, marker) {
			return false
		}
	}
	for _, kind := range []string{"entity", "methodology", "case", "concept", "insight"} {
		if strings.Contains(trimmed, "\ntype: "+kind+"\n") || strings.Contains(trimmed, "\ntype: "+kind+"\r\n") {
			return true
		}
	}
	return false
}

func summarizeWikiContent(content string) string {
	lines := strings.Split(content, "\n")
	start := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				start = i + 1
				break
			}
		}
	}
	for _, line := range lines[start:] {
		line = strings.TrimSpace(line)
		if line == "" || line == "---" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		if len([]rune(line)) > 300 {
			return string([]rune(line)[:300])
		}
		return line
	}
	return "知识页面"
}

// ClampParams coerces each numeric argument inside [minimum, maximum] declared
// by the tool's JSON Schema. The coercion happens before validation so a single
// over-sized `limit` / `offset` / page size cannot abort an otherwise valid
// agent tool call. Bounds defined on `integer` properties are snapped to the
// nearest int64; `number` properties keep float precision. `minLength` /
// `maxLength` on strings are deliberately not clamped because truncation would
// silently lose data (those still go through ValidateParams).
//
// Returns the clamped args JSON and a boolean indicating whether any value was
// adjusted. If the schema or args cannot be parsed, the original args are
// returned unchanged with changed=false.
func ClampParams(args json.RawMessage, schema json.RawMessage) (json.RawMessage, bool) {
	if len(schema) == 0 || len(args) == 0 {
		return args, false
	}

	var schemaDef map[string]interface{}
	if err := json.Unmarshal(schema, &schemaDef); err != nil {
		return args, false
	}
	properties, ok := schemaDef["properties"].(map[string]interface{})
	if !ok || len(properties) == 0 {
		return args, false
	}

	var argsMap map[string]interface{}
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return args, false
	}

	changed := false
	for key, raw := range argsMap {
		if raw == nil {
			continue
		}
		propDef, exists := properties[key]
		if !exists {
			continue
		}
		prop, ok := propDef.(map[string]interface{})
		if !ok {
			continue
		}
		targetType, _ := prop["type"].(string)
		if targetType != "integer" && targetType != "number" {
			continue
		}
		minVal, hasMin := getFloat(prop, "minimum")
		maxVal, hasMax := getFloat(prop, "maximum")
		if !hasMin && !hasMax {
			continue
		}

		var num float64
		switch v := raw.(type) {
		case float64:
			num = v
		case int64:
			num = float64(v)
		case int:
			num = float64(v)
		case json.Number:
			f, err := v.Float64()
			if err != nil {
				continue
			}
			num = f
		case string:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				continue
			}
			num = f
		default:
			continue
		}

		clamped := num
		if hasMin && clamped < minVal {
			clamped = minVal
		}
		if hasMax && clamped > maxVal {
			clamped = maxVal
		}
		if clamped == num {
			continue
		}

		if targetType == "integer" {
			argsMap[key] = int64(math.Round(clamped))
		} else {
			argsMap[key] = clamped
		}
		changed = true
	}

	if !changed {
		return args, false
	}
	out, err := json.Marshal(argsMap)
	if err != nil {
		return args, false
	}
	return out, true
}
