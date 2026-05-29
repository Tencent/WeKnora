package kb

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// TestKBEdit_DryRun_RejectsNoMutationFlag: --dry-run must reject the same
// "no mutation flag supplied" invocation the live path rejects. Before the
// fix the validation lived in runEdit() (the live-only callee) and was
// reached only after HandleDryRun early-exited, so --dry-run silently
// emitted a no-op plan envelope. Industry standard (gh / kubectl / lark)
// validates before previewing.
func TestKBEdit_DryRun_RejectsNoMutationFlag(t *testing.T) {
	iostreams.SetForTest(t)
	f := kbDryRunFactory(t)
	root := withRootHarness(NewCmdEdit(f), "kb_abc", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject identically to live path")

	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed), "expected *cmdutil.Error, got %T %v", err, err)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code, "must use the same code runEdit emits")
}
