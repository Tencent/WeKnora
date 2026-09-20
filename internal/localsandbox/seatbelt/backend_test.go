//go:build darwin

package seatbelt

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/localsandbox/core"
)

func darwinFixture(t *testing.T) (core.Backend, core.Policy, string) {
	t.Helper()
	backend, err := New()
	require.NoError(t, err)
	require.NoError(t, backend.Available())

	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	// The space is deliberate: it is the shape of a real user directory.
	root := filepath.Join(base, "My Project")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))

	secrets := filepath.Join(base, "secrets")
	require.NoError(t, os.MkdirAll(secrets, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(secrets, "note.txt"), []byte("KEY"), 0o600))

	// base stands in for the user's home: another project beside the
	// workspace, and a per-user toolchain that has to keep working.
	other := filepath.Join(base, "Other Project")
	require.NoError(t, os.MkdirAll(other, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(other, "private.txt"), []byte("OTHER"), 0o600))
	toolchain := filepath.Join(base, ".nvm")
	require.NoError(t, os.MkdirAll(toolchain, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(toolchain, "node"), []byte("NODE"), 0o644))

	p := core.Policy{
		WritableRoots: []core.WritableRoot{{
			Path:             root,
			ReadOnlySubpaths: []string{filepath.Join(root, ".git")},
		}},
		ReadableRoots: []string{toolchain},
		PrivateRoots:  []string{base},
		DenyRead:      []string{secrets},
		Network:       core.NetworkDenied,
		Cwd:           root,
	}
	return backend, p, base
}

func runSandboxed(t *testing.T, backend core.Backend, p core.Policy, script string) (core.ExitStatus, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prep, err := backend.Prepare(ctx, p)
	require.NoError(t, err)
	defer prep.Close()

	proc, err := backend.Spawn(ctx, prep, core.Command{
		Argv: []string{"/bin/bash", "-c", script},
		Cwd:  p.Cwd,
	})
	require.NoError(t, err)

	out, err := io.ReadAll(proc.Stdout())
	require.NoError(t, err)
	errOut, err := io.ReadAll(proc.Stderr())
	require.NoError(t, err)

	status, err := proc.Wait(ctx)
	require.NoError(t, err)
	return status, string(out) + string(errOut)
}

func TestSeatbeltAllowsWriteInsideWorkspace(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	status, out := runSandboxed(t, backend, p, `echo hi > ./a.txt && cat ./a.txt`)
	require.Equal(t, 0, status.Code, out)
	require.Contains(t, out, "hi")
}

func TestSeatbeltDeniesWriteOutsideWorkspace(t *testing.T) {
	backend, p, base := darwinFixture(t)
	status, out := runSandboxed(t, backend, p, `echo pwned > `+filepath.Join(base, "escape.txt"))
	require.NotEqual(t, 0, status.Code, out)
	require.True(t, core.ClassifyDenial(status, "", out).IsDenied(), out)
	require.NoFileExists(t, filepath.Join(base, "escape.txt"))
}

func TestSeatbeltDeniesWriteToGit(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	status, out := runSandboxed(t, backend, p, `echo x > ./.git/config`)
	require.NotEqual(t, 0, status.Code, out)
}

// subpath alone would let this succeed; the literal exclusion is what stops it.
func TestSeatbeltDeniesRecreatingProtectedDirectory(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	status, out := runSandboxed(t, backend, p, `rm -rf ./.git && mkdir ./.git`)
	require.NotEqual(t, 0, status.Code, out)
}

func TestSeatbeltDeniesReadingSecrets(t *testing.T) {
	backend, p, base := darwinFixture(t)
	status, out := runSandboxed(t, backend, p, `cat `+filepath.Join(base, "secrets", "note.txt"))
	require.NotEqual(t, 0, status.Code, out)
	require.NotContains(t, out, "KEY")
}

// The point of the private root: everything else in the user's home is
// invisible, not just the handful of named credential directories.
func TestSeatbeltDeniesReadingOtherDirectoriesInHome(t *testing.T) {
	backend, p, base := darwinFixture(t)
	status, out := runSandboxed(t, backend, p,
		`cat `+filepath.Join(base, "Other Project", "private.txt"))
	require.NotEqual(t, 0, status.Code, out)
	require.NotContains(t, out, "OTHER")
}

