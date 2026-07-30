package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRetrievalConfigMissingFeedbackOptInDefaultsFalse(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		decode func(*RetrievalConfig) error
	}{
		{
			name: "json",
			decode: func(cfg *RetrievalConfig) error {
				return json.Unmarshal([]byte(`{"vector_threshold":0.5}`), cfg)
			},
		},
		{
			name: "yaml",
			decode: func(cfg *RetrievalConfig) error {
				return yaml.Unmarshal([]byte("vector_threshold: 0.5\n"), cfg)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var cfg RetrievalConfig
			require.NoError(t, testCase.decode(&cfg))
			require.False(t, cfg.FeedbackRetrievalWeightEnabled)
		})
	}
}

func TestRetrievalConfigFeedbackOptInRoundTrips(t *testing.T) {
	want := RetrievalConfig{FeedbackRetrievalWeightEnabled: true}
	bytes, err := json.Marshal(want)
	require.NoError(t, err)
	var got RetrievalConfig
	require.NoError(t, json.Unmarshal(bytes, &got))
	require.True(t, got.FeedbackRetrievalWeightEnabled)
}
