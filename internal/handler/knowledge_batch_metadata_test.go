package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeBatchCustomMetadataAcceptsScalarObject(t *testing.T) {
	metadata, err := decodeBatchCustomMetadata(json.RawMessage(`{"title":"guide","version":2,"published":true,"obsolete":null}`))
	if err != nil {
		t.Fatalf("decodeBatchCustomMetadata() error = %v", err)
	}
	if metadata["title"] != "guide" || metadata["version"] != float64(2) || metadata["published"] != true {
		t.Fatalf("decoded metadata = %#v", metadata)
	}
	if value, ok := metadata["obsolete"]; !ok || value != nil {
		t.Fatalf("decoded null field = %#v, want nil", value)
	}
}

func TestDecodeBatchCustomMetadataRejectsInvalidShapesAndValues(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing", raw: ""},
		{name: "array", raw: `["not an object"]`},
		{name: "null", raw: `null`},
		{name: "nested object", raw: `{"nested":{"value":1}}`},
		{name: "empty key", raw: `{" ":"value"}`},
		{name: "long key", raw: `{"` + strings.Repeat("x", 65) + `":"value"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeBatchCustomMetadata(json.RawMessage(tt.raw)); err == nil {
				t.Fatalf("decodeBatchCustomMetadata(%q) succeeded, want error", tt.raw)
			}
		})
	}
}

func TestDecodeBatchCustomMetadataAllowsClearing(t *testing.T) {
	metadata, err := decodeBatchCustomMetadata(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("empty object should clear metadata: %v", err)
	}
	if len(metadata) != 0 {
		t.Fatalf("empty object decoded as %#v", metadata)
	}
}
