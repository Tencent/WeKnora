package config

import (
	"strings"
	"testing"

	"github.com/mitchellh/mapstructure"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestChunkFeedbackConfigDefaultsAndYAML(t *testing.T) {
	cfg := &Config{}
	applyChunkFeedbackDefaults(cfg)

	require.Equal(t, 0.8, cfg.ChunkFeedback.HighQualityThreshold)
	require.Equal(t, 0.5, cfg.ChunkFeedback.LowQualityThreshold)
	require.Equal(t, 0.3, cfg.ChunkFeedback.AutoMarkThreshold)
	require.Equal(t, 5, cfg.ChunkFeedback.AutoMarkMinFeedbacks)

	var decoded Config
	require.NoError(t, yaml.Unmarshal([]byte(`
chunk_feedback:
  high_quality_threshold: 0.9
  low_quality_threshold: 0.4
  auto_mark_threshold: 0.2
  auto_mark_min_feedbacks: 3
  weight_boost_factor: 1.25
  weight_penalty_factor: 0.75
  min_weight: 0.2
  max_weight: 1.8
`), &decoded))
	require.NotNil(t, decoded.ChunkFeedback)
	require.Equal(t, 0.9, decoded.ChunkFeedback.HighQualityThreshold)
	require.Equal(t, 0.2, decoded.ChunkFeedback.AutoMarkThreshold)
	require.Equal(t, 3, decoded.ChunkFeedback.AutoMarkMinFeedbacks)
	require.Equal(t, 0.75, decoded.ChunkFeedback.WeightPenaltyFactor)
	require.NoError(t, ValidateConfig(&decoded))
}

func TestPartialChunkFeedbackConfigKeepsSafeDefaults(t *testing.T) {
	cfg := Config{}
	applyChunkFeedbackDefaults(&cfg)
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:  &cfg,
		TagName: "yaml",
	})
	require.NoError(t, err)
	require.NoError(t, decoder.Decode(map[string]any{
		"chunk_feedback": map[string]any{
			"auto_mark_threshold": 0.2,
		},
	}))

	require.Equal(t, 0.2, cfg.ChunkFeedback.AutoMarkThreshold)
	require.Equal(t, 0.8, cfg.ChunkFeedback.HighQualityThreshold)
	require.Equal(t, 0.5, cfg.ChunkFeedback.LowQualityThreshold)
	require.Equal(t, 1.5, cfg.ChunkFeedback.WeightBoostFactor)
	require.Equal(t, 0.5, cfg.ChunkFeedback.WeightPenaltyFactor)
	require.Equal(t, 0.1, cfg.ChunkFeedback.MinWeight)
	require.Equal(t, 2.0, cfg.ChunkFeedback.MaxWeight)
	require.Equal(t, 5, cfg.ChunkFeedback.AutoMarkMinFeedbacks)
	require.NoError(t, ValidateConfig(&cfg))
}

func TestValidateChunkFeedbackConfigRejectsUnsafePolicy(t *testing.T) {
	cfg := &Config{}
	applyChunkFeedbackDefaults(cfg)
	cfg.ChunkFeedback.HighQualityThreshold = 0.4
	cfg.ChunkFeedback.LowQualityThreshold = 0.5
	cfg.ChunkFeedback.AutoMarkThreshold = 0.6
	cfg.ChunkFeedback.WeightBoostFactor = 1
	cfg.ChunkFeedback.WeightPenaltyFactor = 0
	cfg.ChunkFeedback.MinWeight = 2
	cfg.ChunkFeedback.MaxWeight = 1
	cfg.ChunkFeedback.AutoMarkMinFeedbacks = 0

	err := ValidateConfig(cfg)

	require.Error(t, err)
	message := err.Error()
	for _, field := range []string{
		"high_quality_threshold",
		"auto_mark_threshold",
		"weight_boost_factor",
		"weight_penalty_factor",
		"auto_mark_min_feedbacks",
		"min_weight",
	} {
		require.True(t, strings.Contains(message, field), "validation error %q should mention %s", message, field)
	}
}
