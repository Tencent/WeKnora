package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// maxStringParamBytes is the default cap for a string parameter. Document
	// bodies opt into maxAgentStringBytes instead; that matches the manual
	// knowledge API limit of 200000 runes at the longest UTF-8 encoding.
	maxStringParamBytes = 4096
	maxAgentStringBytes = 200000 * 4
)

// Call is a fully validated HTTP request for one operation.
type Call struct {
	Method string
	// Path is escaped and ready to use as a request URI path.
	Path  string
	Query url.Values
	// Body is a JSON object, or nil when the operation has no body params.
	Body []byte
}

// URI is Path plus the encoded query.
func (c Call) URI() string {
	if len(c.Query) == 0 {
		return c.Path
	}
	return c.Path + "?" + c.Query.Encode()
}

// ParamError is a validation failure the model can correct.
type ParamError struct {
	Param   string
	Message string
}

func (e *ParamError) Error() string {
	if e.Param == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Param, e.Message)
}

func paramErr(name, format string, args ...any) error {
	return &ParamError{Param: name, Message: fmt.Sprintf(format, args...)}
}

// Build validates params against the operation and assembles the request.
// Unknown parameters are rejected rather than ignored, so a model that
// guessed a name learns about it instead of silently getting other behavior.
func (op Operation) Build(params map[string]json.RawMessage) (Call, error) {
	for name := range params {
		if _, ok := op.Param(name); !ok {
			return Call{}, paramErr(name, "unknown parameter; allowed: %s", strings.Join(op.paramNames(), ", "))
		}
	}

	pathValues := map[string]string{}
	query := url.Values{}
	body := map[string]any{}
	for _, p := range op.Params {
		raw, present := params[p.Name]
		if present && isJSONNull(raw) {
			present = false
		}
		if !present {
			if p.Required {
				return Call{}, paramErr(p.Name, "is required")
			}
			continue
		}
		// Manual knowledge content is a complete Markdown document; trimming its
		// edges can change an indented first code block or a final hard break.
		preserveContent := p.In == InBody && p.wireKey() == "content" &&
			(op.ID == "knowledge.create_manual" || op.ID == "knowledge.update_manual")
		value, err := decodeParam(p, raw, preserveContent)
		if err != nil {
			return Call{}, err
		}
		switch p.In {
		case InPath:
			if n, ok := value.(int64); ok {
				pathValues[p.wireKey()] = strconv.FormatInt(n, 10)
				break
			}
			s := value.(string)
			if err := validatePathValue(p, s, strings.HasSuffix(op.Route, "/*"+p.wireKey())); err != nil {
				return Call{}, err
			}
			pathValues[p.wireKey()] = s
		case InQuery:
			switch v := value.(type) {
			case []string:
				for _, item := range v {
					query.Add(p.wireKey(), item)
				}
			case []int64:
				for _, item := range v {
					query.Add(p.wireKey(), strconv.FormatInt(item, 10))
				}
			case string:
				query.Set(p.wireKey(), v)
			case int64:
				query.Set(p.wireKey(), strconv.FormatInt(v, 10))
			case float64:
				query.Set(p.wireKey(), strconv.FormatFloat(v, 'f', -1, 64))
			case bool:
				query.Set(p.wireKey(), strconv.FormatBool(v))
			}
		case InBody:
			if err := assignBody(body, p.wireKey(), value); err != nil {
				return Call{}, err
			}
		}
	}

	call := Call{Method: op.Method, Path: renderPath(op.Route, pathValues)}
	if len(query) > 0 {
		call.Query = query
	}
	if op.hasBody() {
		encoded, err := json.Marshal(body)
		if err != nil {
			return Call{}, fmt.Errorf("encode body: %w", err)
		}
		call.Body = encoded
	}
	return call, nil
}

// assignBody writes value at a dotted JSON path, creating objects along the way.
func assignBody(body map[string]any, key string, value any) error {
	parts := strings.Split(key, ".")
	cursor := body
	for _, part := range parts[:len(parts)-1] {
		if part == "" {
			return fmt.Errorf("body field %q has an empty path segment", key)
		}
		existing, ok := cursor[part]
		if !ok {
			next := map[string]any{}
			cursor[part] = next
			cursor = next
			continue
		}
		next, ok := existing.(map[string]any)
		if !ok {
			return fmt.Errorf("body field %q conflicts with another field", key)
		}
		cursor = next
	}
	leaf := parts[len(parts)-1]
	if leaf == "" {
		return fmt.Errorf("body field %q has an empty path segment", key)
	}
	cursor[leaf] = value
	return nil
}

// SelectParams keeps the parameters op accepts.
func SelectParams(op Operation, params map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(op.Params))
	for _, p := range op.Params {
		if raw, ok := params[p.Name]; ok {
			out[p.Name] = raw
		}
	}
	return out
}

