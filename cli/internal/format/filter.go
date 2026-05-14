package format

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/itchyny/gojq"
)

// WriteEnvelopeFiltered serializes env to w, optionally restricting
// data.items[*] (for list envelopes) or data (for single-resource envelopes)
// to the named fields, then applying a jq expression on the result.
//
//   - len(fields) == 0  → no field filter (full envelope)
//   - jqExpr == ""      → no jq filter (just write the envelope)
//
// The envelope structure (ok / data / error / _meta / risk / dry_run / _notice)
// is preserved across field filtering — only Data is rewritten. jq operates
// on the entire envelope JSON so users can `--jq '.data.items[].id'`.
func WriteEnvelopeFiltered(w io.Writer, env Envelope, fields []string, jqExpr string) error {
	raw, err := marshalEnvelope(env)
	if err != nil {
		return err
	}
	if len(fields) > 0 {
		raw, err = applyFieldFilter(raw, fields)
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

// marshalEnvelope returns the canonical newline-terminated envelope JSON used
// by the non-filtered path. Identical to WriteEnvelope but as bytes.
func marshalEnvelope(env Envelope) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// applyFieldFilter rewrites envelope.data so that nested objects keep only
// the named keys. The shape rules:
//
//   - data is an object with `items` (the standard list envelope shape):
//     filter each items[*] to the named fields.
//   - data is an object without `items` (single-resource envelope):
//     filter data itself.
//   - data is an array (uncommon — currently no command produces this, but
//     defensive): filter each [*].
//   - data is nil / scalar: unchanged.
//
// Unknown field names are silently ignored so a user may pass an
// aspirational field set across heterogenous list outputs without per-
// command tailoring.
func applyFieldFilter(envelopeJSON []byte, fields []string) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(envelopeJSON, &raw); err != nil {
		return nil, fmt.Errorf("field filter: parse envelope: %w", err)
	}
	dataRaw, ok := raw["data"]
	if !ok || len(dataRaw) == 0 || string(dataRaw) == "null" {
		return envelopeJSON, nil
	}

	filtered, err := filterDataPayload(dataRaw, fields)
	if err != nil {
		return nil, err
	}
	raw["data"] = filtered

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// filterDataPayload dispatches on the shape of the data JSON value.
func filterDataPayload(dataRaw json.RawMessage, fields []string) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(dataRaw)
	if len(trimmed) == 0 {
		return dataRaw, nil
	}
	switch trimmed[0] {
	case '{':
		return filterObjectData(dataRaw, fields)
	case '[':
		return filterArrayItems(dataRaw, fields)
	default:
		// scalar (number / string / bool / null) — nothing to filter
		return dataRaw, nil
	}
}

// filterObjectData filters either data.items[*] (list shape) or data (single
// resource).
func filterObjectData(dataRaw json.RawMessage, fields []string) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(dataRaw, &obj); err != nil {
		return nil, fmt.Errorf("field filter: parse data object: %w", err)
	}
	if items, ok := obj["items"]; ok {
		filtered, err := filterArrayItems(items, fields)
		if err != nil {
			return nil, err
		}
		obj["items"] = filtered
		return json.Marshal(obj)
	}
	// Single-resource envelope: filter the data object itself.
	return filterObjectKeys(dataRaw, fields)
}

// filterArrayItems applies filterObjectKeys to each element of an array.
// Non-object elements (e.g. an array of strings) are passed through.
func filterArrayItems(arrayRaw json.RawMessage, fields []string) (json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(arrayRaw, &items); err != nil {
		return nil, fmt.Errorf("field filter: parse data items: %w", err)
	}
	for i, item := range items {
		trimmed := bytes.TrimSpace(item)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			continue
		}
		filtered, err := filterObjectKeys(item, fields)
		if err != nil {
			return nil, err
		}
		items[i] = filtered
	}
	return json.Marshal(items)
}

// filterObjectKeys produces a new object containing only the listed keys
// that were present in the source.
func filterObjectKeys(objRaw json.RawMessage, fields []string) (json.RawMessage, error) {
	var src map[string]json.RawMessage
	if err := json.Unmarshal(objRaw, &src); err != nil {
		return nil, fmt.Errorf("field filter: parse object keys: %w", err)
	}
	dst := make(map[string]json.RawMessage, len(fields))
	for _, k := range fields {
		if v, ok := src[k]; ok {
			dst[k] = v
		}
	}
	return json.Marshal(dst)
}

// writeJQ evaluates expr against envelopeJSON and writes each result line
// by line to w. String results render without quotes (so `--jq '.x.name'`
// yields shell-friendly bare strings); non-string results use
// encoding/json.
//
// Returns input.invalid_argument-shaped errors via plain errors.New + fmt;
// the caller is responsible for wrapping with cmdutil.NewError if it wants
// the typed envelope code.
func writeJQ(w io.Writer, envelopeJSON []byte, expr string) error {
	query, err := gojq.Parse(expr)
	if err != nil {
		return fmt.Errorf("jq parse: %w", err)
	}
	var input any
	if err := json.Unmarshal(envelopeJSON, &input); err != nil {
		return fmt.Errorf("jq input parse: %w", err)
	}
	iter := query.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			return nil
		}
		if e, ok := v.(error); ok {
			return fmt.Errorf("jq eval: %w", e)
		}
		if s, ok := v.(string); ok {
			if _, err := fmt.Fprintln(w, s); err != nil {
				return err
			}
			continue
		}
		out, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(out, '\n')); err != nil {
			return err
		}
	}
}
