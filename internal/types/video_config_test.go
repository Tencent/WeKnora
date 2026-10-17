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
	if zero.AllowsAudioParse() || zero.AllowsKeyframeParse() {
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
	if !cfg.AllowsAudioParse() {
		t.Fatalf("cfg = %+v, want audio-only parse enabled", cfg)
	}

	cfg.Mode = "visual"
	if cfg.AllowsAudioParse() || cfg.AllowsKeyframeParse() {
		t.Fatal("an unimplemented mode must not silently parse as a known mode")
	}
	cfg.Normalize()
	if cfg.Mode != VideoParseModeAudioOnly || !cfg.AllowsAudioParse() {
		t.Fatalf("after Normalize = %+v, want audio_only", cfg)
	}
}

// Every implemented mode survives Normalize and selects exactly the sources it
// claims; an unknown mode is still coalesced to the default rather than
// silently running something the pipeline cannot do.
func TestVideoConfigNormalizeKeepsKnownModes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		stored      string
		wantMode    string
		wantAudio   bool
		wantFrames  bool
		wantUnknown bool
	}{
		{stored: "", wantMode: VideoParseModeAudioOnly, wantAudio: true},
		{stored: " audio_only ", wantMode: VideoParseModeAudioOnly, wantAudio: true},
		{stored: VideoParseModeKeyframes, wantMode: VideoParseModeKeyframes, wantFrames: true},
		{
			stored:   VideoParseModeAudioKeyframes,
			wantMode: VideoParseModeAudioKeyframes, wantAudio: true, wantFrames: true,
		},
		{stored: "scene", wantMode: VideoParseModeAudioOnly, wantAudio: true, wantUnknown: true},
		{stored: "AUDIO_ONLY", wantMode: VideoParseModeAudioOnly, wantAudio: true, wantUnknown: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.stored, func(t *testing.T) {
			t.Parallel()
			cfg := VideoConfig{Enabled: true, Mode: tc.stored}
			cfg.Normalize()
			if cfg.Mode != tc.wantMode {
				t.Fatalf("mode = %q, want %q", cfg.Mode, tc.wantMode)
			}
			if got := cfg.AllowsAudioParse(); got != tc.wantAudio {
				t.Fatalf("AllowsAudioParse() = %v, want %v", got, tc.wantAudio)
			}
			if got := cfg.AllowsKeyframeParse(); got != tc.wantFrames {
				t.Fatalf("AllowsKeyframeParse() = %v, want %v", got, tc.wantFrames)
			}
			if tc.wantUnknown && cfg.Mode != VideoParseModeAudioOnly {
				t.Fatalf("unknown mode %q must normalize to audio_only", tc.stored)
			}
		})
	}

	// EffectiveMode must answer without mutating the stored value: the settings
	// UI and the worker both read rows that were never normalized.
	var raw VideoConfig
	if got := raw.EffectiveMode(); got != VideoParseModeAudioOnly {
		t.Fatalf("EffectiveMode() on empty mode = %q, want audio_only", got)
	}
	raw.Enabled = true
	raw.Mode = VideoParseModeKeyframes
	if got := raw.EffectiveMode(); got != VideoParseModeKeyframes {
		t.Fatalf("EffectiveMode() = %q, want keyframes", got)
	}
	if raw.Mode != VideoParseModeKeyframes {
		t.Fatal("EffectiveMode must not rewrite the stored mode")
	}
}

// Only an explicit, known mode turns a source on: a disabled switch never does,
// whatever the mode says.
func TestVideoConfigAllowsMethodsRequireTheSwitch(t *testing.T) {
	t.Parallel()

	modes := []string{VideoParseModeAudioOnly, VideoParseModeKeyframes, VideoParseModeAudioKeyframes}
	for _, mode := range modes {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			cfg := VideoConfig{Mode: mode}
			if cfg.AllowsAudioParse() || cfg.AllowsKeyframeParse() {
				t.Fatalf("disabled %q requested parsing", mode)
			}
			cfg.Enabled = true
			wantAudio := mode == VideoParseModeAudioOnly || mode == VideoParseModeAudioKeyframes
			wantFrames := mode == VideoParseModeKeyframes || mode == VideoParseModeAudioKeyframes
			if got := cfg.AllowsAudioParse(); got != wantAudio {
				t.Fatalf("AllowsAudioParse() = %v, want %v", got, wantAudio)
			}
			if got := cfg.AllowsKeyframeParse(); got != wantFrames {
				t.Fatalf("AllowsKeyframeParse() = %v, want %v", got, wantFrames)
			}
		})
	}
}

