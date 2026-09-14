package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// Past the download ceiling the file is never fetched: paging it would mean
// pulling megabytes over the wire for every page.
func TestReadFileWorkspaceRefusesOversizeBeforeRead(t *testing.T) {
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/output/large.txt",
			Type: sandbox.RemoteEntryFile,
			Size: maxReadSandboxDownloadBytes + 1,
		},
		data: []byte("must not be read"),
	}

	result, err := NewReadFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/output/large.txt","max_bytes":999999}`),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Equal(t, 1, source.statCalls)
	assert.Zero(t, source.readCalls)
	assert.Equal(t, true, result.Data["read_refused"])
	assert.Equal(t, maxReadSandboxDownloadBytes+1, result.Data["size"])
	assert.Contains(t, result.Output, "shell_exec")
}

// A file over the per-call byte budget but under the download ceiling is
// paginated. write_sandbox_file can build such a file by appending, so
// refusing to read it back would strand the agent's own output.
func TestReadFileWorkspacePaginatesOverBudgetFile(t *testing.T) {
	var content strings.Builder
	const lines = 400
	for i := 1; i <= lines; i++ {
		content.WriteString(strings.Repeat("x", 200))
		content.WriteString("\n")
	}
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/output/deck.html",
			Type: sandbox.RemoteEntryFile,
			Size: int64(content.Len()),
		},
		data: []byte(content.String()),
	}
	tool := NewReadFileTool(source)

	// Follow the offsets the way the model would, and reassemble. The property
	// that matters is that paging recovers the file exactly: no gap between
	// consecutive pages, and no line served twice.
	var seen []string
	offset, pages := 1, 0
	for {
		pages++
		require.Less(t, pages, 50, "paging is not making progress")

		result, err := tool.Execute(sandboxFileTestContext(), json.RawMessage(
			fmt.Sprintf(`{"path":"/workspace/output/deck.html","offset":%d}`, offset)))
		require.NoError(t, err)
		require.True(t, result.Success)
		assert.Equal(t, lines, result.Data["total_lines"])
		assert.Equal(t, offset, result.Data["start_line"])

		body := strings.SplitN(result.Output, "```\n", 2)[1]
		body = strings.TrimSuffix(strings.SplitN(body, "\n```", 2)[0], "\n")
		seen = append(seen, strings.Split(body, "\n")...)

		if result.Data["truncated"] != true {
			assert.Contains(t, result.Output, "end of file")
			assert.NotContains(t, result.Data, "next_offset")
			break
		}
		next := result.Data["next_offset"].(int)
		assert.Contains(t, result.Output, fmt.Sprintf("Use offset=%d to continue.", next))
		assert.Equal(t, result.Data["end_line"].(int)+1, next, "pages must not skip a line")
		offset = next
	}

	assert.Greater(t, pages, 1, "the file was supposed to need several pages")
	assert.Len(t, seen, lines)
	assert.Equal(t, strings.TrimSuffix(content.String(), "\n"), strings.Join(seen, "\n"))
}

// Paging cannot make progress when one line exceeds the whole budget, so the
// result has to name the escape hatch instead of returning an empty page the
// model will retry forever.
func TestReadFileWorkspaceReportsUnpageableLine(t *testing.T) {
	content := []byte(strings.Repeat("y", int(maxReadSandboxMaxBytes)+10) + "\n")
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/output/min.js",
			Type: sandbox.RemoteEntryFile,
			Size: int64(len(content)),
		},
		data: content,
	}

	result, err := NewReadFileTool(source).Execute(sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/output/min.js"}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 0, result.Data["returned_bytes"])
	assert.Contains(t, result.Output, "Line 1 is")
	assert.Contains(t, result.Output, "sed -n '1p'")
}

