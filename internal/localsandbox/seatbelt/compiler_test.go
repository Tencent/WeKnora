package seatbelt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/localsandbox/core"
)

func seatbeltPolicy() core.Policy {
	return core.Policy{
		WritableRoots: []core.WritableRoot{{
			Path:             "/Users/dev/My Project",
			ReadOnlySubpaths: []string{"/Users/dev/My Project/.git"},
		}},
		ReadableRoots: []string{"/opt/tools"},
		PrivateRoots:  []string{"/Users/dev"},
		DenyRead:      []string{"/Users/dev/.ssh"},
		Network:       core.NetworkDenied,
		Cwd:           "/Users/dev/My Project",
	}
}

// The base profile has to grant blanket file-read* (see base.sbpl), so the
// only way to protect the user's data is to deny their home afterwards and
// re-open the workspace after that. Order is the whole mechanism.
func TestCompileSeatbeltDeniesPrivateRootThenReopensWorkspace(t *testing.T) {
	prog, err := compileSeatbelt(seatbeltPolicy())
	require.NoError(t, err)

	require.Contains(t, prog.Params, "-DPRIVATE_ROOT_0=/Users/dev")
	privateDeny := strings.Index(prog.Profile, `(deny file-read* (subpath (param "PRIVATE_ROOT_0")))`)
	require.GreaterOrEqual(t, privateDeny, 0)

	workspaceRead := strings.Index(prog.Profile, `(allow file-read* (subpath (param "WRITABLE_ROOT_0")))`)
	require.Greater(t, workspaceRead, privateDeny,
		"the workspace read must be re-opened after the private deny")

	credentialDeny := strings.Index(prog.Profile, `(deny file-read* (subpath (param "DENY_READ_0")))`)
	require.Greater(t, credentialDeny, workspaceRead,
		"credential denies must still win over every re-opened read")
}

// A readable root may be a single file (a shell rc file), which subpath does
// not match on its own.
func TestCompileSeatbeltEmitsReadableRootAsSubpathAndLiteral(t *testing.T) {
	prog, err := compileSeatbelt(seatbeltPolicy())
	require.NoError(t, err)

	require.Contains(t, prog.Profile, `(allow file-read* (subpath (param "READABLE_ROOT_0")))`)
	require.Contains(t, prog.Profile, `(allow file-read* (literal (param "READABLE_ROOT_0")))`)
}

// Writing must survive the private deny: that deny is read-only by design.
func TestCompileSeatbeltPrivateRootDoesNotRevokeWorkspaceWrite(t *testing.T) {
	prog, err := compileSeatbelt(seatbeltPolicy())
	require.NoError(t, err)

	privateDeny := strings.Index(prog.Profile, `(deny file-read* (subpath (param "PRIVATE_ROOT_0")))`)
	require.NotContains(t, prog.Profile[privateDeny:], `(deny file-write* (subpath (param "PRIVATE_ROOT_0")))`)
}

// Paths must travel as -D parameters, never inlined, so quoting and regex
// metacharacters in user paths can never reach the policy text.
func TestCompileSeatbeltPassesPathsAsParams(t *testing.T) {
	prog, err := compileSeatbelt(seatbeltPolicy())
	require.NoError(t, err)

	require.Contains(t, prog.Params, "-DWRITABLE_ROOT_0=/Users/dev/My Project")
	require.Contains(t, prog.Params, "-DWRITABLE_ROOT_0_EXCLUDED_0=/Users/dev/My Project/.git")
	require.Contains(t, prog.Params, "-DREADABLE_ROOT_0=/opt/tools")
	require.Contains(t, prog.Params, "-DDENY_READ_0=/Users/dev/.ssh")
	require.NotContains(t, prog.Profile, "/Users/dev")
}

