package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

// apiDryRunFactory builds a Factory whose Client closure panics if invoked —
// dry-run must early-exit before any SDK call.
func apiDryRunFactory(t *testing.T) *cmdutil.Factory {
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

// TestApi_DryRunWithGet_FlagError: default-method GET + --dry-run must return
// FlagError (exit 2). The only meaningful target for --dry-run on `api` is a
// mutation method; allowing GET to silently succeed would let agents waste a
// round-trip previewing a no-op.
func TestApi_DryRunWithGet_FlagError(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarness(NewCmd(apiDryRunFactory(t)),
		"/api/v1/knowledge-bases", "--dry-run")
	err := root.Execute()
	require.Error(t, err, "GET + --dry-run must error")

	var fe *cmdutil.FlagError
	require.True(t, errors.As(err, &fe), "expected *cmdutil.FlagError, got %T %v", err, err)
	assert.Equal(t, 2, cmdutil.ExitCode(err), "FlagError must map to exit 2")
	assert.Contains(t, err.Error(), "explicit -X POST/PUT/PATCH/DELETE",
		"error message must point users to the concrete repair")
}

// TestApi_DryRunWithExplicitGet_FlagError: explicit `-X GET` + --dry-run is
// also rejected. The reject condition is "method is GET", not "method flag is
// unset"; passing -X GET explicitly must produce the same error so users
// can't bypass the guard by being verbose.
func TestApi_DryRunWithExplicitGet_FlagError(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarness(NewCmd(apiDryRunFactory(t)),
		"/api/v1/knowledge-bases", "-X", "GET", "--dry-run")
	err := root.Execute()
	require.Error(t, err, "explicit -X GET + --dry-run must error")

	var fe *cmdutil.FlagError
	require.True(t, errors.As(err, &fe), "expected *cmdutil.FlagError, got %T %v", err, err)
	assert.Equal(t, 2, cmdutil.ExitCode(err))
}

// TestApi_DryRunWithInputAutoPromotes_EmitsPlan: --input on its own (no
// explicit -X) must auto-promote GET → POST in the dry-run path too,
// matching the live behavior in resolveMethod. Before the fix the dry-run
// branch built `method` directly from `strings.ToUpper(opts.Method)`, which
// is empty when -X is unset, then defaulted to GET — so --input was ignored
// at preview time, producing the misleading "GET is read-only" error.
func TestApi_DryRunWithInputAutoPromotes_EmitsPlan(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	iostreams.IO.In = strings.NewReader(`{}`)
	root := withRootHarness(NewCmd(apiDryRunFactory(t)),
		"/api/v1/knowledge-bases", "--input", "-", "--dry-run", "--format", "json")
	require.NoError(t, root.Execute(), "--input + --dry-run must succeed (POST auto-promotion)")

	var env struct {
		OK   bool `json:"ok"`
		Meta struct {
			DryRun bool           `json:"dry_run"`
			Plan   map[string]any `json:"plan"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "expected valid JSON envelope, got %q", out.String())
	assert.True(t, env.OK)
	assert.True(t, env.Meta.DryRun)
	assert.Equal(t, "api.post", env.Meta.Plan["action"], "plan.action must reflect auto-promoted POST")
	assert.Equal(t, "POST", env.Meta.Plan["method"])
}

// TestApi_DryRunWithPost_EmitsPlan: POST + --dry-run + --input - must emit
// the standard envelope with action=api.post, method/path echoed, and the
// stdin body parsed as JSON under plan.body. No SDK call expected (factory
// would panic if Client() were touched).
func TestApi_DryRunWithPost_EmitsPlan(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	cmd := NewCmd(apiDryRunFactory(t))
	// StdinReader is on Options; set it via the recovered options binding by
	// running through the root harness with --input - and trusting iostreams.IO.In.
	// Simplest path: write to IO.In directly via SetForTest's reader swap.
	iostreams.IO.In = strings.NewReader(`{"name":"foo"}`)
	root := withRootHarness(cmd,
		"/api/v1/knowledge-bases", "-X", "POST", "--input", "-", "--dry-run", "--format", "json")
	require.NoError(t, root.Execute(), "POST + --dry-run must succeed (exit 0) without SDK")

	var env struct {
		OK   bool `json:"ok"`
		Meta struct {
			DryRun bool           `json:"dry_run"`
			Plan   map[string]any `json:"plan"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "expected valid JSON envelope, got %q", out.String())
	assert.True(t, env.OK)
	assert.True(t, env.Meta.DryRun)
	assert.Equal(t, "api.post", env.Meta.Plan["action"], "plan.action must lowercase the method")
	assert.Equal(t, "POST", env.Meta.Plan["method"], "plan.method must be uppercase")
	assert.Equal(t, "/api/v1/knowledge-bases", env.Meta.Plan["path"])
	// Body decoded as JSON object (best-effort) for downstream agent inspection.
	body, ok := env.Meta.Plan["body"].(map[string]any)
	require.True(t, ok, "plan.body must be a JSON object when --input is valid JSON, got %T", env.Meta.Plan["body"])
	assert.Equal(t, "foo", body["name"])
}
