package chat

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestModelFingerprintExcludesChatCredentialsAndConcurrency(t *testing.T) {
	base := &ChatConfig{
		Source:         types.ModelSourceRemote,
		BaseURL:        "https://example.com/v1",
		ModelName:      "chat-model",
		ModelID:        "model-id",
		Provider:       "generic",
		APIKey:         "secret-a",
		AppSecret:      "app-secret-a",
		MaxConcurrency: 4,
	}
	changed := *base
	changed.APIKey = "secret-b"
	changed.AppSecret = "app-secret-b"
	changed.MaxConcurrency = 100
	require.Equal(t, ModelFingerprint(base), ModelFingerprint(&changed))

	changed.ModelName = "different-model"
	require.NotEqual(t, ModelFingerprint(base), ModelFingerprint(&changed))
}
