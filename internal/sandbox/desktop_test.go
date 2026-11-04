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

func TestStartDesktopScriptDropsTheClientCwd(t *testing.T) {
	// websockify inherits the starting Exec's cwd (/workspace/sessions/<sid>);
	// deleting that session left it with a deleted cwd and every later
	// connection died with ENOENT before any HTTP response — a bad handshake
	// for every session on the machine, since websockify is shared. The script
	// must move to a neutral cwd before spawning anything.
	code := shellCode(startDesktopScript(t))
	var found bool
	for _, line := range strings.Split(code, "\n") {
		if strings.TrimSpace(line) == "cd /" {
			found = true
		}
	}
	require.True(t, found, "start-desktop.sh must `cd /` before spawning daemons")
}

func TestStartDesktopScriptBoundsXProbesUnderTheLock(t *testing.T) {
	// A wedged X server (accepts the connection, never replies) makes an
	// unbounded xdpyinfo/xset hang forever while fd 9 — the start lock — is
	// held, so the next start blocks on flock until its Exec times out. The
	// same lock-hang class as the dbus-daemon seat-lock leak in wk-display.
	src := shellCode(startDesktopScript(t))
	require.Contains(t, src, "timeout 2 xdpyinfo -display :0")
	require.Contains(t, src, "timeout 5 xset s off -dpms")
	require.NotContains(t, src, "\nxdpyinfo -display :0",
		"every xdpyinfo probe must carry its own timeout")
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

func TestParseDesktopEnsureAcceptsExactReadyLine(t *testing.T) {
	const secret = "abcdefghijklmnopqrstuvwxyz012345"
	got, token, ok := ParseDesktopEnsure("READY " + secret + "\n")
	require.True(t, ok)
	require.Equal(t, secret, got)
	require.Empty(t, token, "legacy image prints no token")
	got, token, ok = ParseDesktopEnsure("READY " + secret)
	require.True(t, ok)
	require.Equal(t, secret, got)
	require.Empty(t, token)
}

func TestParseDesktopEnsureAcceptsOptionalToken(t *testing.T) {
	const secret = "abcdefghijklmnopqrstuvwxyz012345"
	for _, want := range []string{"0", "1", "7", "42", "99", "05"} {
		got, token, ok := ParseDesktopEnsure("READY " + secret + " " + want + "\n")
		require.True(t, ok, "token %q", want)
		require.Equal(t, secret, got)
		require.Equal(t, want, token)
	}
	_, token, ok := ParseDesktopEnsure("READY " + secret + " 3\r\n")
	require.True(t, ok, "CRLF from a PTY-ish transport is tolerated")
	require.Equal(t, "3", token)
}

func TestParseDesktopEnsureRejectsNoise(t *testing.T) {
	const line = "READY abcdefghijklmnopqrstuvwxyz012345\n"
	_, _, ok := ParseDesktopEnsure(line + "extra\n")
	require.False(t, ok, "suffix after READY is a leak")
	_, _, ok = ParseDesktopEnsure("started\n" + line)
	require.False(t, ok, "prefix before READY must not be ignored; last-line parse would dial with a polluted buffer")
	_, _, ok = ParseDesktopEnsure("\n" + line)
	require.False(t, ok)
	_, _, ok = ParseDesktopEnsure("started\n")
	require.False(t, ok)
	_, _, ok = ParseDesktopEnsure("READY short\n")
	require.False(t, ok)
	_, _, ok = ParseDesktopEnsure("")
	require.False(t, ok)
}

func TestParseDesktopEnsureRejectsBadTokenAndMultiLine(t *testing.T) {
	const ready = "READY abcdefghijklmnopqrstuvwxyz012345"
	for name, in := range map[string]string{
		"token too long":      ready + " 123\n",
		"token not numeric":   ready + " ab\n",
		"token negative":      ready + " -1\n",
		"token empty":         ready + " \n",
		"two tokens":          ready + " 1 2\n",
		"token on next line":  ready + "\n1\n",
		"two READY lines":     ready + " 1\n" + ready + " 1\n",
		"double trailing nl":  ready + " 1\n\n",
		"secret too long":     ready + "6 1\n",
		"leading garbage":     "noise\n" + ready + " 1\n",
		"token with suffix":   ready + " 1x\n",
		"unicode digit token": ready + " \u0661\n",
	} {
		_, _, ok := ParseDesktopEnsure(in)
		require.False(t, ok, name)
	}
}

func TestParseDesktopEnsureSecretIsAThinWrapper(t *testing.T) {
	const secret = "abcdefghijklmnopqrstuvwxyz012345"
	got, ok := ParseDesktopEnsureSecret("READY " + secret + " 4\n")
	require.True(t, ok)
	require.Equal(t, secret, got)
	_, ok = ParseDesktopEnsureSecret("started\nREADY " + secret + "\n")
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
	require.Contains(t, reset, "-rfbport 5900")
	require.NotContains(t, reset, "pkill -x x11vnc",
		"a comm-only pkill kills every seat's x11vnc, not just :0")
	require.Contains(t, reset, "[w]ebsockify")
	require.Contains(t, reset, "/run/desktop/.lock")
	// Seat locks leak the same way (a daemonized dbus-daemon inherited fd 7
	// and held /run/desktop/seat-1.lock forever; every claim of that seat
	// queued until its Exec timed out). Unlinking wedged seat locks unwedges
	// old images the same way the .lock unlink does.
	require.Contains(t, reset, "/run/desktop/seat-*.lock")
	// Xvfb and XFCE are expensive and never the reason 6080 is mute.
	require.NotContains(t, reset, "Xvfb")
	require.NotContains(t, reset, "xfce4-session")
}

func TestDesktopEnsureCmdClaimsASeat(t *testing.T) {
	code := shellCode(DesktopEnsureCmd())
	require.NotContains(t, code, "wk-desk")
	require.Contains(t, code, "command -v wk-display")
	require.Contains(t, code, `start-desktop.sh --proxy-only`)
	claim := `seat=$(wk-display claim "$WEKNORA_SESSION_ID"`
	ready := `printf 'READY %s %s\n' "$secret" "$seat"`
	require.Contains(t, code, claim)
	require.Contains(t, code, ready)
	require.Less(t, strings.Index(code, claim), strings.Index(code, ready),
		"the seat is claimed before READY is printed")
	require.NotContains(t, code, "wk-display claim \"$WEKNORA_SESSION_ID\"\n",
		"claim stdout must be captured; a bare call would leak past the single READY line")
}

func TestDesktopEnsureCmdLegacyLineWhenThereIsNoSeatToken(t *testing.T) {
	code := shellCode(DesktopEnsureCmd())
	require.Contains(t, code, `printf 'READY %s\n' "$secret"`)
	require.Contains(t, code, `printf 'READY %s 0\n' "$secret"`)
	require.Contains(t, code, "/run/desktop/tokens.d/0")
	// The shared path still starts the full desktop, not only the proxy.
	require.Contains(t, code, "/usr/local/bin/start-desktop.sh </dev/null")
}

func TestStartDesktopProxyOnlySkipsTheSharedSession(t *testing.T) {
	code := shellCode(startDesktopScript(t))
	require.Contains(t, code, "--proxy-only")
	require.Contains(t, code, "--token-plugin TokenFile")
	require.Contains(t, code, "--token-source /run/desktop/tokens.d")
	require.Contains(t, code, "0: 127.0.0.1:5900")
	start := strings.Index(code, `[ "$proxy_only" -eq 0 ]`)
	require.GreaterOrEqual(t, start, 0)
	end := strings.Index(code[start:], "\nfi\n")
	require.Greater(t, end, 0)
	block := code[start : start+end]
	require.Contains(t, block, "xfce4-session")
	require.Contains(t, block, "Xvfb :0")
	require.NotContains(t, code, "start-browser.sh",
		"the login Chrome is retired; humans take over on their session's own seat screen")
	rest := code[start+end:]
	require.NotContains(t, rest, "xfce4-session")
	require.Contains(t, rest, "websockify")
	require.NotContains(t, rest, "127.0.0.1:5900",
		"websockify must not keep a fixed target; token 0 is a token file")
}

func TestDesktopReadyFastPathDependsOnMode(t *testing.T) {
	code := shellCode(startDesktopScript(t))
	require.Contains(t, code, "desktop_ready")
	// Full mode still refuses 6080-only. proxy-only is 6080 alone.
	fn := code[strings.Index(code, "desktop_ready()"):]
	fn = fn[:strings.Index(fn, "\n}\n")]
	require.Contains(t, fn, `port_open 6080 && port_open 5900`)
	require.Contains(t, fn, `[ "$proxy_only" -eq 1 ]`)
}

func TestDesktopSeatIsolated(t *testing.T) {
	for _, token := range []string{"1", "2", "8", "99", "01"} {
		require.True(t, DesktopSeatIsolated(token), token)
	}
	for _, token := range []string{"", "0", "00", "100", "ab", "-1", "1x"} {
		require.False(t, DesktopSeatIsolated(token), token)
	}
}
