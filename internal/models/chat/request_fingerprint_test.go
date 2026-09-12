package chat

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRequestFingerprintCoversDynamicContentAndOptions(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "stable system prompt"},
		{Role: "user", Content: "first workload"},
	}
	options := &ChatOptions{Temperature: 0.3, MaxTokens: 128, PromptCacheKey: "cache-a"}

	fingerprint := RequestFingerprint(context.Background(), messages, options)
	require.Len(t, fingerprint, 64)
	require.Equal(t, fingerprint, RequestFingerprint(context.Background(), messages, options))

	changedMessages := append([]Message(nil), messages...)
	changedMessages[1].Content = "second workload"
	require.NotEqual(t, fingerprint, RequestFingerprint(context.Background(), changedMessages, options))
	require.Equal(t, PromptPrefixFingerprint(messages, options), PromptPrefixFingerprint(changedMessages, options))

	changedOptions := *options
	changedOptions.PromptCacheKey = "cache-b"
	require.NotEqual(t, fingerprint, RequestFingerprint(context.Background(), messages, &changedOptions))

	withoutExplicitKey := *options
	withoutExplicitKey.PromptCacheKey = ""
	firstSession := types.WithSessionID(context.Background(), "session-a")
	secondSession := types.WithSessionID(context.Background(), "session-b")
	require.NotEqual(t,
		RequestFingerprint(firstSession, messages, &withoutExplicitKey),
		RequestFingerprint(secondSession, messages, &withoutExplicitKey),
	)
}
