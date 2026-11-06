package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

type approveAll struct{ calls int }

func (a *approveAll) ApproveCommand(
	context.Context, sandbox.CommandApprovalRequest,
) (sandbox.CommandApprovalDecision, error) {
	a.calls++
	return sandbox.CommandApprovalDecision{Approved: true}, nil
}

func outsideDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

type recordingApprover struct {
	requests []sandbox.CommandApprovalRequest
}

func (a *recordingApprover) ApproveCommand(
	_ context.Context, req sandbox.CommandApprovalRequest,
) (sandbox.CommandApprovalDecision, error) {
	a.requests = append(a.requests, req)
	return sandbox.CommandApprovalDecision{Approved: true}, nil
}

func TestAuthorizeFileChangeForwardsTheEditPreview(t *testing.T) {
	a := newTestAdapter(t)
	target := filepath.Join(outsideDir(t), "a.md")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o644))
	user := &recordingApprover{}
	ctx := sandbox.WithCommandApproval(context.Background(), sandbox.CommandApproval{Approver: user})

	_, err := a.AuthorizeFileChange(ctx, "s1", sandbox.FileChange{
		Path: target, Mode: "edit", Preview: "- miss\n",
		Apply: func([]byte, bool) ([]byte, error) { return nil, os.ErrInvalid },
	})
	require.Error(t, err)
	require.Equal(t, "- miss\n", user.requests[0].EditDiff)
}

func TestAuthorizedWriteGoesThroughOnceAndOnlyWithTheApprovedBytes(t *testing.T) {
	a := newTestAdapter(t)
	target := filepath.Join(outsideDir(t), "a.md")
	user := &approveAll{}
	ctx := sandbox.WithCommandApproval(context.Background(), sandbox.CommandApproval{
		Approver: user, Tool: "write_sandbox_file",
	})

	authed, err := a.AuthorizeFileChange(ctx, "s1", sandbox.FileChange{
		Path: target, Mode: "overwrite", Apply: func([]byte, bool) ([]byte, error) { return []byte("hello"), nil },
	})
	require.NoError(t, err)
	require.Equal(t, 1, user.calls)

	require.Error(t, a.WriteSessionWorkspaceFile(authed, "s1", target, []byte("tampered")))
	require.NoError(t, a.WriteSessionWorkspaceFile(authed, "s1", target, []byte("hello")))
	got, _ := os.ReadFile(target)
	require.Equal(t, "hello", string(got))

	require.Error(t, a.WriteSessionWorkspaceFile(ctx, "s1", target, []byte("hello")),
		"without the approval the file is out of scope")
}

// The caller path on macOS is /var/folders/... while the grant stores
// /private/var/folders/.... The digest still has to bind those bytes.
func TestApprovedBytesBindThroughAnUnresolvedAlias(t *testing.T) {
	a := newTestAdapter(t)
	target := filepath.Join(t.TempDir(), "a.md")
	ctx := sandbox.WithCommandApproval(context.Background(), sandbox.CommandApproval{
		Approver: &approveAll{}, Tool: "write_sandbox_file",
	})

	authed, err := a.AuthorizeFileChange(ctx, "s1", sandbox.FileChange{
		Path: target, Mode: "overwrite", Apply: func([]byte, bool) ([]byte, error) { return []byte("hello"), nil },
	})
	require.NoError(t, err)

	require.NoError(t, a.WriteSessionWorkspaceFile(authed, "s1", target, []byte("hello")))
	require.Error(t, a.WriteSessionWorkspaceFile(authed, "s1", target, []byte("tampered")))
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
}

// PathGuard treats a.md and A.md as one file on a case-insensitive volume.
// The digest has to follow that equality.
func TestApprovedBytesBindAcrossCase(t *testing.T) {
	a := newTestAdapter(t)
	dir := outsideDir(t)
	lower := filepath.Join(dir, "a.md")
	upper := filepath.Join(dir, "A.md")
	ctx := sandbox.WithCommandApproval(context.Background(), sandbox.CommandApproval{
		Approver: &approveAll{}, Tool: "write_sandbox_file",
	})

	authed, err := a.AuthorizeFileChange(ctx, "s1", sandbox.FileChange{
		Path: lower, Mode: "overwrite", Apply: func([]byte, bool) ([]byte, error) { return []byte("hello"), nil },
	})
	require.NoError(t, err)
	require.NoError(t, a.WriteSessionWorkspaceFile(authed, "s1", lower, []byte("hello")))

	lowerInfo, err := os.Stat(lower)
	require.NoError(t, err)
	upperInfo, upperErr := os.Stat(upper)
	if upperErr != nil || !os.SameFile(lowerInfo, upperInfo) {
		return
	}
	require.Error(t, a.WriteSessionWorkspaceFile(authed, "s1", upper, []byte("tampered")))
	got, err := os.ReadFile(lower)
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
}

// An approval wait must not hold the workspace lock, or a sibling write in
// the workspace would stall behind a user who has not answered.
func TestWaitingForApprovalDoesNotBlockWorkspaceWrites(t *testing.T) {
	a := newTestAdapter(t)
	release := make(chan struct{})
	started := make(chan struct{})
	blocking := blockingApprover{release: release, started: started}
	ctx := sandbox.WithCommandApproval(context.Background(), sandbox.CommandApproval{Approver: blocking})
	outside := filepath.Join(outsideDir(t), "a.md")
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = a.AuthorizeFileChange(ctx, "s1", sandbox.FileChange{
			Path: outside, Mode: "overwrite",
			Apply: func([]byte, bool) ([]byte, error) { return []byte("x"), nil },
		})
	}()
	t.Cleanup(func() {
		close(release)
		<-finished
	})

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("approval did not start")
	}

	done := make(chan error, 1)
	go func() { done <- a.WriteSessionWorkspaceFile(context.Background(), "s1", "inside.md", []byte("y")) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("a workspace write waited for an unrelated approval")
	}
}

type blockingApprover struct {
	release chan struct{}
	started chan struct{}
}

func (b blockingApprover) ApproveCommand(
	ctx context.Context, _ sandbox.CommandApprovalRequest,
) (sandbox.CommandApprovalDecision, error) {
	if b.started != nil {
		close(b.started)
	}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return sandbox.CommandApprovalDecision{}, nil
}

func TestStoppedTurnDoesNotWriteAfterApproval(t *testing.T) {
	a := newTestAdapter(t)
	target := filepath.Join(outsideDir(t), "a.md")
	ctx, cancel := context.WithCancel(sandbox.WithCommandApproval(context.Background(),
		sandbox.CommandApproval{Approver: &approveAll{}}))
	authed, err := a.AuthorizeFileChange(ctx, "s1", sandbox.FileChange{
		Path: target, Mode: "overwrite", Apply: func([]byte, bool) ([]byte, error) { return []byte("x"), nil },
	})
	require.NoError(t, err)
	cancel()

	require.ErrorIs(t, a.WriteSessionWorkspaceFile(authed, "s1", target, []byte("x")), context.Canceled)
	_, statErr := os.Stat(target)
	require.True(t, os.IsNotExist(statErr))
}
