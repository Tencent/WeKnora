//go:build darwin

// Package seatbelt implements the sandbox backend for macOS on top of
// /usr/bin/sandbox-exec. The policy compiler in this package carries no build
// tag so it stays testable on every platform.
package seatbelt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Tencent/WeKnora/internal/localsandbox/core"
	"github.com/Tencent/WeKnora/internal/logger"
)

// seatbeltExecPath is hard-coded rather than resolved through PATH: a PATH
// lookup would let anything earlier in PATH impersonate the sandbox. If this
// binary itself is tampered with, the attacker already has root.
const seatbeltExecPath = "/usr/bin/sandbox-exec"

type seatbeltBackend struct{}

// New returns the macOS Seatbelt backend.
func New() (core.Backend, error) {
	return &seatbeltBackend{}, nil
}

func (b *seatbeltBackend) Name() string { return "seatbelt" }

func (b *seatbeltBackend) Available() error {
	info, err := os.Stat(seatbeltExecPath)
	if err != nil {
		logger.Errorf(context.Background(), "[LocalSandbox] seatbelt missing: %v", err)
		return fmt.Errorf("%w: %s is missing", core.ErrUnsupportedPlatform, seatbeltExecPath)
	}
	if info.Mode()&0o111 == 0 {
		logger.Errorf(context.Background(), "[LocalSandbox] seatbelt not executable: %s", seatbeltExecPath)
		return fmt.Errorf("%w: %s is not executable", core.ErrUnsupportedPlatform, seatbeltExecPath)
	}
	return nil
}

// EnsureReady is a no-op: Seatbelt needs no installation and no elevation.
// That is what makes it suitable for a double-click desktop app.
func (b *seatbeltBackend) EnsureReady(context.Context) error { return nil }

// TearDown is a no-op for the same reason: nothing persistent was created.
func (b *seatbeltBackend) TearDown(context.Context) error { return nil }

type seatbeltPrepared struct {
	fingerprint string
	program     seatbeltProgram
}

func (p *seatbeltPrepared) Fingerprint() string { return p.fingerprint }
func (p *seatbeltPrepared) Close() error        { return nil }

func (b *seatbeltBackend) Prepare(ctx context.Context, p core.Policy) (core.Prepared, error) {
	if err := b.Available(); err != nil {
		return nil, err
	}
	resolved, err := resolvePolicyPaths(p)
	if err != nil {
		logger.Errorf(ctx, "[LocalSandbox] seatbelt resolve policy paths: %v", err)
		return nil, err
	}
	program, err := compileSeatbelt(resolved)
	if err != nil {
		logger.Errorf(ctx, "[LocalSandbox] seatbelt compile: %v", err)
		return nil, err
	}
	logger.Debugf(ctx, "[LocalSandbox] seatbelt prepared fingerprint=%s params=%d",
		p.Fingerprint(), len(program.Params))
	return &seatbeltPrepared{fingerprint: p.Fingerprint(), program: program}, nil
}

func (b *seatbeltBackend) Spawn(
	ctx context.Context, prep core.Prepared, cmd core.Command,
) (core.Process, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	prepared, ok := prep.(*seatbeltPrepared)
	if !ok {
		return nil, fmt.Errorf("localsandbox: prepared handle is not a seatbelt program")
	}

	argv := []string{"-p", prepared.program.Profile}
	argv = append(argv, prepared.program.Params...)
	argv = append(argv, "--")
	argv = append(argv, cmd.Argv...)

	// Not CommandContext: cancellation must kill the whole process group, and
	// CommandContext only signals the root process.
	execCmd := exec.Command(seatbeltExecPath, argv...)
	execCmd.Dir = cmd.Cwd
	execCmd.Env = withSandboxTemp(mergedEnv(cmd.Env), cmd.Cwd, cmd.Env)
	if len(cmd.Stdin) > 0 {
		execCmd.Stdin = bytes.NewReader(cmd.Stdin)
	}
	// Setpgid puts the child in its own process group so descendants can be
	// terminated together.
	execCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("localsandbox: stdout pipe: %w", err)
	}
	stderr, err := execCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("localsandbox: stderr pipe: %w", err)
	}
	started := time.Now()
	if err := execCmd.Start(); err != nil {
		logger.Errorf(ctx, "[LocalSandbox] seatbelt start cwd=%s: %v", cmd.Cwd, err)
		return nil, fmt.Errorf("localsandbox: start sandboxed command: %w", err)
	}
	pid := 0
	if execCmd.Process != nil {
		pid = execCmd.Process.Pid
	}
	logger.Infof(ctx, "[LocalSandbox] seatbelt spawn pid=%d cwd=%s argv0=%s", pid, cmd.Cwd, cmd.Argv[0])

	proc := &seatbeltProcess{
		cmd:      execCmd,
		stdout:   stdout,
		stderr:   stderr,
		started:  started,
		waitDone: make(chan struct{}),
	}
	go proc.watchContext(ctx)
	return proc, nil
}

