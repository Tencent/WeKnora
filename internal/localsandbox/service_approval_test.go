package localsandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeApprover struct {
	decisions []ApprovalDecision
	err       error
	requests  []ApprovalRequest
}

func (a *fakeApprover) Approve(_ context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	a.requests = append(a.requests, req)
	if a.err != nil {
		return ApprovalDecision{}, a.err
	}
	if len(a.decisions) == 0 {
		return ApprovalDecision{}, nil
	}
	d := a.decisions[0]
	a.decisions = a.decisions[1:]
	return d, nil
}

// deniedOnce makes the first spawn fail with stderr and every later spawn succeed.
func deniedOnce(backend *fakeBackend, stderr string) {
	spawns := 0
	backend.stderr = stderr
	backend.exit = ExitStatus{Code: 1}
	backend.onSpawn = func() {
		spawns++
		if spawns == 2 {
			backend.stderr = ""
			backend.exit = ExitStatus{Code: 0}
		}
	}
}

func TestRunAsksBeforeDelete(t *testing.T) {
	backend := &fakeBackend{}
	svc, root, _ := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "rm a.txt", Approver: approver})
	require.NoError(t, err)
	require.Equal(t, 0, res.Exit.Code)
	require.Len(t, backend.prepared, 1)
	require.Len(t, approver.requests, 1)
	req := approver.requests[0]
	require.Equal(t, ReasonDelete, req.Reason)
	require.Equal(t, "rm a.txt", req.Command)
	require.Equal(t, root, req.Cwd)
	require.True(t, req.AllowSession)
	require.Nil(t, req.Proposed, "a delete inside the workspace needs no directory")
}

func TestRunRefusesDeleteWithoutApprover(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "rm a.txt"})
	require.NoError(t, err)
	require.Equal(t, 1, res.Exit.Code)
	require.Contains(t, res.Notice, "needs the user's approval")
	require.Empty(t, backend.prepared)
}

func TestRunDeleteRejectedDoesNotSpawn(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: false, Reason: "keep it"}}}

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "rm a.txt", Approver: approver})
	require.NoError(t, err)
	require.Contains(t, res.Notice, "keep it")
	require.Empty(t, backend.prepared)
}

func TestRunApproverErrorRefusesDelete(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	approver := &fakeApprover{err: errors.New("the approval request timed out")}

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "rm a.txt", Approver: approver})
	require.NoError(t, err)
	require.Contains(t, res.Notice, "timed out")
	require.Empty(t, backend.prepared)
}

func TestRunRemembersSessionDeleteApprovalPerCwd(t *testing.T) {
	backend := &fakeBackend{}
	svc, root, _ := approvalFixture(t, backend)
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}, {Approved: true}}}
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		_, err := svc.Run(ctx, RunRequest{SessionID: "s1", Command: "rm a.txt", Approver: approver})
		require.NoError(t, err)
	}
	require.Len(t, approver.requests, 1)

	_, err := svc.Run(ctx, RunRequest{SessionID: "s1", Command: "rm a.txt", WorkDir: sub, Approver: approver})
	require.NoError(t, err)
	require.Len(t, approver.requests, 2, "a different cwd must ask again")
}

func runAll(t *testing.T, svc *Service, approver *fakeApprover, commands ...string) {
	t.Helper()
	for _, command := range commands {
		_, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: command, Approver: approver})
		require.NoError(t, err, command)
	}
}

// The model rarely repeats a command word for word. The session remembers the
// shape of each delete (program and options), so other files, another order
// of options, or other commands around it still match.
func TestRunSessionDeleteApprovalCoversTheSameRule(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}

	runAll(t, svc, approver, "rm -rf build")
	require.Len(t, approver.requests, 1)
	require.Equal(t, []string{"rm -f -r"}, approver.requests[0].SessionRules)

	runAll(t, svc, approver, "rm -fr dist", "ls && rm -r -f out", "sudo rm -rf x", "rm a.txt")
	require.Len(t, approver.requests, 1, "the approved rule covers these")

	for _, command := range []string{"rm -rfv y", "git clean -fd", "rmdir d"} {
		before := len(approver.requests)
		runAll(t, svc, approver, command)
		require.Len(t, approver.requests, before+1, command)
	}
}

