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
	listedDir string
}

func (f *fakeSandboxFileSource) ListSessionFiles(_ context.Context, _, dir string) ([]sandbox.RemoteDirEntry, error) {
	f.listedDir = dir
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

func TestReadSandboxFileAllowsSessionInput(t *testing.T) {
	content := []byte("uploaded report\n")
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/input/ab12cd/report.txt",
			Type: sandbox.RemoteEntryFile,
			Size: int64(len(content)),
		},
		data: content,
	}

	result, err := NewReadSandboxFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/input/ab12cd/report.txt"}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 1, source.readCalls)
	assert.Contains(t, result.Output, string(content))
	assert.Equal(t, sandbox.SessionInputRoot, result.Data["root"])
}

// A file the agent wrote itself with write_sandbox_file must be readable
// again. The writers accept anything under /workspace outside the attachment
// tree, so readers that stopped at /workspace/output left the agent unable to
// re-read its own scratch script.
func TestReadSandboxFileAllowsWorkspaceScratchFile(t *testing.T) {
	content := []byte("print('hi')\n")
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/report.py",
			Type: sandbox.RemoteEntryFile,
			Size: int64(len(content)),
		},
		data: content,
	}

	result, err := NewReadSandboxFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/report.py"}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Output, string(content))
	assert.Equal(t, sandbox.SessionWorkspaceRoot, result.Data["root"])
}

func TestReadSandboxFileRefusesOutsideInspectableRoots(t *testing.T) {
	source := &fakeSandboxFileSource{
		data: []byte("must not be read"),
		stat: &sandbox.RemoteStatEntry{Path: "/etc/passwd", Type: sandbox.RemoteEntryFile, Size: 4},
	}

	for _, path := range []string{
		"/etc/passwd",
		"/home/user/.ssh/id_rsa",
		"/opt/weknora/tenant/skills/pdf/SKILL.md",
	} {
		result, err := NewReadSandboxFileTool(source).Execute(
			sandboxFileTestContext(),
			json.RawMessage(`{"path":"`+path+`"}`),
		)
		require.NoError(t, err, path)
		require.False(t, result.Success, path)
		assert.Contains(t, result.Error, "outside that scope", path)
	}
	assert.Zero(t, source.readCalls)
	assert.Zero(t, source.statCalls)
}

func TestListSandboxFilesDefaultsToOutput(t *testing.T) {
	source := &fakeSandboxFileSource{}

	result, err := NewListSandboxFilesTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, sandboxInspectableRoots()[0], source.listedDir)
	assert.Equal(t, sandboxInspectableRoots()[0], result.Data["path"])
}

func TestListSandboxFilesAllowsSessionInput(t *testing.T) {
	source := &fakeSandboxFileSource{
		entries: []sandbox.RemoteDirEntry{{
			Name: "report.txt",
			Path: "/workspace/input/ab12cd/report.txt",
			Type: sandbox.RemoteEntryFile,
		}},
	}

	result, err := NewListSandboxFilesTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/input"}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, sandbox.SessionInputRoot, source.listedDir)
	assert.Equal(t, sandbox.SessionInputRoot, result.Data["root"])
	assert.Equal(t, 1, result.Data["count"])
}

func TestListSandboxFilesRefusesOutsideInspectableRoots(t *testing.T) {
	source := &fakeSandboxFileSource{}

	result, err := NewListSandboxFilesTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/etc"}`),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "outside that scope")
	assert.Empty(t, source.listedDir)
}

func TestListSandboxFilesRedirectsSkillImagePaths(t *testing.T) {
	source := &fakeSandboxFileSource{}
	result, err := NewListSandboxFilesTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/opt/weknora/tenant/skills/ppt-generator"}`),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "outside that scope")
	assert.Contains(t, result.Error, `read_skill(skill_name="ppt-generator")`)
	assert.Contains(t, result.Error, "Do not ls")
	assert.Empty(t, source.listedDir)
}

func TestReadSandboxFileRedirectsSkillImagePaths(t *testing.T) {
	source := &fakeSandboxFileSource{
		data: []byte("must not be read"),
		stat: &sandbox.RemoteStatEntry{Path: "/opt/weknora/tenant/skills/ppt-generator/scripts/generate_ppt.py", Type: sandbox.RemoteEntryFile, Size: 4},
	}
	result, err := NewReadSandboxFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/opt/weknora/tenant/skills/ppt-generator/scripts/generate_ppt.py"}`),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, `file_path="scripts/generate_ppt.py"`)
	assert.Zero(t, source.readCalls)
}
