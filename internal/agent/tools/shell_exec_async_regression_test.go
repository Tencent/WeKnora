package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestCommandInWorkDirPreservesCommentsAndHeredocs(t *testing.T) {
	for _, command := range []string{"printf ok # comment", "cat <<'EOF'\nok\nEOF"} {
		t.Run(command, func(t *testing.T) {
			plain, err := exec.Command("/bin/bash", "-c", command).CombinedOutput()
			require.NoError(t, err)
			wrapped, err := exec.Command("/bin/bash", "-c", commandInWorkDir(t.TempDir(), command)).CombinedOutput()
			require.NoError(t, err, "valid shell input broke after wrapping: %s", wrapped)
			require.Equal(t, string(plain), string(wrapped))
		})
	}
}

func TestCommandInWorkDirDoesNotRunAfterCDReturnsFailure(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "must-not-exist")
	command := "printf ran > " + sandbox.ShellQuote(marker)
	_, err := exec.Command("/bin/bash", "-c", commandInWorkDir(filepath.Join(root, "absent"), command)).CombinedOutput()
	require.Error(t, err)
	_, err = os.Stat(marker)
	require.True(t, os.IsNotExist(err))
}

func TestShellExecAsyncPreservesJSONWithEscapedOutput(t *testing.T) {
	submitter := &fakeSubmitter{response: ShellJobSubmission{Result: interfaces.WorkResult{
		ID: "sj_0123", State: interfaces.WorkStateSucceeded, Terminal: true,
		Payload: map[string]any{"stdout": strings.Repeat("\x01", 16*1024), "stderr": strings.Repeat("\x02", 8*1024)},
	}}}
	registry := NewToolRegistry()
	registry.RegisterTool(NewShellExecAsyncTool(submitter, &fakeShellExecutor{}, nil))
	result, err := registry.ExecuteTool(
		asyncTestCtx(),
		"shell_exec_async",
		json.RawMessage(`{"command":"true","description":"build"}`),
	)
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.True(t, json.Valid([]byte(result.Output)), "the registry must preserve the structured result")
}
