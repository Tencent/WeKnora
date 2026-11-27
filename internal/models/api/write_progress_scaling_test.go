package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSandboxWriteProgressIncrementalStats(t *testing.T) {
	tests := []struct {
		content string
		lines   int
		preview string
	}{
		{"", 0, ""},
		{"a", 1, "a"},
		{"a\n", 1, "a"},
		{"a\n\n", 2, "a\n"},
		{"\n\n", 2, "\n"},
		{strings.Repeat("line\n", 20), 20, strings.Repeat("line\n", 9) + "line"},
		{strings.Repeat("\n", 20), 20, strings.Repeat("\n", 9)},
		{strings.Repeat("x", 10000), 1, strings.Repeat("x", 10000)},
	}
	for _, tt := range tests {
		raw, err := json.Marshal(map[string]string{"path": "report.txt", "content": tt.content})
		require.NoError(t, err)
		progress := NewSandboxFileProgress(writeSandboxFileName)
		for i := 0; i < len(raw); i++ {
			progress.Feed(string(raw[i : i+1]))
		}
		payload := progress.payload()
		require.Equal(t, "report.txt", payload["path"])
		require.Equal(t, len(tt.content), payload["bytes"])
		require.Equal(t, tt.lines, payload["added_lines"])
		require.Equal(t, tt.preview, progress.preview)
	}
}

func BenchmarkStreamAssemblerWriteFileProgress(b *testing.B) {
	fragment := Delta{ToolCalls: []ToolCallDelta{{Index: 0, Arguments: strings.Repeat("x", 64)}}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		assembler := NewStreamAssembler(context.Background(), "test")
		ch := make(chan types.StreamResponse, 16)
		assembler.Process(ch, Delta{ToolCalls: []ToolCallDelta{{
			Index: 0, ID: "call", Name: writeSandboxFileName,
			Arguments: `{"path":"report.txt","content":"`,
		}}})
		for range 1024 {
			assembler.Process(ch, fragment)
		}
		assembler.Process(ch, Delta{ToolCalls: []ToolCallDelta{{Index: 0, Arguments: `"}`}}})
		assembledArgumentBenchmark = assembler.OrderedToolCalls()[0].Function.Arguments
	}
}
