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

type fakeSandboxFileSink struct {
	path    string
	content []byte
	err     error
	calls   int
	// files backs stat/read so append tests can build a file across calls.
	files   map[string][]byte
	statErr error
	readErr error
}

func (f *fakeSandboxFileSink) WriteSessionWorkspaceFile(_ context.Context, _, filePath string, content []byte) error {
	f.calls++
	f.path = filePath
	f.content = append([]byte(nil), content...)
	if f.err != nil {
		return f.err
	}
	if f.files == nil {
		f.files = map[string][]byte{}
	}
	f.files[filePath] = append([]byte(nil), content...)
	return nil
}

func (f *fakeSandboxFileSink) StatSessionFile(
	_ context.Context, _, filePath string,
) (*sandbox.RemoteStatEntry, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	data, ok := f.files[filePath]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", filePath)
	}
	return &sandbox.RemoteStatEntry{
		Path: filePath, Type: sandbox.RemoteEntryFile, Size: int64(len(data)),
	}, nil
}

func (f *fakeSandboxFileSink) ReadSessionFile(_ context.Context, _, filePath string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	data, ok := f.files[filePath]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", filePath)
	}
	return append([]byte(nil), data...), nil
}

func TestWriteSandboxFileWritesTextUnderOutput(t *testing.T) {
	script := "from pptx import Presentation\n" + strings.Repeat("# slide content for a long deck\n", 400)
	require.Greater(t, len(script), 8*1024)

	sink := &fakeSandboxFileSink{}
	result, err := NewWriteSandboxFileTool(sink, 0).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/output/generate_ppt.py", script),
	)

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, 1, sink.calls)
	assert.Equal(t, "/workspace/output/generate_ppt.py", sink.path)
	assert.Equal(t, script, string(sink.content))
	assert.Equal(t, "/workspace/output/generate_ppt.py", result.Data["path"])
	assert.Equal(t, len(script), result.Data["size"])
	assert.Equal(t, CountContentLines(script), result.Data["added_lines"])
	assert.Equal(t, 0, result.Data["removed_lines"])
	assert.Contains(t, result.Output, formatSandboxDiffStat(CountContentLines(script), 0))
	assert.NotContains(t, result.Output, script)
	assert.Contains(t, result.Output, "/workspace/output/generate_ppt.py")
	_, hasContent := result.Data["content"]
	assert.False(t, hasContent)
}

func TestWriteSandboxFileFlagsNestedPythonQuotes(t *testing.T) {
	script := "slides = [(\"这不是一个\"大干快上\"的夜晚\", False)]\n"
	sink := &fakeSandboxFileSink{}
	result, err := NewWriteSandboxFileTool(sink, 0).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/output/generate_fortune_ppt.py", script),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Equal(t, 1, sink.calls, "the file is still written so edit_sandbox_file can fix it")
	assert.Contains(t, result.Error, "line 1")
	assert.Contains(t, result.Error, "edit_sandbox_file")
	assert.Equal(t, true, result.Data["syntax_error"])
}

func TestWriteSandboxFileAllowsWorkspaceScratch(t *testing.T) {
	sink := &fakeSandboxFileSink{}
	result, err := NewWriteSandboxFileTool(sink, 0).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/scratch.py", "print(1)\n"),
	)

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, sandbox.SessionWorkspaceRoot, result.Data["root"])
	assert.Equal(t, "/workspace/scratch.py", sink.path)
}

func TestWriteSandboxFileRefusesSessionInput(t *testing.T) {
	sink := &fakeSandboxFileSink{}
	result, err := NewWriteSandboxFileTool(sink, 0).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/input/secret.txt", "nope"),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Zero(t, sink.calls)
	assert.Contains(t, result.Error, "outside that scope")
	assert.Contains(t, result.Error, sandbox.SessionWorkspaceRoot)
}

func TestWriteSandboxFileRefusesDirectoryPaths(t *testing.T) {
	sink := &fakeSandboxFileSink{}
	for _, p := range []string{"/workspace", "/workspace/output", "/workspace/input"} {
		result, err := NewWriteSandboxFileTool(sink, 0).Execute(
			sandboxFileTestContext(),
			mustWriteSandboxArgs(p, "nope"),
		)
		require.NoError(t, err, p)
		require.False(t, result.Success, p)
	}
	assert.Zero(t, sink.calls)
}

