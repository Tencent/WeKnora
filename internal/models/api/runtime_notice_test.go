package api_test

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/anthropicmessages"
	"github.com/Tencent/WeKnora/internal/models/api/googlegenai"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/Tencent/WeKnora/internal/models/api/openairesponses"
	"github.com/stretchr/testify/require"
)

type requestBuilder interface {
	BuildRequestBody([]api.Message, *api.Options, bool) (map[string]any, error)
}

func TestRuntimeNoticePreservesWireCachePrefix(t *testing.T) {
	endpoint := api.Endpoint{Model: "test", BaseURL: "https://example.com"}
	settings := api.DefaultOpenAICompletions()
	settings.CacheControlFormat = "anthropic"
	settings.SupportsDeveloperRole = true
	for _, tc := range []struct {
		name, sequence, role string
		client               requestBuilder
	}{
		{
			"completions",
			"messages",
			"system",
			openaicompletions.New(openaicompletions.Config{
				Endpoint: endpoint,
				Settings: api.DefaultOpenAICompletions(),
			}),
		},

		{
			"completions_cache_markers",
			"messages",
			"developer",
			openaicompletions.New(openaicompletions.Config{
				Endpoint:  endpoint,
				Settings:  settings,
				Reasoning: true,
			}),
		},

		{
			"responses",
			"input",
			"developer",
			openairesponses.New(openairesponses.Config{
				Endpoint: endpoint,
				Settings: api.DefaultOpenAIResponses(),
			}),
		},

		{
			"anthropic",
			"messages",
			"user",
			anthropicmessages.New(anthropicmessages.Config{
				Endpoint: endpoint,
				Settings: api.DefaultAnthropicMessages(),
			}),
		},

		{
			"gemini",
			"contents",
			"user",
			googlegenai.New(googlegenai.Config{
				Endpoint: endpoint,
				Settings: api.DefaultGoogleGenerativeAI(),
			}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []api.Message{
				{Role: "system", Content: "Stable policy"},
				{Role: "user", Content: "Original task"},
				{
					Role: "assistant",
					ToolCalls: []api.ToolCall{
						{ID: "call", Type: "function", Function: api.FunctionCall{Name: "read", Arguments: `{}`}},
					},
				},
				{Role: "tool", ToolCallID: "call", Name: "read", Content: "Existing evidence"},
			}
			options := api.Options{
				PromptCacheKey: "same-session",
				Tools: []api.Tool{
					{
						Type:     "function",
						Function: api.FunctionDef{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)},
					},
				},
			}
			original, err := json.Marshal(messages)
			require.NoError(t, err)
			normalize := func(opts *api.Options) map[string]any {
				body, err := tc.client.BuildRequestBody(messages, opts, true)
				require.NoError(t, err)
				data, err := json.Marshal(body)
				require.NoError(t, err)
				var result map[string]any
				require.NoError(t, json.Unmarshal(data, &result))
				return result
			}
			before := normalize(&options)
			fingerprint := api.PromptPrefixFingerprint(messages, &options)
			retryOptions := options
			retryOptions.RuntimeNotice = `<runtime_event>` +
				`{"source":"runtime","type":"empty_response_retry"}</runtime_event>`
			retry := normalize(&retryOptions)
			require.Equal(t, fingerprint, api.PromptPrefixFingerprint(messages, &retryOptions))
			base := before[tc.sequence].([]any)
			extended := retry[tc.sequence].([]any)
			require.Len(t, extended, len(base)+1)
			require.Equal(
				t,
				base,
				extended[:len(base)],
				"all existing wire messages and cache breakpoints must be byte-equivalent",
			)
			tail := extended[len(base)].(map[string]any)
			require.Equal(t, tc.role, tail["role"])
			tailJSON, err := json.Marshal(tail)
			require.NoError(t, err)
			require.Contains(t, string(tailJSON), "empty_response_retry")
			// Compare every other field: system, tools, cache key, generation options.
			retry[tc.sequence] = extended[:len(base)]
			require.Equal(t, before, retry)
			require.Equal(t, before, normalize(&options), "notice must not leak into the next request")
			after, err := json.Marshal(messages)
			require.NoError(t, err)
			require.Equal(t, original, after)
		})
	}
}
