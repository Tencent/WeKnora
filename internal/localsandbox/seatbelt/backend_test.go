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
	return runSandboxedArgv(t, backend, p, []string{"/bin/bash", "-c", script}, nil)
}

func runSandboxedArgv(
	t *testing.T, backend core.Backend, p core.Policy, argv []string, env map[string]string,
) (core.ExitStatus, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prep, err := backend.Prepare(ctx, p)
	require.NoError(t, err)
	defer func() { _ = prep.Close() }()

	proc, err := backend.Spawn(ctx, prep, core.Command{
		Argv: argv,
		Env:  env,
		Cwd:  p.Cwd,
	})
	require.NoError(t, err)

	outCh := make(chan []byte, 1)
	errCh := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(proc.Stdout())
		outCh <- b
	}()
	go func() {
		b, _ := io.ReadAll(proc.Stderr())
		errCh <- b
	}()
	status, err := proc.Wait(ctx)
	require.NoError(t, err)
	return status, string(<-outCh) + string(<-errCh)
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

func TestSeatbeltDeniesSymlinkEscapeToSecrets(t *testing.T) {
	backend, p, base := darwinFixture(t)
	status, out := runSandboxed(t, backend, p,
		`ln -s `+filepath.Join(base, "secrets", "note.txt")+` ./stolen && cat ./stolen`)
	require.NotEqual(t, 0, status.Code, out)
	require.NotContains(t, out, "KEY")
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
	secretProfile := []byte("export AWS_SECRET_ACCESS_KEY=super-secret\n")
	require.NoError(t, os.WriteFile(filepath.Join(home, ".profile"), secretProfile, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".bashrc"), []byte("export GITHUB_TOKEN=gho_secret\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".zshrc"), []byte("export OPENAI_API_KEY=sk-secret\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ssh", "id_rsa"), []byte("PRIVATEKEY"), 0o600))

	builder := core.NewPolicyBuilder(home, filepath.Join(home, "Library", "App"))
	p, err := builder.Build(core.ModeAuto, core.Workspace{Kind: core.WorkspaceSession, Root: workspace})
	require.NoError(t, err)

	env := map[string]string{
		"HOME": home,
		"PATH": os.Getenv("PATH"),
	}
	noProfile := []string{"/bin/bash", "--noprofile", "--norc", "-c"}
	writeCmd := `echo alive && echo w > ./f.txt && cat ./f.txt`
	status, out := runSandboxedArgv(t, backend, p, append(noProfile, writeCmd), env)
	require.Equal(t, 0, status.Code, out)
	require.Contains(t, out, "alive")
	require.Contains(t, out, "w")

	status, out = runSandboxedArgv(t, backend, p, append(noProfile, `cat `+filepath.Join(home, ".ssh", "id_rsa")), env)
	require.NotEqual(t, 0, status.Code, out)
	require.NotContains(t, out, "PRIVATEKEY")

	status, out = runSandboxedArgv(t, backend, p, append(noProfile, `cat `+filepath.Join(home, ".zshrc")), env)
	require.NotEqual(t, 0, status.Code, out)
	require.NotContains(t, out, "sk-secret")

	status, out = runSandboxedArgv(t, backend, p, append(noProfile, "printenv"), env)
	require.Equal(t, 0, status.Code, out)
	require.NotContains(t, out, "super-secret")
	require.NotContains(t, out, "gho_secret")
	require.NotContains(t, out, "sk-secret")
}

// Cancelling the spawn context after Wait must not SIGKILL the process group:
// the pid may already have been reused. Service.Run's defer cancel() hits
// this on every successful command.
func TestSeatbeltDoesNotKillAfterSuccessfulWait(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	prep, err := backend.Prepare(ctx, p)
	require.NoError(t, err)
	defer func() { _ = prep.Close() }()

	proc, err := backend.Spawn(ctx, prep, core.Command{
		Argv: []string{"/bin/bash", "-c", "echo hi"},
		Cwd:  p.Cwd,
	})
	require.NoError(t, err)

	outCh := make(chan []byte, 1)
	errCh := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(proc.Stdout())
		outCh <- b
	}()
	go func() {
		b, _ := io.ReadAll(proc.Stderr())
		errCh <- b
	}()
	status, err := proc.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, status.Code, string(<-outCh)+string(<-errCh))
	require.False(t, status.Killed)

	cancel()
	time.Sleep(50 * time.Millisecond)
	sp := proc.(*seatbeltProcess)
	sp.mu.Lock()
	killed := sp.killed
	sp.mu.Unlock()
	require.False(t, killed, "spawn-context cancel after Wait must not Kill(-pid)")
}

func TestSeatbeltKillAfterWaitDoesNotSignal(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prep, err := backend.Prepare(ctx, p)
	require.NoError(t, err)
	defer func() { _ = prep.Close() }()

	proc, err := backend.Spawn(ctx, prep, core.Command{
		Argv: []string{"/bin/bash", "-c", "echo hi"},
		Cwd:  p.Cwd,
	})
	require.NoError(t, err)

	go func() { _, _ = io.ReadAll(proc.Stdout()) }()
	go func() { _, _ = io.ReadAll(proc.Stderr()) }()
	status, err := proc.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, status.Code)

	require.NoError(t, proc.Kill())
	sp := proc.(*seatbeltProcess)
	sp.mu.Lock()
	killed := sp.killed
	sp.mu.Unlock()
	require.False(t, killed, "Kill after Wait must be a no-op")
}

