package localsandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

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
	// ReviewCommand is the command as the model wrote it, before stdin or skill
	// wrapping. Approval reads it; empty means Command.
	ReviewCommand string
	// Approver asks the user. Nil means nobody can be asked, so every
	// question is answered no.
	Approver Approver
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
	mode := ModeAuto
	if s.modes != nil {
		mode = s.modes.ModeForSession(ctx, sessionID)
	}
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
// It derives from the same Policy the backend compiles, which is what keeps
// the two enforcement paths in agreement.
func (s *Service) GuardForSession(
	ctx context.Context, sessionID string,
) (*PathGuard, Workspace, error) {
	ws, policy, err := s.workspaceFor(ctx, sessionID)
	if err != nil {
		return nil, Workspace{}, err
	}
	logger.Debugf(ctx, "[LocalSandbox] guard session=%s root=%s", sessionID, ws.Root)
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
// behind a user who has not answered yet.
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
	review := req.ReviewCommand
	if review == "" {
		review = req.Command
	}
	if refused := s.approveDelete(ctx, req, review, cwd); refused != nil {
		return refused, nil
	}
	res, err := s.runPolicy(ctx, ws.Root, policy, req)
	if err != nil || !res.Denial.IsDenied() {
		return res, err
	}
	return s.escalate(ctx, ws.Root, policy, cwd, review, req, res)
}

var errNoApprover = errors.New("nobody can approve it in this session")

func (s *Service) approveDelete(ctx context.Context, req RunRequest, review, cwd string) *RunResult {
	segments := DeleteSegments(review, s.builder.HomeDir())
	if len(segments) == 0 {
		return nil
	}
	dangerous := false
	opaque := false
	rules := make([]DeleteRule, 0, len(segments))
	for _, segment := range segments {
		dangerous = dangerous || segment.Dangerous
		opaque = opaque || segment.Opaque
		rules = append(rules, segment.Rule)
	}
	// The session remembers the shape of each delete, like a Codex prefix
	// rule, so the model may name other files or reorder options. Every
	// delete in the command must be covered. An opaque command hides a
	// delete no rule can name, so it is asked every time.
	rememberable := !dangerous && !opaque
	if rememberable && s.sessions.rulesCover(req.SessionID, cwd, rules) {
		return nil
	}
	reason := ReasonDelete
	if dangerous {
		reason = ReasonDangerous
	}
	approval := ApprovalRequest{
		SessionID:    req.SessionID,
		Command:      review,
		Cwd:          cwd,
		Reason:       reason,
		AllowSession: rememberable,
	}
	if rememberable {
		approval.SessionRules = ruleLabels(rules)
	}
	d, err := s.ask(ctx, req.Approver, approval)
	if err != nil || !d.Approved {
		return &RunResult{Exit: ExitStatus{Code: 1}, Notice: refusalNotice("delete command", d, err)}
	}
	if d.Session && rememberable {
		for _, rule := range rules {
			s.sessions.approveRule(req.SessionID, cwd, rule)
		}
	}
	return nil
}

func ruleLabels(rules []DeleteRule) []string {
	labels := make([]string, 0, len(rules))
	for _, rule := range rules {
		if label := rule.String(); label != "" && !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	return labels
}

// maxEscalations is how many distinct directories one command may ask to
// widen. The first denial asks, the retry asks once more if it hits a
// different directory, and a further denial is returned as-is.
const maxEscalations = 2

func (s *Service) escalate(
	ctx context.Context, lockRoot string, policy Policy, cwd, review string, req RunRequest, res *RunResult,
) (*RunResult, error) {
	for range maxEscalations {
		proposed, ok := s.builder.ProposeGrant(policy, res.Denial, cwd)
		if !ok {
			res.Notice = blockedNotice(res.Denial)
			return res, nil
		}
		d, err := s.ask(ctx, req.Approver, ApprovalRequest{
			SessionID:       req.SessionID,
			Command:         review,
			Cwd:             cwd,
			Reason:          ReasonDenied,
			Proposed:        &proposed,
			DenialSnippet:   res.Denial.Snippet,
			FirstAttemptRan: true,
			AllowSession:    true,
		})
		if err != nil || !d.Approved {
			res.Notice = blockedNotice(res.Denial) + "\n" + refusalNotice("access request", d, err)
			return res, nil
		}
		grant := narrowGrant(proposed, d.Grant)
		relaxed, err := s.builder.Relax(policy, grant)
		if err != nil {
			res.Notice = fmt.Sprintf("[sandbox] could not widen the sandbox for %s: %v", grant.Path, err)
			return res, nil
		}
		if d.Session {
			s.sessions.addGrant(req.SessionID, grant)
		}
		logger.Infof(ctx, "[LocalSandbox] re-running with grant session=%s path=%s access=%s session_scope=%t",
			req.SessionID, grant.Path, grant.Access, d.Session)
		next, err := s.runPolicy(ctx, lockRoot, relaxed, req)
		if err != nil {
			return nil, err
		}
		if !next.Denial.IsDenied() {
			return next, nil
		}
		policy = relaxed
		res = next
	}
	res.Notice = blockedNotice(res.Denial)
	return res, nil
}

func (s *Service) ask(ctx context.Context, approver Approver, req ApprovalRequest) (ApprovalDecision, error) {
	if approver == nil {
		return ApprovalDecision{}, errNoApprover
	}
	logger.Infof(ctx, "[LocalSandbox] approval requested session=%s reason=%s command=%q",
		req.SessionID, req.Reason, previewCommand(req.Command))
	d, err := approver.Approve(ctx, req)
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] approval failed session=%s reason=%s: %v", req.SessionID, req.Reason, err)
		return ApprovalDecision{}, err
	}
	logger.Infof(ctx, "[LocalSandbox] approval answered session=%s reason=%s approved=%t session_scope=%t",
		req.SessionID, req.Reason, d.Approved, d.Session)
	return d, nil
}

// narrowGrant applies the user's choice. Only write to read is honoured; the
// path always stays the one proposed.
func narrowGrant(proposed Grant, chosen *Grant) Grant {
	if chosen != nil && chosen.Access == AccessRead {
		proposed.Access = AccessRead
	}
	return proposed
}

func blockedNotice(d Denial) string {
	if d.Path != "" {
		return fmt.Sprintf("[sandbox] blocked: %s is outside what this session may access", d.Path)
	}
	return "[sandbox] blocked by the workspace sandbox"
}

func refusalNotice(what string, d ApprovalDecision, err error) string {
	switch {
	case errors.Is(err, errNoApprover):
		return fmt.Sprintf("[sandbox] this %s needs the user's approval, and %v", what, err)
	case err != nil:
		return fmt.Sprintf("[sandbox] the approval request for this %s failed: %v", what, err)
	case d.Reason != "":
		return fmt.Sprintf("[sandbox] the user denied this %s: %q. Do not retry it another way.", what, d.Reason)
	default:
		return fmt.Sprintf("[sandbox] the user denied this %s. Do not retry it another way.", what)
	}
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
	return command[:commandLogLimit] + "…"
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
