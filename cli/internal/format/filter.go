package format

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/itchyny/gojq"
)

// filterArrayItems applies filterObjectKeys to each element of an array.
// Non-object elements (e.g. an array of strings) are passed through.
func filterArrayItems(arrayRaw json.RawMessage, fields []string) (json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(arrayRaw, &items); err != nil {
		return nil, fmt.Errorf("field filter: parse array: %w", err)
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

// JQError marks a failure attributable to the user-supplied --jq expression
// (parse or evaluation), as opposed to an internal serialization fault. The
// cmdutil layer (which can't be imported here without a cycle) maps it to
// input.invalid_argument so agents fix the expression rather than treating a
// bad --jq as a CLI bug (internal.error / exit 1).
type JQError struct{ Err error }

func (e *JQError) Error() string { return e.Err.Error() }
func (e *JQError) Unwrap() error { return e.Err }

// writeJQ evaluates expr against raw and writes each result line by line to w.
// String results render without quotes (so `--jq '.name'` yields shell-friendly
// bare strings); non-string results use encoding/json.
//
// Expression-attributable failures (parse / eval) are wrapped in *JQError so
// the caller can map them to a typed input.invalid_argument code. A failure to
// parse `raw` itself is internal (our own envelope JSON) and stays plain.
func writeJQ(w io.Writer, raw []byte, expr string) error {
	query, err := gojq.Parse(expr)
	if err != nil {
		return &JQError{Err: fmt.Errorf("jq parse: %w", err)}
	}
	var input any
	if err := json.Unmarshal(raw, &input); err != nil {
		return fmt.Errorf("jq input parse: %w", err)
	}
	iter := query.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			return nil
		}
		if e, ok := v.(error); ok {
			return &JQError{Err: fmt.Errorf("jq eval: %w", e)}
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