func TestWriteSandboxFileRefusesBinaryAndOversize(t *testing.T) {
	sink := &fakeSandboxFileSink{}

	binary, err := NewWriteSandboxFileTool(sink, 0).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/output/x.bin", "pre\x00post"),
	)
	require.NoError(t, err)
	require.False(t, binary.Success)
	assert.Contains(t, binary.Error, "binary")

	// The file-size limit is a real resource bound, so it is enforced on the
	// overwrite path too, not only when appending.
	oversize, err := NewWriteSandboxFileTool(sink, 0).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/output/big.py", strings.Repeat("a", maxSandboxFileBytes+1)),
	)
	require.NoError(t, err)
	require.False(t, oversize.Success)
	assert.Contains(t, oversize.Error, "file limit")
	assert.Zero(t, sink.calls)
}

// The advertised limit has to track the agent's token budget. A tenant that
// sets a small max_completion_tokens used to be told it could write 262144
// bytes; the model planned for that, got cut off, and retried the identical
// call forever.
func TestWriteSandboxBudgetFollowsCompletionTokens(t *testing.T) {
	// Unknown budget falls back to the hard cap and nothing else.
	assert.Equal(t, maxWriteSandboxBytes, writeBudgetBytes(0))

	// A budget large enough to reach the hard cap is clamped by it.
	assert.Equal(t, maxWriteSandboxBytes, writeBudgetBytes(1_000_000))

	// A small budget produces a proportionally small cap.
	small := writeBudgetBytes(8192)
	assert.Equal(t, (8192-completionTokensReservedForCall)*bytesPerCompletionToken, small)
	assert.Less(t, small, maxWriteSandboxBytes)

	// A budget smaller than the per-call reservation still leaves room to
	// write something, so the model gets an actionable limit rather than 0.
	assert.Positive(t, writeBudgetBytes(16))
}

// When the budget is the binding constraint the model must be told *why*,
// otherwise it reads the number as arbitrary and argues with it.
func TestWriteSandboxDescriptionExplainsABudgetDerivedLimit(t *testing.T) {
	budgeted := NewWriteSandboxFileTool(&fakeSandboxFileSink{}, 8192).Description()
	assert.Contains(t, budgeted, "token budget")
	assert.Contains(t, budgeted, "append")
	assert.NotContains(t, budgeted, "262144",
		"the hard cap is not what bounds this agent, so quoting it would mislead")

	uncapped := NewWriteSandboxFileTool(&fakeSandboxFileSink{}, 0).Description()
	assert.Contains(t, uncapped, "262144")
}

// The advertised budget is a forecast, not a rule. Content that arrived intact
// beat the forecast, which means the forecast was wrong — its bytes-per-token
// factor swings by 3x between ASCII and CJK. Rejecting it would discard work
// already paid for in tokens and force the model to re-emit the same bytes in
// chunks, which costs more and is likelier to truncate than the call that just
// succeeded. The genuinely truncated cases are refused in act.go, before the
// tool ever runs.
func TestWriteSandboxAcceptsContentOverTheAdvertisedBudget(t *testing.T) {
	sink := &fakeSandboxFileSink{}
	budget := writeBudgetBytes(8192)
	content := strings.Repeat("a", budget+1)

	result, err := NewWriteSandboxFileTool(sink, 8192).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/output/big.html", content),
	)

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, 1, sink.calls)
	assert.Equal(t, content, string(sink.files["/workspace/output/big.html"]))
}

// The whole point of append: a file too large to fit in one response gets
// built across calls, and later chunks must not resend what already landed.
func TestWriteSandboxFileAppendBuildsFileAcrossCalls(t *testing.T) {
	const path = "/workspace/output/deck.html"
	sink := &fakeSandboxFileSink{}
	tool := NewWriteSandboxFileTool(sink, 0)

	first, err := tool.Execute(sandboxFileTestContext(), mustWriteSandboxArgs(path, "<html><body>"))
	require.NoError(t, err)
	require.True(t, first.Success, first.Error)

	second, err := tool.Execute(sandboxFileTestContext(), json.RawMessage(
		`{"path":"`+path+`","content":"<h1>hi</h1>","mode":"append"}`,
	))
	require.NoError(t, err)
	require.True(t, second.Success, second.Error)

	third, err := tool.Execute(sandboxFileTestContext(), json.RawMessage(
		`{"path":"`+path+`","content":"</body></html>","mode":"append"}`,
	))
	require.NoError(t, err)
	require.True(t, third.Success, third.Error)

	assert.Equal(t, "<html><body><h1>hi</h1></body></html>", string(sink.files[path]))
	// The running total is how the model knows how much has landed.
	assert.Equal(t, len("<html><body><h1>hi</h1></body></html>"), third.Data["size"])
	assert.Equal(t, len("</body></html>"), third.Data["appended"])
	assert.Contains(t, third.Output, "total_bytes=")
}

