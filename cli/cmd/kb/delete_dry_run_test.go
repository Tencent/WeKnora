package kb

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// TestKBDelete_DryRun_NoExit10: --dry-run must bypass ConfirmDestructive.
// Non-TTY without -y normally yields exit 10 (CodeInputConfirmationRequired);
// with --dry-run it must early-exit successfully (exit 0) because no real
// delete happens — preview implies no execution, so the confirmation gate
// is irrelevant.
func TestKBDelete_DryRun_NoExit10(t *testing.T) {
	out, _ := iostreams.SetForTest(t) // non-TTY
	f := kbDryRunFactory(t)
	root := withRootHarness(NewCmdDelete(f), "kb_to_preview", "--dry-run", "--format", "json")
	err := root.Execute()
	require.NoError(t, err, "dry-run must succeed (exit 0); ConfirmDestructive must be skipped")
	assert.Equal(t, 0, cmdutil.ExitCode(err), "exit code must be 0, not 10 (confirmation_required)")

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
	assert.Equal(t, "kb.delete", env.Meta.Plan["action"])
}

// TestKBDelete_DryRunWithYes_EquivalentToDryRunAlone: -y is a no-op when
// --dry-run is set (no real delete happens, so no confirmation gate to skip).
// The envelope shape must match the dry-run-alone case exactly so agents can
// rely on the plan output regardless of whether the human pre-approved.
func TestKBDelete_DryRunWithYes_EquivalentToDryRunAlone(t *testing.T) {
	// First invocation: --dry-run alone.
	outA, _ := iostreams.SetForTest(t)
	rootA := withRootHarness(NewCmdDelete(kbDryRunFactory(t)),
		"kb_to_preview", "--dry-run", "--format", "json")
	require.NoError(t, rootA.Execute())
	gotA := outA.String()

	// Second invocation: --dry-run + -y.
	outB, _ := iostreams.SetForTest(t)
	rootB := withRootHarness(NewCmdDelete(kbDryRunFactory(t)),
		"kb_to_preview", "--dry-run", "-y", "--format", "json")
	require.NoError(t, rootB.Execute(), "--dry-run + -y must succeed identically to --dry-run alone")
	gotB := outB.String()

	assert.JSONEq(t, gotA, gotB,
		"--dry-run output must be identical with or without -y; -y is a no-op under dry-run")
}
