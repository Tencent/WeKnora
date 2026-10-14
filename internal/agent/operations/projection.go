package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxProjectedStringRunes = 2000

// Projected is the model-visible slice of a response.
type Projected struct {
	// Data is an object, or a list of objects for list results.
	Data any `json:"data"`
	// Meta holds Result.Meta fields, when declared.
	Meta map[string]any `json:"meta,omitempty"`
	// Truncated reports that a list was cut at MaxItems.
	Truncated bool `json:"truncated,omitempty"`
}

// Project extracts the declared fields from a JSON response body.
func (op Operation) Project(body []byte) (Projected, error) {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return Projected{}, fmt.Errorf("response is not JSON: %w", err)
	}

	out := Projected{}
	if len(op.Result.Meta) > 0 {
		if obj, ok := doc.(map[string]any); ok {
			out.Meta = projectObject(obj, op.Result.Meta)
		}
	}

	root, ok := lookup(doc, op.Result.Root)
	if !ok || root == nil {
		if op.Result.List {
			out.Data = []any{}
		} else {
			out.Data = map[string]any{}
		}
		return out, nil
	}
	if !op.Result.List {
		obj, ok := root.(map[string]any)
		if !ok {
			return Projected{}, fmt.Errorf("response %q is not an object", op.Result.Root)
		}
		out.Data = projectObject(obj, op.Result.Fields)
		return out, nil
	}

	items, ok := root.([]any)
	if !ok {
		return Projected{}, fmt.Errorf("response %q is not a list", op.Result.Root)
	}
	limit := op.Result.MaxItems
	if limit <= 0 {
		limit = defaultMaxResultItems
	}
	if len(items) > limit {
		items = items[:limit]
		out.Truncated = true
	}
	list := make([]any, 0, len(items))
	for _, item := range items {
		if obj, ok := item.(map[string]any); ok {
			list = append(list, projectObject(obj, op.Result.Fields))
		}
	}
	out.Data = list
	return out, nil
}

// Label returns the projected string field key, if present.
func (p Projected) Label(key string) string {
	obj, ok := p.Data.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := obj[key].(string)
	return strings.TrimSpace(s)
}

func projectObject(obj map[string]any, fields []Field) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		v, ok := lookup(obj, f.From)
		if !ok {
			continue
		}
		if bounded := boundValue(v); bounded != nil {
			out[f.outputKey()] = bounded
		}
	}
	return out
}

func lookup(doc any, path string) (any, bool) {
	if path == "" {
		return doc, true
	}
	cur := doc
	for _, key := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[key]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// boundValue keeps scalar fields, and lists of scalars, within a size the
// model context can afford. Nested objects are not projected implicitly;
// declare their fields instead.
func boundValue(v any) any {
	switch t := v.(type) {
	case string:
		return truncateRunes(t, maxProjectedStringRunes)
	case []any:
		out := make([]any, 0, min(len(t), defaultMaxListItems))
		for _, item := range t {
			if len(out) == defaultMaxListItems {
				break
			}
			switch s := item.(type) {
			case string:
				out = append(out, truncateRunes(s, maxProjectedStringRunes))
			case json.Number, bool:
				out = append(out, s)
			}
		}
		return out
	case map[string]any:
		return nil
	default:
		return t
	}
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + "…"
}
