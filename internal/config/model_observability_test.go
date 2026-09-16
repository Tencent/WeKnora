package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyModelObservabilityDefaults(t *testing.T) {
	t.Setenv("WEKNORA_MODEL_CALL_OBSERVABILITY_ENABLED", "")
	t.Setenv("WEKNORA_MODEL_CALL_RETENTION_DAYS", "")
	cfg := &Config{}
	applyModelObservabilityDefaults(cfg)
	require.True(t, cfg.ModelObservability.IsEnabled())
	require.Equal(t, 30, cfg.ModelObservability.RetentionDays)
	require.Equal(t, 4096, cfg.ModelObservability.QueueSize)
	require.Equal(t, 100, cfg.ModelObservability.BatchSize)
	require.NoError(t, ValidateConfig(cfg))
}

func TestApplyModelObservabilityEnvironmentOverrides(t *testing.T) {
	t.Setenv("WEKNORA_MODEL_CALL_OBSERVABILITY_ENABLED", "false")
	t.Setenv("WEKNORA_MODEL_CALL_RETENTION_DAYS", "45")
	cfg := &Config{}
	applyModelObservabilityDefaults(cfg)
	require.False(t, cfg.ModelObservability.IsEnabled())
	require.Equal(t, 45, cfg.ModelObservability.RetentionDays)
}

func TestValidateModelObservabilityConfig(t *testing.T) {
	enabled := true
	err := ValidateConfig(&Config{ModelObservability: &ModelObservabilityConfig{
		Enabled: &enabled, RetentionDays: -1, QueueSize: 10, BatchSize: 11,
	}})
	require.ErrorContains(t, err, "retention_days must be > 0")
	require.ErrorContains(t, err, "batch_size must not exceed queue_size")
}
