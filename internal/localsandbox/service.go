package localsandbox

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/utils"
)

// defaultRunTimeout matches the shell_exec tool default so behaviour does not
// change when a session moves between a remote sandbox and this one.
const defaultRunTimeout = 120 * time.Second

// maxRunTimeout hard-caps whatever the caller asks for.
const maxRunTimeout = 10 * time.Minute

// maxCapturedStreamBytes bounds what one stream of one command can pull into
// this process. shell_exec caps the model-visible slice far lower (64 KiB
// hard), but that truncation runs on the string the executor already
// returned. On a remote backend the bytes pile up on the other machine; here
// the producer is a local process and the consumer is the desktop app, so
// `cat /dev/urandom` would grow the heap until the run timeout fires.
const maxCapturedStreamBytes = 1 << 20

// ModeLookup reports the approval mode in force for a session.
type ModeLookup interface {
	ModeForSession(ctx context.Context, sessionID string) ApprovalMode
}

// Service is the facade the application layer uses. It hides policy
// derivation, preparation, spawning and denial classification behind one call.
type Service struct {
	backend  Backend
	resolver WorkspaceResolver
	builder  *PolicyBuilder
	modes    ModeLookup
	shell    []string
	roots    *rootLocks
	sessions *sessionStates
}

// NewService constructs the application-facing sandbox facade.
func NewService(
	backend Backend, resolver WorkspaceResolver, builder *PolicyBuilder, modes ModeLookup,
) *Service {
	return &Service{
		backend:  backend,
		resolver: resolver,
		builder:  builder,
		modes:    modes,
		shell:    []string{"/bin/bash", "--noprofile", "--norc", "-c"},
		roots:    newRootLocks(),
		sessions: newSessionStates(),
	}
}

// RunRequest is one shell command to execute inside a session workspace.
type RunRequest struct {
	SessionID string
	Command   string
	// WorkDir is optional and must stay inside the workspace.
	WorkDir string
	Timeout time.Duration
	// Env overlays the filtered host inherit. It is not itself allowlisted:
	// pass only the keys this command needs (skill credentials, PATH tweaks).
	// Do not copy os.Environ() into it.
	Env map[string]string
	// Spec is what approval reads. An empty Spec.Command means Command.
	Spec ReviewSpec
	// Approver asks the user (ModeAsk). Nil means nobody can be asked.
	Approver Approver
	// Reviewer is the review model (ModeAuto). Nil means auto refuses.
	Reviewer Approver
}

// RunResult is the captured outcome of one Run.
type RunResult struct {
	Stdout string
	Stderr string
	Exit   ExitStatus
	Denial Denial
	// Notice is a sandbox note for the model, appended to stderr by the caller.
	Notice string
}

// workspaceFor resolves the session workspace and its policy.
func (s *Service) workspaceFor(
	ctx context.Context, sessionID string,
) (Workspace, Policy, error) {
	ws, err := s.resolver.Resolve(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] resolve workspace session=%s: %v", sessionID, err)
		return Workspace{}, Policy{}, err
	}
	mode := s.ModeForSession(ctx, sessionID)
	// Strict enforcement boundary. Normalizing user input is the caller's job
	// (see ParseApprovalMode); by the time a mode reaches here, honouring it
	// or refusing are the only honest options — substituting a different
	// permission stance for the one that was asked for is not.
	if !mode.Shipped() {
		logger.Warnf(ctx, "[LocalSandbox] refusing session=%s: approval mode %q is not available",
			sessionID, mode)
		return Workspace{}, Policy{}, fmt.Errorf("%w: %q", ErrApprovalModeNotShipped, mode)
	}
	policy, err := s.builder.Build(mode, ws)
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] build policy session=%s mode=%s root=%s: %v",
			sessionID, mode, ws.Root, err)
		return Workspace{}, Policy{}, err
	}
	for _, g := range s.sessions.grants(sessionID) {
		relaxed, err := s.builder.Relax(policy, g)
		if err != nil {
			// A grant stops fitting when the session's project changes.
			logger.Warnf(ctx, "[LocalSandbox] drop session grant session=%s path=%s: %v",
				sessionID, g.Path, err)
			continue
		}
		policy = relaxed
	}
	logger.Debugf(ctx, "[LocalSandbox] workspace session=%s kind=%d root=%s mode=%s writable=%d",
		sessionID, ws.Kind, ws.Root, mode, len(policy.WritableRoots))
	return ws, policy, nil
}

