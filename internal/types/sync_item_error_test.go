package types

import (
	"encoding/json"
	"testing"
)

// SyncResult.Errors is persisted as jsonb and read back when the sync-log drawer
// is opened. Historically each entry was a plain string; it is now a structured
// SyncItemError (title + i18n code + params + fallback message) so the frontend
// can localise it. Old rows must still deserialize, so a bare string decodes into
// the Message field.
func TestSyncItemError_UnmarshalAcceptsLegacyStringAndObject(t *testing.T) {
	// Legacy format: array of plain strings.
	var legacy []SyncItemError
	if err := json.Unmarshal([]byte(`["季度报告: export failed"]`), &legacy); err != nil {
		t.Fatalf("legacy string form must still decode: %v", err)
	}
	if len(legacy) != 1 || legacy[0].Message != "季度报告: export failed" {
		t.Fatalf("legacy string should map to Message, got %+v", legacy)
	}
	if legacy[0].Code != "" {
		t.Errorf("legacy string must not invent a code, got %q", legacy[0].Code)
	}

	// New format: structured object round-trips.
	in := SyncItemError{
		Title:   "季度报告",
		Code:    "feishu_api_error",
		Params:  map[string]string{"code": "1663"},
		Message: "Feishu API error (code=1663); will retry",
	}
	b, err := json.Marshal([]SyncItemError{in})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out []SyncItemError
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal structured: %v", err)
	}
	if len(out) != 1 || out[0].Code != "feishu_api_error" || out[0].Params["code"] != "1663" || out[0].Title != "季度报告" {
		t.Fatalf("structured form did not round-trip: %+v", out)
	}
}
