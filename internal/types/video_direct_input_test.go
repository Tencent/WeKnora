package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// The transport is stored, so it has to survive Normalize, the JSONB column and
// Equal. An empty value is the decode result of rows written before the field
// existed and means auto; anything this build cannot run is a typo and means
// auto as well, never a mode the client would act on.
func TestVideoDirectInputNormalizesToAKnownTransport(t *testing.T) {
	t.Parallel()

	cases := []struct {
		stored string
		want   string
	}{
		{stored: "", want: VideoDirectInputAuto},
		{stored: "   ", want: VideoDirectInputAuto},
		{stored: VideoDirectInputAuto, want: VideoDirectInputAuto},
		{stored: " auto ", want: VideoDirectInputAuto},
		{stored: VideoDirectInputBase64, want: VideoDirectInputBase64},
		{stored: " base64 ", want: VideoDirectInputBase64},
		{stored: VideoDirectInputURL, want: VideoDirectInputURL},
		{stored: VideoDirectInputUpload, want: VideoDirectInputUpload},
		// Unknown values, including the wrong case, are not a transport the
		// client implements.
		{stored: "stream", want: VideoDirectInputAuto},
		{stored: "BASE64", want: VideoDirectInputAuto},
		{stored: "inline", want: VideoDirectInputAuto},
	}
	for _, tc := range cases {
		if got := NormalizeVideoDirectInput(tc.stored); got != tc.want {
			t.Errorf("NormalizeVideoDirectInput(%q) = %q, want %q", tc.stored, got, tc.want)
		}
	}
}

// Normalize stores the effective transport so the JSONB column cannot keep a
// value the client would never act on.
func TestVideoConfigNormalizeStoresTheEffectiveDirectInput(t *testing.T) {
	t.Parallel()

	var zero VideoConfig
	zero.Normalize()
	if zero.DirectInput != VideoDirectInputAuto {
		t.Fatalf("direct_input = %q, want %q", zero.DirectInput, VideoDirectInputAuto)
	}

	unknown := VideoConfig{DirectInput: "stream"}
	unknown.Normalize()
	if unknown.DirectInput != VideoDirectInputAuto {
		t.Fatalf("an unknown transport must coalesce to auto, got %q", unknown.DirectInput)
	}

	kept := VideoConfig{DirectInput: "  upload  "}
	kept.Normalize()
	if kept.DirectInput != VideoDirectInputUpload {
		t.Fatalf("direct_input = %q, want the trimmed upload", kept.DirectInput)
	}
}

// EffectiveDirectInput answers without mutating the row: the settings UI and
// the worker both read rows that were never normalized.
func TestVideoConfigEffectiveDirectInputReadsUnnormalizedRows(t *testing.T) {
	t.Parallel()

	raw := VideoConfig{DirectInput: " base64 "}
	if got := raw.EffectiveDirectInput(); got != VideoDirectInputBase64 {
		t.Fatalf("EffectiveDirectInput() = %q, want base64", got)
	}
	if raw.DirectInput != " base64 " {
		t.Fatal("EffectiveDirectInput must not rewrite the stored value")
	}
	if got := (VideoConfig{}).EffectiveDirectInput(); got != VideoDirectInputAuto {
		t.Fatalf("EffectiveDirectInput() on an empty row = %q, want auto", got)
	}
}

// Changing only the transport is a change to the section, so the update path
// has to see it and persist it.
func TestVideoConfigEqualComparesTheTransport(t *testing.T) {
	t.Parallel()

	inline := VideoConfig{Enabled: true, Mode: VideoParseModeDirect, DirectInput: VideoDirectInputBase64}
	upload := inline
	upload.DirectInput = VideoDirectInputUpload
	if inline.Equal(upload) {
		t.Fatal("changing the transport must be reported as a change")
	}

	// An unnormalized row and a normalized one express the same transport.
	empty := VideoConfig{Enabled: true, Mode: VideoParseModeDirect}
	explicitAuto := empty
	explicitAuto.DirectInput = VideoDirectInputAuto
	if !empty.Equal(explicitAuto) {
		t.Fatal("an empty transport and auto are the same setting")
	}
}

// The transport round-trips through the driver.Valuer / sql.Scanner pair the
// repository relies on, and a stored value that is not auto is kept verbatim.
func TestVideoConfigDirectInputSurvivesTheJSONBColumn(t *testing.T) {
	t.Parallel()

	want := VideoConfig{
		Enabled:      true,
		Mode:         VideoParseModeDirect,
		VideoModelID: "video-1",
		DirectInput:  VideoDirectInputBase64,
	}
	value, err := want.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if !strings.Contains(string(value.([]byte)), `"direct_input":"base64"`) {
		t.Fatalf("stored JSON = %s, want the transport", value)
	}

	var got VideoConfig
	if err := got.Scan(value); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got.DirectInput != VideoDirectInputBase64 {
		t.Fatalf("round trip = %q, want base64", got.DirectInput)
	}
	if !got.Equal(want) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}

	// A row written before the field existed decodes to empty, which reads as
	// auto; Normalize then spells it out.
	var legacy VideoConfig
	if err := json.Unmarshal([]byte(`{"enabled":true,"mode":"video_direct"}`), &legacy); err != nil {
		t.Fatalf("decode legacy row: %v", err)
	}
	if legacy.DirectInput != "" || legacy.EffectiveDirectInput() != VideoDirectInputAuto {
		t.Fatalf("legacy row = %q/%q, want empty/auto", legacy.DirectInput, legacy.EffectiveDirectInput())
	}
	legacy.Normalize()
	if legacy.DirectInput != VideoDirectInputAuto {
		t.Fatalf("after Normalize = %q, want auto", legacy.DirectInput)
	}
}