// GuardForSession returns the path guard the trusted file tools must use.
// extra adds grants for this call only, such as a file write just approved.
func (s *Service) GuardForSession(
	ctx context.Context, sessionID string, extra ...Grant,
) (*PathGuard, Workspace, error) {
	ws, policy, err := s.workspaceFor(ctx, sessionID)
	if err != nil {
		return nil, Workspace{}, err
	}
	for _, g := range extra {
		relaxed, err := s.builder.Relax(policy, g)
		if err != nil {
			return nil, Workspace{}, err
		}
		policy = relaxed
	}
	logger.Debugf(ctx, "[LocalSandbox] guard session=%s root=%s extra=%d", sessionID, ws.Root, len(extra))
	return NewPathGuard(policy), ws, nil
}

// SessionGrants returns the grants the user approved for the rest of a session.
func (s *Service) SessionGrants(sessionID string) []Grant {
	return s.sessions.grants(sessionID)
}

// ReleaseSession forgets every approval made in a session.
func (s *Service) ReleaseSession(sessionID string) {
	s.sessions.release(sessionID)
}

// Run executes one shell command inside the session's workspace. Approval
// waits happen outside the root lock so parallel tool calls are not stalled
// behind an approver that has not answered yet.
func (s *Service) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := s.ensureBackend(ctx); err != nil {
		return nil, err
	}
	ws, policy, err := s.workspaceFor(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	cwd, err := commandCwd(policy, req.WorkDir)
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] work_dir denied session=%s work_dir=%q: %v",
			req.SessionID, req.WorkDir, err)
		return nil, err
	}
	if req.Spec.Command == "" {
		req.Spec.Command = req.Command
	}
	turn := s.newTurn(ctx, req, ws)
	policy, refused, err := s.approveDelete(ctx, turn, policy, cwd)
	if err != nil || refused != nil {
		return refused, err
	}
	res, err := s.runPolicy(ctx, ws.Root, policy, req)
	if err != nil || !res.Denial.IsDenied() {
		return res, err
	}
	return s.escalate(ctx, turn, ws.Root, policy, cwd, res)
}

