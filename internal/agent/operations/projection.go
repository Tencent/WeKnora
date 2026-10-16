package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxProjectedStringRunes = 2000
	// maxAgentFieldRunes is the largest x-agent-max-runes value. Revision
	// content uses it so a revert can be read; ordinary fields stay at
	// maxProjectedStringRunes.
	maxAgentFieldRunes = 8000
)

// Projected is the model-visible slice of a response.
type Projected struct {
	// Data is an object, or a list of objects for list results.
	Data any `json:"data"`
	// Meta holds Result.Meta fields, when declared.
	Meta map[string]any `json:"meta,omitempty"`
	// Truncated reports any loss of string content or list items in the projection.
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
			out.Meta, out.Truncated = projectObject(obj, op.Result.Meta)
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
		data, truncated := projectObject(obj, op.Result.Fields)
		out.Data = data
		out.Truncated = out.Truncated || truncated
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
			data, truncated := projectObject(obj, op.Result.Fields)
			list = append(list, data)
			out.Truncated = out.Truncated || truncated
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

func projectObject(obj map[string]any, fields []Field) (map[string]any, bool) {
	out := make(map[string]any, len(fields))
	truncated := false
	for _, f := range fields {
		v, ok := lookup(obj, f.From)
		if !ok {
			continue
		}
		if len(f.Items) > 0 {
			list, cut := projectItems(v, f.Items)
			truncated = truncated || cut
			out[f.outputKey()] = list
			continue
		}
		limit := f.MaxRunes
		if limit <= 0 {
			limit = maxProjectedStringRunes
		}
		bounded, cut := boundValue(v, limit)
		truncated = truncated || cut
		if bounded != nil {
			out[f.outputKey()] = bounded
		}
	}
	return out, truncated
}

// projectItems projects a list of objects element by element. A null list
// becomes an empty one so the model can tell "none" from "not reported".
func projectItems(v any, fields []Field) ([]any, bool) {
	items, _ := v.([]any)
	out := make([]any, 0, min(len(items), defaultMaxListItems))
	truncated := len(items) > defaultMaxListItems
	for _, item := range items {
		if len(out) == defaultMaxListItems {
			break
		}
		if obj, ok := item.(map[string]any); ok {
			data, cut := projectObject(obj, fields)
			out = append(out, data)
			truncated = truncated || cut
		}
	}
	return out, truncated
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
func boundValue(v any, maxRunes int) (any, bool) {
	switch t := v.(type) {
	case string:
		return truncateRunes(t, maxRunes), utf8.RuneCountInString(t) > maxRunes
	case []any:
		out := make([]any, 0, min(len(t), defaultMaxListItems))
		truncated := len(t) > defaultMaxListItems
		for _, item := range t {
			if len(out) == defaultMaxListItems {
				break
			}
			switch s := item.(type) {
			case string:
				out = append(out, truncateRunes(s, maxRunes))
				truncated = truncated || utf8.RuneCountInString(s) > maxRunes
			case json.Number, bool:
				out = append(out, s)
			}
		}
		return out, truncated
	case map[string]any:
		return nil, false
	default:
		return t, false
	}
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + "…"
}