func TestRunSessionDeleteApprovalDoesNotWidenOptions(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}

	runAll(t, svc, approver, "rm a.txt", "rm -rf dir")
	require.Len(t, approver.requests, 2, "a plain rm approval must not cover rm -rf")
}

func TestRunSessionDeleteApprovalNeedsEveryDeleteCovered(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}

	runAll(t, svc, approver, "rm a.txt", "rm b.txt && rmdir d")
	require.Len(t, approver.requests, 2)
	require.Equal(t, []string{"rm", "rmdir"}, approver.requests[1].SessionRules)
}

// A substitution hides a delete the rule cannot name; an approved rm next to
// it must not let it through.
func TestRunHiddenDeleteIsNotCoveredByAnApprovedRule(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}

	runAll(t, svc, approver, "rm a.txt", "rm a.txt; echo $(rm -rf .)")
	require.Len(t, approver.requests, 2)
	require.False(t, approver.requests[1].AllowSession)
	require.Empty(t, approver.requests[1].SessionRules)
}

func TestRunShellScriptDeleteCannotBeRememberedForTheSession(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}

	_, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: "rm a.txt; bash -c 'rm -rf .'", Approver: approver,
	})
	require.NoError(t, err)
	require.Len(t, approver.requests, 1)
	require.False(t, approver.requests[0].AllowSession)
}

func TestRunDangerousDeleteIsNeverRemembered(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}, {Approved: true}}}
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		_, err := svc.Run(ctx, RunRequest{SessionID: "s1", Command: "rm -rf ~", Approver: approver})
		require.NoError(t, err)
	}
	require.Len(t, approver.requests, 2)
	require.Equal(t, ReasonDangerous, approver.requests[0].Reason)
	require.False(t, approver.requests[0].AllowSession)
	require.Empty(t, approver.requests[0].SessionRules)
}

// shell_exec wraps stdin and skill environments around the command; the
// wrapper must not hide a delete from the approval check.
func TestRunReviewsOriginalCommand(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	_, err := svc.Run(context.Background(), RunRequest{
		SessionID:     "s1",
		Command:       "printf %s cm0gYS50eHQ= | base64 -d | /bin/bash --noprofile --norc -c 'sh'",
		ReviewCommand: "rm a.txt",
		Approver:      approver,
	})
	require.NoError(t, err)
	require.Len(t, approver.requests, 1)
	require.Equal(t, "rm a.txt", approver.requests[0].Command)
}

func TestRunEscalatesOutsideWriteAndRetriesOnce(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	deniedOnce(backend, "touch: "+filepath.Join(other, "a")+": Operation not permitted")
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	res, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: "touch " + filepath.Join(other, "a"), Approver: approver,
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.Exit.Code)
	require.Len(t, backend.prepared, 2)
	require.NotEqual(t, backend.prepared[0], backend.prepared[1])
	require.Len(t, approver.requests, 1)
	req := approver.requests[0]
	require.Equal(t, ReasonDenied, req.Reason)
	require.Equal(t, &Grant{Path: other, Access: AccessWrite}, req.Proposed)
	require.True(t, req.FirstAttemptRan)
	require.Empty(t, svc.SessionGrants("s1"), "a one-time approval must not stick")
}

func TestRunEscalationRetriesAtMostOnce(t *testing.T) {
	backend := &fakeBackend{exit: ExitStatus{Code: 1}}
	svc, _, other := approvalFixture(t, backend)
	backend.stderr = "touch: " + filepath.Join(other, "a") + ": Operation not permitted"
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}, {Approved: true}}}

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "touch x", Approver: approver})
	require.NoError(t, err)
	require.Len(t, backend.prepared, 2)
	require.Len(t, approver.requests, 1)
	require.Contains(t, res.Notice, "[sandbox] blocked")
}

