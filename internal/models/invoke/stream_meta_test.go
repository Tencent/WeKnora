package invoke

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// TestStreamMetaPendingThenProgress pins the A4 revival of v1's stream Data
// producers: the pending notification fires on the first args-continuing
// fragment after the name stabilized, and sandbox write progress flows as
// {path, added_lines, bytes} payloads (never the whole file).
func TestStreamMetaPendingThenProgress(t *testing.T) {
	m := newStreamMetaState()

	// Fragment 1: name + id arrive with the first arguments text — v1
	// semantics defer the PENDING notification to the next fragment, but the
	// stabilized path already rides as a progress payload.
	out := m.feedToolCallDelta(&ToolCallDelta{
		Index: 0, ID: "c1", Name: "write_sandbox_file",
		Arguments: `{"path":"a.md","content":"hello`,
	})
	require.Len(t, out, 1)
	require.Equal(t, types.ResponseTypeToolCall, out[0].ResponseType)
	args, ok := out[0].Data["arguments"].(map[string]any)
	require.True(t, ok, "path 已稳定时进度载荷应随行")
	require.Equal(t, "a.md", args["path"])

	// Fragment 2: args keep arriving → pending notification（载荷省略：
	// bytes-only 变化受 120ms 最小间隔门控，行数未变；content 字符串未闭合）。
	out = m.feedToolCallDelta(&ToolCallDelta{
		Index: 0, Arguments: ` world`,
	})
	require.Len(t, out, 1)
	require.Equal(t, types.ResponseTypeToolCall, out[0].ResponseType)
	require.Equal(t, "write_sandbox_file", out[0].Data["tool_name"])
	require.Equal(t, "c1", out[0].Data["tool_call_id"])
	_, hasArgs := out[0].Data["arguments"]
	require.False(t, hasArgs, "pending 通知仅在同帧有新进度时携带载荷")

	// Fragment 3: line count changes → progress notification（行数变化
	// 不受时间门控）。
	out = m.feedToolCallDelta(&ToolCallDelta{
		Index: 0, Arguments: `!\nsecond line"}`,
	})
	require.NotEmpty(t, out)
	progress := out[len(out)-1]
	require.Equal(t, types.ResponseTypeToolCall, progress.ResponseType)
	args, ok = progress.Data["arguments"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 2, args["added_lines"])
	require.Greater(t, args["bytes"], 0)
}

// TestStreamMetaThinkingToolThought pins the thinking-tool thought echo:
// streamed "thought" argument fragments re-emerge as thinking chunks tagged
// Data["source"]="thinking_tool" (think.go routes them to thought events).
func TestStreamMetaThinkingToolThought(t *testing.T) {
	m := newStreamMetaState()

	out := m.feedToolCallDelta(&ToolCallDelta{
		Index: 0, ID: "t1", Name: "thinking",
		Arguments: `{"thought":"he`,
	})
	// Fragment 1 carries the name → no pending (v1 semantics), but the
	// thought chunk flows.
	var thought *types.StreamResponse
	for i := range out {
		if out[i].ResponseType == types.ResponseTypeThinking {
			thought = &out[i]
		}
	}
	require.NotNil(t, thought, "first fragment should stream the thought prefix")
	require.Equal(t, "he", thought.Content)
	require.Equal(t, "thinking_tool", thought.Data["source"])
	require.Equal(t, "t1", thought.Data["tool_call_id"])

	// Fragment 2 fires the pending notification AND the thought tail.
	out = m.feedToolCallDelta(&ToolCallDelta{Index: 0, Arguments: `llo"}`})
	require.Len(t, out, 2)
	require.Equal(t, types.ResponseTypeThinking, out[1].ResponseType)
	require.Equal(t, "llo", out[1].Content)
}
