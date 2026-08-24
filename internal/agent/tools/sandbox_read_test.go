package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSandboxFileSource struct {
	stat      *sandbox.RemoteStatEntry
	statErr   error
	data      []byte
	readErr   error
	entries   []sandbox.RemoteDirEntry
	statCalls int
	readCalls int
}

func (f *fakeSandboxFileSource) ListSessionFiles(context.Context, string, string) ([]sandbox.RemoteDirEntry, error) {
	return f.entries, nil
}

func (f *fakeSandboxFileSource) StatSessionFile(context.Context, string, string) (*sandbox.RemoteStatEntry, error) {
	f.statCalls++
	return f.stat, f.statErr
}

func (f *fakeSandboxFileSource) ReadSessionFile(context.Context, string, string) ([]byte, error) {
	f.readCalls++
	return f.data, f.readErr
}

func sandboxFileTestContext() context.Context {
	return WithToolExecContext(context.Background(), &ToolExecContext{SessionID: "session-1"})
}

func TestReadSandboxFileRefusesOversizeBeforeRead(t *testing.T) {
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{Path: "/workspace/output/large.txt", Type: sandbox.RemoteEntryFile, Size: maxReadSandboxMaxBytes + 1},
		data: []byte("must not be read"),
	}

	result, err := NewReadSandboxFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/output/large.txt","max_bytes":999999}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 1, source.statCalls)
	assert.Zero(t, source.readCalls)
	assert.Equal(t, true, result.Data["read_refused"])
	assert.Equal(t, int64(maxReadSandboxMaxBytes+1), result.Data["size"])
	assert.Contains(t, result.Output, "shell_exec")
}

func TestReadSandboxFileReturnsSmallTextOnlyInOutput(t *testing.T) {
	content := []byte("hello sandbox\n")
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{Path: "/workspace/output/report.txt", Type: sandbox.RemoteEntryFile, Size: int64(len(content))},
		data: content,
	}

	result, err := NewReadSandboxFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/output/report.txt"}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 1, source.readCalls)
	assert.Contains(t, result.Output, string(content))
	_, duplicated := result.Data["content"]
	assert.False(t, duplicated)
}

// The output-directory guard is a string prefix test, so a symlink planted
// under that directory satisfies it while pointing anywhere. The backends stat
// the final component without following it, and this is the check that turns
// that into a refusal before any read is attempted.
//
// The path here names the link itself, which is the case this actually covers.
// A link used as an intermediate component is resolved by the kernel and still
// stats as a regular file; see the note in Execute for why that is a convention
// leak rather than a privilege one.
func TestReadSandboxFileRefusesNonRegularFile(t *testing.T) {
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/output/esc",
			Type: sandbox.RemoteEntryOther,
			Size: 4,
		},
		data: []byte("must not be read"),
	}

	result, err := NewReadSandboxFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/output/esc"}`),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Zero(t, source.readCalls, "a non-regular path must never be downloaded")
	assert.Contains(t, result.Error, "not a regular file")
}

func TestReadSandboxFileSuppressesBinaryWithoutBase64(t *testing.T) {
	content := []byte{0xff, 0x00, 0x01}
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{Path: "/workspace/output/image.bin", Type: sandbox.RemoteEntryFile, Size: int64(len(content))},
		data: content,
	}

	result, err := NewReadSandboxFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/output/image.bin"}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, true, result.Data["binary"])
	assert.NotContains(t, result.Output, string(content))
	assert.Contains(t, result.Output, "content suppressed")
	_, hasBase64 := result.Data["content_base64"]
	assert.False(t, hasBase64)
}

func TestListSandboxFilesHardCapsEntries(t *testing.T) {
	entries := make([]sandbox.RemoteDirEntry, 600)
	for i := range entries {
		entries[i] = sandbox.RemoteDirEntry{
			Name: fmt.Sprintf("%03d.txt", i),
			Path: fmt.Sprintf("/workspace/output/%03d.txt", i),
			Type: sandbox.RemoteEntryFile,
		}
	}
	source := &fakeSandboxFileSource{entries: entries}

	result, err := NewListSandboxFilesTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"max_entries":999999}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, maxListSandboxMaxEntries, result.Data["count"])
	assert.Equal(t, true, result.Data["truncated"])
	assert.Equal(t, maxListSandboxMaxEntries, strings.Count(result.Output, "\n- "))
}
