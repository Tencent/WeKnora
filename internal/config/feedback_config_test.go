package config

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDefaultFeedbackConfigIsCollectionOnly(t *testing.T) {
	cfg := DefaultFeedbackConfig()
	require.True(t, cfg.Enabled)
	require.False(t, cfg.RetrievalWeightEnabled)
	require.Equal(t, int64(5), cfg.MinimumSampleCount)
	require.NoError(t, cfg.Validate())
	require.True(t, FeedbackCollectionEnabled(nil))
}

func TestFeedbackConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*FeedbackConfig)
	}{
		{"low below zero", func(c *FeedbackConfig) { c.LowRateThreshold = -0.1 }},
		{"high above one", func(c *FeedbackConfig) { c.HighRateThreshold = 1.1 }},
		{"threshold order", func(c *FeedbackConfig) { c.LowRateThreshold = 0.9; c.HighRateThreshold = 0.8 }},
		{"optimization below zero", func(c *FeedbackConfig) { c.OptimizationThreshold = -0.1 }},
		{"minimum samples", func(c *FeedbackConfig) { c.MinimumSampleCount = 0 }},
		{"zero weight", func(c *FeedbackConfig) { c.NormalRecallWeight = 0 }},
		{"negative weight", func(c *FeedbackConfig) { c.LowRecallWeight = -1 }},
		{"nan threshold", func(c *FeedbackConfig) { c.HighRateThreshold = math.NaN() }},
		{"positive infinity", func(c *FeedbackConfig) { c.HighRecallWeight = math.Inf(1) }},
		{"negative infinity", func(c *FeedbackConfig) { c.LowRecallWeight = math.Inf(-1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultFeedbackConfig()
			tt.mutate(cfg)
			require.Error(t, cfg.Validate())
		})
	}
}

func TestFeedbackConfigJSONAndYAMLRoundTrip(t *testing.T) {
	want := DefaultFeedbackConfig()
	jsonBytes, err := json.Marshal(want)
	require.NoError(t, err)
	var fromJSON FeedbackConfig
	require.NoError(t, json.Unmarshal(jsonBytes, &fromJSON))
	require.Equal(t, *want, fromJSON)

	yamlBytes, err := yaml.Marshal(want)
	require.NoError(t, err)
	var fromYAML FeedbackConfig
	require.NoError(t, yaml.Unmarshal(yamlBytes, &fromYAML))
	require.Equal(t, *want, fromYAML)
}

func TestFeedbackPolicyFingerprintIsStableAndPolicyScoped(t *testing.T) {
	first := DefaultFeedbackConfig()
	second := DefaultFeedbackConfig()
	require.Equal(t, first.PolicyFingerprint(), second.PolicyFingerprint())
	second.MinimumSampleCount++
	require.NotEqual(t, first.PolicyFingerprint(), second.PolicyFingerprint())
}

func TestFeedbackViperDefaultsAndEnvironmentOverrides(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	setFeedbackConfigDefaults()
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	var omitted Config
	require.NoError(t, viper.Unmarshal(&omitted))
	applyFeedbackConfig(&omitted)
	require.NotNil(t, omitted.Feedback)
	require.True(t, omitted.Feedback.Enabled)
	require.False(t, omitted.Feedback.RetrievalWeightEnabled)

	t.Setenv("WEKNORA_FEEDBACK_ENABLED", "false")
	t.Setenv("WEKNORA_FEEDBACK_RETRIEVAL_WEIGHT_ENABLED", "true")
	var overridden Config
	require.NoError(t, viper.Unmarshal(&overridden))
	applyFeedbackConfig(&overridden)
	require.NotNil(t, overridden.Feedback)
	require.False(t, overridden.Feedback.Enabled)
	require.True(t, overridden.Feedback.RetrievalWeightEnabled)
}