// The registry truncates over-budget output by deleting the MIDDLE and keeping
// head and tail. A page must never reach that path: the continuation hint sits
// at the tail, so it would survive and vouch for lines that were silently
// dropped out of the middle. The page therefore has to be sized against the
// same rune budget the registry enforces.
//
// ASCII is the case that breaks, not CJK: 64 KiB of Chinese is ~22k runes and
// fits under the ceiling, while 64 KiB of ASCII is 65k runes and does not.
func TestReadFileWorkspacePageSurvivesRegistryTruncation(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 1500; i++ {
		content.WriteString(strings.Repeat("x", 200))
		content.WriteString("\n")
	}
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/output/big.txt",
			Type: sandbox.RemoteEntryFile,
			Size: int64(content.Len()),
		},
		data: []byte(content.String()),
	}

	registry := NewToolRegistry()
	registry.RegisterTool(NewReadFileTool(source))

	result, err := registry.ExecuteTool(sandboxFileTestContext(),
		ToolReadFile, json.RawMessage(`{"path":"/workspace/output/big.txt"}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.NotContains(t, result.Output, "output truncated",
		"the page was over the registry ceiling, so its middle was cut away")
	assert.LessOrEqual(t, utf8.RuneCountInString(result.Output), DefaultMaxToolOutput)
	assert.Contains(t, result.Output, "to continue.")
}

// No sandbox backend exposes a range read, so a page is cut out of a whole
// download. Without a cache, paging a file means downloading it once per page.
func TestReadFileWorkspacePagingDownloadsTheFileOnce(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 1500; i++ {
		content.WriteString(strings.Repeat("x", 200))
		content.WriteString("\n")
	}
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path:    "/workspace/output/big.txt",
			Type:    sandbox.RemoteEntryFile,
			Size:    int64(content.Len()),
			ModTime: time.Unix(1700000000, 0),
		},
		data: []byte(content.String()),
	}
	tool := NewReadFileTool(source)

	offset := 1
	for pages := 0; ; pages++ {
		require.Less(t, pages, 50, "paging is not making progress")
		result, err := tool.Execute(sandboxFileTestContext(), json.RawMessage(
			fmt.Sprintf(`{"path":"/workspace/output/big.txt","offset":%d}`, offset)))
		require.NoError(t, err)
		require.True(t, result.Success)
		if result.Data["truncated"] != true {
			break
		}
		offset = result.Data["next_offset"].(int)
	}

	assert.Equal(t, 1, source.readCalls, "each page re-downloaded the whole file")
	assert.Greater(t, source.statCalls, 1, "every page must still stat, to notice a change")
}

// The cache must never outlive the file it describes. Stat alone cannot see a
// same-length replacement within one mtime tick, so a completed mutation
// invalidates regardless of what stat reports.
func TestReadFileWorkspaceCacheIsDroppedAfterAMutation(t *testing.T) {
	stat := &sandbox.RemoteStatEntry{
		Path:    "/workspace/run.py",
		Type:    sandbox.RemoteEntryFile,
		Size:    9,
		ModTime: time.Unix(1700000000, 0),
	}
	source := &fakeSandboxFileSource{stat: stat, data: []byte("DEBUG = 1")}
	tool := NewReadFileTool(source)
	args := json.RawMessage(`{"path":"/workspace/run.py"}`)

	first, err := tool.Execute(sandboxFileTestContext(), args)
	require.NoError(t, err)
	require.True(t, first.Success)
	assert.Contains(t, first.Output, "DEBUG = 1")

	// Cached: same session, path, size and mtime, and nothing was written.
	_, err = tool.Execute(sandboxFileTestContext(), args)
	require.NoError(t, err)
	assert.Equal(t, 1, source.readCalls)

	// A same-length edit leaves size and mtime untouched, which is exactly the
	// case stat cannot detect.
	lockSandboxFile("session-1", "/workspace/run.py")()
	source.data = []byte("DEBUG = 0")

	third, err := tool.Execute(sandboxFileTestContext(), args)
	require.NoError(t, err)
	assert.Equal(t, 2, source.readCalls, "a completed mutation must drop the cache")
	assert.Contains(t, third.Output, "DEBUG = 0")
}

func TestReadFileWorkspaceCacheIsDroppedAfterShellExec(t *testing.T) {
	stat := &sandbox.RemoteStatEntry{
		Path:    "/workspace/run.py",
		Type:    sandbox.RemoteEntryFile,
		Size:    9,
		ModTime: time.Unix(1700000000, 0),
	}
	source := &fakeSandboxFileSource{stat: stat, data: []byte("DEBUG = 1")}
	reader := NewReadFileTool(source)
	args := json.RawMessage(`{"path":"/workspace/run.py"}`)

	first, err := reader.Execute(sandboxFileTestContext(), args)
	require.NoError(t, err)
	require.True(t, first.Success)
	require.Contains(t, first.Output, "DEBUG = 1")

	_, err = NewShellExecTool(&fakeShellExecutor{result: &sandbox.ExecuteResult{ExitCode: 0}}, nil).
		Execute(sandboxFileTestContext(), json.RawMessage(`{"command":"printf 'DEBUG = 0' > /workspace/run.py"}`))
	require.NoError(t, err)
	source.data = []byte("DEBUG = 0")

	second, err := reader.Execute(sandboxFileTestContext(), args)
	require.NoError(t, err)
	assert.Equal(t, 2, source.readCalls, "shell_exec must drop the workspace read cache")
	assert.Contains(t, second.Output, "DEBUG = 0")
}

// Pages break at line boundaries, which is what makes them UTF-8 safe: 0x0A
// cannot appear inside a multi-byte sequence, so no byte budget can land
// mid-rune.
func TestPaginateSandboxFileNeverSplitsARune(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 100; i++ {
		content.WriteString("这是一行中文文本，用来测试分页边界")
		content.WriteString("\n")
	}

	// A budget that lands in the middle of a line, and so inside a rune if the
	// split were done on raw bytes.
	page := paginateSandboxFile(content.String(), 1, 0, 55, 1000)

	assert.True(t, utf8.ValidString(page.text))
	assert.Positive(t, page.nextOffset)
	assert.True(t, strings.HasSuffix(page.text, "\n"))
}

// The rune budget binds independently of the byte budget, so ASCII content
// stops at the rune ceiling even with bytes to spare.
func TestPaginateSandboxFileHonoursTheRuneBudget(t *testing.T) {
	content := strings.Repeat(strings.Repeat("a", 100)+"\n", 50)

	page := paginateSandboxFile(content, 1, 0, 1<<20, 250)

	assert.LessOrEqual(t, utf8.RuneCountInString(page.text), 250)
	assert.Positive(t, page.nextOffset)
}

// A trailing newline terminates the last line rather than starting an empty
// one; counting it would report N+1 lines and hand out a blank final page.
func TestPaginateSandboxFileLineCounting(t *testing.T) {
	page := paginateSandboxFile("a\nb\nc\n", 0, 0, 1024, 1024)
	assert.Equal(t, 3, page.totalLines)
	assert.Equal(t, 1, page.startLine)
	assert.Equal(t, 3, page.endLine)
	assert.Zero(t, page.nextOffset)
	assert.Equal(t, "a\nb\nc\n", page.text)

	// Without a trailing newline the final line still counts.
	assert.Equal(t, 3, paginateSandboxFile("a\nb\nc", 0, 0, 1024, 1024).totalLines)

	// The line limit ends the page before the byte budget does.
	limited := paginateSandboxFile("a\nb\nc\n", 1, 2, 1024, 1024)
	assert.Equal(t, "a\nb\n", limited.text)
	assert.Equal(t, 3, limited.nextOffset)

	// An offset past the end yields nothing rather than an error.
	beyond := paginateSandboxFile("a\nb\n", 9, 0, 1024, 1024)
	assert.Empty(t, beyond.text)
	assert.Zero(t, beyond.nextOffset)
}

func TestReadFileWorkspaceReturnsSmallTextOnlyInOutput(t *testing.T) {
	content := []byte("hello sandbox\n")
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{Path: "/workspace/output/report.txt", Type: sandbox.RemoteEntryFile, Size: int64(len(content))},
		data: content,
	}

	result, err := NewReadFileTool(source).Execute(
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
func TestReadFileWorkspaceRefusesNonRegularFile(t *testing.T) {
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/output/esc",
			Type: sandbox.RemoteEntryOther,
			Size: 4,
		},
		data: []byte("must not be read"),
	}

	result, err := NewReadFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/output/esc"}`),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Zero(t, source.readCalls, "a non-regular path must never be downloaded")
	assert.Contains(t, result.Error, "not a regular file")
}

