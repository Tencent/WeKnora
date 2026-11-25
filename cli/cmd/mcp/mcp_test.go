package mcpcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	sdk "github.com/Tencent/WeKnora/client"
)

// newTestCmd builds the mcp tree with the given factory and silences
// cobra's usage/error printing so failures stay readable in test output.
func newTestCmd(t *testing.T, f *cmdutil.Factory) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	cmd := NewCmd(f)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	return cmd, &out, &errBuf
}

func TestNewCmd_Tree(t *testing.T) {
	cmd := NewCmd(cmdutil.New())

	assert.Equal(t, "mcp", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.LessOrEqual(t, len(cmd.Short), 70, "Short must stay one line (AGENTS.md rule)")

	// The Long text is the curation contract: it names the read-only
	// tools and states that destructive verbs are deliberately excluded.
	assert.Contains(t, cmd.Long, "kb_list")
	assert.Contains(t, cmd.Long, "chat")
	assert.Contains(t, cmd.Long, "Destructive")

	sub := cmd.Commands()
	require.Len(t, sub, 1, "mcp must expose exactly one subcommand")
	assert.Equal(t, "serve", sub[0].Name())
	assert.NotNil(t, cmd.Run, "bare `mcp` prints help via Run")
}

func TestNewCmd_BareInvokesHelp(t *testing.T) {
	cmd, out, _ := newTestCmd(t, cmdutil.New())
	cmd.SetArgs([]string{})

	require.NoError(t, cmd.Execute())
	got := out.String()
	// The bare help renders Long (not Short), so assert on the Long
	// text and the listed subcommand.
	assert.Contains(t, got, "MCP tools")
	assert.Contains(t, got, "serve")
}

func TestNewCmd_RejectsArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "parent positional", args: []string{"extra"}},
		{name: "unknown subcommand", args: []string{"bogus"}},
		{name: "serve positional", args: []string{"serve", "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, _ := newTestCmd(t, cmdutil.New())
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown command")
		})
	}
}

func TestServe_ClientErrorPropagates(t *testing.T) {
	// `mcp serve` eagerly resolves the SDK client at startup so a missing
	// profile fails fast with auth.unauthenticated instead of handshaking
	// successfully and then erroring on every tool call.
	sentinel := errors.New("no profile configured")
	calls := 0
	f := &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			calls++
			return nil, sentinel
		},
	}
	cmd, _, _ := newTestCmd(t, f)
	cmd.SetArgs([]string{"serve"})

	err := cmd.Execute()
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, calls, "client must be resolved exactly once, before any handshake")
}

func TestServe_CancelledContextExitsCleanly(t *testing.T) {
	// With the client resolved, RunE hands off to the stdio server. A
	// cancelled context is a normal host shutdown (stdin closed / client
	// went away), so the command must exit 0 instead of surfacing an
	// error. The zero-value client is safe here: tool handlers only call
	// the SDK when a request arrives, and none arrives before shutdown.
	f := &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			return &sdk.Client{}, nil
		},
	}
	cmd, _, _ := newTestCmd(t, f)
	cmd.SetArgs([]string{"serve"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after context cancellation")
	}
}

func TestServe_AgentHelp(t *testing.T) {
	serve := NewCmd(cmdutil.New()).Commands()[0]

	ah, ok := cmdutil.AgentHelpFor(serve)
	require.True(t, ok, "serve must carry an AgentHelp blob (root coverage test enforces this for leaves)")
	assert.Contains(t, ah.UsedFor, "MCP")
	assert.Contains(t, ah.Output, "JSON-RPC")
	assert.Contains(t, ah.Examples, "weknora mcp serve")
	require.Len(t, ah.Warnings, 2)
	assert.Contains(t, ah.Warnings[0], "long-running")
	assert.Contains(t, ah.Warnings[1], "auth.unauthenticated")
}

func TestServe_AgentHelpJSON(t *testing.T) {
	// The WEKNORA_AGENT_HELP=1 path renders the blob as JSON on stdout
	// instead of human help text; decode it the way an agent would.
	t.Setenv("WEKNORA_AGENT_HELP", "1")
	serve := NewCmd(cmdutil.New()).Commands()[0]
	var buf bytes.Buffer
	serve.SetOut(&buf)

	require.NoError(t, serve.Help())

	var ah struct {
		UsedFor  string   `json:"used_for"`
		Output   string   `json:"output"`
		Examples []string `json:"examples"`
		Warnings []string `json:"warnings"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &ah), "agent help must be valid JSON, got: %q", buf.String())
	assert.NotEmpty(t, ah.UsedFor)
	assert.NotEmpty(t, ah.Output)
	assert.Contains(t, ah.Examples, "weknora mcp serve")
	assert.NotEmpty(t, ah.Warnings)
}

func TestServe_HumanHelpShowsWarnings(t *testing.T) {
	// Without the env toggle, the same warnings render as an "AI agents:"
	// block appended to human help; the two channels share one source
	// list, so this pins that they cannot drift.
	t.Setenv("WEKNORA_AGENT_HELP", "")
	serve := NewCmd(cmdutil.New()).Commands()[0]
	var buf bytes.Buffer
	serve.SetOut(&buf)

	require.NoError(t, serve.Help())

	got := buf.String()
	assert.Contains(t, got, "AI agents:")
	assert.Contains(t, got, "long-running stdio server")
}

func TestServe_LongDocumentsRegistration(t *testing.T) {
	// The Long text carries the copy-paste mcpServers snippet agents use
	// to register the binary with their MCP client.
	serve := NewCmd(cmdutil.New()).Commands()[0]

	assert.Contains(t, serve.Long, "mcpServers")
	assert.Contains(t, serve.Long, `"command": "weknora"`)
	assert.Contains(t, serve.Long, `"mcp", "serve"`)
	assert.Contains(t, serve.Long, "auth.unauthenticated")
}