// The JSONB value round-trips through the driver.Valuer / sql.Scanner pair the
// repository relies on, including the video-specific model id.
func TestVideoConfigValueAndScanRoundTrip(t *testing.T) {
	t.Parallel()

	want := VideoConfig{
		Enabled: true,
		Mode:    VideoParseModeAudioKeyframes,
		ModelID: "asr-video",
	}
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

// Each mode needs its own text source: audio modes need the video section's
// own ASR model, keyframe modes need a VLM model. The switch alone never
// suffices, the audio section's ASR model does not satisfy the video, and the
// video model id is presence-checked only — type validation happens in the
// worker.
func TestVideoAudioParseEnabledPerMode(t *testing.T) {
	t.Parallel()

	asr := ASRConfig{Enabled: true, ModelID: "asr-1"}
	vlm := VLMConfig{Enabled: true, ModelID: "vlm-1"}
	cases := []struct {
		name string
		kb   *KnowledgeBase
		want bool
	}{
		{name: "nil kb", kb: nil, want: false},
		{name: "default config", kb: &KnowledgeBase{}, want: false},
		{
			name: "switch on, no model",
			kb:   &KnowledgeBase{VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeAudioOnly}},
			want: false,
		},
		{
			name: "switch off, audio model configured",
			kb:   &KnowledgeBase{ASRConfig: asr},
			want: false,
		},
		{
			name: "audio_only with only the audio-section asr model",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeAudioOnly},
				ASRConfig:   asr,
			},
			want: false,
		},
		{
			name: "audio_only with the video's own model and no audio model",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{
					Enabled: true, Mode: VideoParseModeAudioOnly, ModelID: "asr-video",
				},
			},
			want: true,
		},
		{
			name: "legacy empty mode with only the audio-section asr model",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true},
				ASRConfig:   asr,
			},
			want: false,
		},
		{
			name: "legacy empty mode with the video's own model",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true, ModelID: "asr-video"},
			},
			want: true,
		},
		{
			name: "keyframes with vlm",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeKeyframes},
				VLMConfig:   vlm,
			},
			want: true,
		},
		{
			name: "keyframes without vlm",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeKeyframes},
				ASRConfig:   asr,
			},
			want: false,
		},
		{
			name: "audio_and_keyframes with only the audio-section asr",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeAudioKeyframes},
				ASRConfig:   asr,
			},
			want: false,
		},
		{
			name: "audio_and_keyframes with the video's own model",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{
					Enabled: true, Mode: VideoParseModeAudioKeyframes, ModelID: "asr-video",
				},
			},
			want: true,
		},
		{
			name: "audio_and_keyframes with only vlm",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeAudioKeyframes},
				VLMConfig:   vlm,
			},
			want: true,
		},
		{
			name: "audio_and_keyframes without any model",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeAudioKeyframes},
			},
			want: false,
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

// The video section may name its own ASR model; an empty id means the video
// cannot transcribe — there is no audio-section fallback.
func TestVideoConfigHasOwnASRModel(t *testing.T) {
	t.Parallel()

	if (VideoConfig{}).HasOwnASRModel() {
		t.Fatal("an empty model id must not report a video model")
	}
	if (VideoConfig{ModelID: "   "}).HasOwnASRModel() {
		t.Fatal("a whitespace model id must not report a video model")
	}
	if !(VideoConfig{ModelID: "asr-video"}).HasOwnASRModel() {
		t.Fatal("a configured model id must be used")
	}
}

// The language hint belongs to the video section: Normalize trims it and keeps
// an empty value as the auto-detect signal.
func TestVideoConfigNormalizeKeepsItsOwnLanguage(t *testing.T) {
	t.Parallel()

	config := VideoConfig{Language: "  zh  "}
	config.Normalize()
	if config.Language != "zh" {
		t.Fatalf("Language = %q, want %q", config.Language, "zh")
	}

	empty := VideoConfig{Language: "   "}
	empty.Normalize()
	if empty.Language != "" {
		t.Fatalf("Language = %q, want the empty auto-detect value", empty.Language)
	}
	if (VideoConfig{}).Language != "" {
		t.Fatal("a zero VideoConfig must mean auto-detect")
	}
}
