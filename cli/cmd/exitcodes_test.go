package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

func TestExitCodesRows_CoversDocumentedMatrix(t *testing.T) {
	rows := exitCodeRows()
	codes := make([]int, 0, len(rows))
	for _, r := range rows {
		require.NotEmpty(t, r.Meaning)
		require.NotEmpty(t, r.AgentAction)
		codes = append(codes, r.Code)
	}
	assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 10, 124, 130}, codes)
}

// TestExitCodesRows_MatrixSnapshot guards key rows against silent drift.
func TestExitCodesRows_MatrixSnapshot(t *testing.T) {
	byCode := map[int]exitCodeRow{}
	for _, r := range exitCodeRows() {
		byCode[r.Code] = r
	}

	// exit 130 — produced by main.go signal guard, not ExitCode(); no typed code routes here
	r130 := byCode[130]
	assert.Equal(t, "cancelled by signal (SIGINT/SIGTERM)", r130.Meaning)
	assert.Equal(t, "", r130.ErrorTypes, "exit 130 must have empty ErrorTypes (omitempty)")
	assert.Equal(t, "stop, do not retry", r130.AgentAction)

	// exit 1 — fallback bucket; operation.cancelled typed errors land here
	r1 := byCode[1]
	assert.Contains(t, r1.ErrorTypes, "operation.cancelled")
	assert.Contains(t, r1.ErrorTypes, "server.session_create_failed")
	assert.Contains(t, r1.ErrorTypes, "local.*")

	// exit 7 — server transient, excluding the two special-cased server codes
	r7 := byCode[7]
	assert.Contains(t, r7.ErrorTypes, "network.*")
	assert.Contains(t, r7.ErrorTypes, "rate_limited→6")
	assert.Contains(t, r7.ErrorTypes, "session_create_failed→1")
}

func TestExitCodes_JSONOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	root := NewRootCmd(cmdutil.New())
	root.SetArgs([]string{"exit-codes", "--format", "json"})
	root.SetOut(&bytes.Buffer{}) // discard cobra's writer; real output goes to iostreams.IO.Out
	root.SetErr(&bytes.Buffer{})
	require.NoError(t, root.Execute())

	got := out.String()
	// Must contain the envelope key for code 10
	assert.Contains(t, got, `"code":10`, "JSON output must include code 10")
	// Must contain the count
	assert.Contains(t, got, `"count":11`, "JSON output must include count:11")
	// Must contain exit 130 (signal-guard exit)
	assert.Contains(t, got, `"code":130`, "JSON output must include code 130")
}

func TestExitCodes_TextOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	root := NewRootCmd(cmdutil.New())
	root.SetArgs([]string{"exit-codes", "--format", "text"})
	root.SetOut(&bytes.Buffer{}) // discard cobra's writer; real output goes to iostreams.IO.Out
	root.SetErr(&bytes.Buffer{})
	require.NoError(t, root.Execute())

	got := out.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	// Header line + 11 data rows = 12 lines
	require.Len(t, lines, 12, "text output must have header + 11 data rows; got:\n%s", got)
	// Header must contain column names
	assert.Contains(t, lines[0], "CODE")
	assert.Contains(t, lines[0], "MEANING")
	assert.Contains(t, lines[0], "AGENT ACTION")
	// First data row (exit 0) must mention "success"
	assert.Contains(t, lines[1], "success", "first data row must describe exit 0 success")
}

func TestExitCodes_HelpOutput(t *testing.T) {
	var out bytes.Buffer
	root := NewRootCmd(cmdutil.New())
	root.SetArgs([]string{"help", "exit-codes"})
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	require.NoError(t, root.Execute())

	got := out.String()
	assert.Contains(t, got, "Exit codes and the agent action for each", "help output must contain the Long description header")
}
