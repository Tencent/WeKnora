package localsandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func autoFixture(t *testing.T, backend *fakeBackend) (*Service, string, string, *fakeApprover, *fakeApprover) {
	t.Helper()
	svc, root, other := approvalFixture(t, backend)
	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))
	return svc, root, other, &fakeApprover{}, &fakeApprover{
		decisions: []ApprovalDecision{{Approved: true}, {Approved: true}},
	}
}

func runAuto(t *testing.T, svc *Service, user, reviewer *fakeApprover, command string) *RunResult {
	t.Helper()
	res, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: command, Approver: user, Reviewer: reviewer,
	})
	require.NoError(t, err, command)
	return res
}

func TestAutoReviewsFindAndGitCleanOutsideTheWorkspace(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other, user, reviewer := autoFixture(t, backend)
	reviewer.decisions = []ApprovalDecision{{Approved: false, Reason: "no"}, {Approved: false, Reason: "no"}}

	for _, command := range []string{
		"find " + other + " -delete",
		"git -C " + other + " clean -fdx",
	} {
		backend.prepared = nil
		user.requests = nil
		reviewer.requests = nil
		res := runAuto(t, svc, user, reviewer, command)
		require.Len(t, reviewer.requests, 1, command)
		require.Equal(t, ReasonDelete, reviewer.requests[0].Reason, command)
		require.Equal(t, &Grant{Path: other, Access: AccessWrite}, reviewer.requests[0].Proposed, command)
		require.Empty(t, user.requests, command)
		require.Empty(t, backend.prepared, command)
		require.Equal(t, 1, res.Exit.Code, command)
	}
}

func TestAutoRefusesFindOrGitCleanOfHomeOrRoot(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _, user, reviewer := autoFixture(t, backend)

	for _, command := range []string{
		"find ~ -delete",
		"find / -delete",
		"git -C ~ clean -fdx",
		"bash -c 'find ~ -delete'",
	} {
		res := runAuto(t, svc, user, reviewer, command)
		require.Equal(t, dangerousDeleteNotice, res.Notice, command)
		require.Equal(t, 1, res.Exit.Code, command)
	}
	require.Empty(t, user.requests)
	require.Empty(t, reviewer.requests)
	require.Empty(t, backend.prepared)
}

func TestAutoRunsAWorkspaceFindWithoutAsking(t *testing.T) {
	backend := &fakeBackend{}
	svc, root, _, user, reviewer := autoFixture(t, backend)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "build"), 0o755))

	runAuto(t, svc, user, reviewer, "find build -delete")
	require.Empty(t, reviewer.requests)
	require.Empty(t, user.requests)
	require.Len(t, backend.prepared, 1)
}

func TestAutoRunsAWorkspaceDeleteWithoutAsking(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _, user, reviewer := autoFixture(t, backend)

	runAuto(t, svc, user, reviewer, "rm a.txt")
	runAuto(t, svc, user, reviewer, "rm -rf build")
	require.Empty(t, reviewer.requests)
	require.Empty(t, user.requests)
	require.Len(t, backend.prepared, 2)
}

func TestAutoReviewsAnOutsideDeleteWithItsDirectory(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other, user, reviewer := autoFixture(t, backend)

	runAuto(t, svc, user, reviewer, "rm "+filepath.Join(other, "a.txt"))
	require.Len(t, reviewer.requests, 1)
	req := reviewer.requests[0]
	require.Equal(t, ReasonDelete, req.Reason)
	require.False(t, req.AllowSession, "a reviewer answer is never remembered")
	require.Equal(t, &Grant{Path: other, Access: AccessWrite}, req.Proposed)
	require.Empty(t, svc.SessionGrants("s1"))
}

func TestAutoReviewsAHiddenDelete(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _, user, reviewer := autoFixture(t, backend)

	runAuto(t, svc, user, reviewer, "bash -c 'rm x'")
	require.Len(t, reviewer.requests, 1)
	require.True(t, reviewer.requests[0].Hidden)
}

func TestDangerousDeleteIsRefusedInBothModesWithoutAsking(t *testing.T) {
	for _, mode := range []ApprovalMode{ModeAsk, ModeAuto} {
		backend := &fakeBackend{}
		svc, _, _ := approvalFixture(t, backend)
		require.NoError(t, svc.SetSessionMode("s1", mode))
		user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}
		reviewer := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

		for _, command := range []string{"rm -rf ~", "rm -rf /", "bash -c 'rm -rf ~'"} {
			res, err := svc.Run(context.Background(), RunRequest{
				SessionID: "s1", Command: command, Approver: user, Reviewer: reviewer,
			})
			require.NoError(t, err, command)
			require.Equal(t, 1, res.Exit.Code, command)
			require.Equal(t, dangerousDeleteNotice, res.Notice, command)
		}
		require.Empty(t, user.requests, mode)
		require.Empty(t, reviewer.requests, mode)
		require.Empty(t, backend.prepared, mode)
	}
}

func TestAutoEscalatesABlockedFileWithAFileGrant(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other, user, reviewer := autoFixture(t, backend)
	target := filepath.Join(other, "b.txt")
	deniedOnce(backend, "bash: "+target+": Operation not permitted")

	res := runAuto(t, svc, user, reviewer, "echo x > "+target)
	require.Equal(t, 0, res.Exit.Code)
	require.Len(t, reviewer.requests, 1)
	require.Equal(t, &Grant{Path: target, Access: AccessWrite, File: true}, reviewer.requests[0].Proposed)
	require.Empty(t, svc.SessionGrants("s1"))
}

func TestAutoEscalatesABlockedDirectoryWithADirectoryGrant(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other, user, reviewer := autoFixture(t, backend)
	dir := filepath.Join(other, "dir")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	deniedOnce(backend, "touch: "+dir+": Operation not permitted")

	runAuto(t, svc, user, reviewer, "touch "+filepath.Join(dir, "x"))
	require.Equal(t, &Grant{Path: dir, Access: AccessWrite}, reviewer.requests[0].Proposed)
}

func TestAutoWithoutAReviewerRefuses(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	res, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: "rm " + filepath.Join(other, "a.txt"), Approver: user,
	})
	require.NoError(t, err)
	require.Contains(t, res.Notice, "nobody can approve")
	require.Empty(t, user.requests, "auto never falls back to the user")
}

func TestAutoRefusalNamesTheReviewer(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, other := approvalFixture(t, backend)
	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))
	reviewer := &fakeApprover{decisions: []ApprovalDecision{{Approved: false, Reason: "not asked for"}}}

	res, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: "rm " + filepath.Join(other, "a.txt"), Reviewer: reviewer,
	})
	require.NoError(t, err)
	require.Contains(t, res.Notice, `auto review denied this delete command: "not asked for"`)
}
