package sessioncmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

// AGENTS.md exit-10 contract: "retry_argv showing the exact argv that would
// proceed". The decision-relevant flags are part of that argv — dropping
// --modified-args means the retry envelope advertises an approval of the
// ORIGINAL (possibly dangerous) arguments.
func TestRunResolve_Exit10RetryArgvCarriesModifiedArgsAndReason(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeResolveSvc{}
	err := runResolve(context.Background(), &ResolveOptions{
		PendingID:    "p1",
		ModifiedArgs: `{"cmd":"safe command"}`,
		Reason:       "reviewed by human",
	}, jsonOpts(), svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	require.NotEmpty(t, typed.RetryArgv)
	assert.Contains(t, typed.RetryArgv, "--modified-args")
	assert.Contains(t, typed.RetryArgv, `{"cmd":"safe command"}`)
	assert.Contains(t, typed.RetryArgv, "--reason")
	assert.Contains(t, typed.RetryArgv, "reviewed by human")
	assert.Contains(t, typed.RetryArgv, "-y")
	assert.NotContains(t, typed.RetryArgv, "--reject")
	assert.Empty(t, svc.gotPendingID, "SDK must not be called without confirmation")
}

func TestRunResolve_Exit10RetryArgvRejectCarriesReason(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeResolveSvc{}
	err := runResolve(context.Background(), &ResolveOptions{
		PendingID: "p1",
		Reject:    true,
		Reason:    "wrong target",
	}, jsonOpts(), svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	require.NotEmpty(t, typed.RetryArgv)
	assert.Contains(t, typed.RetryArgv, "--reject")
	assert.Contains(t, typed.RetryArgv, "--reason")
	assert.Contains(t, typed.RetryArgv, "wrong target")
}

func TestRunResolve_Exit10RetryArgvPlainApproveStaysMinimal(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeResolveSvc{}
	err := runResolve(context.Background(), &ResolveOptions{PendingID: "p1"},
		jsonOpts(), svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.NotContains(t, typed.RetryArgv, "--modified-args")
	assert.NotContains(t, typed.RetryArgv, "--reason")
	assert.NotContains(t, typed.RetryArgv, "--reject")
}