func TestRunEscalationAsksAgainForASecondDirectory(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	second := filepath.Join(filepath.Dir(other), "second")
	require.NoError(t, os.MkdirAll(second, 0o755))
	denials := []string{
		"touch: " + filepath.Join(other, "a") + ": Operation not permitted",
		"touch: " + filepath.Join(second, "b") + ": Operation not permitted",
	}
	spawns := 0
	backend.onSpawn = func() {
		if spawns < len(denials) {
			backend.stderr = denials[spawns]
			backend.exit = ExitStatus{Code: 1}
		} else {
			backend.stderr = ""
			backend.exit = ExitStatus{Code: 0}
		}
		spawns++
	}
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}, {Approved: true}}}

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "touch x", Approver: approver})
	require.NoError(t, err)
	require.Equal(t, 0, res.Exit.Code)
	require.Len(t, approver.requests, 2)
	require.Equal(t, other, approver.requests[0].Proposed.Path)
	require.Equal(t, second, approver.requests[1].Proposed.Path)
	require.Len(t, backend.prepared, 3)
}

func TestRunDoesNotEscalateWhenExitIsZero(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	path := filepath.Join(other, "a")
	backend.onSpawn = func() {
		backend.stderr = "bash: " + path + ": Operation not permitted"
		backend.exit = ExitStatus{Code: 0}
	}
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "touch x", Approver: approver})
	require.NoError(t, err)
	require.Equal(t, 0, res.Exit.Code)
	require.Empty(t, res.Notice)
	require.Empty(t, approver.requests)
	require.Len(t, backend.prepared, 1)
}

// A delete outside the workspace asks once, before it runs, for both the
// delete and the directory, and then runs a single time with that access.
func TestRunOutsideDeleteAsksOnceWithTheDirectory(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	target := filepath.Join(other, "test.txt")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	runAll(t, svc, approver, "ls", "rm "+target)

	require.Len(t, approver.requests, 1)
	req := approver.requests[0]
	require.Equal(t, ReasonDelete, req.Reason)
	require.Equal(t, &Grant{Path: other, Access: AccessWrite}, req.Proposed)
	require.False(t, req.FirstAttemptRan)
	require.Len(t, backend.prepared, 2, "the delete must run once")
	require.NotEqual(t, backend.prepared[0], backend.prepared[1], "the delete must run with the directory open")
	require.Empty(t, svc.SessionGrants("s1"), "a one-time approval must not stick")
}

func TestRunOutsideDeleteSessionApprovalCoversTheNextFile(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}

	runAll(t, svc, approver, "rm "+filepath.Join(other, "a.txt"), "rm "+filepath.Join(other, "b.txt"))

	require.Len(t, approver.requests, 1)
	require.Equal(t, []Grant{{Path: other, Access: AccessWrite}}, svc.SessionGrants("s1"))
	require.Len(t, backend.prepared, 2)
}

// Removing a directory writes its parent; a grant on the directory itself
// would leave rmdir blocked by the writable-root anchor.
func TestRunOutsideDirectoryDeleteGrantsTheParent(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	dir := filepath.Join(other, "dir")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	runAll(t, svc, approver, "rm -rf "+dir)

	require.Len(t, approver.requests, 1)
	require.Equal(t, &Grant{Path: other, Access: AccessWrite}, approver.requests[0].Proposed)
}

// A session-approved rule skips the delete question, but the directory was
// never opened; that is still one card, not a run that fails first.
func TestRunApprovedDeleteRuleStillAsksOnceForANewDirectory(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}, {Approved: true}}}

	runAll(t, svc, approver, "rm a.txt", "rm "+filepath.Join(other, "b.txt"))

	require.Len(t, approver.requests, 2)
	require.Nil(t, approver.requests[0].Proposed)
	require.Equal(t, ReasonDelete, approver.requests[1].Reason)
	require.Equal(t, &Grant{Path: other, Access: AccessWrite}, approver.requests[1].Proposed)
	require.Len(t, backend.prepared, 2)
}

