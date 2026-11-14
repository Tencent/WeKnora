package chat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOllamaRuntimeNoticePreservesHistory(t *testing.T) {
	client := &OllamaChat{modelName: "local"}
	messages := []Message{{Role: "system", Content: "Stable policy"}, {Role: "user", Content: "Task"}}
	opts := &ChatOptions{PromptCacheKey: "session"}
	before := client.buildChatRequest(messages, opts, true)
	retryOpts := *opts
	retryOpts.RuntimeNotice = `<runtime_event>{"source":"runtime","type":"empty_response_retry"}</runtime_event>`
	retry := client.buildChatRequest(messages, &retryOpts, true)
	require.Len(t, retry.Messages, len(before.Messages)+1)
	require.Equal(t, before.Messages, retry.Messages[:len(before.Messages)])
	require.Equal(t, retryOpts.RuntimeNotice, retry.Messages[len(before.Messages)].Content)
	require.Equal(t, before, client.buildChatRequest(messages, opts, true))
	require.Len(t, messages, 2)
}
