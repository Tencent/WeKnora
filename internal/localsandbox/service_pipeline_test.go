package localsandbox

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// cancelingApprover approves and then cancels the turn, the order a user
// produces by clicking Allow just before Stop.
type cancelingApprover struct {
	cancel context.CancelFunc
	calls  int
}

func (a *cancelingApprover) Approve(context.Context, ApprovalRequest) (ApprovalDecision, error) {
	a.calls++
	a.cancel()
	return ApprovalDecision{Approved: true}, nil
}

func TestRunAsksTheUserInAskAndTheReviewerInAuto(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}
	reviewer := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}
	ctx := context.Background()
	target := filepath.Join(other, "a.txt")

	_, err := svc.Run(ctx, RunRequest{SessionID: "s1", Command: "rm " + target, Approver: user, Reviewer: reviewer})
	require.NoError(t, err)
	require.Len(t, user.requests, 1)
	require.Empty(t, reviewer.requests)

	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))
	_, err = svc.Run(ctx, RunRequest{SessionID: "s1", Command: "rm " + target, Approver: user, Reviewer: reviewer})
	require.NoError(t, err)
	require.Len(t, user.requests, 1, "auto never asks the user")
	require.Len(t, reviewer.requests, 1)
}

func TestApprovalRequestCarriesTheReviewSpecAndTargets(t *testing.T) {
	backend := &fakeBackend{}
	svc, root, other := approvalFixture(t, backend)
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}
	target := filepath.Join(other, "a.txt")
	spec := ReviewSpec{Tool: "shell_exec", Command: "rm " + target, Env: map[string]string{"A": "1"}, SkillName: "pdf"}

	_, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: "wrapped " + spec.Command, Spec: spec, Approver: user,
	})
	require.NoError(t, err)
	req := user.requests[0]
	require.Equal(t, spec, req.Spec)
	require.Equal(t, root, req.WorkspaceRoot)
	require.True(t, req.TargetsComplete)
	require.Equal(t, []TargetFact{{Path: target, Type: "missing"}}, req.Targets)
}

func TestRunDoesNotExecuteAfterTheTurnStopped(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	ctx, cancel := context.WithCancel(context.Background())
	approver := &cancelingApprover{cancel: cancel}

	_, err := svc.Run(ctx, RunRequest{
		SessionID: "s1", Command: "rm " + filepath.Join(other, "a.txt"), Approver: approver,
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, approver.calls)
	require.Empty(t, backend.prepared, "an approval made just before stop must not run")
}

func TestReviewSpecTextIncludesAProgramStdin(t *testing.T) {
	require.Equal(t, "bash\nrm -rf out", ReviewSpec{Command: "bash", Stdin: "rm -rf out", StdinIsProgram: true}.Text())
	require.Equal(t, "cat", ReviewSpec{Command: "cat", Stdin: "rm -rf out"}.Text())
	require.Equal(t, "ls", ReviewSpec{Command: "ls"}.Text())
}
