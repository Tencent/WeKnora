package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

func TestAdapterSatisfiesCapabilityInterfaces(t *testing.T) {
	var a any = New(nil)
	_, ok := a.(sandbox.Manager)
	require.True(t, ok)
	provider, ok := a.(sandbox.SessionCapabilityProvider)
	require.True(t, ok)
	require.NotNil(t, provider)
}

func TestAdapterReportsHostType(t *testing.T) {
	require.Equal(t, sandbox.SandboxTypeHost, New(nil).GetType())
}

func TestAdapterDoesNotVersionWorkspace(t *testing.T) {
	require.False(t, New(nil).VersionsWorkspace(context.Background(), "s1"))
}

// Capabilities the local sandbox genuinely lacks must be absent rather than
// stubbed: a fake PTY is worse than none.
func TestAdapterDoesNotAdvertiseTerminalOrDesktop(t *testing.T) {
	var a any = New(nil)
	_, hasTerminal := a.(sandbox.SessionTerminalProvider)
	require.False(t, hasTerminal)
	_, hasDesktop := a.(sandbox.SessionDesktopProvider)
	require.False(t, hasDesktop)
}

func TestAdapterDoesNotImplementSessionDestroyer(t *testing.T) {
	var a any = New(nil)
	_, ok := a.(sandbox.SessionDestroyer)
	require.False(t, ok)
}

// Deleting a chat session must never delete the user's files. The host adapter
// therefore must not implement SessionDestroyer: destroyBoundSandbox resolves
// the host manager for unpinned sessions and would call it.
func TestHostAdapterIsNotASessionDestroyer(t *testing.T) {
	var a any = New(nil)
	_, isDestroyer := a.(interface {
		DestroySession(context.Context, string) error
	})
	require.False(t, isDestroyer,
		"a host workspace is the user's own directory; deleting a session must not delete it")
}

func TestAdapterDoesNotImplementTurnHolderOrInteractiveManagers(t *testing.T) {
	var a any = New(nil)
	_, hasTurn := a.(sandbox.SessionTurnHolder)
	require.False(t, hasTurn)
	_, hasTerminal := a.(sandbox.SessionTerminalManager)
	require.False(t, hasTerminal)
	_, hasDesktop := a.(sandbox.SessionDesktopManager)
	require.False(t, hasDesktop)
}

func TestLayoutForProjectWorkspace(t *testing.T) {
	ws := localsandbox.Workspace{
		Kind: localsandbox.WorkspaceProject,
		Root: "/Users/dev/My Project",
	}
	l := LayoutFor(ws)

	require.Equal(t, sandbox.WorkspaceOriginHost, l.Origin)
	require.Equal(t, ws.Root, l.Root)
	require.Equal(t, []string{ws.Root}, l.WriteRoots)
	require.Equal(t, []string{ws.Root}, l.ReadRoots)
	require.Empty(t, l.InputDir)
	require.Empty(t, l.OutputDir)
}

func TestLayoutForReadRootsAreTheWorkspace(t *testing.T) {
	l := LayoutFor(localsandbox.Workspace{Root: "/Users/dev/proj"})
	require.Equal(t, []string{"/Users/dev/proj"}, l.ReadRoots)
}

func TestLayoutForHintIsTheWorkspaceRoot(t *testing.T) {
	l := LayoutFor(localsandbox.Workspace{Root: "/Users/dev/My Project"})
	require.Equal(t, l.Root, l.Hint)
}

// A session workspace keeps output inside the tree, so Root alone is writable.
func TestLayoutForSessionWorkspaceHasNoInputOrOutput(t *testing.T) {
	l := LayoutFor(localsandbox.Workspace{
		Kind: localsandbox.WorkspaceSession,
		Root: "/Users/dev/Documents/WeKnora/s1",
	})
	require.Equal(t, []string{"/Users/dev/Documents/WeKnora/s1"}, l.WriteRoots)
	require.Empty(t, l.InputDir)
	require.Empty(t, l.OutputDir)
}

func TestAdapterProvidesWorkspaceLayout(t *testing.T) {
	var a any = New(nil)
	_, ok := a.(sandbox.SessionWorkspaceLayoutProvider)
	require.True(t, ok)
}

func TestAdapterIsTheSessionShellAndFileStore(t *testing.T) {
	a := New(testService(t, &stubBackend{exit: localsandbox.ExitStatus{Code: 0}}))
	require.Equal(t, a, a.SessionShellExecutor())
	require.Equal(t, a, a.SessionFileStore())
}

func TestAdapterNilServiceAdvertisesNoShellOrFiles(t *testing.T) {
	a := New(nil)
	require.Nil(t, a.SessionShellExecutor())
	require.Nil(t, a.SessionFileStore())
}

// Description() used to look up the layout with sessionID="". Host adapters
// still refuse that so unbound tools fall back to remote copy instead of
// stuffing an absolute home path into Hint.
func TestAdapterSessionWorkspaceLayoutRejectsEmptySessionID(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	layout, err := a.SessionWorkspaceLayout(context.Background(), "")
	require.Error(t, err)
	require.Empty(t, layout.Root)
	require.NotContains(t, layout.Hint, "/Users")
}

func TestAdapterSessionWorkspaceLayoutErrorsWithoutService(t *testing.T) {
	_, err := New(nil).SessionWorkspaceLayout(context.Background(), "")
	require.Error(t, err)
}

