package types

import (
	"encoding/json"
	"testing"
)

func TestJSONMapScanValuePreservesExactNumbers(t *testing.T) {
	const raw = `{"access_id":9007199254740993,"nested":{"ids":[9007199254740995]}}`

	for _, source := range []any{[]byte(raw), raw} {
		var value JSONMap
		if err := value.Scan(source); err != nil {
			t.Fatalf("Scan(%T) error = %v", source, err)
		}
		if got := value["access_id"]; got != json.Number("9007199254740993") {
			t.Fatalf("Scan(%T) access_id = %#v, want exact json.Number", source, got)
		}
		nested, ok := value["nested"].(map[string]any)
		if !ok {
			t.Fatalf("Scan(%T) nested = %#v, want object", source, value["nested"])
		}
		ids, ok := nested["ids"].([]any)
		if !ok || len(ids) != 1 || ids[0] != json.Number("9007199254740995") {
			t.Fatalf("Scan(%T) nested ids = %#v, want exact json.Number", source, nested["ids"])
		}

		encoded, err := value.Value()
		if err != nil {
			t.Fatalf("Value() error = %v", err)
		}
		if got := string(encoded.([]byte)); got != raw {
			t.Fatalf("Value() = %s, want %s", got, raw)
		}
	}
}