// RunWithPolicy runs one command under a policy the caller built. Skill
// installs use it: their policy belongs to an install directory, not to a
// chat session's workspace.
func (s *Service) RunWithPolicy(ctx context.Context, policy Policy, req RunRequest) (*RunResult, error) {
	if err := s.ensureBackend(ctx); err != nil {
		return nil, err
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return s.runPolicy(ctx, policy.Cwd, policy, req)
}

func (s *Service) ensureBackend(ctx context.Context) error {
	if err := s.backend.Available(); err != nil {
		logger.Errorf(ctx, "[LocalSandbox] backend unavailable: %v", err)
		return err
	}
	if err := s.backend.EnsureReady(ctx); err != nil {
		logger.Errorf(ctx, "[LocalSandbox] backend not ready: %v", err)
		return fmt.Errorf("ensure sandbox ready: %w", err)
	}
	return nil
}

func (s *Service) runPolicy(ctx context.Context, lockRoot string, policy Policy, req RunRequest) (*RunResult, error) {
	unlock := s.LockRoot(lockRoot)
	defer unlock()

	// The approval that let this run may have been given just before the
	// user stopped the turn; the lock wait is the last point to notice.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cwd, err := commandCwd(policy, req.WorkDir)
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] work_dir denied session=%s work_dir=%q: %v",
			req.SessionID, req.WorkDir, err)
		return nil, err
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}
	if timeout > maxRunTimeout {
		timeout = maxRunTimeout
	}
	logger.Infof(ctx, "[LocalSandbox] run session=%s cwd=%s timeout=%s command=%q",
		req.SessionID, cwd, timeout, previewCommand(req.Command))
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prep, err := s.backend.Prepare(runCtx, policy)
	if err != nil {
		logger.Errorf(ctx, "[LocalSandbox] prepare session=%s: %v", req.SessionID, err)
		return nil, fmt.Errorf("prepare sandbox: %w", err)
	}
	defer func() { _ = prep.Close() }()

	argv := append(append([]string(nil), s.shell...), req.Command)
	var extraPATH []string
	if s.builder != nil {
		extraPATH = s.builder.ToolchainBins()
	}
	proc, err := s.backend.Spawn(runCtx, prep, Command{
		Argv: argv,
		Env:  BuildCommandEnv(req.Env, extraPATH),
		Cwd:  cwd,
	})
	if err != nil {
		logger.Errorf(ctx, "[LocalSandbox] spawn session=%s cwd=%s: %v", req.SessionID, cwd, err)
		return nil, fmt.Errorf("spawn sandboxed command: %w", err)
	}

	// Kill the process group on timeout or caller cancel. Stop the watcher
	// as soon as Wait returns so a finished process is never signalled —
	// process-group Kill(-pid) after wait has a PID-reuse window.
	// Drain both pipes concurrently before Wait: sequential reads deadlock
	// when the unread pipe fills.
	done := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watchKill(runCtx, done, proc)
	}()

	stdout, stderr := drain(proc)
	status, err := proc.Wait(runCtx)
	close(done)
	<-watchDone
	if err != nil {
		logger.Errorf(ctx, "[LocalSandbox] wait session=%s: %v", req.SessionID, err)
		return nil, err
	}

	denial := ClassifyRunDenial(policy, status, stdout, stderr)
	if denial.IsDenied() {
		logger.Warnf(ctx, "[LocalSandbox] denied session=%s reason=%d path=%q exit=%d killed=%t duration=%s",
			req.SessionID, denial.Reason, denial.Path, status.Code, status.Killed, status.Duration)
	} else {
		logger.Infof(ctx, "[LocalSandbox] done session=%s exit=%d killed=%t duration=%s stdout=%d stderr=%d",
			req.SessionID, status.Code, status.Killed, status.Duration, len(stdout), len(stderr))
	}

	return &RunResult{
		Stdout: stdout,
		Stderr: stderr,
		Exit:   status,
		Denial: denial,
	}, nil
}

func commandCwd(policy Policy, workDir string) (string, error) {
	if workDir == "" {
		return policy.Cwd, nil
	}
	// work_dir is a convenience for the model, not a privilege boundary;
	// it is checked against the same guard the file tools use.
	return NewPathGuard(policy).CheckDir(workDir)
}

func watchKill(ctx context.Context, done <-chan struct{}, proc Process) {
	select {
	case <-done:
		return
	case <-ctx.Done():
	}
	select {
	case <-done:
		return
	default:
		_ = proc.Kill()
	}
}

const commandLogLimit = 240

func previewCommand(command string) string {
	command = strings.Join(strings.Fields(command), " ")
	command = utils.MaskCommandAssignments(command)
	if len(command) <= commandLogLimit {
		return command
	}
	// Back up to a rune start so a multibyte character (a CJK file name or
	// argument) is not split into invalid UTF-8 in the log line.
	cut := commandLogLimit
	for cut > 0 && !utf8.RuneStart(command[cut]) {
		cut--
	}
	return command[:cut] + "…"
}

func drain(proc Process) (string, string) {
	var (
		stdout string
		stderr string
		wg     sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		stdout = captureStream(proc.Stdout())
	}()
	go func() {
		defer wg.Done()
		stderr = captureStream(proc.Stderr())
	}()
	wg.Wait()
	return stdout, stderr
}

// captureStream keeps at most maxCapturedStreamBytes and discards the rest.
//
// Discarding rather than closing matters: a child blocked writing into a full
// pipe never exits, and Wait would block behind it until the run timeout.
func captureStream(r io.Reader) string {
	if r == nil {
		return ""
	}
	kept, err := io.ReadAll(io.LimitReader(r, maxCapturedStreamBytes))
	if err != nil || len(kept) < maxCapturedStreamBytes {
		return string(kept)
	}
	dropped, _ := io.Copy(io.Discard, r)
	if dropped == 0 {
		return string(kept)
	}
	return fmt.Sprintf("%s\n...[truncated %d more bytes]...", kept, dropped)
}
