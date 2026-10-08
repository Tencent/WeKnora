package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type desktopFakeClient struct {
	*fakeRemoteClient
	dialed     bool
	ttlStarted int
}

func (c *desktopFakeClient) DialDesktop(
	context.Context, RemoteSandboxHandle, RemoteDesktopOptions,
) (*websocket.Conn, error) {
	c.dialed = true
	return nil, nil
}

func (c *desktopFakeClient) StartDesktopTTLRefresh(context.Context, RemoteSandboxHandle) {
	c.ttlStarted++
}

func TestDesktopManagerFromRequiresBothSignals(t *testing.T) {
	// Methods present but capability off: a provider that grew the method
	// before the deployment was ready must still read as unsupported.
	off := &desktopFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	off.capabilities.SupportsDesktop = false
	_, ok := DesktopManagerFrom(off)
	require.False(t, ok, "capability flag alone must be able to veto")

	// Capability on but no methods: the plain fake has no DialDesktop.
	plain := newFakeRemoteClient(SandboxTypeCube)
	plain.capabilities.SupportsDesktop = true
	_, ok = DesktopManagerFrom(plain)
	require.False(t, ok, "type assertion alone must be able to veto")

	on := &desktopFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	on.capabilities.SupportsDesktop = true
	mgr, ok := DesktopManagerFrom(on)
	require.True(t, ok)
	require.NotNil(t, mgr)
}

func TestDesktopManagerFromNilClient(t *testing.T) {
	_, ok := DesktopManagerFrom(nil)
	require.False(t, ok)
}

func TestDesktopTTLRefresherFromRequiresTimeoutCapability(t *testing.T) {
	off := &desktopFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	off.capabilities.SupportsTimeoutRefresh = false
	_, ok := DesktopTTLRefresherFrom(off)
	require.False(t, ok, "Docker-like backends have the method via wrappers but no timeout to refresh")

	on := &desktopFakeClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeCube)}
	on.capabilities.SupportsTimeoutRefresh = true
	refresher, ok := DesktopTTLRefresherFrom(on)
	require.True(t, ok)
	require.NotNil(t, refresher)
}

func startDesktopScript(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "docker", "scripts", "start-desktop.sh"))
	require.NoError(t, err)
	return string(body)
}

// shellCode drops comment lines so an assertion about what a script *does*
// cannot be satisfied (or broken) by prose explaining it.
func shellCode(src string) string {
	var code []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code = append(code, line)
	}
	return strings.Join(code, "\n")
}

func TestStartDesktopScriptFastPathRequiresVNCPort(t *testing.T) {
	// 6080-only "already ready" lets websockify sit up while x11vnc is dead.
	// DialDesktop then 101s, RFB hello never comes, and the session slot is
	// held until the handshake read times out — every retry is DESKTOP_BUSY.
	src := startDesktopScript(t)
	require.Contains(t, src, "port_open 6080")
	require.Contains(t, src, "port_open 5900")
}

func TestStartDesktopScriptClosesLockFDInEveryDaemon(t *testing.T) {
	// Reproduced against wechatopenai/weknora-sandbox:main-desktop: Xvfb,
	// xfce4-session, dbus-launch, x11vnc and websockify all inherited fd 9
	// and kept holding the flock after the script exited. Killing x11vnc and
	// websockify then left Xvfb/XFCE holding it, so the next run blocked on
	// flock forever — Exec timed out at 60s (exit=-1) and 6080 never returned.
	code := shellCode(startDesktopScript(t))
	// spawn() carries both `9>&-` and the private stdio; the two daemons that
	// cannot use it (dbus --fork, x11vnc -bg) spell it out.
	require.Contains(t, code, `"$@" </dev/null >>"/run/desktop/$name.log" 2>&1 9>&- &`)
	for _, launch := range []string{
		"spawn xvfb Xvfb :0",
		"spawn xfce4-session xfce4-session",
		"spawn websockify websockify --auth-plugin",
		"dbus-daemon --system --fork </dev/null >>/run/desktop/dbus.log 2>&1 9>&-",
		"</dev/null >>/run/desktop/x11vnc.log 2>&1 9>&-",
	} {
		require.Contains(t, code, launch,
			"every daemon must close fd 9 and own its stdio")
	}
}

func TestParseDesktopEnsureSecretAcceptsExactReadyLine(t *testing.T) {
	const secret = "abcdefghijklmnopqrstuvwxyz012345"
	got, ok := ParseDesktopEnsureSecret("READY " + secret + "\n")
	require.True(t, ok)
	require.Equal(t, secret, got)
	got, ok = ParseDesktopEnsureSecret("READY " + secret)
	require.True(t, ok)
	require.Equal(t, secret, got)
}

