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

// castValue attempts to convert val to the expected targetType.
// Returns (newValue, true) if a conversion was made, (val, false) otherwise.
func castValue(val interface{}, targetType string) (interface{}, bool) {
	switch targetType {
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