// Appending to a file that is not there would silently drop every earlier
// chunk, so it fails and names the mode to use for the first one instead.
func TestWriteSandboxFileAppendRefusesMissingFile(t *testing.T) {
	sink := &fakeSandboxFileSink{}
	result, err := NewWriteSandboxFileTool(sink, 0).Execute(sandboxFileTestContext(), json.RawMessage(
		`{"path":"/workspace/output/absent.html","content":"tail","mode":"append"}`,
	))

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Zero(t, sink.calls)
	assert.Contains(t, result.Error, "does not exist yet")
	assert.Contains(t, result.Error, "overwrite")
}

func TestWriteSandboxFileRejectsUnknownMode(t *testing.T) {
	sink := &fakeSandboxFileSink{}
	result, err := NewWriteSandboxFileTool(sink, 0).Execute(sandboxFileTestContext(), json.RawMessage(
		`{"path":"/workspace/output/a.txt","content":"x","mode":"prepend"}`,
	))

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Zero(t, sink.calls)
	assert.Contains(t, result.Error, "unknown mode")
}

// A chunk boundary lands mid-source, so the syntax check has to look at the
// assembled file — judging the chunk alone would flag every valid split.
func TestWriteSandboxFileAppendChecksSyntaxOfWholeFile(t *testing.T) {
	const path = "/workspace/output/gen.py"
	sink := &fakeSandboxFileSink{}
	tool := NewWriteSandboxFileTool(sink, 0)

	first, err := tool.Execute(sandboxFileTestContext(), mustWriteSandboxArgs(path, "title = \"这不是一个\""))
	require.NoError(t, err)
	require.True(t, first.Success, first.Error)

	second, err := tool.Execute(sandboxFileTestContext(), json.RawMessage(
		`{"path":"`+path+`","content":"大干快上\"的夜晚\"\n","mode":"append"}`,
	))
	require.NoError(t, err)
	require.False(t, second.Success, "the assembled file has the broken-quote pattern")
	assert.Equal(t, true, second.Data["syntax_error"])
}

func TestWriteSandboxFileRegistryHintsWhenPathMissing(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(NewWriteSandboxFileTool(&fakeSandboxFileSink{}, 0))

	result, err := registry.ExecuteTool(
		sandboxFileTestContext(),
		ToolWriteSandboxFile,
		json.RawMessage(`{"content":"print(1)\n"}`),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "path")
	assert.Contains(t, result.Error, "put `path` first")
}

// The sandbox and skill tools are registered from the capability itself
// (registerSandboxFileTools / registerSandboxShellIfAllowed /
// initializeSkillsManager), never from the agent's tool allowlist. Offering
// them as checkboxes told the operator they could withhold a tool that would
// be registered anyway.
func TestSandboxCapabilityToolsAreNotToolListCheckboxes(t *testing.T) {
	for _, name := range []string{
		ToolListSandboxFiles, LegacyToolReadSandboxFile,
		ToolWriteSandboxFile, ToolEditSandboxFile, ToolShellExec,
		LegacyToolReadSkill, LegacyToolExecuteSkillScript,
	} {
		require.NotContains(t, DefaultAllowedTools(), name)
		for _, definition := range AvailableToolDefinitions() {
			require.NotEqual(t, name, definition.Name,
				"%s follows the sandbox switch, so a tool checkbox for it would do nothing", name)
		}
	}
}

func mustWriteSandboxArgs(path, content string) json.RawMessage {
	raw, err := json.Marshal(map[string]string{"path": path, "content": content})
	if err != nil {
		panic(err)
	}
	return raw
}
