package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type outputLinkExecutor struct {
	fakeShellExecutor
	before, after []sandbox.RemoteDirEntry
	listError     error
	listed        int
}

func (f *outputLinkExecutor) ListSessionFiles(_ context.Context, sessionID, dir string) ([]sandbox.RemoteDirEntry, error) {
	f.listed++
	if f.listError != nil {
		return nil, f.listError
	}
	if f.calls == 0 {
		return f.before, nil
	}
	return f.after, nil
}

func TestShellOutputLinksUseChangedFilesAndDoNotReplay(t *testing.T) {
	t.Setenv("WEKNORA_SKILL_OUTPUT_DIR", "/workspace/output")
	old := sandbox.RemoteDirEntry{Path: "/workspace/output/deck.pptx", Type: sandbox.RemoteEntryFile, Size: 100, ModTime: time.Unix(1, 0)}
	next := old
	next.ModTime = time.Unix(2, 0)
	unchanged := sandbox.RemoteDirEntry{Path: "/workspace/output/data.json", Type: sandbox.RemoteEntryFile, Size: 20}
	executor := &outputLinkExecutor{
		before: []sandbox.RemoteDirEntry{old, unchanged},
		after: []sandbox.RemoteDirEntry{next, unchanged,
			{Path: "/workspace/output/new.csv", Type: sandbox.RemoteEntryFile},
			{Path: "/workspace/output/subdir", Type: sandbox.RemoteEntryDir},
		},
	}
	result, err := NewShellExecTool(executor, nil).Execute(shellExecTestContext(), json.RawMessage(`{"command":"python3 generate.py"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 2, executor.listed)
	require.Contains(t, result.OutputFiles, "sandbox:deck.pptx")
	require.Contains(t, result.OutputFiles, "sandbox:new.csv")
	require.NotContains(t, result.OutputFiles, "sandbox:data.json")
	require.NotContains(t, result.OutputFiles, "sandbox:subdir")
	steps := SanitizeAgentStepsForStorage([]types.AgentStep{{ToolCalls: []types.ToolCall{{Name: ToolShellExec, Result: result}}}})
	require.NotContains(t, steps[0].ToolCalls[0].Result.Output, "sandbox:")
	encoded, err := json.Marshal(steps)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "sandbox:")
	require.Contains(t, result.OutputFiles, "sandbox:deck.pptx", "storage sanitization must not change the live result")
}

func TestShellOutputLinksOmitUnverifiedOutputs(t *testing.T) {
	t.Setenv("WEKNORA_SKILL_OUTPUT_DIR", "/workspace/output")
	created := sandbox.RemoteDirEntry{Path: "/workspace/output/deck.pptx", Type: sandbox.RemoteEntryFile, Size: 10, ModTime: time.Unix(2, 0)}
	t.Run("inspection failed", func(t *testing.T) {
		executor := &outputLinkExecutor{
			listError: errors.New("unavailable"),
			after:     []sandbox.RemoteDirEntry{created},
		}
		result, err := NewShellExecTool(executor, nil).Execute(shellExecTestContext(), json.RawMessage(`{"command":"python3 generate.py"}`))
		require.NoError(t, err)
		require.Empty(t, result.OutputFiles)
		require.Nil(t, result.OutputFiles, "failed inspection must not claim that no output files were found")
		require.Equal(t, 1, executor.calls)
	})
	t.Run("failed command", func(t *testing.T) {
		executor := &outputLinkExecutor{
			fakeShellExecutor: fakeShellExecutor{result: &sandbox.ExecuteResult{ExitCode: 1}},
			after:             []sandbox.RemoteDirEntry{created},
		}
		result, err := NewShellExecTool(executor, nil).Execute(shellExecTestContext(), json.RawMessage(`{"command":"python3 generate.py"}`))
		require.NoError(t, err)
		require.True(t, result.Success, "a non-zero exit is still a completed tool call")
		require.Equal(t, []string{"sandbox:deck.pptx"}, result.OutputFiles)
		require.Equal(t, 2, executor.listed)
	})
	t.Run("killed command", func(t *testing.T) {
		executor := &outputLinkExecutor{
			fakeShellExecutor: fakeShellExecutor{result: &sandbox.ExecuteResult{Killed: true}},
			after:             []sandbox.RemoteDirEntry{created},
		}
		result, err := NewShellExecTool(executor, nil).Execute(shellExecTestContext(), json.RawMessage(`{"command":"python3 generate.py"}`))
		require.NoError(t, err)
		require.False(t, result.Success)
		require.Equal(t, []string{"sandbox:deck.pptx"}, result.OutputFiles)
		require.Equal(t, 2, executor.listed)
	})
}

func TestShellPreviewListingDoesNotBecomeDownloadLinks(t *testing.T) {
	t.Setenv("WEKNORA_SKILL_OUTPUT_DIR", "/workspace/output")
	doc := sandbox.RemoteDirEntry{Path: "/workspace/output/report.docx", Type: sandbox.RemoteEntryFile, Size: 100}
	executor := &outputLinkExecutor{
		fakeShellExecutor: fakeShellExecutor{result: &sandbox.ExecuteResult{
			Stdout: "page-1.png\npage-2.png\npage-3.png\npage-4.png\n",
		}},
		before: []sandbox.RemoteDirEntry{doc},
		after:  []sandbox.RemoteDirEntry{doc},
	}
	result, err := NewShellExecTool(executor, nil).Execute(
		shellExecTestContext(), json.RawMessage(`{"command":"ls /tmp/task/previews/"}`),
	)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Empty(t, result.OutputFiles)
	require.NotNil(t, result.OutputFiles, "a completed inspection should explicitly report no output changes")
	registry := modelcontext.NewRegistry(true)
	modelOutput := registry.ModelToolResultForTool(ToolShellExec, result)
	require.Contains(t, modelOutput, "page-1.png", "preserve stdout for the model to inspect")
	require.Contains(t, modelOutput, "Output files: none identified by this call.")
	require.NotContains(t, modelOutput, "sandbox:")
}

func TestOutputLinksEscapeNamesAndStayInsideOutputDirectory(t *testing.T) {
	t.Setenv("WEKNORA_SKILL_OUTPUT_DIR", "/workspace/output")
	links := sandboxOutputLinks(
		"/workspace/output/report [1](final).pdf",
		"/workspace/script.py",
		"/workspace/output/../input/a.pdf",
		"/tmp/task/previews/page-1.png",
		"/workspace/output-previews/page-1.png",
		"/workspace/output/../../tmp/task/previews/page-1.png",
	)
	require.Equal(t, []string{"sandbox:report%20%5B1%5D%28final%29.pdf"}, links)
	require.Equal(t, []string{"sandbox:比赛信息.pptx"}, sandboxOutputLinks("/workspace/output/比赛信息.pptx"))
}

func TestFileMutationToolsReturnDirectHTMLDeliverables(t *testing.T) {
	t.Setenv("WEKNORA_SKILL_OUTPUT_DIR", "/workspace/output")
	sink := &fakeSandboxFileSink{}
	for _, filePath := range []string{"/workspace/output/report.html", "/workspace/scratch.html"} {
		result, err := NewWriteSandboxFileTool(sink, 0).Execute(sandboxFileTestContext(), mustWriteSandboxArgs(filePath, "old"))
		require.NoError(t, err)
		require.True(t, result.Success)
		require.Contains(t, result.Output, filePath)
		if filePath == "/workspace/output/report.html" {
			require.Equal(t, []string{"sandbox:report.html"}, result.OutputFiles)
		} else {
			require.Empty(t, result.OutputFiles)
		}
	}
	editor := &fakeSandboxFileEditor{files: sink.files}
	result, err := NewEditSandboxFileTool(editor).Execute(sandboxFileTestContext(), json.RawMessage(`{"path":"/workspace/output/report.html","edits":[{"old_string":"old","new_string":"new"}]}`))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Contains(t, result.Output, "/workspace/output/report.html")
	require.Equal(t, []string{"sandbox:report.html"}, result.OutputFiles)
}
