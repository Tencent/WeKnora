package format

import (
	"bytes"
	"encoding/json"
	"io"
)

// WriteJSON serializes v as one-line JSON to w. Bare-data contract: list
// commands emit their array directly, single-resource commands emit their
// object directly. The shape is whatever the producing command marshals.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// WriteJSONFiltered serializes v to w with optional field restriction and
// optional jq evaluation.
//
//   - len(fields) == 0 → no field filter
//   - jqExpr == ""     → no jq filter
//
// Field filter rules (mirrors gh CLI's `--json field,field` semantics):
//
//   - v marshals to a top-level array  → each [*] object is restricted to
//     the named keys
//   - v marshals to a top-level object → the object is restricted to the
//     named keys
//   - v marshals to a scalar           → unchanged
//
// Unknown field names are silently dropped so users can pass an aspirational
// field set across heterogenous list outputs.
func WriteJSONFiltered(w io.Writer, v any, fields []string, jqExpr string) error {
	if len(fields) == 0 && jqExpr == "" {
		return WriteJSON(w, v)
	}
	raw, err := marshalJSON(v)
	if err != nil {
		return err
	}
	if len(fields) > 0 {
		raw, err = applyBareFieldFilter(raw, fields)
		if err != nil {
			return err
		}
	}
	if jqExpr != "" {
		return writeJQ(w, raw, jqExpr)
	}
	_, err = w.Write(raw)
	return err
}

// marshalJSON encodes v to a newline-terminated byte slice using the same
// encoder settings as WriteJSON (HTML escaping disabled).
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// applyBareFieldFilter dispatches on the JSON shape of raw and restricts
// elements / object keys to the named fields.
func applyBareFieldFilter(raw []byte, fields []string) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, nil
	}
	switch trimmed[0] {
	case '[':
		return filterArrayItems(raw, fields)
	case '{':
		return filterObjectKeys(raw, fields)
	default:
		return raw, nil
	}
}