func resolvePolicyPaths(p core.Policy) (core.Policy, error) {
	out := p
	out.WritableRoots = make([]core.WritableRoot, len(p.WritableRoots))
	for i, root := range p.WritableRoots {
		path, err := resolveExisting(root.Path)
		if err != nil {
			return core.Policy{}, err
		}
		subs := make([]string, len(root.ReadOnlySubpaths))
		for j, sub := range root.ReadOnlySubpaths {
			subs[j], err = resolveExisting(sub)
			if err != nil {
				return core.Policy{}, err
			}
		}
		out.WritableRoots[i] = core.WritableRoot{Path: path, ReadOnlySubpaths: subs}
	}
	out.ReadableRoots = make([]string, len(p.ReadableRoots))
	for i, root := range p.ReadableRoots {
		var err error
		out.ReadableRoots[i], err = resolveExisting(root)
		if err != nil {
			return core.Policy{}, err
		}
	}
	// The kernel matches the resolved path, so an unresolved private root
	// silently protects nothing: /tmp/... never matches /private/tmp/...
	out.PrivateRoots = make([]string, len(p.PrivateRoots))
	for i, private := range p.PrivateRoots {
		var err error
		out.PrivateRoots[i], err = resolveExisting(private)
		if err != nil {
			return core.Policy{}, err
		}
	}
	out.DenyRead = make([]string, len(p.DenyRead))
	for i, deny := range p.DenyRead {
		var err error
		out.DenyRead[i], err = resolveExisting(deny)
		if err != nil {
			return core.Policy{}, err
		}
	}
	cwd, err := resolveExisting(p.Cwd)
	if err != nil {
		return core.Policy{}, err
	}
	out.Cwd = cwd
	return out, nil
}

// resolveExisting EvalSymlinks the longest existing prefix. /tmp/foo becomes
// /private/tmp/foo; a missing ~/.ssh keeps the realpath of the parent.
func resolveExisting(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return cleaned, nil
	}
	resolvedParent, err := resolveExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(cleaned)), nil
}

func mergedEnv(env map[string]string) []string {
	// Nil is a direct-Spawn fallback: filtered inherit. Service always
	// passes a map already run through BuildCommandEnv.
	if env == nil {
		return core.FilterInheritedEnv(os.Environ())
	}
	return core.EnvSlice(env)
}

func withSandboxTemp(env []string, cwd string, explicit map[string]string) []string {
	// Command.Env is the only way a caller opts into a custom temp dir. An
	// inherited host TMPDIR (Go tests set one under /var/folders) would
	// otherwise skip injection and leave python/git writing outside the
	// writable root.
	if _, ok := explicit["TMPDIR"]; ok {
		return env
	}
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMPDIR=") ||
			strings.HasPrefix(kv, "TMP=") ||
			strings.HasPrefix(kv, "TEMP=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"TMPDIR="+cwd,
		"TMP="+cwd,
		"TEMP="+cwd,
	)
}

type seatbeltProcess struct {
	cmd      *exec.Cmd
	stdout   io.ReadCloser
	stderr   io.ReadCloser
	started  time.Time
	waitDone chan struct{}

	mu     sync.Mutex
	killed bool
	reaped bool
}

func (p *seatbeltProcess) Stdout() io.Reader { return p.stdout }
func (p *seatbeltProcess) Stderr() io.Reader { return p.stderr }
func (p *seatbeltProcess) PID() int          { return p.cmd.Process.Pid }

func (p *seatbeltProcess) watchContext(ctx context.Context) {
	select {
	case <-p.waitDone:
		return
	case <-ctx.Done():
	}
	// ctx fired. If Wait already reaped the process, skip Kill: SIGKILL to
	// -pid after wait has a PID-reuse window. waitDone is closed first.
	select {
	case <-p.waitDone:
		return
	default:
		_ = p.Kill()
	}
}

// Kill terminates the entire process group. Killing only the root would leave
// descendants running after a timeout.
func (p *seatbeltProcess) Kill() error {
	p.mu.Lock()
	// ProcessState is written before cmd.Wait returns, so this closes the
	// window between Wait returning and reaped being set.
	if p.reaped || p.cmd.ProcessState != nil {
		p.mu.Unlock()
		return nil
	}
	p.killed = true
	pid := 0
	if p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	p.mu.Unlock()

	if pid == 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil &&
		err != syscall.ESRCH {
		return fmt.Errorf("localsandbox: kill process group: %w", err)
	}
	return nil
}

func (p *seatbeltProcess) Wait(context.Context) (core.ExitStatus, error) {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.reaped = true
	killed := p.killed
	p.mu.Unlock()
	close(p.waitDone)
	duration := time.Since(p.started)

	status := core.ExitStatus{Code: p.cmd.ProcessState.ExitCode(), Killed: killed, Duration: duration}
	// A non-zero exit is a normal result, not a transport failure: only errors
	// other than *exec.ExitError mean the execution itself went wrong.
	var exitErr *exec.ExitError
	if err == nil || errors.As(err, &exitErr) {
		return status, nil
	}
	return status, fmt.Errorf("localsandbox: wait: %w", err)
}