// Denying home wholesale must not take the user's toolchain with it.
func TestSeatbeltAllowsReadingReopenedToolchain(t *testing.T) {
	backend, p, base := darwinFixture(t)
	status, out := runSandboxed(t, backend, p, `cat `+filepath.Join(base, ".nvm", "node"))
	require.Equal(t, 0, status.Code, out)
	require.Contains(t, out, "NODE")
}

// The workspace sits inside the private root and must stay readable.
func TestSeatbeltAllowsReadingWorkspaceInsidePrivateRoot(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	status, out := runSandboxed(t, backend, p, `echo body > ./f.txt && cat ./f.txt`)
	require.Equal(t, 0, status.Code, out)
	require.Contains(t, out, "body")
}

// End-to-end through the real PolicyBuilder rather than a hand-written
// policy: this is what actually ships, and `bash -lc` reading its startup
// files under a denied home is the part most likely to regress.
func TestSeatbeltPolicyFromBuilderRunsLoginShell(t *testing.T) {
	backend, err := New()
	require.NoError(t, err)
	require.NoError(t, backend.Available())

	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	workspace := filepath.Join(home, "Documents", "WeKnora", "s1")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".zshrc"), []byte("# rc\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ssh", "id_rsa"), []byte("PRIVATEKEY"), 0o600))

	builder := core.NewPolicyBuilder(home, filepath.Join(home, "Library", "App"))
	p, err := builder.Build(core.ModeAuto, core.Workspace{Kind: core.WorkspaceSession, Root: workspace})
	require.NoError(t, err)

	status, out := runSandboxed(t, backend, p, `echo alive && echo w > ./f.txt && cat ./f.txt`)
	require.Equal(t, 0, status.Code, out)
	require.Contains(t, out, "alive")
	require.Contains(t, out, "w")

	status, out = runSandboxed(t, backend, p, `cat `+filepath.Join(home, ".ssh", "id_rsa"))
	require.NotEqual(t, 0, status.Code, out)
	require.NotContains(t, out, "PRIVATEKEY")
}

func TestSeatbeltDeniesNetwork(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	status, out := runSandboxed(t, backend, p,
		`curl --max-time 5 -sS https://example.com`)
	require.NotEqual(t, 0, status.Code)
	// Seatbelt does not surface EPERM for DNS; curl exits 6.
	require.True(t, core.LooksLikeNetworkDenial(status, "", out), out)
	require.False(t, core.ClassifyDenial(status, "", out).IsDenied(), out)
}

func TestSeatbeltPrepareResolvesTmpAlias(t *testing.T) {
	backend, err := New()
	require.NoError(t, err)

	logical, err := os.MkdirTemp("/tmp", "weknora-seatbelt-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(logical) })

	p := core.Policy{
		WritableRoots: []core.WritableRoot{{Path: logical}},
		Network:       core.NetworkDenied,
		Cwd:           logical,
	}
	status, out := runSandboxed(t, backend, p, `echo hi > ./from-tmp.txt && cat ./from-tmp.txt`)
	require.Equal(t, 0, status.Code, out)
	require.Contains(t, out, "hi")
}

func TestSeatbeltInjectsTMPDIR(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	status, out := runSandboxed(t, backend, p, `printf '%s\n' "$TMPDIR"`)
	require.Equal(t, 0, status.Code, out)
	require.Contains(t, out, p.Cwd)
}

func TestSeatbeltKillsProcessTreeOnContextCancel(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	ctx, cancel := context.WithCancel(context.Background())

	prep, err := backend.Prepare(ctx, p)
	require.NoError(t, err)
	defer prep.Close()

	proc, err := backend.Spawn(ctx, prep, core.Command{
		Argv: []string{"/bin/bash", "-c", `sleep 60 & sleep 60`},
		Cwd:  p.Cwd,
	})
	require.NoError(t, err)

	time.AfterFunc(200*time.Millisecond, cancel)
	status, err := proc.Wait(ctx)
	require.NoError(t, err)
	require.True(t, status.Killed)
	require.Less(t, status.Duration, 30*time.Second)
}

func TestSeatbeltPreparedFingerprintMatchesPolicy(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	prep, err := backend.Prepare(context.Background(), p)
	require.NoError(t, err)
	defer prep.Close()
	require.Equal(t, p.Fingerprint(), prep.Fingerprint())
}
