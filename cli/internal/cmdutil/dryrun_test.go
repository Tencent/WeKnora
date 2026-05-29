package cmdutil

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
)

func TestAddDryRunFlag_RegistersFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "create"}
	var dryRun bool
	AddDryRunFlag(cmd, &dryRun)
	flag := cmd.Flags().Lookup("dry-run")
	require.NotNil(t, flag, "expected --dry-run flag registered")
	assert.Equal(t, "false", flag.DefValue, "default should be false")
}

func TestEmitDryRun_EmitsEnvelopeWithPlan(t *testing.T) {
	var buf bytes.Buffer
	plan := DryRunPlan{
		Action: "kb.create",
		Args: map[string]any{
			"name":        "foo",
			"description": "bar",
		},
	}
	err := EmitDryRun(&buf, &FormatOptions{Mode: FormatJSON}, plan)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))

	assert.Equal(t, true, got["ok"])
	assert.NotContains(t, got, "data", "data should be omitempty when nil")

	meta, ok := got["meta"].(map[string]any)
	require.True(t, ok, "expected meta object")
	assert.Equal(t, true, meta["dry_run"])

	planOut, ok := meta["plan"].(map[string]any)
	require.True(t, ok, "expected meta.plan object")
	assert.Equal(t, "kb.create", planOut["action"])

	args, ok := planOut["args"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "foo", args["name"])
	assert.Equal(t, "bar", args["description"])
}

func TestEmitDryRun_ApiPlanShape(t *testing.T) {
	var buf bytes.Buffer
	plan := DryRunPlan{
		Action: "api.post",
		Method: "POST",
		Path:   "/api/v1/knowledge-bases",
		Body:   map[string]any{"name": "foo"},
	}
	err := EmitDryRun(&buf, &FormatOptions{Mode: FormatJSON}, plan)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))

	planOut := got["meta"].(map[string]any)["plan"].(map[string]any)
	assert.Equal(t, "api.post", planOut["action"])
	assert.Equal(t, "POST", planOut["method"])
	assert.Equal(t, "/api/v1/knowledge-bases", planOut["path"])

	body, ok := planOut["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "foo", body["name"])
}

func TestEmitDryRun_InjectsNotice(t *testing.T) {
	// Regression: dry-run envelope must carry the same _notice channel as
	// every other success envelope. PendingNotice is the system-level hook
	// used for deprecation / version_skew / security signals.
	output.PendingNotice = func() map[string]any {
		return map[string]any{"deprecation": "use weknora kb create-v2"}
	}
	t.Cleanup(func() { output.PendingNotice = nil })

	var buf bytes.Buffer
	err := EmitDryRun(&buf, &FormatOptions{Mode: FormatJSON},
		DryRunPlan{Action: "kb.create", Args: map[string]any{"name": "x"}})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	notice, ok := got["_notice"].(map[string]any)
	require.True(t, ok, "expected _notice field on dry-run envelope; got %v", got)
	assert.Equal(t, "use weknora kb create-v2", notice["deprecation"])
}

func TestEmitDryRun_HonorsTTYIndent(t *testing.T) {
	var buf bytes.Buffer
	err := EmitDryRun(&buf, &FormatOptions{Mode: FormatJSON, TTY: true},
		DryRunPlan{Action: "kb.create"})
	require.NoError(t, err)
	got := buf.String()
	assert.Contains(t, got, "\n  \"", "TTY=true should produce indented JSON; got %q", got)
}

func TestEmitDryRun_HonorsJQ(t *testing.T) {
	// jq runs against the full envelope, so the user can project any
	// envelope field (.meta.dry_run, .meta.plan.action, etc.). Routing
	// through FormatOptions.Emit is what makes this work.
	var buf bytes.Buffer
	err := EmitDryRun(&buf, &FormatOptions{Mode: FormatJSON, JQ: ".meta.plan"},
		DryRunPlan{Action: "kb.create", Args: map[string]any{"name": "foo"}})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "kb.create", got["action"])
	args, ok := got["args"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "foo", args["name"])
	// Sanity: jq output should NOT carry the surrounding envelope fields.
	assert.NotContains(t, got, "ok")
	assert.NotContains(t, got, "meta")
}

