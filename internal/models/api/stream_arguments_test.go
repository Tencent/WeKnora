package api

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestStreamAssemblerArgumentSnapshots(t *testing.T) {
	a := NewStreamAssembler(context.Background(), "test")
	ch := make(chan types.StreamResponse, 8)
	a.Process(ch, Delta{ToolCalls: []ToolCallDelta{
		{Index: 1, ID: "one", Name: "lookup", Arguments: `{"q":"`},
		{Index: 4, ID: "four", Name: "lookup", Arguments: `{"q":"other"}`},
	}})
	snapshot := a.OrderedToolCalls()
	for _, fragment := range []string{"中", "🙂", `"}`} {
		a.Process(ch, Delta{ToolCalls: []ToolCallDelta{{Index: 1, Arguments: fragment}}})
	}
	require.Equal(t, `{"q":"`, snapshot[0].Function.Arguments)
	current := a.OrderedToolCalls()
	require.Equal(t, `{"q":"中🙂"}`, current[0].Function.Arguments)
	require.Equal(t, `{"q":"other"}`, current[1].Function.Arguments)
	a.End(ch)
	var terminal types.StreamResponse
	for len(ch) > 0 {
		terminal = <-ch
	}
	require.True(t, terminal.Done)
	require.Equal(t, current, terminal.ToolCalls)
}

func TestStreamAssemblerReplacementArgumentPrefix(t *testing.T) {
	a := NewStreamAssembler(context.Background(), "test")
	ch := make(chan types.StreamResponse, 8)
	a.Process(ch, Delta{ToolCalls: []ToolCallDelta{{Index: 0, ID: "old", Name: "lookup", Arguments: "stale"}}})
	replacement := types.LLMToolCall{
		ID: "new", Type: "function",
		Function: types.FunctionCall{Name: "lookup", Arguments: `{"q":"`},
	}
	a.ReplaceToolCalls([]types.LLMToolCall{replacement})
	a.Process(ch, Delta{ToolCalls: []ToolCallDelta{{Index: 0, Arguments: `new"}`}}})
	require.Equal(t, `{"q":"new"}`, a.OrderedToolCalls()[0].Function.Arguments)
	require.Equal(t, `{"q":"`, replacement.Function.Arguments)
}

func BenchmarkStreamAssemblerFragmentedArguments(b *testing.B) {
	fragment := strings.Repeat("x", 64)
	delta := Delta{ToolCalls: []ToolCallDelta{{Index: 0, Arguments: fragment}}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		a := NewStreamAssembler(context.Background(), "test")
		ch := make(chan types.StreamResponse, 2)
		a.Process(ch, Delta{ToolCalls: []ToolCallDelta{{Index: 0, ID: "call", Name: "lookup"}}})
		for range 1024 {
			a.Process(ch, delta)
		}
		assembledArgumentBenchmark = a.OrderedToolCalls()[0].Function.Arguments
	}
}

var assembledArgumentBenchmark string