// When the workspace is the home directory, credential files sit inside the
// writable root. subpath deny does not match a file, so without a literal
// write deny `echo pwned > ~/.cargo/credentials.toml` succeeds.
func TestSeatbeltDeniesWritingCredentialFileInsideWritableHome(t *testing.T) {
	backend, err := New()
	require.NoError(t, err)
	require.NoError(t, backend.Available())

	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	cargo := filepath.Join(home, ".cargo")
	require.NoError(t, os.MkdirAll(cargo, 0o755))
	creds := filepath.Join(cargo, "credentials.toml")
	require.NoError(t, os.WriteFile(creds, []byte("SECRET"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cargo, "config.toml"), []byte("ok\n"), 0o644))

	p := core.Policy{
		WritableRoots: []core.WritableRoot{{
			Path:             home,
			ReadOnlySubpaths: []string{filepath.Join(home, ".git")},
		}},
		ReadableRoots: []string{home, cargo},
		PrivateRoots:  []string{home},
		DenyRead:      []string{creds},
		Network:       core.NetworkDenied,
		Cwd:           home,
	}
	require.NoError(t, p.Validate())

	status, out := runSandboxed(t, backend, p, `echo pwned > `+creds)
	require.NotEqual(t, 0, status.Code, out)
	got, err := os.ReadFile(creds)
	require.NoError(t, err)
	require.Equal(t, "SECRET", string(got))

	status, out = runSandboxed(t, backend, p, `echo x > `+filepath.Join(cargo, "config.toml"))
	require.Equal(t, 0, status.Code, out)
}

func TestSeatbeltDeniesWritingAppDataInsideWritableHome(t *testing.T) {
	backend, err := New()
	require.NoError(t, err)
	require.NoError(t, backend.Available())

	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	appData := filepath.Join(home, "Library", "App")
	require.NoError(t, os.MkdirAll(appData, 0o755))
	prefs := filepath.Join(appData, "prefs.json")
	require.NoError(t, os.WriteFile(prefs, []byte("keep"), 0o600))

	p := core.Policy{
		WritableRoots: []core.WritableRoot{{
			Path:             home,
			ReadOnlySubpaths: []string{filepath.Join(home, ".git")},
		}},
		ReadableRoots: []string{home},
		PrivateRoots:  []string{home},
		DenyRead:      []string{appData},
		Network:       core.NetworkDenied,
		Cwd:           home,
	}
	require.NoError(t, p.Validate())

	status, out := runSandboxed(t, backend, p, `echo pwned > `+prefs)
	require.NotEqual(t, 0, status.Code, out)
	got, err := os.ReadFile(prefs)
	require.NoError(t, err)
	require.Equal(t, "keep", string(got))
}

// file-read* is blanket; /tmp and /var/folders must stay behind PrivateRoots
// or the agent can read other apps' temp tokens. Workspace under a temp
// fixture must still be writable after those denials.
func TestSeatbeltDeniesReadingTmpAndVarFolders(t *testing.T) {
	backend, err := New()
	require.NoError(t, err)
	require.NoError(t, backend.Available())

	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	workspace := filepath.Join(home, "Documents", "WeKnora", "s1")
	require.NoError(t, os.MkdirAll(workspace, 0o755))

	tmpSecret := filepath.Join("/tmp", "weknora-seatbelt-"+t.Name())
	require.NoError(t, os.WriteFile(tmpSecret, []byte("TMPSECRET"), 0o600))
	t.Cleanup(func() { _ = os.Remove(tmpSecret) })

	vfSecret := filepath.Join(os.TempDir(), "weknora-seatbelt-"+t.Name())
	require.NoError(t, os.WriteFile(vfSecret, []byte("VFSECRET"), 0o600))
	t.Cleanup(func() { _ = os.Remove(vfSecret) })

	builder := core.NewPolicyBuilder(home, filepath.Join(home, "Library", "App"))
	p, err := builder.Build(core.ModeAuto, core.Workspace{Kind: core.WorkspaceSession, Root: workspace})
	require.NoError(t, err)

	env := map[string]string{
		"HOME": home,
		"PATH": os.Getenv("PATH"),
	}
	noProfile := []string{"/bin/bash", "--noprofile", "--norc", "-c"}

	status, out := runSandboxedArgv(t, backend, p, append(noProfile, `echo ok > ./f.txt && cat ./f.txt`), env)
	require.Equal(t, 0, status.Code, out)
	require.Contains(t, out, "ok")

	status, out = runSandboxedArgv(t, backend, p, append(noProfile, `cat `+tmpSecret), env)
	require.NotEqual(t, 0, status.Code, out)
	require.NotContains(t, out, "TMPSECRET")

	status, out = runSandboxedArgv(t, backend, p, append(noProfile, `cat `+vfSecret), env)
	require.NotEqual(t, 0, status.Code, out)
	require.NotContains(t, out, "VFSECRET")
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

func TestSeatbeltDoesNotInheritSecretEnv(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "super-secret")
	t.Setenv("GITHUB_TOKEN", "gho_secret")

	backend, p, _ := darwinFixture(t)
	status, out := runSandboxed(t, backend, p, `printenv`)
	require.Equal(t, 0, status.Code, out)
	require.NotContains(t, out, "super-secret")
	require.NotContains(t, out, "gho_secret")
	require.NotContains(t, out, "AWS_SECRET_ACCESS_KEY")
	require.NotContains(t, out, "GITHUB_TOKEN")
}

func TestSeatbeltKillsProcessTreeOnContextCancel(t *testing.T) {
	backend, p, _ := darwinFixture(t)
	ctx, cancel := context.WithCancel(context.Background())

	prep, err := backend.Prepare(ctx, p)
	require.NoError(t, err)
	defer func() { _ = prep.Close() }()

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
	defer func() { _ = prep.Close() }()
	require.Equal(t, p.Fingerprint(), prep.Fingerprint())
}
