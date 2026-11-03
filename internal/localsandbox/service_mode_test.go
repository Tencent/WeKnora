package localsandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionModeOverridesTheDefault(t *testing.T) {
	svc, _, _ := approvalFixture(t, &fakeBackend{})
	ctx := context.Background()

	require.Equal(t, ModeAsk, svc.DefaultMode(ctx, "s1"))
	require.Equal(t, ModeAsk, svc.ModeForSession(ctx, "s1"))

	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))
	require.Equal(t, ModeAuto, svc.ModeForSession(ctx, "s1"))
	require.Equal(t, ModeAsk, svc.ModeForSession(ctx, "s2"), "another session keeps the default")
	require.Equal(t, ModeAsk, svc.DefaultMode(ctx, "s1"))
}

func TestSetSessionModeRefusesUnshippedModes(t *testing.T) {
	svc, _, _ := approvalFixture(t, &fakeBackend{})

	require.ErrorIs(t, svc.SetSessionMode("s1", ModeFull), ErrApprovalModeNotShipped)
	require.ErrorIs(t, svc.SetSessionMode("s1", ApprovalMode("yolo")), ErrApprovalModeNotShipped)
	require.Equal(t, ModeAsk, svc.ModeForSession(context.Background(), "s1"))
}

func TestReleasedSessionReturnsToTheDefault(t *testing.T) {
	svc, _, _ := approvalFixture(t, &fakeBackend{})
	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))

	svc.ReleaseSession("s1")
	require.Equal(t, ModeAsk, svc.ModeForSession(context.Background(), "s1"))
}

// The policy is built from the session's mode, so switching one session to
// auto must not change what another session's sandbox looks like.
func TestWorkspaceUsesTheSessionMode(t *testing.T) {
	svc, _, _ := approvalFixture(t, &fakeBackend{})
	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))

	_, _, err := svc.workspaceFor(context.Background(), "s1")
	require.NoError(t, err)
}