func (op Operation) hasBody() bool {
	for _, p := range op.Params {
		if p.In == InBody {
			return true
		}
	}
	return false
}

func (op Operation) paramNames() []string {
	names := make([]string, 0, len(op.Params))
	for _, p := range op.Params {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return []string{"(none)"}
	}
	return names
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func decodeParam(p Param, raw json.RawMessage, preserveContent bool) (any, error) {
	switch p.Type {
	case TypeString:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, paramErr(p.Name, "must be a string")
		}
		if !preserveContent {
			s = strings.TrimSpace(s)
		}
		if err := validateString(p, s); err != nil {
			return nil, err
		}
		if strings.TrimSpace(s) == "" && p.Required && !p.AllowEmpty {
			return nil, paramErr(p.Name, "must not be empty")
		}
		return s, nil
	case TypeStringList:
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, paramErr(p.Name, "must be an array of strings")
		}
		limit := p.MaxItems
		if limit <= 0 {
			limit = defaultMaxListItems
		}
		if len(list) > limit {
			return nil, paramErr(p.Name, "accepts at most %d items", limit)
		}
		if p.Required && !p.AllowEmpty && len(list) == 0 {
			return nil, paramErr(p.Name, "must not be empty")
		}
		out := make([]string, 0, len(list))
		for _, item := range list {
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, paramErr(p.Name, "must not contain empty items")
			}
			if err := validateString(p, item); err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	case TypeIntegerList:
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, paramErr(p.Name, "must be an array of integers")
		}
		limit := p.MaxItems
		if limit <= 0 {
			limit = defaultMaxListItems
		}
		if len(list) > limit {
			return nil, paramErr(p.Name, "accepts at most %d items", limit)
		}
		if p.Required && !p.AllowEmpty && len(list) == 0 {
			return nil, paramErr(p.Name, "must not be empty")
		}
		out := make([]int64, 0, len(list))
		for _, item := range list {
			v, err := decodeInteger(p, item)
			if err != nil {
				return nil, paramErr(p.Name, "must be an array of integers")
			}
			out = append(out, v)
		}
		return out, nil
	case TypeInteger:
		return decodeInteger(p, raw)
	case TypeNumber:
		if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] == '"' {
			return nil, paramErr(p.Name, "must be a number")
		}
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, paramErr(p.Name, "must be a number")
		}
		return f, nil
	case TypeBoolean:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, paramErr(p.Name, "must be a boolean")
		}
		return b, nil
	}
	return nil, paramErr(p.Name, "unsupported type")
}

func decodeInteger(p Param, raw json.RawMessage) (int64, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] == '"' {
		return 0, paramErr(p.Name, "must be an integer")
	}
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&n); err != nil {
		return 0, paramErr(p.Name, "must be an integer")
	}
	v, err := n.Int64()
	if err != nil {
		return 0, paramErr(p.Name, "must be an integer")
	}
	if p.Maximum > 0 && (v < int64(p.Minimum) || v > int64(p.Maximum)) {
		return 0, paramErr(p.Name, "must be between %d and %d", p.Minimum, p.Maximum)
	}
	return v, nil
}

func validateString(p Param, s string) error {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = maxStringParamBytes
	}
	if len(s) > limit {
		return paramErr(p.Name, "is too long")
	}
	if !utf8.ValidString(s) {
		return paramErr(p.Name, "must be valid UTF-8")
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return paramErr(p.Name, "must not contain control characters")
		}
	}
	if len(p.Enum) > 0 {
		for _, allowed := range p.Enum {
			if s == allowed {
				return nil
			}
		}
		return paramErr(p.Name, "must be one of: %s", strings.Join(p.Enum, ", "))
	}
	return nil
}

// validatePathValue keeps a value inside its route segment. A catch-all may
// span segments, but none of them may be empty or a dot segment.
func validatePathValue(p Param, s string, catchAll bool) error {
	if strings.ContainsAny(s, "\\?#\n\t") {
		return paramErr(p.Name, "contains characters not allowed in a path")
	}
	if catchAll {
		for _, seg := range strings.Split(s, "/") {
			if seg == "" || seg == "." || seg == ".." {
				return paramErr(p.Name, "is not a valid path")
			}
		}
		return nil
	}
	if strings.Contains(s, "/") || s == "." || s == ".." {
		return paramErr(p.Name, "must be a single path segment")
	}
	return nil
}

func renderPath(route string, values map[string]string) string {
	segments := strings.Split(route, "/")
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		switch seg[0] {
		case ':':
			segments[i] = url.PathEscape(values[seg[1:]])
		case '*':
			parts := strings.Split(values[seg[1:]], "/")
			for j, part := range parts {
				parts[j] = url.PathEscape(part)
			}
			segments[i] = strings.Join(parts, "/")
		}
	}
	return strings.Join(segments, "/")
}
