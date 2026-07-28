package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestProcessingArtifactConfigSupportsYAMLJSONAndEnvironment(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg := &Config{}
		require.NoError(t, applyProcessingArtifactDefaults(cfg))
		assert.Equal(t, 64<<20, cfg.ProcessingArtifact.MaxPayloadBytes)
		assert.Equal(t, 30, cfg.ProcessingArtifact.RetentionDays)
		assert.Equal(t, 24, cfg.ProcessingArtifact.CleanupIntervalHours)
		assert.Equal(t, 100, cfg.ProcessingArtifact.CleanupBatchSize)
	})

	t.Run("yaml and json", func(t *testing.T) {
		for _, decode := range []struct {
			data   []byte
			decode func([]byte, *Config) error
		}{
			{[]byte("processing_artifact:\n  max_payload_bytes: 3\n"), func(data []byte, cfg *Config) error { return yaml.Unmarshal(data, cfg) }},
			{[]byte(`{"processing_artifact":{"max_payload_bytes":3}}`), func(data []byte, cfg *Config) error { return json.Unmarshal(data, cfg) }},
		} {
			cfg := &Config{}
			require.NoError(t, decode.decode(decode.data, cfg))
			require.NoError(t, applyProcessingArtifactDefaults(cfg))
			assert.Equal(t, 3, cfg.ProcessingArtifact.MaxPayloadBytes)
			assert.Equal(t, 30, cfg.ProcessingArtifact.RetentionDays)
			assert.Equal(t, 24, cfg.ProcessingArtifact.CleanupIntervalHours)
			assert.Equal(t, 100, cfg.ProcessingArtifact.CleanupBatchSize)
		}
	})

	t.Run("explicit zero retention is preserved", func(t *testing.T) {
		cfg := &Config{}
		require.NoError(t, yaml.Unmarshal([]byte("processing_artifact:\n  max_payload_bytes: 3\n  retention_days: 0\n"), cfg))
		require.NoError(t, applyProcessingArtifactDefaults(cfg))
		assert.Equal(t, 0, cfg.ProcessingArtifact.RetentionDays)
		assert.Equal(t, 24, cfg.ProcessingArtifact.CleanupIntervalHours)
		assert.Equal(t, 100, cfg.ProcessingArtifact.CleanupBatchSize)
	})

	t.Run("retention-only section keeps payload default", func(t *testing.T) {
		for _, decode := range []struct {
			data   []byte
			decode func([]byte, *Config) error
		}{
			{[]byte("processing_artifact:\n  retention_days: 0\n"), func(data []byte, cfg *Config) error { return yaml.Unmarshal(data, cfg) }},
			{[]byte(`{"processing_artifact":{"retention_days":0}}`), func(data []byte, cfg *Config) error { return json.Unmarshal(data, cfg) }},
		} {
			cfg := &Config{}
			require.NoError(t, decode.decode(decode.data, cfg))
			require.NoError(t, applyProcessingArtifactDefaults(cfg))
			assert.Equal(t, DefaultProcessingArtifactMaxPayloadBytes, cfg.ProcessingArtifact.MaxPayloadBytes)
			assert.Equal(t, 0, cfg.ProcessingArtifact.RetentionDays)
			require.NoError(t, ValidateConfig(cfg))
		}
	})

	t.Run("environment overrides file", func(t *testing.T) {
		t.Setenv("WEKNORA_PROCESSING_ARTIFACT_MAX_PAYLOAD_BYTES", "5")
		t.Setenv("WEKNORA_PROCESSING_ARTIFACT_RETENTION_DAYS", "0")
		t.Setenv("WEKNORA_PROCESSING_ARTIFACT_CLEANUP_INTERVAL_HOURS", "3")
		t.Setenv("WEKNORA_PROCESSING_ARTIFACT_CLEANUP_BATCH_SIZE", "999")
		cfg := &Config{ProcessingArtifact: &ProcessingArtifactConfig{MaxPayloadBytes: 3, RetentionDays: 1, CleanupIntervalHours: 2, CleanupBatchSize: 4}}
		require.NoError(t, applyProcessingArtifactDefaults(cfg))
		assert.Equal(t, 5, cfg.ProcessingArtifact.MaxPayloadBytes)
		assert.Equal(t, 0, cfg.ProcessingArtifact.RetentionDays)
		assert.Equal(t, 3, cfg.ProcessingArtifact.CleanupIntervalHours)
		assert.Equal(t, 999, cfg.ProcessingArtifact.CleanupBatchSize)
	})
}

