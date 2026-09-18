package tools

import (
	"encoding/json"
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
		newVal, didCast := castProperty(val, prop)
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

// castProperty casts val against one property schema. For string arrays it
// also unwraps elements that arrive as single-string objects. Other element
// types are deliberately left to validation: external MCP schemas are
// enforced strictly, and "2" in an integer array must stay an error there.
func castProperty(val interface{}, prop map[string]interface{}) (interface{}, bool) {
	targetType, _ := prop["type"].(string)
	if targetType == "" {
		return val, false
	}
	newVal, changed := castValue(val, targetType)
	if targetType != "array" {
		return newVal, changed
	}
	items, _ := prop["items"].(map[string]interface{})
	if itemType, _ := items["type"].(string); itemType != "string" {
		return newVal, changed
	}
	list, ok := newVal.([]interface{})
	if !ok {
		return newVal, changed
	}
	out := make([]interface{}, len(list))
	for i, item := range list {
		out[i] = item
		if s, ok := unwrapSingleString(item); ok {
			out[i] = s
			changed = true
		}
	}
	return out, changed
}

// unwrapSingleString returns the string inside an object that carries
// exactly one string field, e.g. {"text": "..."} or {"query": "..."}.
// Models occasionally wrap scalar arguments this way; the intent is
// unambiguous, and passing the object on only yields an opaque decode error.
func unwrapSingleString(val interface{}) (string, bool) {
	obj, ok := val.(map[string]interface{})
	if !ok || len(obj) != 1 {
		return "", false
	}
	for _, v := range obj {
		s, ok := v.(string)
		return s, ok
	}
	return "", false
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
		if s, ok := unwrapSingleString(val); ok {
			return s, true
		}
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
