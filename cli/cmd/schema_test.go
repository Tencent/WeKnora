package cmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// TestSchema_Index: `weknora schema` lists every leaf command with its purpose.
func TestSchema_Index(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	root := NewRootCmd(cmdutil.New())
	root.SetArgs([]string{"schema", "--format", "json"})
	require.NoError(t, root.Execute())

	var env struct {
		OK   bool               `json:"ok"`
		Data []schemaIndexEntry `json:"data"`
		Meta struct {
			Count int `json:"count"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "got %q", out.String())
	assert.True(t, env.OK)
	assert.Greater(t, env.Meta.Count, 30, "index should enumerate the whole leaf surface")

	byCmd := map[string]string{}
	for _, e := range env.Data {
		byCmd[e.Command] = e.UsedFor
	}
	// schema lists itself and other leaves, each with a used_for.
	assert.Contains(t, byCmd, "kb create")
	assert.Contains(t, byCmd, "schema")
	assert.NotEmpty(t, byCmd["kb create"])
	// group commands (kb, doc) are not leaves and must not appear.
	assert.NotContains(t, byCmd, "kb")
}

// TestSchema_SingleCommand: a command path emits its full contract incl. flags.
func TestSchema_SingleCommand(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	root := NewRootCmd(cmdutil.New())
	root.SetArgs([]string{"schema", "kb", "create", "--format", "json"})
	require.NoError(t, root.Execute())

	var env struct {
		OK   bool          `json:"ok"`
		Data commandSchema `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "got %q", out.String())
	assert.True(t, env.OK)
	assert.Equal(t, "kb create", env.Data.Command)
	assert.NotEmpty(t, env.Data.UsedFor)
	assert.NotEmpty(t, env.Data.Examples)

	flagNames := map[string]bool{}
	for _, f := range env.Data.Flags {
		flagNames[f.Name] = true
	}
	assert.True(t, flagNames["description"], "kb create local flags should include --description; got %v", flagNames)
	// global persistent flags (--format, --profile) are inherited, not local —
	// schema lists only command-specific flags.
	assert.False(t, flagNames["profile"], "inherited global flags must be excluded")
}

// TestSchema_SurfacesRisk: a destructive command exposes its risk annotation,
// so an agent can discover confirmation-gating without running the command.
func TestSchema_SurfacesRisk(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	root := NewRootCmd(cmdutil.New())
	root.SetArgs([]string{"schema", "doc", "update", "--format", "json"})
	require.NoError(t, root.Execute())

	var env struct {
		Data commandSchema `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "got %q", out.String())
	require.NotNil(t, env.Data.Risk)
	assert.Equal(t, "doc.update", env.Data.Risk.Action)
	assert.Equal(t, "write", env.Data.Risk.Level)
}

// TestSchema_UnknownCommand: a bad path returns a typed unknown_subcommand
// error (exit 5) with a did-you-mean hint — never a silent empty result.
func TestSchema_UnknownCommand(t *testing.T) {
	iostreams.SetForTest(t)
	root := NewRootCmd(cmdutil.New())
	root.SetArgs([]string{"schema", "kbb", "--format", "json"})
	err := root.Execute()
	require.Error(t, err)
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputUnknownSubcommand, ce.Code)
	assert.Contains(t, ce.Hint, "did you mean")
}
