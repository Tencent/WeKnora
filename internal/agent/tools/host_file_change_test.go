package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// hostSink is a host-layout file store that records authorization calls.
type hostSink struct {
	layoutShellExecutor
	files      map[string][]byte
	authorized []sandbox.FileChange
	authErr    error
}

func (s *hostSink) StatSessionFile(_ context.Context, _ string, p string) (*sandbox.RemoteStatEntry, error) {
	if data, ok := s.files[p]; ok {
		return &sandbox.RemoteStatEntry{Path: p, Type: sandbox.RemoteEntryFile, Size: int64(len(data))}, nil
	}
	return nil, errors.New("not found")
}

func (s *hostSink) ReadSessionFile(_ context.Context, _ string, p string) ([]byte, error) {
	if data, ok := s.files[p]; ok {
		return data, nil
	}
	return nil, errors.New("not found")
}

func (s *hostSink) WriteSessionWorkspaceFile(_ context.Context, _ string, p string, content []byte) error {
	s.files[p] = content
	return nil
}

func (s *hostSink) AuthorizeFileChange(
	ctx context.Context, _ string, change sandbox.FileChange,
) (context.Context, error) {
	s.authorized = append(s.authorized, change)
	return ctx, s.authErr
}

func newHostSink() *hostSink {
	return &hostSink{layoutShellExecutor: layoutShellExecutor{layout: hostLayout()}, files: map[string][]byte{}}
}

func TestWriteOutsideTheHostWorkspaceIsAuthorizedFirst(t *testing.T) {
	sink := newHostSink()
	tool := NewWriteSandboxFileTool(sink, 0)
	_, ctx := hostExecContext(t)
	args, _ := json.Marshal(WriteSandboxFileInput{Path: "/Users/dev/Desktop/a.md", Content: "hello"})

	res, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Len(t, sink.authorized, 1)
	require.Equal(t, "overwrite", sink.authorized[0].Mode)
	next, err := sink.authorized[0].Apply(nil, false)
	require.NoError(t, err)
	require.Equal(t, "hello", string(next))
	require.Equal(t, "hello", string(sink.files["/Users/dev/Desktop/a.md"]))
}

func TestWriteInsideTheHostWorkspaceSkipsAuthorization(t *testing.T) {
	sink := newHostSink()
	tool := NewWriteSandboxFileTool(sink, 0)
	_, ctx := hostExecContext(t)
	args, _ := json.Marshal(WriteSandboxFileInput{Path: hostLayout().Root + "/a.md", Content: "x"})

	res, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Empty(t, sink.authorized)
}

func TestARefusedOutsideWriteWritesNothing(t *testing.T) {
	sink := newHostSink()
	sink.authErr = errors.New("[sandbox] the user denied this file write")
	tool := NewWriteSandboxFileTool(sink, 0)
	_, ctx := hostExecContext(t)
	args, _ := json.Marshal(WriteSandboxFileInput{Path: "/Users/dev/Desktop/a.md", Content: "x"})

	res, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	require.False(t, res.Success)
	require.Contains(t, res.Error, "denied")
	require.Empty(t, sink.files)
}

func TestEditOutsideTheHostWorkspaceIsAuthorizedWithTheResult(t *testing.T) {
	sink := newHostSink()
	path := "/Users/dev/Desktop/a.md"
	sink.files[path] = []byte("one two")
	tool := NewEditSandboxFileTool(sink)
	_, ctx := hostExecContext(t)
	args, _ := json.Marshal(EditSandboxFileInput{
		Path: path, Edits: []SandboxEdit{{OldString: "two", NewString: "TWO"}},
	})

	res, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Len(t, sink.authorized, 1)
	require.Equal(t, "edit", sink.authorized[0].Mode)
	require.Contains(t, sink.authorized[0].Preview, "- two")
	require.Contains(t, sink.authorized[0].Preview, "+ TWO")
	next, err := sink.authorized[0].Apply([]byte("one two"), true)
	require.NoError(t, err)
	require.Equal(t, "one TWO", string(next))
}
