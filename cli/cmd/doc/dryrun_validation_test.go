// Package doc — dryrun_validation_test.go asserts that --dry-run on
// doc subcommands rejects identically to the live path (industry standard:
// gh / kubectl / lark all validate before previewing). Before the surrounding
// fix, validation lived in runX() and was reached only after HandleDryRun
// short-circuited, so --dry-run silently emitted plan envelopes for inputs
// the live path would reject — agents got false-positive previews.
package doc

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

// docDryRunFactory builds a Factory whose Client closure panics if invoked —
// dry-run must early-exit before any SDK call. Factory.ResolveKB is a method
// that short-circuits when --kb is a uuid (IsKBID), so the tests below pass
// a literal uuid to avoid the Client()-based name lookup path.
func docDryRunFactory(t *testing.T) *cmdutil.Factory {
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

// withRootHarnessDoc wraps a doc subcommand under a synthetic root cmd that
// registers the global persistent flags (mirrors addGlobalFlags in
// cmd/root.go).
func withRootHarnessDoc(sub *cobra.Command, args ...string) *cobra.Command {
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

// TestDocDelete_DryRun_RejectsAllWithoutKB: --all without --kb is rejected
// on the live path; --dry-run must do the same.
func TestDocDelete_DryRun_RejectsAllWithoutKB(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmdDelete(docDryRunFactory(t)),
		"--all", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed), "expected *cmdutil.Error, got %T %v", err, err)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

// TestDocDelete_DryRun_RejectsAllWithIDs: --all + positional ids is mutually
// exclusive; --dry-run must reject the same.
func TestDocDelete_DryRun_RejectsAllWithIDs(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmdDelete(docDryRunFactory(t)),
		"doc_a", "--all", "--kb", "00000000-0000-0000-0000-000000000001", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	// FlagError ⇒ exit 2.
	assert.Equal(t, 2, cmdutil.ExitCode(err))
}

// TestDocDelete_DryRun_RejectsNoIDsNoAll: no positional ids and no --all is
// rejected on the live path; --dry-run must do the same.
func TestDocDelete_DryRun_RejectsNoIDsNoAll(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmdDelete(docDryRunFactory(t)),
		"--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	assert.Equal(t, 2, cmdutil.ExitCode(err))
}

// TestDocFetch_DryRun_RejectsInvalidURL: malformed URL is rejected on the
// live path via cmdutil.ValidateHTTPURL; --dry-run must do the same.
func TestDocFetch_DryRun_RejectsInvalidURL(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmdFetch(docDryRunFactory(t)),
		"not-a-url", "--kb", "00000000-0000-0000-0000-000000000001", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject malformed URL")
}

// TestDocUpload_DryRun_RejectsMissingPositional: positional file path is
// required (or --recursive); --dry-run must reject identically.
func TestDocUpload_DryRun_RejectsMissingPositional(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmdUpload(docDryRunFactory(t)),
		"--kb", "00000000-0000-0000-0000-000000000001", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject missing positional file path")
}

// TestDocUpload_DryRun_RejectsBadMetadata: --metadata key=value enforces
// the `key=value` shape; --dry-run must reject malformed values too.
func TestDocUpload_DryRun_RejectsBadMetadata(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmdUpload(docDryRunFactory(t)),
		"./somefile", "--kb", "00000000-0000-0000-0000-000000000001", "--metadata", "no-equals-sign",
		"--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject malformed --metadata")
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

// TestDocUpload_DryRun_RejectsBadEnableMultimodel: --enable-multimodel
// expects a parseable tri-bool; --dry-run must reject the same garbage
// values the live path rejects.
func TestDocUpload_DryRun_RejectsBadEnableMultimodel(t *testing.T) {
	iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmdUpload(docDryRunFactory(t)),
		"./somefile", "--kb", "00000000-0000-0000-0000-000000000001", "--enable-multimodel=garbage",
		"--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}
