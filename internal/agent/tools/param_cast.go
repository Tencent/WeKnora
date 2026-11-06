package tools

import (
	"bytes"
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

	// Keep untouched arguments as raw JSON. Decoding the entire payload into
	// float64 values would round numbers even when only another field is cast.
	var argsMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return args
	}

	changed := false
	for key, rawVal := range argsMap {
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

		// A JSON number keeps its literal text when it becomes a string.
		// Decoding into interface{} would round it through float64 first, so
		// large identifiers lose digits and precise decimals lose precision.
		if targetType == "string" {
			if literal, ok := jsonNumberLiteral(rawVal); ok {
				converted, err := json.Marshal(literal)
				if err != nil {
					return args
				}
				argsMap[key] = converted
				changed = true
				continue
			}
		}

		var val interface{}
		if err := json.Unmarshal(rawVal, &val); err != nil {
			continue
		}
		// Integer literals already have the requested JSON representation. A
		// float64-to-int64 round trip would corrupt values above 2^53. Keep
		// decimal/exponent notation on the existing normalization path.
		if _, isNumber := val.(float64); isNumber && targetType == "integer" &&
			!strings.ContainsAny(string(rawVal), ".eE") {
			continue
		}
		newVal, didCast := castValue(val, targetType)
		if didCast {
			converted, err := json.Marshal(newVal)
			if err != nil {
				return args
			}
			argsMap[key] = converted
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

// jsonNumberLiteral reports whether raw holds a JSON number and returns its
// literal text unchanged. Strings, booleans, null, arrays and objects are not
// numbers and are left to the regular casting path.
func jsonNumberLiteral(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", false
	}
	if c := trimmed[0]; c != '-' && (c < '0' || c > '9') {
		return "", false
	}
	if !json.Valid(trimmed) {
		return "", false
	}
	return string(trimmed), true
}

// castValue attempts to convert val to the expected targetType.
// Returns (newValue, true) if a conversion was made, (val, false) otherwise.
func castValue(val interface{}, targetType string) (interface{}, bool) {
	switch targetType {
	case "array":
		if s, ok := val.(string); ok {
			// Unwrap serialized arrays without decoding their elements through
			// float64, which would round IDs and precise nested numeric values.
			var parsed []json.RawMessage
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				return parsed, true
			}
			// An attempted array with invalid JSON must reach validation as an
			// error, not silently become one option/path/query containing the list.
			if strings.HasPrefix(strings.TrimSpace(s), "[") {
				return val, false
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
				// Keep the literal text: marshaling the parsed float64 would
				// round integers above 2^53 and long decimals, the same way
				// untouched arguments and integer literals are preserved.
				// Literals ParseFloat accepts but JSON rejects (inf, +5)
				// still fall back to the float64 normalization below.
				var literal json.Number
				if json.Unmarshal([]byte(s), &literal) == nil {
					return literal, true
				}
				return f, true
			}
		}

	case "string":
		// Non-string values -> string (e.g., number or bool passed as non-string).
		// JSON numbers are already quoted from their literal text in CastParams;
		// the float64 branch is only a fallback for direct callers.
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
