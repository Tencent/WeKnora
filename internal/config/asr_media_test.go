package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The ASR cutter (internal/models/asr) reads the deployment's segment ceiling
// and media binaries from the knowledge-base section. The defaults have to keep
// the single-request path for anything a vendor already accepted, which is why
// the ceiling is 120 s and both binaries resolve on the PATH of the application
// container.
func TestApplyKnowledgeBaseEnvOverrides_ASRMedia(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg := &Config{KnowledgeBase: &KnowledgeBaseConfig{}}
		applyKnowledgeBaseEnvOverrides(cfg)

		assert.Equal(t, DefaultASRMaxSegmentDuration, cfg.KnowledgeBase.MaxAudioSegmentDuration)
		assert.Equal(t, "ffmpeg", cfg.KnowledgeBase.FFmpegPath)
		assert.Equal(t, "ffprobe", cfg.KnowledgeBase.FFprobePath)
	})

	t.Run("environment overrides", func(t *testing.T) {
		t.Setenv("WEKNORA_ASR_MAX_SEGMENT_SECONDS", "90")
		t.Setenv("WEKNORA_FFMPEG_PATH", "/opt/bin/ffmpeg")
		t.Setenv("WEKNORA_FFPROBE_PATH", "/opt/bin/ffprobe")
		cfg := &Config{KnowledgeBase: &KnowledgeBaseConfig{}}
		applyKnowledgeBaseEnvOverrides(cfg)

		assert.Equal(t, 90*time.Second, cfg.KnowledgeBase.MaxAudioSegmentDuration)
		assert.Equal(t, "/opt/bin/ffmpeg", cfg.KnowledgeBase.FFmpegPath)
		assert.Equal(t, "/opt/bin/ffprobe", cfg.KnowledgeBase.FFprobePath)
	})

	t.Run("unparsable seconds are ignored", func(t *testing.T) {
		t.Setenv("WEKNORA_ASR_MAX_SEGMENT_SECONDS", "two minutes")
		cfg := &Config{KnowledgeBase: &KnowledgeBaseConfig{MaxAudioSegmentDuration: 45 * time.Second}}
		applyKnowledgeBaseEnvOverrides(cfg)

		assert.Equal(t, 45*time.Second, cfg.KnowledgeBase.MaxAudioSegmentDuration)
	})

	t.Run("configured values win over the defaults", func(t *testing.T) {
		cfg := &Config{KnowledgeBase: &KnowledgeBaseConfig{
			MaxAudioSegmentDuration: 300 * time.Second,
			FFprobePath:             "/usr/local/bin/ffprobe",
		}}
		applyKnowledgeBaseEnvOverrides(cfg)

		assert.Equal(t, 300*time.Second, cfg.KnowledgeBase.MaxAudioSegmentDuration)
		assert.Equal(t, "/usr/local/bin/ffprobe", cfg.KnowledgeBase.FFprobePath)
	})
}
