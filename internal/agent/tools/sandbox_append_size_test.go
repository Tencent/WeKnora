package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

type appendSizeSink struct {
	size       int64
	content    []byte
	readCalls  int
	writeCalls int
}

func (s *appendSizeSink) StatSessionFile(context.Context, string, string) (*sandbox.RemoteStatEntry, error) {
	return &sandbox.RemoteStatEntry{Type: sandbox.RemoteEntryFile, Size: s.size}, nil
}

func (s *appendSizeSink) ReadSessionFile(context.Context, string, string) ([]byte, error) {
	s.readCalls++
	return s.content, nil
}

func (s *appendSizeSink) WriteSessionWorkspaceFile(_ context.Context, _, _ string, data []byte) error {
	s.writeCalls++
	s.content = data
	return nil
}

func TestWriteSandboxAppendRefusesKnownOversizeBeforeDownloading(t *testing.T) {
	for _, tc := range []struct {
		name  string
		size  int64
		chunk string
	}{
		{name: "already oversized", size: maxSandboxFileBytes + 1, chunk: "x"},
		{name: "full file", size: maxSandboxFileBytes, chunk: "x"},
		{name: "multibyte chunk", size: maxSandboxFileBytes - 2, chunk: "文"},
		{name: "empty append to oversized file", size: maxSandboxFileBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &appendSizeSink{size: tc.size, content: []byte(strings.Repeat("a", int(tc.size)))}
			args, err := json.Marshal(WriteSandboxFileInput{
				Path: "/workspace/output/large.txt", Content: tc.chunk, Mode: "append",
			})
			require.NoError(t, err)
			ctx := WithToolExecContext(t.Context(), &ToolExecContext{SessionID: "append-size"})
			result, err := NewWriteSandboxFileTool(sink, 0).Execute(ctx, args)
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Contains(t, result.Error, "file limit")
			require.Zero(t, sink.readCalls, "the stat already proves the append cannot fit")
			require.Zero(t, sink.writeCalls)
		})
	}
}

func TestWriteSandboxAppendChecksActualSizeAfterStat(t *testing.T) {
	for _, tc := range []struct {
		name       string
		actualSize int
		wantOK     bool
	}{
		{name: "exact fit", actualSize: maxSandboxFileBytes - 1, wantOK: true},
		{name: "grew after stat", actualSize: maxSandboxFileBytes, wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &appendSizeSink{
				size: maxSandboxFileBytes - 1, content: []byte(strings.Repeat("a", tc.actualSize)),
			}
			ctx := WithToolExecContext(t.Context(), &ToolExecContext{SessionID: "append-size"})
			args := json.RawMessage(`{"path":"/workspace/output/large.txt","content":"x","mode":"append"}`)
			result, err := NewWriteSandboxFileTool(sink, 0).Execute(ctx, args)
			require.NoError(t, err)
			require.Equal(t, tc.wantOK, result.Success, result.Error)
			require.Equal(t, 1, sink.readCalls)
			if tc.wantOK {
				require.Equal(t, 1, sink.writeCalls)
				require.Len(t, sink.content, maxSandboxFileBytes)
				require.Equal(t, byte('x'), sink.content[len(sink.content)-1])
			} else {
				require.Contains(t, result.Error, "file limit")
				require.Zero(t, sink.writeCalls)
			}
		})
	}
}