func TestProcessingArtifactConfigRejectsInvalidPayloadLimits(t *testing.T) {
	for _, value := range []int{0, -1} {
		err := ValidateConfig(&Config{ProcessingArtifact: &ProcessingArtifactConfig{MaxPayloadBytes: value}})
		assert.Error(t, err)
	}

	for _, data := range [][]byte{
		[]byte("processing_artifact:\n  max_payload_bytes: 0\n"),
		[]byte(`{"processing_artifact":{"max_payload_bytes":-1}}`),
	} {
		cfg := &Config{}
		if data[0] == '{' {
			require.NoError(t, json.Unmarshal(data, cfg))
		} else {
			require.NoError(t, yaml.Unmarshal(data, cfg))
		}
		require.NoError(t, applyProcessingArtifactDefaults(cfg))
		assert.Error(t, ValidateConfig(cfg))
	}

	t.Setenv("WEKNORA_PROCESSING_ARTIFACT_MAX_PAYLOAD_BYTES", "invalid")
	assert.Error(t, applyProcessingArtifactDefaults(&Config{}))
}

func TestProcessingArtifactConfigRejectsInvalidRetentionSettings(t *testing.T) {
	for _, cfg := range []*ProcessingArtifactConfig{
		{MaxPayloadBytes: 1, RetentionDays: -1, CleanupIntervalHours: 24, CleanupBatchSize: 100},
		{MaxPayloadBytes: 1, RetentionDays: 30, CleanupIntervalHours: 0, CleanupBatchSize: 100},
		{MaxPayloadBytes: 1, RetentionDays: 30, CleanupIntervalHours: 24, CleanupBatchSize: 0},
		{MaxPayloadBytes: 1, RetentionDays: 30, CleanupIntervalHours: 24, CleanupBatchSize: 1001},
	} {
		assert.Error(t, ValidateConfig(&Config{ProcessingArtifact: cfg}))
	}

	valid := &ProcessingArtifactConfig{
		MaxPayloadBytes:      DefaultProcessingArtifactMaxPayloadBytes,
		RetentionDays:        MaxProcessingArtifactRetentionDays,
		CleanupIntervalHours: MaxProcessingArtifactCleanupIntervalHours,
		CleanupBatchSize:     100,
	}
	assert.NoError(t, ValidateConfig(&Config{ProcessingArtifact: valid}))
	for _, cfg := range []*ProcessingArtifactConfig{
		{MaxPayloadBytes: 1, RetentionDays: MaxProcessingArtifactRetentionDays + 1, CleanupIntervalHours: 24, CleanupBatchSize: 100},
		{MaxPayloadBytes: 1, RetentionDays: 30, CleanupIntervalHours: MaxProcessingArtifactCleanupIntervalHours + 1, CleanupBatchSize: 100},
	} {
		assert.Error(t, ValidateConfig(&Config{ProcessingArtifact: cfg}))
	}

	for _, name := range []string{
		"WEKNORA_PROCESSING_ARTIFACT_RETENTION_DAYS",
		"WEKNORA_PROCESSING_ARTIFACT_CLEANUP_INTERVAL_HOURS",
		"WEKNORA_PROCESSING_ARTIFACT_CLEANUP_BATCH_SIZE",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "invalid")
			assert.Error(t, applyProcessingArtifactDefaults(&Config{}))
		})
	}

	for _, value := range []string{"", "   "} {
		t.Run("blank override", func(t *testing.T) {
			t.Setenv("WEKNORA_PROCESSING_ARTIFACT_RETENTION_DAYS", value)
			assert.Error(t, applyProcessingArtifactDefaults(&Config{}))
		})
	}
}
