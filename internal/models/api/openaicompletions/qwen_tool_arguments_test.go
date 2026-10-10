package openaicompletions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func replayedToolCall(arguments string) []api.Message {
	return []api.Message{{
		Role: "assistant",
		ToolCalls: []api.ToolCall{{
			ID: "call_weather",
			Function: api.FunctionCall{
				Name:      "get_weather",
				Arguments: arguments,
			},
		}},
	}}
}

func replayedArguments(t *testing.T, body map[string]any) any {
	t.Helper()
	toolCalls := body["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)
	return toolCalls[0].(map[string]any)["function"].(map[string]any)["arguments"]
}

const successfulChatResponse = `{"choices":[{"index":0,"message":` +
	`{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

const successfulStreamResponse = "data: {\"choices\":[{\"index\":0,\"delta\":" +
	"{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func TestToolCallArguments_DefaultOpenAIString(t *testing.T) {
	c := newClient(t, nil)
	body := bodyJSON(t, c, replayedToolCall(`{"city":"Paris"}`), nil, false)
	assert.Equal(t, `{"city":"Paris"}`, replayedArguments(t, body))
}

func TestToolCallArguments_QwenCompatibilityObject(t *testing.T) {
	c := newClient(t, func(c *Config) {
		c.Settings.ToolCallArgumentsAsObject = true
	})
	body := bodyJSON(t, c, replayedToolCall(`{"city":"Paris","days":2}`), nil, false)
	assert.Equal(t, map[string]any{"city": "Paris", "days": float64(2)}, replayedArguments(t, body))
}

func TestToolCallArguments_PreservesNumbersOnWire(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	for _, asObject := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, input := range []string{
				`{"id":9007199254740993}`,
				`{"nested":{"ids":[9007199254740991,9007199254740992,9007199254740993,` +
					`-9007199254740993,18446744073709551615],` +
					`"decimal":0.1234567890123456789,"exponent":1.234567890123456789e+30}}`,
				`{"large":1e400,"small":1e-400}`,
				`{"zero":0,"negativeZero":-0,"string":"9007199254740993",` +
					`"bool":false,"null":null,"object":{},"array":[]}`,
				`{}`,
			} {
				t.Run(fmt.Sprintf("object=%t/stream=%t/%s", asObject, stream, input), func(t *testing.T) {
					requests := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests++
						var body map[string]any
						decoder := json.NewDecoder(r.Body)
						decoder.UseNumber()
						if !assert.NoError(t, decoder.Decode(&body)) {
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						arguments := replayedArguments(t, body)
						if asObject {
							var want map[string]any
							decoder = json.NewDecoder(strings.NewReader(input))
							decoder.UseNumber()
							assert.NoError(t, decoder.Decode(&want))
							assert.Equal(t, want, arguments, "numeric tokens must survive HTTP serialization exactly")
						} else {
							assert.Equal(t, input, arguments)
						}
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = w.Write([]byte(successfulStreamResponse))
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = w.Write([]byte(successfulChatResponse))
						}
					}))
					defer server.Close()
					settings := api.DefaultOpenAICompletions()
					settings.ToolCallArgumentsAsObject = asObject
					c := New(Config{Endpoint: api.Endpoint{BaseURL: server.URL, Model: "test"}, Settings: settings})
					if stream {
						ch, err := c.ChatStream(context.Background(), replayedToolCall(input), nil)
						require.NoError(t, err)
						var content string
						for chunk := range ch {
							content += chunk.Content
						}
						assert.Equal(t, "ok", content)
					} else {
						response, err := c.Chat(context.Background(), replayedToolCall(input), nil)
						require.NoError(t, err)
						assert.Equal(t, "ok", response.Content)
					}
					assert.Equal(t, 1, requests)
				})
			}
		}
	}
}

func TestToolCallArguments_QwenCompatibilityRejectsInvalidValues(t *testing.T) {
	c := newClient(t, func(c *Config) {
		c.Settings.ToolCallArgumentsAsObject = true
	})
	for _, tc := range []struct {
		name      string
		arguments string
		want      string
	}{
		{name: "empty", arguments: "", want: "arguments are empty"},
		{name: "whitespace", arguments: "  \n", want: "arguments are empty"},
		{name: "malformed", arguments: `{"city":`, want: "arguments are invalid JSON"},
		{name: "null", arguments: "null", want: "arguments must decode to a JSON object"},
		{name: "array", arguments: `[]`, want: "arguments must decode to a JSON object"},
		{name: "scalar", arguments: `"Paris"`, want: "arguments must decode to a JSON object"},
		{name: "trailing object", arguments: `{} {}`, want: "arguments are invalid JSON"},
		{name: "trailing garbage", arguments: `{} invalid`, want: "arguments are invalid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.BuildRequestBody(replayedToolCall(tc.arguments), nil, false)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
			assert.ErrorContains(t, err, "call_weather")
		})
	}
}

func TestToolCallArguments_AutoNegotiatesObjectMode(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	const input = `{"city":"Paris","id":9007199254740993}`
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "stream"}[stream], func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body map[string]any
				decoder := json.NewDecoder(r.Body)
				decoder.UseNumber()
				require.NoError(t, decoder.Decode(&body))
				arguments := replayedArguments(t, body)
				if requests == 1 {
					assert.Equal(t, input, arguments)
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"detail":"Can only get item pairs from a mapping."}`))
					return
				}
				assert.Equal(t, map[string]any{"city": "Paris", "id": json.Number("9007199254740993")}, arguments)
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte(successfulStreamResponse))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(successfulChatResponse))
			}))
			defer server.Close()

			c := New(Config{
				Endpoint: api.Endpoint{BaseURL: server.URL, Model: "Qwen3.5-2B"},
				Settings: func() api.OpenAICompletionsSettings {
					s := api.DefaultOpenAICompletions()
					s.AutoToolCallArgumentsObject = true
					return s
				}(),
			})
			if stream {
				ch, err := c.ChatStream(context.Background(), replayedToolCall(input), nil)
				require.NoError(t, err)
				chunks := 0
				for range ch {
					chunks++
				}
				assert.Positive(t, chunks)
				ch, err = c.ChatStream(context.Background(), replayedToolCall(input), nil)
				require.NoError(t, err)
				chunks = 0
				for range ch {
					chunks++
				}
				assert.Positive(t, chunks)
			} else {
				_, err := c.Chat(context.Background(), replayedToolCall(input), nil)
				require.NoError(t, err)
				_, err = c.Chat(context.Background(), replayedToolCall(input), nil)
				require.NoError(t, err)
			}
			assert.Equal(t, 3, requests, "the negotiated format should be reused without another failed probe")
			assert.True(t, c.Settings().Settings.ToolCallArgumentsAsObject)
		})
	}
}

