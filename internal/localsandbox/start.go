package localsandbox

import (
	"context"
	"fmt"
	"sync"
)

// StartForSession resolves the session policy and starts a managed job
// process. Unlike Run it takes no root lock, applies no timeout and reads no
// pipes — the caller owns draining and Wait.
func (s *Service) StartForSession(ctx context.Context, req RunRequest) (Process, error) {
	_, policy, err := s.workspaceFor(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	return s.Start(ctx, policy, req)
}

// Start spawns one command under a prepared policy. The returned Process wraps
// the prepared handle so its resources are released exactly once at Wait; a
// failed Spawn closes it here. ctx cancellation kills the whole process group
// (seatbelt watchContext), so callers must pass an application-level ctx.
func (s *Service) Start(ctx context.Context, policy Policy, req RunRequest) (Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.ensureBackend(ctx); err != nil {
		return nil, err
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	cwd := policy.Cwd
	if req.WorkDir != "" {
		resolved, err := NewPathGuard(policy).CheckDir(req.WorkDir)
		if err != nil {
			return nil, err
		}
		cwd = resolved
	}
	prep, err := s.backend.Prepare(ctx, policy)
	if err != nil {
		return nil, fmt.Errorf("prepare sandbox: %w", err)
	}
	argv := append(append([]string(nil), s.shell...), req.Command)
	var bins []string
	if s.builder != nil {
		bins = s.builder.ToolchainBins()
	}
	proc, err := s.backend.Spawn(ctx, prep, Command{
		Argv: argv, Env: BuildCommandEnv(req.Env, bins), Cwd: cwd,
	})
	if err != nil {
		_ = prep.Close()
		return nil, fmt.Errorf("spawn sandboxed command: %w", err)
	}
	return &preparedProcess{Process: proc, prepared: prep}, nil
}

type preparedProcess struct {
	Process
	prepared Prepared
	once     sync.Once
}

func (p *preparedProcess) Wait(ctx context.Context) (ExitStatus, error) {
	status, err := p.Process.Wait(ctx)
	p.once.Do(func() { _ = p.prepared.Close() })
	return status, err
}