func TestAdapterSessionWorkspaceLayoutMapsResolvedWorkspace(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	layout, err := a.SessionWorkspaceLayout(context.Background(), "s1")
	require.NoError(t, err)
	require.NotEmpty(t, layout.Root)
	require.Equal(t, []string{layout.Root}, layout.WriteRoots)
	require.Equal(t, layout.Root, layout.ReadRoots[len(layout.ReadRoots)-1])
	require.Equal(t, layout.Hint, LayoutFor(localsandbox.Workspace{Root: layout.Root}).Hint)
	require.Equal(t, layout.Root, layout.Hint)
}

func TestAdapterWritesAndReadsWorkspaceFile(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	ctx := context.Background()

	require.NoError(t, a.WriteSessionWorkspaceFile(ctx, "s1", "notes.md", []byte("hello")))
	got, err := a.ReadSessionFile(ctx, "s1", "notes.md")
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), got)

	err = a.WriteSessionWorkspaceFile(ctx, "s1", "/etc/passwd", []byte("nope"))
	require.ErrorIs(t, err, localsandbox.ErrPathDenied)
}

func TestAdapterWriteSessionInputFileIsDisabled(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	err := a.WriteSessionInputFile(context.Background(), "s1", "report.pdf", []byte("escape"))
	require.ErrorIs(t, err, errNoHostAttachmentDir)
}

func TestAdapterSessionWorkspaceHasNoAttachmentDir(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	ctx := context.Background()
	layout, err := a.SessionWorkspaceLayout(ctx, "s1")
	require.NoError(t, err)
	require.Empty(t, layout.InputDir)
	require.Empty(t, layout.OutputDir)

	dest := filepath.Join(layout.Root, "stolen.pdf")
	err = a.WriteSessionInputFile(ctx, "s1", dest, []byte("nope"))
	require.ErrorIs(t, err, errNoHostAttachmentDir)
	_, statErr := os.Stat(dest)
	require.Error(t, statErr)
}

func TestAdapterExecShellCommandMapsResult(t *testing.T) {
	backend := &stubBackend{
		stdout: "hello\n",
		exit:   localsandbox.ExitStatus{Code: 0, Duration: 12 * time.Millisecond},
	}
	a := New(testService(t, backend))

	res, err := a.ExecShellCommand(context.Background(), "s1", "echo hello", "", 0, nil)
	require.NoError(t, err)
	require.Equal(t, "hello\n", res.Stdout)
	require.Equal(t, 0, res.ExitCode)
	require.Equal(t, 12*time.Millisecond, res.Duration)
	require.False(t, res.Killed)
}

func TestAdapterExecShellCommandAnnotatesDenial(t *testing.T) {
	backend := &stubBackend{
		stderr: "touch: /etc/x: Operation not permitted",
		exit:   localsandbox.ExitStatus{Code: 1},
	}
	a := New(testService(t, backend))

	res, err := a.ExecShellCommand(context.Background(), "s1", "touch /etc/x", "", 0, nil)
	require.NoError(t, err)
	require.Equal(t, 1, res.ExitCode)
	require.Contains(t, res.Stderr, "[sandbox] denied by workspace policy")
}

func TestAdapterListSessionFilesStopsWalkingAtCap(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	ctx := context.Background()
	layout, err := a.SessionWorkspaceLayout(ctx, "s1")
	require.NoError(t, err)
	bulk := filepath.Join(layout.Root, "bulk")
	require.NoError(t, os.MkdirAll(bulk, 0o755))
	for i := 0; i < maxListSessionFiles+40; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(bulk, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o644))
	}

	entries, err := a.ListSessionFiles(ctx, "s1", ".")
	require.NoError(t, err)
	require.Equal(t, maxListSessionFiles, len(entries))
}

type stubProjects struct{}

func (stubProjects) ProjectDirForSession(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func testService(t *testing.T, backend localsandbox.Backend) *localsandbox.Service {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	appData := filepath.Join(home, "App Support", "WeKnora Lite")
	require.NoError(t, os.MkdirAll(home, 0o755))
	resolver := localsandbox.NewWorkspaceResolver(localsandbox.DirLayout{
		SessionRoot: filepath.Join(home, "Documents", "WeKnora"),
	}, stubProjects{})
	return localsandbox.NewService(backend, resolver, localsandbox.NewPolicyBuilder(home, appData), nil)
}

type stubBackend struct {
	stdout    string
	stderr    string
	exit      localsandbox.ExitStatus
	available error
}

func (s *stubBackend) Name() string                      { return "stub" }
func (s *stubBackend) Available() error                  { return s.available }
func (s *stubBackend) EnsureReady(context.Context) error { return nil }
func (s *stubBackend) TearDown(context.Context) error    { return nil }

type stubPrepared struct{ fp string }

func (p stubPrepared) Fingerprint() string { return p.fp }
func (p stubPrepared) Close() error        { return nil }

func (s *stubBackend) Prepare(_ context.Context, p localsandbox.Policy) (localsandbox.Prepared, error) {
	return stubPrepared{fp: p.Fingerprint()}, nil
}

func (s *stubBackend) Spawn(
	_ context.Context, _ localsandbox.Prepared, _ localsandbox.Command,
) (localsandbox.Process, error) {
	return &stubProcess{
		stdout: io.NopCloser(bytes.NewBufferString(s.stdout)),
		stderr: io.NopCloser(bytes.NewBufferString(s.stderr)),
		exit:   s.exit,
	}, nil
}

type stubProcess struct {
	stdout io.ReadCloser
	stderr io.ReadCloser
	exit   localsandbox.ExitStatus
}

func (p *stubProcess) Stdout() io.Reader { return p.stdout }
func (p *stubProcess) Stderr() io.Reader { return p.stderr }
func (p *stubProcess) Kill() error       { return nil }
func (p *stubProcess) PID() int          { return 1 }
func (p *stubProcess) Wait(context.Context) (localsandbox.ExitStatus, error) {
	return p.exit, nil
}
