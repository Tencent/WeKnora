package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// ModelTypeVideo is a stored value the API round-trips and the builtin loader
// accepts, so both the string constant and the JSON shape are part of the
// contract: a rename would orphan every saved row.
func TestModelTypeVideoSerializesAsVideo(t *testing.T) {
	if ModelTypeVideo != "Video" {
		t.Fatalf("ModelTypeVideo = %q, want the stored value %q", ModelTypeVideo, "Video")
	}

	raw, err := json.Marshal(Model{ID: "video-1", Type: ModelTypeVideo})
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	if !strings.Contains(string(raw), `"type":"Video"`) {
		t.Fatalf("marshalled model = %s, want type Video", raw)
	}

	var decoded Model
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal model: %v", err)
	}
	if decoded.Type != ModelTypeVideo {
		t.Fatalf("decoded type = %q, want %q", decoded.Type, ModelTypeVideo)
	}
}

// The builtin YAML loader refuses a type it does not know, and a video model in
// builtin_models.yaml has to load like any other.
func TestBuiltinModelLoaderAcceptsVideoRows(t *testing.T) {
	entry := &BuiltinModelEntry{
		ID: "video-1", Name: "qwen3.8-max", Type: ModelTypeVideo, Source: ModelSourceRemote,
	}
	if err := validateBuiltinModelEntry(entry, 0); err != nil {
		t.Fatalf("validateBuiltinModelEntry() error = %v", err)
	}
	model := entry.toModel()
	if model.Type != ModelTypeVideo || !model.IsBuiltin {
		t.Fatalf("toModel() = %+v, want a builtin Video row", model)
	}
	if got := model.TenantID; got != DefaultBuiltinModelTenantID {
		t.Fatalf("tenant = %d, want the builtin default", got)
	}
}