// literal and subpath must both be excluded, otherwise creating the protected
// directory itself (mkdir .git) succeeds.
func TestCompileSeatbeltExcludesReadOnlySubpathTwice(t *testing.T) {
	prog, err := compileSeatbelt(seatbeltPolicy())
	require.NoError(t, err)

	require.Contains(t, prog.Profile,
		`(require-not (literal (param "WRITABLE_ROOT_0_EXCLUDED_0")))`)
	require.Contains(t, prog.Profile,
		`(require-not (subpath (param "WRITABLE_ROOT_0_EXCLUDED_0")))`)
}

// A credential path may be a file (~/.npmrc, ~/.cargo/credentials.toml).
// subpath does not match a file, which is why reads already emit literal;
// the write deny has to do the same or a writable home still clobbers it.
func TestCompileSeatbeltDeniesCredentialWriteAsSubpathAndLiteral(t *testing.T) {
	prog, err := compileSeatbelt(seatbeltPolicy())
	require.NoError(t, err)

	require.Contains(t, prog.Profile, `(deny file-write* (subpath (param "DENY_READ_0")))`)
	require.Contains(t, prog.Profile, `(deny file-write* (literal (param "DENY_READ_0")))`)
}

// sbpl gives precedence to later rules, so every deny must follow every allow.
func TestCompileSeatbeltPutsDeniesLast(t *testing.T) {
	prog, err := compileSeatbelt(seatbeltPolicy())
	require.NoError(t, err)

	lastAllow := strings.LastIndex(prog.Profile, "(allow ")
	firstDenyRead := strings.Index(prog.Profile, `(deny file-read* (subpath (param "DENY_READ_0")))`)
	require.Greater(t, firstDenyRead, lastAllow)
}

// Replacing the writable root with a symlink would invalidate the next
// policy build, so unlinking the root directory itself is denied.
func TestCompileSeatbeltAnchorsWritableRoot(t *testing.T) {
	prog, err := compileSeatbelt(seatbeltPolicy())
	require.NoError(t, err)

	require.Contains(t, prog.Profile,
		`(deny file-write-unlink (require-all (literal (param "WRITABLE_ROOT_0")) (vnode-type DIRECTORY)))`)
}

func TestCompileSeatbeltDeniesNetworkByDefault(t *testing.T) {
	prog, err := compileSeatbelt(seatbeltPolicy())
	require.NoError(t, err)
	require.NotContains(t, prog.Profile, "network-outbound")
}

func TestCompileSeatbeltBasePolicyAllowsUnfilteredFileRead(t *testing.T) {
	prog, err := compileSeatbelt(seatbeltPolicy())
	require.NoError(t, err)
	require.Contains(t, prog.Profile, "(allow file-read*)")
	require.NotRegexp(t, `(?m)^\(allow file-write\*\)$`, prog.Profile)
}

func TestCompileSeatbeltAllowsConfiguredLoopbackPorts(t *testing.T) {
	p := seatbeltPolicy()
	p.Network = core.NetworkLoopback
	p.LoopbackPorts = []int{11434}

	prog, err := compileSeatbelt(p)
	require.NoError(t, err)
	require.Contains(t, prog.Profile, `(allow network-outbound (remote ip "localhost:11434"))`)
}

// Loopback mode without any port is a configuration error, not "allow all".
func TestCompileSeatbeltFailsClosedOnLoopbackWithoutPorts(t *testing.T) {
	p := seatbeltPolicy()
	p.Network = core.NetworkLoopback

	_, err := compileSeatbelt(p)
	require.Error(t, err)
}

func TestCompileSeatbeltRejectsInvalidPolicy(t *testing.T) {
	_, err := compileSeatbelt(core.Policy{Cwd: "/tmp/work"})
	require.ErrorIs(t, err, core.ErrNoWritableRoot)
}

func TestCompileSeatbeltAllowsAskModeWithNoWritableRoot(t *testing.T) {
	p := seatbeltPolicy()
	p.WritableRoots = nil
	p.ReadableRoots = append(p.ReadableRoots, p.Cwd)
	prog, err := compileSeatbelt(p)
	require.NoError(t, err)
	require.NotContains(t, prog.Profile, "WRITABLE_ROOT")
}