func TestToolCallArguments_AutoModeDoesNotRetryUnrelatedErrors(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"backend overloaded"}`))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL, Model: "Qwen3.5-2B"},
		Settings: func() api.OpenAICompletionsSettings {
			s := api.DefaultOpenAICompletions()
			s.AutoToolCallArgumentsObject = true
			return s
		}(),
	})
	_, err := c.Chat(context.Background(), replayedToolCall(`{"city":"Paris"}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend overloaded")
	assert.Equal(t, 1, requests)
}

func TestToolCallArguments_AutoModeKeepsStandardStringWhenAccepted(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, `{"city":"Paris"}`, replayedArguments(t, body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successfulChatResponse))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL, Model: "Qwen3.5-2B"},
		Settings: func() api.OpenAICompletionsSettings {
			s := api.DefaultOpenAICompletions()
			s.AutoToolCallArgumentsObject = true
			return s
		}(),
	})
	_, err := c.Chat(context.Background(), replayedToolCall(`{"city":"Paris"}`), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, requests)
	assert.False(t, c.Settings().Settings.ToolCallArgumentsAsObject)
}

func TestToolCallArguments_AutoModeReportsInvalidJSONAfterNegotiation(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"Can only get item pairs from a mapping"}`))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL, Model: "Qwen3.5-2B"},
		Settings: func() api.OpenAICompletionsSettings {
			s := api.DefaultOpenAICompletions()
			s.AutoToolCallArgumentsObject = true
			return s
		}(),
	})
	_, err := c.Chat(context.Background(), replayedToolCall(`{"city":`), nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "arguments are invalid JSON"), err.Error())
	assert.Equal(t, 1, requests, "invalid object retry must fail before another HTTP request")
}