func TestParseDesktopEnsureSecretRejectsNoise(t *testing.T) {
	const line = "READY abcdefghijklmnopqrstuvwxyz012345\n"
	_, ok := ParseDesktopEnsureSecret(line + "extra\n")
	require.False(t, ok, "suffix after READY is a leak")
	_, ok = ParseDesktopEnsureSecret("started\n" + line)
	require.False(t, ok, "prefix before READY must not be ignored; last-line parse would dial with a polluted buffer")
	_, ok = ParseDesktopEnsureSecret("\n" + line)
	require.False(t, ok)
	_, ok = ParseDesktopEnsureSecret("started\n")
	require.False(t, ok)
	_, ok = ParseDesktopEnsureSecret("READY short\n")
	require.False(t, ok)
	_, ok = ParseDesktopEnsureSecret("")
	require.False(t, ok)
}

func TestIsDesktopEnsureUnsupportedRequiresMarker(t *testing.T) {
	require.False(t, IsDesktopEnsureUnsupported(nil))
	require.False(t, IsDesktopEnsureUnsupported(&ExecuteResult{
		ExitCode: 2, Stderr: "Syntax error: end of file unexpected",
	}), "POSIX sh uses exit 2 for syntax errors")
	require.True(t, IsDesktopEnsureUnsupported(&ExecuteResult{
		ExitCode: 2, Stderr: DesktopEnsureUnsupportedMarker + "\n",
	}))
	require.False(t, IsDesktopEnsureUnsupported(&ExecuteResult{
		ExitCode: 2, Killed: true, Stderr: DesktopEnsureUnsupportedMarker,
	}))
	require.False(t, IsDesktopEnsureUnsupported(&ExecuteResult{
		ExitCode: 1, Stderr: DesktopEnsureUnsupportedMarker,
	}))
}

func TestDesktopEnsureCmdDetachesDaemonStdio(t *testing.T) {
	// envd's exec waits for stdout and stderr to close. Invoking
	// DesktopStartScript by path let the backgrounded daemons hold those
	// pipes for the life of the sandbox, so the Exec hit its 60s timeout
	// (exit=-1 -> DESKTOP_START_FAILED) while the desktop was actually up.
	cmd := DesktopEnsureCmd()
	code := shellCode(cmd)
	require.Contains(t, cmd, DesktopStartScript)
	require.Contains(t, code, "</dev/null")
	require.Contains(t, code, ">>\"$log\" 2>&1")
	require.Contains(t, code, "tail -c")
	require.Contains(t, code, `printf 'READY %s\n' "$secret"`)
	require.Contains(t, code, "WEKNORA_DESKTOP_UNSUPPORTED")
	require.Contains(t, code, "exit 2")
	require.NotContains(t, code, "pkill")
}

func TestDesktopEnsureCmdProbesNeverDial(t *testing.T) {
	code := shellCode(DesktopEnsureCmd())
	require.Contains(t, code, "/proc/net/tcp")
	require.Contains(t, code, "5900")
	require.Contains(t, code, "6080")
	for _, dialish := range []string{"connect_ex", "create_connection", "nc -z", "ss -", "curl"} {
		require.NotContains(t, code, dialish)
	}
	require.NotContains(t, shellCode(startDesktopScript(t)), "connect_ex")
}

func TestStartDesktopScriptGuardsListenersOnPortNotPgrep(t *testing.T) {
	// Reproduced in the desktop image: x11vnc -bg leaves a zombie when PID 1
	// does not reap it, pgrep counts zombies as running, and the x11vnc step
	// becomes a permanent no-op. websockify keeps answering on 6080, so
	// DialDesktop 101s and the RFB hello never arrives.
	code := shellCode(startDesktopScript(t))
	require.Contains(t, code, "port_open 5900 || \\")
	require.Contains(t, code, "port_open 6080 || \\")
	require.NotContains(t, code, "pgrep -x x11vnc")
	require.NotContains(t, code, "pgrep -f \"websockify")
	// The processes with no port of their own need the zombie-aware helper.
	require.Contains(t, code, "proc_alive Xvfb")
	require.Contains(t, code, "proc_alive xfce4-session")
	require.NotContains(t, code, "pgrep -x Xvfb")
	require.NotContains(t, code, "pgrep -x xfce4-session")
}

func TestDesktopResetListenersCmdClearsLeakedLock(t *testing.T) {
	// Older images still leak the flock fd, so the reset has to unwedge them
	// without an image rebuild.
	reset := shellCode(DesktopResetListenersCmd())
	require.Contains(t, reset, "x11vnc")
	require.Contains(t, reset, "[w]ebsockify")
	require.Contains(t, reset, "/run/desktop/.lock")
	// Xvfb and XFCE are expensive and never the reason 6080 is mute.
	require.NotContains(t, reset, "Xvfb")
	require.NotContains(t, reset, "xfce4-session")
}
