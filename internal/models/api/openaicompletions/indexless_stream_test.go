package openaicompletions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wantCall is one call of the vendor's non-stream ground truth.
type wantCall struct {
	Name      string
	Arguments string
}

// The real captured streams with delta.tool_calls[].index stripped, plus the
// non-stream message.tool_calls ground truth of the same request. Capture
// provenance: upstream-issues/3570-experiment/captures/.
var indexlessStreamCases = []struct {
	name string
	file string
	want []wantCall
}{
	{name: "deepseek-same-tool", file: "testdata/indexless-deepseek-same-tool.jsonl", want: []wantCall{
		{Name: "get_weather", Arguments: "{\"city\": \"北京\"}"},
		{Name: "get_weather", Arguments: "{\"city\": \"上海\"}"},
	}},
	{name: "deepseek-two-tools", file: "testdata/indexless-deepseek-two-tools.jsonl", want: []wantCall{
		{Name: "get_weather", Arguments: "{\"city\": \"北京\"}"},
		{Name: "get_local_time", Arguments: "{\"city\": \"上海\"}"},
	}},
	{name: "aliyun-same-tool", file: "testdata/indexless-aliyun-same-tool.jsonl", want: []wantCall{
		{Name: "get_weather", Arguments: "{\"city\": \"北京\"}"},
		{Name: "get_weather", Arguments: "{\"city\": \"上海\"}"},
	}},
	{name: "aliyun-two-tools", file: "testdata/indexless-aliyun-two-tools.jsonl", want: []wantCall{
		{Name: "get_weather", Arguments: "{\"city\": \"北京\"}"},
		{Name: "get_local_time", Arguments: "{\"city\": \"上海\"}"},
	}},
}

// drainToolCalls feeds raw through the real SSE loop and returns the last chunk
// that carried assembled tool calls.
func drainToolCalls(t *testing.T, c *Client, raw []byte) types.StreamResponse {
	t.Helper()
	ch := make(chan types.StreamResponse, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(ch)
		c.processStream(context.Background(), bytes.NewReader(raw), ch, nil)
	}()

	var last types.StreamResponse
	for chunk := range ch {
		require.NotEqual(t, types.ResponseTypeError, chunk.ResponseType, chunk.Content)
		if len(chunk.ToolCalls) > 0 {
			last = chunk
		}
	}
	<-done
	return last
}

// End-to-end regression through the real SSE decode path: a gateway that drops
// tool_calls[].index must still yield the two parallel calls the vendor returns
// without streaming. The assembler used to number each entry by its position
// inside the chunk, which merged the calls (doubled name, arguments glued into
// invalid JSON) and lost the whole tool round.
func TestProcessStream_IndexlessParallelToolCalls(t *testing.T) {
	for _, tc := range indexlessStreamCases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(tc.file)
			require.NoError(t, err)

			last := drainToolCalls(t, newClient(t, nil), raw)

			require.Len(t, last.ToolCalls, 2, "parallel tool calls collapsed: %#v", last.ToolCalls)
			for i, want := range tc.want {
				assert.Equal(t, want.Name, last.ToolCalls[i].Function.Name, "call %d name", i)
				assert.Equal(t, want.Arguments, last.ToolCalls[i].Function.Arguments, "call %d arguments", i)
				assert.True(t, json.Valid([]byte(last.ToolCalls[i].Function.Arguments)),
					"call %d arguments are not valid JSON: %q", i, last.ToolCalls[i].Function.Arguments)
			}
			// The id arrives on the first fragment only; the empty ids of the
			// continuation fragments must not wipe it or hand it to a sibling.
			assert.NotEmpty(t, last.ToolCalls[0].ID)
			assert.NotEmpty(t, last.ToolCalls[1].ID)
			assert.NotEqual(t, last.ToolCalls[0].ID, last.ToolCalls[1].ID)
		})
	}
}

// Provider state such as extra_content travels with the delta it came from and
// is filed under the slot that delta resolved to. The old loop re-derived that
// slot from the entry's position inside the chunk, so once the index was gone
// every payload landed on the first call and the others lost their state.
func TestProcessStream_IndexlessToolCallsKeepTheirOwnMetadata(t *testing.T) {
	const stream = `data: {"choices":[{"delta":{"tool_calls":[{"id":"call_a","type":"function",` +
		`"function":{"name":"wiki_read_page","arguments":"{\"slug\":\"g\"}"},` +
		`"extra_content":{"marker":"a"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_b","type":"function",` +
		`"function":{"name":"wiki_replace_text","arguments":"{\"slug\":\"g\"}"},` +
		`"extra_content":{"marker":"b"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		"data: [DONE]\n"

	c := newClient(t, func(c *Config) { c.Settings.ToolCallExtraFields = []string{"extra_content"} })
	last := drainToolCalls(t, c, []byte(stream))

	require.Len(t, last.ToolCalls, 2)
	assert.Equal(t, "wiki_read_page", last.ToolCalls[0].Function.Name)
	assert.Equal(t, "wiki_replace_text", last.ToolCalls[1].Function.Name)
	require.NotNil(t, last.ToolCalls[0].ProviderMetadata)
	require.NotNil(t, last.ToolCalls[1].ProviderMetadata)
	assert.JSONEq(t, `{"marker":"a"}`, string(last.ToolCalls[0].ProviderMetadata["extra_content"]))
	assert.JSONEq(t, `{"marker":"b"}`, string(last.ToolCalls[1].ProviderMetadata["extra_content"]))
}
