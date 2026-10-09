package localsandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// approvalFixture resolves the temp dir first: macOS hands out /var/folders
// paths that PathGuard sees as /private/var after EvalSymlinks.
func approvalFixture(t *testing.T, backend *fakeBackend) (*Service, string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home := filepath.Join(base, "home")
	root := filepath.Join(home, "proj")
	other := filepath.Join(base, "other")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(other, 0o755))
	resolver := NewWorkspaceResolver(DirLayout{
		SessionRoot: filepath.Join(home, "Documents", "WeKnora"),
	}, fixedProject(root))
	builder := NewPolicyBuilder(home, filepath.Join(home, "AppData"))
	return NewService(backend, resolver, builder, fixedMode(ModeAuto)), root, other
}

func TestSessionStatesTrackApprovalsPerSession(t *testing.T) {
	s := newSessionStates()
	g := Grant{Path: "/x", Access: AccessWrite}

	s.addGrant("s1", g)
	s.addGrant("s1", g)
	require.Equal(t, []Grant{g}, s.grants("s1"))
	require.Empty(t, s.grants("s2"))

	rf := DeleteRule{Program: "rm", Flags: []string{"-f", "-r"}}
	plain := DeleteRule{Program: "rm"}
	require.False(t, s.rulesCover("s1", "/w", []DeleteRule{plain}))
	s.approveRule("s1", "/w", rf)
	s.approveRule("s1", "/w", rf)
	require.True(t, s.rulesCover("s1", "/w", []DeleteRule{plain, rf}))
	require.False(t, s.rulesCover("s1", "/other", []DeleteRule{plain}))
	require.False(t, s.rulesCover("s1", "/w", nil), "nothing to cover is not an approval")
	require.False(t, s.rulesCover("s1", "/w", []DeleteRule{plain, {Program: "rmdir"}}))

	s.release("s1")
	require.Empty(t, s.grants("s1"))
	require.False(t, s.rulesCover("s1", "/w", []DeleteRule{plain}))
}

func TestSessionGrantWidensGuardUntilReleased(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	ctx := context.Background()
	target := filepath.Join(other, "b.txt")

	guard, _, err := svc.GuardForSession(ctx, "s1")
	require.NoError(t, err)
	_, err = guard.CheckWrite(target)
	require.ErrorIs(t, err, ErrPathDenied)

	svc.sessions.addGrant("s1", Grant{Path: other, Access: AccessWrite})
	require.Equal(t, []Grant{{Path: other, Access: AccessWrite}}, svc.SessionGrants("s1"))
	guard, _, err = svc.GuardForSession(ctx, "s1")
	require.NoError(t, err)
	_, err = guard.CheckWrite(target)
	require.NoError(t, err)

	svc.ReleaseSession("s1")
	guard, _, err = svc.GuardForSession(ctx, "s1")
	require.NoError(t, err)
	_, err = guard.CheckWrite(target)
	require.ErrorIs(t, err, ErrPathDenied)
}
