package types

import (
	"encoding/json"
	"testing"
)

// A knowledge base written before the column existed (or one created with an
// empty body) must decode to the documented default: off, audio_only. Opting in
// has to be a deliberate act.
func TestVideoConfigDefaultsToDisabledAudioOnly(t *testing.T) {
	t.Parallel()

	var zero VideoConfig
	zero.Normalize()
	if zero.Enabled {
		t.Fatal("zero VideoConfig must be disabled")
	}
	if zero.Mode != VideoParseModeAudioOnly {
		t.Fatalf("mode = %q, want %q", zero.Mode, VideoParseModeAudioOnly)
	}
	if zero.AllowsAudioOnlyParse() {
		t.Fatal("a disabled VideoConfig must not request parsing")
	}

	var decoded VideoConfig
	if err := json.Unmarshal([]byte(`{}`), &decoded); err != nil {
		t.Fatalf("decode empty video_config: %v", err)
	}
	decoded.Normalize()
	if decoded.Enabled || decoded.Mode != VideoParseModeAudioOnly {
		t.Fatalf("decoded = %+v, want disabled audio_only", decoded)
	}
}

// An operator only needs to send {"enabled": true}; the mode default must not
// depend on the client having learned about it yet.
func TestVideoConfigEnabledWithoutModeMeansAudioOnly(t *testing.T) {
	t.Parallel()

	var cfg VideoConfig
	if err := json.Unmarshal([]byte(`{"enabled":true}`), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.AllowsAudioOnlyParse() {
		t.Fatalf("cfg = %+v, want audio-only parse enabled", cfg)
	}

	cfg.Mode = "visual"
	if cfg.AllowsAudioOnlyParse() {
		t.Fatal("an unimplemented mode must not silently parse as audio_only")
	}
	cfg.Normalize()
	if cfg.Mode != VideoParseModeAudioOnly || !cfg.AllowsAudioOnlyParse() {
		t.Fatalf("after Normalize = %+v, want audio_only", cfg)
	}
}

// The JSONB value round-trips through the driver.Valuer / sql.Scanner pair the
// repository relies on.
func TestVideoConfigValueAndScanRoundTrip(t *testing.T) {
	t.Parallel()

	want := VideoConfig{Enabled: true, Mode: VideoParseModeAudioOnly}
	value, err := want.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	raw, ok := value.([]byte)
	if !ok {
		t.Fatalf("Value returned %T, want []byte", value)
	}
	var got VideoConfig
	if err := got.Scan(raw); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}

	var nilTarget VideoConfig
	if err := nilTarget.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) must be a no-op: %v", err)
	}
	if nilTarget.Enabled || nilTarget.Mode != "" {
		t.Fatalf("Scan(nil) changed the receiver: %+v", nilTarget)
	}
}

// Both halves are required: the switch says the operator wants video text, the
// ASR model is the only thing that can produce it.
func TestVideoAudioParseEnabledRequiresSwitchAndASR(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		kb   *KnowledgeBase
		want bool
	}{
		{
			name: "nil kb",
			kb:   nil,
			want: false,
		},
		{
			name: "default config",
			kb:   &KnowledgeBase{},
			want: false,
		},
		{
			name: "switch on, no asr",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeAudioOnly},
			},
			want: false,
		},
		{
			name: "switch off, asr configured",
			kb: &KnowledgeBase{
				ASRConfig: ASRConfig{Enabled: true, ModelID: "asr-1"},
			},
			want: false,
		},
		{
			name: "switch on and asr configured",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeAudioOnly},
				ASRConfig:   ASRConfig{Enabled: true, ModelID: "asr-1"},
			},
			want: true,
		},
		{
			name: "switch on with legacy empty mode",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true},
				ASRConfig:   ASRConfig{Enabled: true, ModelID: "asr-1"},
			},
			want: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := VideoAudioParseEnabled(tc.kb); got != tc.want {
				t.Fatalf("VideoAudioParseEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}