func TestRunOutsideDeleteCannotBeNarrowedToRead(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{
		Approved: true, Session: true, Grant: &Grant{Path: other, Access: AccessRead},
	}}}

	runAll(t, svc, approver, "rm "+filepath.Join(other, "a.txt"))

	require.Equal(t, []Grant{{Path: other, Access: AccessWrite}}, svc.SessionGrants("s1"))
}

func TestRunDeleteOfAnUngrantableTargetOffersNoDirectory(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	runAll(t, svc, approver, "rm ~/.ssh/id_rsa")

	require.Len(t, approver.requests, 1)
	require.Nil(t, approver.requests[0].Proposed)
}

func TestRunRejectedDirectoryAsksAgainOnALaterDelete(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	target := filepath.Join(other, "a")
	backend.stderr = "touch: " + target + ": Operation not permitted"
	backend.exit = ExitStatus{Code: 1}
	approver := &fakeApprover{decisions: []ApprovalDecision{
		{Approved: false, Reason: "no"},
		{Approved: true},
		{Approved: false, Reason: "no"},
	}}
	ctx := context.Background()

	res, err := svc.Run(ctx, RunRequest{SessionID: "s1", Command: "touch x", Approver: approver})
	require.NoError(t, err)
	require.Contains(t, res.Notice, `"no"`)
	require.Equal(t, ReasonDenied, approver.requests[0].Reason)

	res, err = svc.Run(ctx, RunRequest{SessionID: "s1", Command: "rm " + target, Approver: approver})
	require.NoError(t, err)
	require.Len(t, approver.requests, 2)
	require.Equal(t, ReasonDelete, approver.requests[1].Reason)
	require.Equal(t, other, approver.requests[1].Proposed.Path)
	require.NotContains(t, res.Notice, "already denied")
}

func TestRunSessionGrantCoversNextCommand(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	deniedOnce(backend, "touch: "+filepath.Join(other, "a")+": Operation not permitted")
	approver := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}
	ctx := context.Background()

	_, err := svc.Run(ctx, RunRequest{SessionID: "s1", Command: "touch x", Approver: approver})
	require.NoError(t, err)
	require.Equal(t, []Grant{{Path: other, Access: AccessWrite}}, svc.SessionGrants("s1"))

	guard, _, err := svc.GuardForSession(ctx, "s1")
	require.NoError(t, err)
	_, err = guard.CheckWrite(filepath.Join(other, "b"))
	require.NoError(t, err, "file tools must see the session grant too")
}

func TestRunNarrowsGrantToRead(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	deniedOnce(backend, "cat: "+filepath.Join(other, "a")+": Operation not permitted")
	approver := &fakeApprover{decisions: []ApprovalDecision{{
		Approved: true, Session: true, Grant: &Grant{Path: "/ignored", Access: AccessRead},
	}}}

	_, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "cat x", Approver: approver})
	require.NoError(t, err)
	require.Equal(t, []Grant{{Path: other, Access: AccessRead}}, svc.SessionGrants("s1"))
}

func TestRunDoesNotEscalateInsideWorkspace(t *testing.T) {
	backend := &fakeBackend{}
	svc, root, _ := approvalFixture(t, backend)
	backend.stderr = "touch: " + filepath.Join(root, "a") + ": Permission denied"
	backend.exit = ExitStatus{Code: 1}
	approver := &fakeApprover{}

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "touch a", Approver: approver})
	require.NoError(t, err)
	require.Empty(t, approver.requests)
	require.Len(t, backend.prepared, 1)
	require.Contains(t, res.Notice, "[sandbox] blocked")
}

func TestRunEscalationWithoutApproverOnlyReports(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	backend.stderr = "touch: " + filepath.Join(other, "a") + ": Operation not permitted"
	backend.exit = ExitStatus{Code: 1}

	res, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: "touch x"})
	require.NoError(t, err)
	require.Len(t, backend.prepared, 1)
	require.Contains(t, res.Notice, "[sandbox] blocked")
	require.Contains(t, res.Notice, "nobody can approve")
}
