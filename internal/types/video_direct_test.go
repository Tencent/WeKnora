package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// video_direct is a stored mode, so it has to survive Normalize, the JSONB
// column and Equal; an unknown mode still coalesces to the default rather than
// running a read the pipeline cannot perform.
func TestVideoDirectModeSurvivesNormalizeAndJSON(t *testing.T) {
	t.Parallel()

	cfg := VideoConfig{
		Enabled:      true,
		Mode:         " video_direct ",
		VideoModelID: "  video-1  ",
		DirectPrompt: "  重点关注菜单。  ",
	}
	cfg.Normalize()

	if cfg.Mode != VideoParseModeDirect {
		t.Fatalf("mode = %q, want %q", cfg.Mode, VideoParseModeDirect)
	}
	if cfg.VideoModelID != "video-1" {
		t.Fatalf("video model id = %q, want the trimmed id", cfg.VideoModelID)
	}
	if cfg.DirectPrompt != "重点关注菜单。" {
		t.Fatalf("direct prompt = %q, want the trimmed instructions", cfg.DirectPrompt)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"mode":"video_direct"`, `"video_model_id":"video-1"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("marshalled video_config = %s, want %s", raw, want)
		}
	}

	// A row that never configured the direct mode keeps its old JSON shape:
	// the two new fields are omitted instead of appearing as empty strings.
	var legacy VideoConfig
	if err := json.Unmarshal([]byte(`{"enabled":true,"mode":"audio_only","model_id":"asr-1"}`), &legacy); err != nil {
		t.Fatalf("decode legacy row: %v", err)
	}
	legacy.Normalize()
	raw, err = json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy row: %v", err)
	}
	if strings.Contains(string(raw), "video_model_id") || strings.Contains(string(raw), "direct_prompt") {
		t.Fatalf("legacy row gained empty fields: %s", raw)
	}
}

// The new fields are part of the setting: adding or clearing a video model must
// mark the section changed so the update path persists it.
func TestVideoConfigEqualTracksTheDirectFields(t *testing.T) {
	t.Parallel()

	base := VideoConfig{Enabled: true, Mode: VideoParseModeDirect, VideoModelID: "video-1"}
	if !base.Equal(base) {
		t.Fatal("a config must equal itself")
	}
	other := base
	other.VideoModelID = "video-2"
	if base.Equal(other) {
		t.Fatal("a different video model is a change")
	}
	other = base
	other.DirectPrompt = "只看后半段"
	if base.Equal(other) {
		t.Fatal("different direct instructions are a change")
	}
}

// Each mode still selects exactly what it reads: the direct mode turns on only
// the direct source, and it needs the video section's own video model.
func TestVideoDirectModeSelectsOnlyTheVideoModel(t *testing.T) {
	t.Parallel()

	direct := VideoConfig{Enabled: true, Mode: VideoParseModeDirect, VideoModelID: "video-1"}
	if !direct.AllowsDirectParse() {
		t.Fatal("video_direct must select the direct source")
	}
	if direct.AllowsAudioParse() || direct.AllowsKeyframeParse() {
		t.Fatal("video_direct must not also run the narration or frame sources")
	}
	if !direct.HasOwnVideoModel() {
		t.Fatal("a configured video model must count")
	}
	if (VideoConfig{Enabled: true, Mode: VideoParseModeDirect}).HasOwnVideoModel() {
		t.Fatal("an empty video model must not count")
	}
	if (VideoConfig{Enabled: false, Mode: VideoParseModeDirect, VideoModelID: "video-1"}).
		AllowsDirectParse() {
		t.Fatal("the switch still wins")
	}
	// An unknown mode must not silently turn the direct read on.
	if (VideoConfig{Enabled: true, Mode: "direct", VideoModelID: "video-1"}).AllowsDirectParse() {
		t.Fatal("only the exact stored value selects the direct mode")
	}
}

// The import gate follows the mode: video_direct is parseable only with a video
// model of its own, and neither of the other two models stands in for it.
func TestVideoAudioParseEnabledForDirectMode(t *testing.T) {
	t.Parallel()

	vlm := VLMConfig{Enabled: true, ModelID: "vlm-1"}
	cases := []struct {
		name string
		kb   *KnowledgeBase
		want bool
	}{
		{
			name: "direct without any model",
			kb:   &KnowledgeBase{VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeDirect}},
			want: false,
		},
		{
			name: "direct with the video model",
			kb: &KnowledgeBase{VideoConfig: VideoConfig{
				Enabled: true, Mode: VideoParseModeDirect, VideoModelID: "video-1",
			}},
			want: true,
		},
		{
			name: "direct with only the video asr model",
			kb: &KnowledgeBase{VideoConfig: VideoConfig{
				Enabled: true, Mode: VideoParseModeDirect, ModelID: "asr-video",
			}},
			want: false,
		},
		{
			name: "direct with only the frame vlm",
			kb: &KnowledgeBase{VideoConfig: VideoConfig{
				Enabled: true, Mode: VideoParseModeDirect, VLMConfig: &vlm,
			}},
			want: false,
		},
		{
			name: "direct with only the audio-section asr model",
			kb: &KnowledgeBase{
				VideoConfig: VideoConfig{Enabled: true, Mode: VideoParseModeDirect},
				ASRConfig:   ASRConfig{Enabled: true, ModelID: "asr-audio"},
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

// The direct prompt is user-authored guidance, so it goes through the same
// length limit as every other instruction field, and Normalize trims it.
func TestVideoDirectPromptIsValidatedLikeOtherInstructions(t *testing.T) {
	t.Parallel()

	kb := &KnowledgeBase{VideoConfig: VideoConfig{
		DirectPrompt: strings.Repeat("长", MaxCustomPromptInstructionsLength+1),
	}}
	if err := ValidateKnowledgeBasePromptInstructions(kb); err == nil {
		t.Fatal("an over-long direct prompt must be rejected")
	}

	kb.VideoConfig.DirectPrompt = "  关注命令输出  "
	NormalizeKnowledgeBasePromptInstructions(kb)
	if kb.VideoConfig.DirectPrompt != "关注命令输出" {
		t.Fatalf("prompt = %q, want the trimmed value", kb.VideoConfig.DirectPrompt)
	}
	if err := ValidateKnowledgeBasePromptInstructions(kb); err != nil {
		t.Fatalf("a normal prompt must be accepted: %v", err)
	}
}