func TestReadFileWorkspaceSuppressesBinaryWithoutBase64(t *testing.T) {
	content := []byte{0xff, 0x00, 0x01}
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{Path: "/workspace/output/image.bin", Type: sandbox.RemoteEntryFile, Size: int64(len(content))},
		data: content,
	}

	result, err := NewReadFileTool(source).Execute(
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

func TestReadFileWorkspaceAllowsSessionInput(t *testing.T) {
	content := []byte("uploaded report\n")
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/input/ab12cd/report.txt",
			Type: sandbox.RemoteEntryFile,
			Size: int64(len(content)),
		},
		data: content,
	}

	result, err := NewReadFileTool(source).Execute(
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
func TestReadFileWorkspaceAllowsWorkspaceScratchFile(t *testing.T) {
	content := []byte("print('hi')\n")
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/report.py",
			Type: sandbox.RemoteEntryFile,
			Size: int64(len(content)),
		},
		data: content,
	}

	result, err := NewReadFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/report.py"}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Output, string(content))
	assert.Equal(t, sandbox.SessionWorkspaceRoot, result.Data["root"])
}

func TestReadFileWorkspaceRefusesOutsideInspectableRoots(t *testing.T) {
	source := &fakeSandboxFileSource{
		data: []byte("must not be read"),
		stat: &sandbox.RemoteStatEntry{Path: "/etc/passwd", Type: sandbox.RemoteEntryFile, Size: 4},
	}

	for _, path := range []string{
		"/etc/passwd",
		"/home/user/.ssh/id_rsa",
		"/opt/weknora/tenant/skills/pdf/SKILL.md",
	} {
		result, err := NewReadFileTool(source).Execute(
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
	assert.Contains(t, result.Error, `read_file(path="skill://ppt-generator/SKILL.md")`)
	assert.Contains(t, result.Error, "Do not ls")
	assert.Empty(t, source.listedDir)
}

func TestReadFileWorkspaceRedirectsSkillImagePaths(t *testing.T) {
	source := &fakeSandboxFileSource{
		data: []byte("must not be read"),
		stat: &sandbox.RemoteStatEntry{Path: "/opt/weknora/tenant/skills/ppt-generator/scripts/generate_ppt.py", Type: sandbox.RemoteEntryFile, Size: 4},
	}
	result, err := NewReadFileTool(source).Execute(
		sandboxFileTestContext(),
		json.RawMessage(`{"path":"/opt/weknora/tenant/skills/ppt-generator/scripts/generate_ppt.py"}`),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "skill://")
	assert.Zero(t, source.readCalls)
}
