package kb

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbDryRunFactory builds a Factory whose Client closure panics if invoked —
// dry-run must early-exit before any SDK call. Prompter is similarly trapped:
// dry-run is non-interactive by contract.
func kbDryRunFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	return &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			t.Fatal("dry-run path must not call Factory.Client(); SDK side effect leaked")
			return nil, nil
		},
		Prompter: func() prompt.Prompter {
			t.Fatal("dry-run path must not call Factory.Prompter(); confirm-prompt side effect leaked")
			return nil
		},
	}
}

// withRootHarness wraps a kb command under a synthetic root cmd that
// registers the global persistent flags (mirrors addGlobalFlags in
// cmd/root.go). Required because kb subcommands inherit --yes / --format /
// --jq from root in production.
func withRootHarness(sub *cobra.Command, args ...string) *cobra.Command {
	root := &cobra.Command{Use: "weknora"}
	pf := root.PersistentFlags()
	pf.BoolP("yes", "y", false, "")
	pf.String("format", "", "")
	pf.StringP("jq", "q", "", "")
	root.AddCommand(sub)
	root.SetArgs(append([]string{sub.Name()}, args...))
	root.SetContext(context.Background())
	root.SilenceErrors = true
	root.SilenceUsage = true
	return root
}

// TestKBCreate_DryRun_EmitsPlan: --dry-run on `kb create` must emit the
// standard dry-run envelope (ok:true, meta.dry_run:true, meta.plan.action) and
// must NOT touch the SDK. Verifies that the cobra-layer early-exit runs before
// f.Client() and that the plan shape matches the envelope contract.
func TestKBCreate_DryRun_EmitsPlan(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	f := kbDryRunFactory(t)
	root := withRootHarness(NewCmdCreate(f),
		"foo", "--description", "bar", "--dry-run", "--format", "json")
	require.NoError(t, root.Execute(), "dry-run must succeed (exit 0) without SDK")

	var env struct {
		OK   bool `json:"ok"`
		Meta struct {
			DryRun bool           `json:"dry_run"`
			Plan   map[string]any `json:"plan"`
		} `json:"meta"`
		Data any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "expected valid JSON envelope, got %q", out.String())
	assert.True(t, env.OK, "envelope.ok must be true on dry-run success")
	assert.True(t, env.Meta.DryRun, "meta.dry_run must be true")
	assert.Equal(t, "kb.create", env.Meta.Plan["action"], "plan.action must be kb.create")
	// plan.args contains the user-provided flags so agents can diff "what would happen".
	planArgs, ok := env.Meta.Plan["args"].(map[string]any)
	require.True(t, ok, "plan.args must be a map, got %T", env.Meta.Plan["args"])
	assert.Equal(t, "foo", planArgs["name"], "plan.args.name must echo positional <name>")
	assert.Equal(t, "bar", planArgs["description"], "plan.args.description must echo --description")
	assert.Nil(t, env.Data, "data must be omitted on dry-run (no real result)")
}

// TestKBCreate_DryRun_RejectsInvalidStorageProvider: --dry-run must reject
// the same invalid --storage-provider value the live path rejects. Before
// the fix the enum check lived only in runCreate(), which HandleDryRun
// short-circuited past — so --dry-run silently accepted "garbage".
func TestKBCreate_DryRun_RejectsInvalidStorageProvider(t *testing.T) {
	iostreams.SetForTest(t)
	f := kbDryRunFactory(t)
	root := withRootHarness(NewCmdCreate(f),
		"foo", "--storage-provider", "garbage", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject invalid --storage-provider")

	// runCreate emits a FlagError (exit 2) for this — make sure dry-run preserves
	// that exact mapping.
	assert.Equal(t, 2, cmdutil.ExitCode(err), "FlagError must map to exit 2")
}
