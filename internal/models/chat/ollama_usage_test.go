package chat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenUsageFromOllamaCounts(t *testing.T) {
	usage := tokenUsageFromOllamaCounts(1081, 519)
	require.Equal(t, 1081, usage.PromptTokens)
	require.Equal(t, 519, usage.CompletionTokens)
	require.Equal(t, 1600, usage.TotalTokens)
}

func TestTokenUsageFromOllamaCountsClampsInvalidProviderCounts(t *testing.T) {
	usage := tokenUsageFromOllamaCounts(-1, -2)
	require.Zero(t, usage.PromptTokens)
	require.Zero(t, usage.CompletionTokens)
	require.Zero(t, usage.TotalTokens)
}