func TestEmitDryRun_NilFormatOptionsDefaultsJSON(t *testing.T) {
	var buf bytes.Buffer
	err := EmitDryRun(&buf, nil, DryRunPlan{Action: "kb.create"})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, true, got["ok"])
	meta, ok := got["meta"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, meta["dry_run"])
}

func TestEmitDryRun_NDJSONFallsBackToJSON(t *testing.T) {
	// NDJSON mode is meaningless for a single dry-run envelope; the helper
	// should fall back to JSON envelope shape so meta.plan is surfaced.
	var buf bytes.Buffer
	err := EmitDryRun(&buf, &FormatOptions{Mode: FormatNDJSON},
		DryRunPlan{Action: "kb.create"})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, true, got["ok"])
	meta, ok := got["meta"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, meta["dry_run"])
}

func TestHandleDryRun_NotDryRun_SkipsAndReturnsHandledFalse(t *testing.T) {
	// dryRun=false short-circuits before any flag read / IO touch; helper
	// returns (false, nil) so RunE proceeds to the live SDK path.
	cmd := &cobra.Command{Use: "x"}
	var dryRun bool
	AddDryRunFlag(cmd, &dryRun)
	handled, err := HandleDryRun(cmd, false, DryRunPlan{Action: "kb.create"})
	assert.False(t, handled, "handled must be false when dryRun=false")
	assert.NoError(t, err)
}

func TestHandleDryRun_DryRun_EmitsEnvelopeAndReturnsHandledTrue(t *testing.T) {
	// dryRun=true → helper resolves FormatOptions, emits the envelope to
	// iostreams.IO.Out, and returns (true, nil). RunE returns the error
	// immediately without touching SDK.
	out, _ := iostreams.SetForTest(t)
	cmd := &cobra.Command{Use: "x"}
	var dryRun bool
	AddDryRunFlag(cmd, &dryRun)
	// --format is a persistent root flag in production; for the unit test
	// we register it locally so CheckFormatFlag has something to read.
	cmd.Flags().String("format", "", "")
	cmd.Flags().String("jq", "", "")
	require.NoError(t, cmd.Flags().Set("format", "json"))

	handled, err := HandleDryRun(cmd, true, DryRunPlan{
		Action: "kb.create",
		Args:   map[string]any{"name": "foo"},
	})
	assert.True(t, handled, "handled must be true when dryRun=true")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, true, got["ok"])
	meta, ok := got["meta"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, meta["dry_run"])
	plan, ok := meta["plan"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "kb.create", plan["action"])
}

func TestHandleDryRun_InvalidFormat_ReturnsHandledTrueWithError(t *testing.T) {
	// Invalid --format must propagate as (true, FlagError) so the caller
	// returns immediately; otherwise a broken --format would silently fall
	// through to the live SDK path.
	_, _ = iostreams.SetForTest(t)
	cmd := &cobra.Command{Use: "x"}
	var dryRun bool
	AddDryRunFlag(cmd, &dryRun)
	cmd.Flags().String("format", "", "")
	require.NoError(t, cmd.Flags().Set("format", "garbage"))

	handled, err := HandleDryRun(cmd, true, DryRunPlan{Action: "kb.create"})
	assert.True(t, handled, "handled must be true even on flag error so RunE stops")
	require.Error(t, err)
}

func TestEmitDryRun_PopulatesProfile(t *testing.T) {
	SetProfile("staging")
	t.Cleanup(func() { SetProfile("") })

	var buf bytes.Buffer
	err := EmitDryRun(&buf, &FormatOptions{Mode: FormatJSON}, DryRunPlan{Action: "kb.create"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), `"profile":"staging"`, "expected profile field; got %q", buf.String())
}
