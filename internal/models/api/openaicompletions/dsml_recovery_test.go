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
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dsmlTestBlock(parameters string) string {
	return `<｜DSML｜function_calls><｜DSML｜invoke name="search_knowledge">` + parameters +
		`</｜DSML｜invoke></｜DSML｜function_calls>`
}

// Exercise both public entry points so the tests also cover request options,
// streamed marker boundaries, native calls and the final finish reason.
func TestChatDSMLRecovery(t *testing.T) {
	parameter := `<｜DSML｜parameter name="query" string="true">example</｜DSML｜parameter>`
	block := dsmlTestBlock(parameter)
	tools := []api.Tool{{Type: "function", Function: api.FunctionDef{Name: "search_knowledge"}}}
	opts := &api.Options{Tools: tools}
	sequential := false
	native := []types.LLMToolCall{{
		ID: "native-call", Type: "function",
		Function: types.FunctionCall{Name: "search_knowledge", Arguments: `{"query":"example"}`},
	}}
	cases := []struct {
		name          string
		content       string
		opts          *api.Options
		wantContent   string
		wantArguments []string
		native        []types.LLMToolCall
		nativeTiming  string
		reason        string
		noFinish      bool
		noDone        bool
	}{
		{
			name: "complete", content: block, opts: opts,
			wantArguments: []string{`{"query":"example"}`},
		},
		{
			name: "preamble", content: "先查一下。\n" + block, opts: opts,
			wantContent: "先查一下。\n", wantArguments: []string{`{"query":"example"}`},
		},
		{
			name: "ascii", content: strings.ReplaceAll(block, "｜", "|"), opts: opts,
			wantArguments: []string{`{"query":"example"}`},
		},
		{
			name: "no_arguments", content: dsmlTestBlock(""), opts: opts,
			wantArguments: []string{`{}`},
		},
		{
			name: "parallel", content: dsmlSample, opts: opts, wantContent: "先查一下。\n",
			wantArguments: []string{`{"limit":5,"query":"c2p_tber_0024"}`, `{"query":"c2p_err_0024 是什么故障"}`},
		},
		{
			name: "large_integer", opts: opts,
			content: dsmlTestBlock(`<｜DSML｜parameter name="id" string="false">` +
				`9007199254740993</｜DSML｜parameter>`),
			wantArguments: []string{`{"id":9007199254740993}`},
		},
		{
			name: "nested_json", opts: opts,
			content: dsmlTestBlock(`<｜DSML｜parameter name="filter" string="false">` +
				`{"ids":[9007199254740993],"enabled":true,"missing":null,"ratio":1.234567890123456789}` +
				`</｜DSML｜parameter>`),
			wantArguments: []string{`{"filter":` +
				`{"ids":[9007199254740993],"enabled":true,"missing":null,"ratio":1.234567890123456789}}`},
		},
		{
			name: "raw_string", opts: opts,
			content: dsmlTestBlock(`<｜DSML｜parameter name="query" string="true">  001\path` +
				"\n\"quoted\"  </｜DSML｜parameter>"),
			wantArguments: []string{`{"query":"  001\\path\n\"quoted\"  "}`},
		},
		{
			name: "missing_string_attribute", opts: opts,
			content:       dsmlTestBlock(`<｜DSML｜parameter name="query">example</｜DSML｜parameter>`),
			wantArguments: []string{`{"query":"example"}`},
		},
		{name: "fenced_example", content: "Example:\n```xml\n" + block + "\n```", opts: opts},
		{name: "unclosed_fence", content: "Example:\n```xml\n" + block, opts: opts},
		{name: "tilde_fence", content: "Example:\n~~~xml\n" + block + "\n~~~", opts: opts},
		{name: "inline_example", content: "Example: `" + block + "`", opts: opts},
		{name: "quoted_example", content: "> " + block, opts: opts},
		{name: "indented_example", content: "    " + block, opts: opts},
		{name: "trailing_explanation", content: block + "\nThis is an example.", opts: opts},
		{name: "no_tools", content: block},
		{name: "empty_tools", content: block, opts: &api.Options{}},
		{name: "tools_disabled", content: block, opts: &api.Options{Tools: tools, ToolChoice: "none"}},
		{name: "unknown_tool", content: strings.ReplaceAll(block, "search_knowledge", "other_tool"), opts: opts},
		{name: "different_named_tool", content: block, opts: &api.Options{Tools: tools, ToolChoice: "other_tool"}},
		{
			name: "matching_named_tool", content: block,
			opts:          &api.Options{Tools: tools, ToolChoice: "search_knowledge"},
			wantArguments: []string{`{"query":"example"}`},
		},
		{
			name: "parallel_disabled", content: dsmlSample,
			opts: &api.Options{Tools: tools, ParallelToolCalls: &sequential},
		},
		{
			name: "malformed_parameter", opts: opts,
			content: dsmlTestBlock(parameter + `<｜DSML｜parameter name="knowledge_base_id" string="true">kb-123`),
		},
		{
			name: "invalid_json", opts: opts,
			content: dsmlTestBlock(`<｜DSML｜parameter name="filter" string="false">{"id":</｜DSML｜parameter>`),
		},
		{name: "duplicate_parameter", content: dsmlTestBlock(parameter + parameter), opts: opts},
		{
			name: "incomplete_second_call", opts: opts,
			content: strings.TrimSuffix(block, "</｜DSML｜function_calls>") +
				`<｜DSML｜invoke name="search_knowledge">` + parameter,
		},
		{name: "missing_envelope_end", content: strings.TrimSuffix(block, "</｜DSML｜function_calls>"), opts: opts},
		{
			name: "native_before", content: block, opts: opts, native: native, nativeTiming: "before",
			wantArguments: []string{`{"query":"example"}`}, reason: "tool_calls",
		},
		{
			name: "native_with_finish", content: block, opts: opts, native: native,
			wantArguments: []string{`{"query":"example"}`}, reason: "tool_calls",
		},
		{
			name: "native_with_stop", content: block, opts: opts, native: native,
			wantArguments: []string{`{"query":"example"}`},
		},
		{name: "token_limit", content: block, opts: opts, reason: "length"},
		{name: "filtered", content: block, opts: opts, reason: "content_filter"},
		{name: "incomplete", content: block, opts: opts, reason: types.FinishReasonIncomplete},
		{name: "missing_finish_done", content: block, opts: opts, noFinish: true},
		{name: "missing_finish_eof", content: block, opts: opts, noFinish: true, noDone: true},
		{
			name: "complete_eof", content: block, opts: opts, noDone: true,
			wantArguments: []string{`{"query":"example"}`},
		},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				t.Setenv("SSRF_WHITELIST", "127.0.0.1")
				reason := tc.reason
				if reason == "" && !tc.noFinish {
					reason = "stop"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if !stream {
						_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
							"message":       map[string]any{"content": tc.content, "tool_calls": tc.native},
							"finish_reason": reason,
						}}})
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					write := func(delta map[string]any, finish string) {
						data, err := json.Marshal(map[string]any{"choices": []any{map[string]any{
							"delta": delta, "finish_reason": finish,
						}}})
						if err != nil {
							t.Error(err)
							return
						}
						_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
					}
					if tc.nativeTiming == "before" {
						write(map[string]any{"tool_calls": tc.native}, "")
					}
					for _, r := range tc.content {
						write(map[string]any{"content": string(r)}, "")
					}
					if !tc.noFinish {
						delta := map[string]any{}
						if tc.nativeTiming != "before" {
							delta["tool_calls"] = tc.native
						}
						write(delta, reason)
					}
					_, _ = fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3}}\n\n")
					if !tc.noDone {
						_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
					}
				}))
				defer server.Close()
				c := New(Config{
					Endpoint: api.Endpoint{BaseURL: server.URL, Model: "m"}, Settings: api.DefaultOpenAICompletions(),
				})
				messages := []api.Message{{Role: "user", Content: "test"}}
				var response types.ChatResponse
				if stream {
					ch, err := c.ChatStream(context.Background(), messages, tc.opts)
					require.NoError(t, err)
					for chunk := range ch {
						require.NotEqual(t, types.ResponseTypeError, chunk.ResponseType, chunk.Content)
						response.Content += chunk.Content
						if chunk.Done {
							response.ToolCalls = chunk.ToolCalls
							response.FinishReason = chunk.FinishReason
							if chunk.Usage != nil {
								response.Usage = *chunk.Usage
							}
						}
					}
					assert.Equal(t, 3, response.Usage.PromptTokens)
				} else {
					got, err := c.Chat(context.Background(), messages, tc.opts)
					require.NoError(t, err)
					response = *got
				}
				wantContent := tc.wantContent
				if len(tc.wantArguments) == 0 {
					wantContent = tc.content
				}
				assert.Equal(t, wantContent, response.Content)
				require.Len(t, response.ToolCalls, len(tc.wantArguments))
				for i, arguments := range tc.wantArguments {
					assert.Equal(t, "search_knowledge", response.ToolCalls[i].Function.Name)
					assert.Equal(t, arguments, response.ToolCalls[i].Function.Arguments)
					if len(tc.native) > 0 {
						assert.Equal(t, tc.native[i].ID, response.ToolCalls[i].ID)
					} else {
						assert.True(t, strings.HasPrefix(response.ToolCalls[i].ID, "call_dsml_"))
					}
				}
				wantReason := reason
				if len(tc.wantArguments) > 0 && len(tc.native) == 0 {
					wantReason = "tool_calls"
				} else if stream && tc.noFinish && tc.noDone {
					wantReason = types.FinishReasonIncomplete
				}
				assert.Equal(t, wantReason, response.FinishReason)
			})
		}
	}
}
