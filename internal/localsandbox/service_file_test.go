package localsandbox

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func overwrite(content string) func([]byte, bool) ([]byte, error) {
	return func([]byte, bool) ([]byte, error) { return []byte(content), nil }
}

func TestAuthorizeFileChangeInsideScopeAsksNobody(t *testing.T) {
	svc, root, _ := approvalFixture(t, &fakeBackend{})
	user := &fakeApprover{}

	got, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: filepath.Join(root, "a.md"), Mode: "overwrite", Apply: overwrite("x"), Approver: user,
	})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, user.requests)
}

func TestAuthorizeFileChangeAsksForTheOneFileWithItsContent(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	target := filepath.Join(other, "a.md")
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	got, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: target, Mode: "overwrite", Apply: overwrite("hello"),
		Spec: ReviewSpec{Tool: "write_sandbox_file"}, Approver: user,
	})
	require.NoError(t, err)
	require.Equal(t, Grant{Path: target, Access: AccessWrite, File: true}, got.Grant)
	require.Equal(t, sha256.Sum256([]byte("hello")), got.Digest)

	req := user.requests[0]
	require.Equal(t, ReasonFileWrite, req.Reason)
	require.Equal(t, "overwrite", req.WriteMode)
	require.Equal(t, "hello", req.WriteContent)
	require.True(t, req.AllowSession)
	require.Equal(t, "write_sandbox_file", req.Spec.Tool)
	require.Empty(t, svc.SessionGrants("s1"), "allow once does not stick")
}

func TestAuthorizeFileChangeSendsADiffForAnEdit(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	target := filepath.Join(other, "a.md")
	require.NoError(t, os.WriteFile(target, []byte("one\ntwo\n"), 0o644))
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}

	_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: target, Mode: "edit", Approver: user,
		Apply: func(cur []byte, exists bool) ([]byte, error) {
			require.True(t, exists)
			require.Equal(t, "one\ntwo\n", string(cur))
			return []byte("one\nTWO\n"), nil
		},
	})
	require.NoError(t, err)
	require.Contains(t, user.requests[0].EditDiff, "-two")
	require.Contains(t, user.requests[0].EditDiff, "+TWO")
	require.Empty(t, user.requests[0].WriteContent)
	require.Equal(t, len("one\nTWO\n"), user.requests[0].WriteBytes)
	require.Equal(t, []Grant{{Path: target, Access: AccessWrite, File: true}}, svc.SessionGrants("s1"))
}

// A miss must still ask: answering at once would tell the model what the file
// does not contain, without anyone approving the read.
func TestAuthorizeFileChangeAsksBeforeReportingAFailedEdit(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	target := filepath.Join(other, "secret.md")
	require.NoError(t, os.WriteFile(target, []byte("private text"), 0o644))
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}

	_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: target, Mode: "edit", Preview: "- guess\n+ other\n", Approver: user,
		Apply: func([]byte, bool) ([]byte, error) { return nil, errors.New(`old_string "private text" not unique`) },
	})
	require.ErrorIs(t, err, ErrEditNotApplicable)
	require.NotContains(t, err.Error(), "private")
	require.Equal(t, "- guess\n+ other\n", user.requests[0].EditDiff)
	require.Empty(t, svc.SessionGrants("s1"), "nothing was written, so nothing is remembered")
}

func TestAuthorizeFileChangeRefusedFailedEditLooksRefused(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	target := filepath.Join(other, "secret.md")
	require.NoError(t, os.WriteFile(target, []byte("private text"), 0o644))
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: false, Reason: "no"}}}

	_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: target, Mode: "edit", Approver: user,
		Apply: func([]byte, bool) ([]byte, error) { return nil, errors.New("not found") },
	})
	require.ErrorIs(t, err, ErrFileChangeRefused)
}

func TestAuthorizeFileChangeRefusesUngrantablePaths(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	user := &fakeApprover{}
	for _, path := range []string{other, filepath.Join(other, "missing", "a.md")} {
		_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
			SessionID: "s1", Path: path, Mode: "overwrite", Apply: overwrite("x"), Approver: user,
		})
		require.ErrorIs(t, err, ErrPathDenied, path)
	}
	require.Empty(t, user.requests)
}

func TestAuthorizeFileChangeInAutoAsksTheReviewerOnce(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))
	user := &fakeApprover{}
	reviewer := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}

	_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: filepath.Join(other, "a.md"), Mode: "overwrite", Apply: overwrite("x"),
		Approver: user, Reviewer: reviewer,
	})
	require.NoError(t, err)
	require.Empty(t, user.requests)
	require.False(t, reviewer.requests[0].AllowSession)
	require.Empty(t, svc.SessionGrants("s1"), "a reviewer cannot widen the session")
}

func TestAuthorizeFileChangeRefusalIsAnError(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: false, Reason: "no"}}}
	_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: filepath.Join(other, "a.md"), Mode: "overwrite", Apply: overwrite("x"), Approver: user,
	})
	require.ErrorIs(t, err, ErrFileChangeRefused)
	require.Contains(t, err.Error(), `"no"`)
}

func TestAuthorizeFileChangeOverwriteDoesNotReadExistingBytes(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	target := filepath.Join(other, "secret.md")
	require.NoError(t, os.WriteFile(target, []byte("old secret"), 0o644))
	// Mode 0 makes a read fail. Overwrite must still ask, and the card shows
	// the new content, because the previous bytes are not required.
	require.NoError(t, os.Chmod(target, 0))
	t.Cleanup(func() { _ = os.Chmod(target, 0o644) })
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	got, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: target, Mode: "overwrite", Approver: user,
		Apply: func(cur []byte, _ bool) ([]byte, error) {
			if len(cur) != 0 {
				return nil, errors.New("overwrite was given the previous bytes")
			}
			return []byte("fresh"), nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256([]byte("fresh")), got.Digest)
	require.Equal(t, "fresh", user.requests[0].WriteContent)
	require.Empty(t, user.requests[0].EditDiff)
}

func TestAuthorizeFileChangeHidesTheSizeOfALargeFile(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	target := filepath.Join(other, "huge.md")
	require.NoError(t, os.WriteFile(target, []byte(strings.Repeat("a", 256*1024+1)), 0o644))
	// A read of this file fails with permission denied. The size check has to
	// come from Lstat, before the file is opened.
	require.NoError(t, os.Chmod(target, 0))
	t.Cleanup(func() { _ = os.Chmod(target, 0o644) })
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: target, Mode: "edit", Preview: "p", Approver: user,
		Apply: func(cur []byte, _ bool) ([]byte, error) {
			return append(append([]byte(nil), cur...), 'x'), nil
		},
	})
	require.ErrorIs(t, err, ErrEditNotApplicable)
	require.NotContains(t, err.Error(), "262145")
}

func TestAuthorizeFileChangeStoppedTurnApprovesNothing(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	ctx, cancel := context.WithCancel(context.Background())
	_, err := svc.AuthorizeFileChange(ctx, FileChangeRequest{
		SessionID: "s1", Path: filepath.Join(other, "a.md"), Mode: "overwrite", Apply: overwrite("x"),
		Approver: &cancelingApprover{cancel: cancel},
	})
	require.ErrorIs(t, err, context.Canceled)
}
