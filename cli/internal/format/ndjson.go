package format

import (
	"io"
	"reflect"
)

// WriteNDJSON serializes v as NDJSON: one JSON value per line, '\n' terminated.
// If v is a slice or array, each element gets its own line; otherwise v is
// emitted as a single line. Per http://ndjson.org.
//
// HTML escaping is disabled (via WriteJSON) so '<', '>', '&' stay literal —
// cleaner for agent stream-parsers that don't need HTML-safe output.
func WriteNDJSON(w io.Writer, v any) error {
	rv := reflect.ValueOf(v)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		for i := 0; i < rv.Len(); i++ {
			if err := WriteJSON(w, rv.Index(i).Interface()); err != nil {
				return err
			}
		}
		return nil
	}
	return WriteJSON(w, v)
}
