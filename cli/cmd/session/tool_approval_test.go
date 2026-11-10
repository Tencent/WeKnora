package sessioncmd

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeResolveSvc struct {
	err          error
	gotPendingID string
	gotReq       *sdk.ResolveToolApprovalRequest
}

func (s *fakeResolveSvc) ResolveToolApproval(_ context.Context, pendingID string, req *sdk.ResolveToolApprovalRequest) error {
	s.gotPendingID, s.gotReq = pendingID, req
	return s.err
}

func TestRunResolve_ApproveWithYes(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeResolveSvc{}
	opts := &ResolveOptions{PendingID: "p1", Yes: true}
	require.NoError(t, runResolve(context.Background(), opts, jsonOpts(), svc, &testutil.ConfirmPrompter{}))
	assert.Equal(t, "p1", svc.gotPendingID)
	assert.Equal(t, "approve", svc.gotReq.Decision)
	assert.Contains(t, out.String(), `"decision":"approve"`)
}

func TestRunResolve_ApproveWithYes_ConfirmationRequiredCode(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeResolveSvc{}
	// Verify the "no-yes" path yields CodeInputConfirmationRequired.
	err := runResolve(context.Background(), &ResolveOptions{PendingID: "p1", Yes: false}, jsonOpts(), svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Empty(t, svc.gotPendingID, "SDK must not be called without confirmation")
}

func TestRunResolve_RejectWithReason(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeResolveSvc{}
	opts := &ResolveOptions{PendingID: "p1", Reject: true, Reason: "wrong target", Yes: true}
	require.NoError(t, runResolve(context.Background(), opts, jsonOpts(), svc, &testutil.ConfirmPrompter{}))
	assert.Equal(t, "reject", svc.gotReq.Decision)
	assert.Equal(t, "wrong target", svc.gotReq.Reason)
}

func TestRunResolve_NoYesIsConfirmationRequired(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeResolveSvc{}
	err := runResolve(context.Background(), &ResolveOptions{PendingID: "p1"}, jsonOpts(), svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Empty(t, svc.gotPendingID, "SDK must not be called without confirmation")
}

func TestRunResolve_ModifiedArgsMustBeJSONObject(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runResolve(context.Background(), &ResolveOptions{PendingID: "p1", ModifiedArgs: `["not","an","object"]`, Yes: true}, jsonOpts(), &fakeResolveSvc{}, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JSON object")
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestRunResolve_RejectAndModifiedArgsConflict(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runResolve(context.Background(), &ResolveOptions{PendingID: "p1", Reject: true, ModifiedArgs: `{"a":1}`, Yes: true}, jsonOpts(), &fakeResolveSvc{}, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

// TestRunResolve_TTY_ConfirmYes verifies that on a TTY with Answer=true the SDK is called.
func TestRunResolve_TTY_ConfirmYes(t *testing.T) {
	out, _ := iostreams.SetForTestWithTTY(t)
	svc := &fakeResolveSvc{}
	p := &testutil.ConfirmPrompter{Answer: true}
	require.NoError(t, runResolve(context.Background(), &ResolveOptions{PendingID: "p1"}, textOpts(), svc, p))
	assert.True(t, p.Asked)
	assert.Equal(t, "p1", svc.gotPendingID)
	assert.Contains(t, out.String(), "Approved")
}

// TestRunResolve_TTY_ConfirmNo verifies that on a TTY with Answer=false the SDK is NOT called.
func TestRunResolve_TTY_ConfirmNo(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeResolveSvc{}
	p := &testutil.ConfirmPrompter{Answer: false}
	err := runResolve(context.Background(), &ResolveOptions{PendingID: "p1"}, textOpts(), svc, p)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.Empty(t, svc.gotPendingID, "answer=false must not call SDK")
	assert.Contains(t, errBuf.String(), "Aborted")
}

// TestRunResolve_ServiceError_Propagates verifies that SDK errors are wrapped and propagated.
func TestRunResolve_ServiceError_Propagates(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeResolveSvc{err: errors.New("HTTP error 404: not found")}
	err := runResolve(context.Background(), &ResolveOptions{PendingID: "p1", Yes: true}, jsonOpts(), svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

// TestRunResolve_ModifiedArgs_LegalObject verifies that a valid JSON object is passed through to the SDK.
func TestRunResolve_ModifiedArgs_LegalObject(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeResolveSvc{}
	opts := &ResolveOptions{PendingID: "p1", ModifiedArgs: `{"key":"val"}`, Yes: true}
	require.NoError(t, runResolve(context.Background(), opts, jsonOpts(), svc, &testutil.ConfirmPrompter{}))
	require.NotNil(t, svc.gotReq.ModifiedArgs)
	var m map[string]any
	require.NoError(t, json.Unmarshal(svc.gotReq.ModifiedArgs, &m))
	assert.Equal(t, "val", m["key"])
}

// TestRunResolve_ModifiedArgsEmptyObjectRejected verifies that {} is rejected before the SDK is called,
// because an empty object would silently wipe the original tool arguments (downstream executor uses len>0 to decide replacement).
func TestRunResolve_ModifiedArgsEmptyObjectRejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeResolveSvc{}
	opts := &ResolveOptions{PendingID: "p1", ModifiedArgs: `{}`, Yes: true}
	err := runResolve(context.Background(), opts, jsonOpts(), svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	assert.Contains(t, typed.Message, "empty object")
	assert.Empty(t, svc.gotPendingID, "SDK must not be called when --modified-args is an empty object")
}

// TestValidateModifiedArgs covers the shared input validator that now runs
// before the dry-run gate (so --dry-run --modified-args '{}' errors instead
// of previewing an arg-wiping action). Each case mirrors a runResolve guard.
func TestValidateModifiedArgs(t *testing.T) {
	cases := []struct {
		name    string
		opts    *ResolveOptions
		wantErr string // substring; "" means expect success
	}{
		{"unset is ok", &ResolveOptions{}, ""},
		{"valid object", &ResolveOptions{ModifiedArgs: `{"path":"/x"}`}, ""},
		{"empty object rejected", &ResolveOptions{ModifiedArgs: `{}`}, "empty object"},
		{"array rejected", &ResolveOptions{ModifiedArgs: `[1,2]`}, "non-null JSON object"},
		{"null rejected", &ResolveOptions{ModifiedArgs: `null`}, "non-null JSON object"},
		{"reject+modified conflict", &ResolveOptions{Reject: true, ModifiedArgs: `{"a":1}`}, "conflicts with --reject"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateModifiedArgs(tc.opts)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var typed *cmdutil.Error
			require.ErrorAs(t, err, &typed)
			assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
			assert.Contains(t, typed.Message, tc.wantErr)
			assert.Nil(t, got)
		})
	}
}
