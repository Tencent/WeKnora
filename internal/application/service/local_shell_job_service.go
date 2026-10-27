//go:build desktop

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// localShellJobLease is the Lite registration lease. Remote jobs renew for
	// 30s; the in-process watcher heartbeats this shorter lease itself.
	localShellJobLease     = 15 * time.Second
	localShellJobHeartbeat = 5 * time.Second
	localShellJobStopWait  = 5 * time.Second
	localPreviewBytes      = 8 * 1024
	localPreviewEvery      = 500 * time.Millisecond
)

type localJobStarter interface {
	StartForSession(context.Context, localsandbox.RunRequest) (localsandbox.Process, error)
	GuardForSession(context.Context, string) (*localsandbox.PathGuard, localsandbox.Workspace, error)
}

type localJob struct {
	meta        sandbox.ShellJobMeta
	proc        localsandbox.Process
	done        chan struct{} // closed by watcher after terminal status is persisted
	decision    chan bool     // submitter's single verdict: backgrounded?
	release     chan struct{} // closed after work_started was attempted
	reported    chan struct{} // closed after the watcher's report path finished
	requests    chan string   // cancel / lost wakeups (non-blocking send)
	reportMu    sync.Mutex    // guards suppress vs the watcher's report section
	mu          sync.Mutex
	status      sandbox.ShellJobStatus
	background  bool
	cause       string
	preview     string
	dirty       bool
	suppress    bool
	originRun   string
	lastPreview time.Time
	killOnce    sync.Once
}

type localCursor struct {
	from    sandbox.ShellJobOffsets
	expires time.Time
}

// LocalShellJobService is Lite's watcher runtime. It implements the plan-4
// LocalShellJobs contract; ShellJobService delegates host sessions here.
type LocalShellJobService struct {
	starter      localJobStarter
	bg           *BackgroundWork
	events       *BackgroundEventEmitter
	lifecycle    background.Lifecycle
	root         string
	appCtx       context.Context
	cancel       context.CancelFunc
	identity     func(context.Context, int) (int64, error)
	submitMu     sync.Mutex
	mu           sync.Mutex
	closing      bool
	blocked      map[interfaces.SessionRef]bool
	jobs         map[string]*localJob
	cursors      map[string]localCursor
	skillCapture skillEnvCaptures
}

var _ LocalShellJobs = (*LocalShellJobService)(nil)

// HoldSkillEnvCapture keeps a credential write-back until the host watcher
// sees this job succeed. Secrets stay in this process only.
func (s *LocalShellJobService) HoldSkillEnvCapture(
	ref interfaces.SessionRef, jobID string, run func(context.Context),
) {
	if s == nil || run == nil || jobID == "" {
		return
	}
	s.mu.Lock()
	j := s.jobs[jobID]
	if s.closing || s.blocked[ref] || j == nil || localRef(j.meta) != ref {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	// Serialize registration with reporting and suppression. The watcher may
	// have reported before SubmitShellJob returned its background handle.
	j.reportMu.Lock()
	defer j.reportMu.Unlock()
	s.mu.Lock()
	blocked := s.closing || s.blocked[ref]
	s.mu.Unlock()
	if blocked || j.suppress {
		return
	}
	s.skillCapture.put(ref, jobID, run)
	j.mu.Lock()
	state := j.status.State
	j.mu.Unlock()
	if interfaces.IsTerminalWorkState(state) {
		s.skillCapture.settle(context.Background(), ref, jobID, state)
	}
}

// NewLocalShellJobService starts the host watcher runtime. root is the absolute
// jobs directory. The process-identity function is supplied by crash recovery.
func NewLocalShellJobService(
	local *localsandbox.Service, bg *BackgroundWork, events *BackgroundEventEmitter, root string,
) (*LocalShellJobService, error) {
	if local == nil {
		return nil, errors.New("local shell is unavailable")
	}
	return newLocalShellJobService(local, bg, events, root, localProcessStartedAt)
}

func newLocalShellJobService(
	starter localJobStarter, bg *BackgroundWork, events *BackgroundEventEmitter, root string,
	identity func(context.Context, int) (int64, error),
) (*LocalShellJobService, error) {
	if bg == nil || bg.Registry == nil || bg.Inbox == nil {
		return nil, errors.New("background stores unavailable")
	}
	if !filepath.IsAbs(root) {
		return nil, errors.New("jobs root must be absolute")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc := &LocalShellJobService{
		starter: starter, bg: bg, events: events, root: root,
		appCtx: ctx, cancel: cancel, identity: identity,
		blocked: make(map[interfaces.SessionRef]bool),
		jobs:    make(map[string]*localJob),
		cursors: make(map[string]localCursor),
		lifecycle: background.Lifecycle{
			Registry: bg.Registry, Inbox: bg.Inbox, Logf: logger.Warnf,
		},
	}
	if events != nil {
		svc.lifecycle.Events = events
	}
	if err := svc.recoverJobs(ctx); err != nil {
		cancel()
		return nil, err
	}
	return svc, nil
}

func localRef(meta sandbox.ShellJobMeta) interfaces.SessionRef {
	return interfaces.SessionRef{TenantID: meta.TenantID, SessionID: meta.SessionID}
}

// SubmitShellJob starts one host command and waits out the yield window.
// The watcher and the child use the application context: cancelling ctx only
// ends this wait.
func (s *LocalShellJobService) SubmitShellJob(
	ctx context.Context, ref interfaces.SessionRef, req tools.ShellJobSubmitRequest,
) (tools.ShellJobSubmission, error) {
	s.submitMu.Lock()
	unlockSubmit := true
	defer func() {
		if unlockSubmit {
			s.submitMu.Unlock()
		}
	}()
	s.mu.Lock()
	unavailable := s.closing || s.blocked[ref]
	s.mu.Unlock()
	if unavailable {
		return tools.ShellJobSubmission{}, errors.New("session is closing or deleted")
	}
	if ref.TenantID == 0 || ref.SessionID == "" {
		return tools.ShellJobSubmission{}, errors.New("session owner is required")
	}
	id, err := allocateShellJobID(func(candidate string) bool {
		if _, statErr := os.Stat(filepath.Join(s.root, candidate)); statErr == nil {
			return true
		}
		s.mu.Lock()
		_, live := s.jobs[candidate]
		s.mu.Unlock()
		return live
	})
	if err != nil {
		return tools.ShellJobSubmission{}, err
	}
	requestCtx := types.WithSandboxTenantID(
		types.WithExecutionTenant(context.WithoutCancel(ctx), ref.TenantID), ref.TenantID)
	_, ws, err := s.starter.GuardForSession(requestCtx, ref.SessionID)
	if err != nil {
		return tools.ShellJobSubmission{}, err
	}
	now := time.Now().UTC()
	j := &localJob{
		meta: sandbox.ShellJobMeta{
			ID: id, TenantID: ref.TenantID, SandboxTenantID: ref.TenantID,
			SessionID: ref.SessionID, Root: ws.Root,
			Command: req.MaskedCommand(), WorkDir: req.WorkDir,
			CreatedAt: now, YieldUntil: now.Add(req.YieldTime),
			ParentMessageID: req.ParentMessageID, ToolCallID: req.ToolCallID,
			Title:     req.Description,
			OutputDir: req.OutputDir, OutputSeen: req.OutputSeen, OutputBefore: req.OutputBefore,
		},
		done: make(chan struct{}), decision: make(chan bool, 1),
		release:   make(chan struct{}),
		reported:  make(chan struct{}),
		requests:  make(chan string, 1),
		originRun: req.RunID,
	}
	if j.meta.WorkDir == "" {
		j.meta.WorkDir = ws.Root
	}
	directory := filepath.Join(s.root, id)
	var out, errLog *localJobLog
	activity := interfaces.BackgroundActivity{
		ID: id, Kind: "shell", Title: req.Description, StartedAt: now,
	}
	err = s.lifecycle.Begin(requestCtx, ref, activity, localShellJobLease, shellJobKindCap,
		func(context.Context) error {
			if err := os.Mkdir(directory, 0o700); err != nil {
				return err
			}
			if err := sandbox.WriteShellJobJSON(filepath.Join(directory, "meta.json"), j.meta); err != nil {
				return err
			}
			var lerr error
			out, lerr = newLocalJobLog(filepath.Join(directory, "stdout"))
			if lerr != nil {
				return lerr
			}
			errLog, lerr = newLocalJobLog(filepath.Join(directory, "stderr"))
			if lerr != nil {
				_ = out.Close()
				return lerr
			}
			// Application ctx (plus the session tenant): a /stop run ctx never reaches Spawn.
			processCtx := types.WithSandboxTenantID(
				types.WithExecutionTenant(s.appCtx, ref.TenantID), ref.TenantID)
			j.proc, lerr = s.starter.StartForSession(processCtx, localsandbox.RunRequest{
				SessionID: ref.SessionID, Command: req.Command,
				WorkDir: req.WorkDir, Env: req.Env,
			})
			if lerr != nil {
				_ = out.Close()
				_ = errLog.Close()
				return lerr
			}
			startedAt, serr := s.identity(processCtx, j.proc.PID())
			if errors.Is(serr, localsandbox.ErrUnsupportedPlatform) {
				startedAt = 0
				serr = nil
			}
			if serr != nil {
				_ = j.proc.Kill()
				localDrainAndReap(j.proc)
				_ = out.Close()
				_ = errLog.Close()
				return fmt.Errorf("record job process identity: %w", serr)
			}
			j.status = sandbox.ShellJobStatus{
				State: interfaces.WorkStateRunning, WatcherPID: os.Getpid(),
				PGID: j.proc.PID(), ProcStartedAt: startedAt, StartedAt: now,
			}
			if serr := sandbox.WriteShellJobJSON(filepath.Join(directory, "status.json"), j.status); serr != nil {
				_ = j.proc.Kill()
				localDrainAndReap(j.proc)
				_ = out.Close()
				_ = errLog.Close()
				return serr
			}
			s.mu.Lock()
			s.jobs[id] = j
			s.mu.Unlock()
			go s.watch(j, out, errLog, req.OnOutput)
			return nil
		})
	unlockSubmit = false
	s.submitMu.Unlock()
	if err != nil {
		// The watcher is started only after the directory is published. A failed
		// start must not leave a running-looking directory for crash recovery.
		_ = os.RemoveAll(directory)
		if errors.Is(err, interfaces.ErrActivityLimit) {
			return tools.ShellJobSubmission{}, errors.New(
				"ERR_WORK_LIMIT: this session already runs 64 background shell jobs")
		}
		return tools.ShellJobSubmission{}, err
	}
	timer := time.NewTimer(max(req.YieldTime, time.Nanosecond))
	defer timer.Stop()
	var stop <-chan struct{}
	if ctx != nil {
		stop = ctx.Done()
	}
	// /stop ends the wait and does not kill the command. A command that
	// finished in the same instant still returns inline: done wins over the
	// timer and the cancel when more than one is ready.
	backgrounded := backgroundedAfterYield(j.done, stop, timer.C)
	j.mu.Lock()
	j.background = backgrounded
	j.mu.Unlock()
	j.decision <- backgrounded
	if backgrounded && s.events != nil {
		_ = s.events.EmitWorkStarted(context.WithoutCancel(ctx), ref,
			req.ParentMessageID, activity, req.ToolCallID)
	}
	close(j.release)
	if !backgrounded {
		if derr := s.lifecycle.Discard(context.WithoutCancel(ctx), ref, id); derr != nil {
			logger.Warnf(ctx, "[LocalShellJob] discard id=%s: %v", id, derr)
		}
	}
	result, serr := s.Status(ctx, ref, []string{id}, interfaces.StatusOptions{
		Detailed: true, RunID: req.RunID,
	})
	if serr != nil {
		return tools.ShellJobSubmission{}, serr
	}
	return tools.ShellJobSubmission{Result: result[0], Background: backgrounded}, nil
}

// backgroundedAfterYield reports whether the yield wait ended while the
// command was still running. done is closed when the watcher has persisted a
// terminal status. stop is the submitter's context. When done and another
// channel are both ready, select would choose at random and a finished
// command would also be reported as background work.
func backgroundedAfterYield(done, stop <-chan struct{}, timer <-chan time.Time) bool {
	select {
	case <-done:
		return false
	case <-timer:
	case <-stop:
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

func localDrainAndReap(proc localsandbox.Process) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.Discard, proc.Stdout())
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.Discard, proc.Stderr())
	}()
	wg.Wait()
	_, _ = proc.Wait(context.Background())
}

// requestStop records why the watcher should kill the child. lost wins over
// cancelled so a shutdown cannot be rewritten as a user cancel.
func (j *localJob) requestStop(cause string) {
	j.mu.Lock()
	if j.cause == "lost" || (j.cause != "" && cause != "lost") {
		j.mu.Unlock()
		return
	}
	j.cause = cause
	j.mu.Unlock()
	select {
	case j.requests <- cause:
	default:
	}
}

func (j *localJob) killProc() {
	j.killOnce.Do(func() {
		if j.proc != nil {
			_ = j.proc.Kill()
		}
	})
}

func (s *LocalShellJobService) watch(j *localJob, out, errLog *localJobLog, onOutput func(string, []byte)) {
	defer close(j.reported)
	defer func() { _ = out.Close() }()
	defer func() { _ = errLog.Close() }()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.copyStream(j, j.proc.Stdout(), out, "stdout", onOutput)
	}()
	go func() {
		defer wg.Done()
		s.copyStream(j, j.proc.Stderr(), errLog, "stderr", onOutput)
	}()
	copiesDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(copiesDone)
	}()
	exitCh := make(chan localsandbox.ExitStatus, 1)
	go func(drained <-chan struct{}) {
		// exec.Cmd.Wait closes StdoutPipe/StderrPipe. Drain both first, even
		// when forwarding output is slower than the command. The select below
		// continues to handle cancellation and renew the lease while draining.
		<-drained
		// Wait's ctx is the application ctx. Cancelling it kills a real process
		// tree; the submitting run's ctx is never passed here.
		st, _ := j.proc.Wait(s.appCtx)
		exitCh <- st
	}(copiesDone)

	var exit localsandbox.ExitStatus
	copiesClosed := false
	waitClosed := false
	appDone := s.appCtx.Done()
	ticker := time.NewTicker(localShellJobHeartbeat)
	defer ticker.Stop()
	for !copiesClosed || !waitClosed {
		select {
		case <-copiesDone:
			copiesClosed = true
			copiesDone = nil
		case st := <-exitCh:
			exit = st
			waitClosed = true
			exitCh = nil
		case <-j.requests:
			j.killProc()
		case <-appDone:
			appDone = nil
			j.requestStop("lost")
			j.killProc()
		case <-ticker.C:
			s.heartbeat(j)
		}
	}
	s.writeTerminal(j, exit)
	close(j.done)
	backgrounded := <-j.decision
	<-j.release
	if !backgrounded {
		return
	}
	s.flushPreview(j, true)
	s.report(j)
}

func (s *LocalShellJobService) copyStream(
	j *localJob, r io.Reader, log *localJobLog, stream string, onOutput func(string, []byte),
) {
	if r == nil {
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := log.Write(buf[:n]); werr != nil {
				logger.Warnf(s.appCtx, "[LocalShellJob] write %s id=%s: %v", stream, j.meta.ID, werr)
			}
			body := append([]byte(nil), buf[:n]...)
			s.emitPreview(j, stream, body, onOutput)
		}
		if err != nil {
			return
		}
	}
}

// emitPreview forwards yield-window bytes to the submitter and, once the job
// has been backgrounded, publishes a throttled 8 KiB tail.
func (s *LocalShellJobService) emitPreview(
	j *localJob, stream string, chunk []byte, onOutput func(string, []byte),
) {
	if len(chunk) == 0 {
		return
	}
	j.mu.Lock()
	j.preview = appendPreview(j.preview, string(chunk), localPreviewBytes)
	backgrounded := j.background
	due := j.lastPreview.IsZero() || time.Since(j.lastPreview) >= localPreviewEvery
	output := j.preview
	if backgrounded && due {
		j.lastPreview = time.Now()
		j.dirty = false
	} else if backgrounded {
		j.dirty = true
	}
	data := event.CommandOutputData{
		ToolCallID: j.meta.ToolCallID, Command: j.meta.Command,
		StartedAt: j.status.StartedAt, Output: output,
	}
	ref := localRef(j.meta)
	parent := j.meta.ParentMessageID
	id := j.meta.ID
	j.mu.Unlock()
	if !backgrounded {
		if onOutput != nil {
			onOutput(stream, chunk)
		}
		return
	}
	if !due || s.events == nil {
		return
	}
	if err := s.events.EmitCommandOutput(s.appCtx, ref, parent, id, data); err != nil {
		logger.Warnf(s.appCtx, "[LocalShellJob] preview id=%s: %v", id, err)
	}
}

func (s *LocalShellJobService) flushPreview(j *localJob, done bool) {
	if s.events == nil {
		return
	}
	j.mu.Lock()
	if !j.dirty && !done {
		j.mu.Unlock()
		return
	}
	j.dirty = false
	j.lastPreview = time.Now()
	data := event.CommandOutputData{
		ToolCallID: j.meta.ToolCallID, Command: j.meta.Command,
		StartedAt: j.status.StartedAt, Output: j.preview, Done: done,
	}
	ref := localRef(j.meta)
	parent := j.meta.ParentMessageID
	id := j.meta.ID
	j.mu.Unlock()
	if err := s.events.EmitCommandOutput(s.appCtx, ref, parent, id, data); err != nil {
		logger.Warnf(s.appCtx, "[LocalShellJob] preview id=%s: %v", id, err)
	}
}

func appendPreview(existing, chunk string, limit int) string {
	next := existing + chunk
	if len(next) <= limit {
		return next
	}
	next = next[len(next)-limit:]
	for len(next) > 0 && !utf8.RuneStart(next[0]) {
		next = next[1:]
	}
	return strings.ToValidUTF8(next, "")
}

func (s *LocalShellJobService) heartbeat(j *localJob) {
	if s.appCtx.Err() != nil || jobTerminal(j) {
		return
	}
	ref := localRef(j.meta)
	found, err := s.lifecycle.Alive(s.appCtx, ref, j.meta.ID, localShellJobLease)
	if err != nil {
		logger.Warnf(s.appCtx, "[LocalShellJob] renew id=%s: %v", j.meta.ID, err)
		return
	}
	if found {
		return
	}
	j.mu.Lock()
	started := j.status.StartedAt
	j.mu.Unlock()
	if started.IsZero() {
		started = j.meta.CreatedAt
	}
	activity := interfaces.BackgroundActivity{
		ID: j.meta.ID, Kind: "shell", Title: j.meta.Title, StartedAt: started,
	}
	if err := s.bg.Registry.Add(s.appCtx, ref, activity, localShellJobLease, 0); err != nil {
		logger.Warnf(s.appCtx, "[LocalShellJob] re-add id=%s: %v", j.meta.ID, err)
	}
}

func jobTerminal(j *localJob) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return interfaces.IsTerminalWorkState(j.status.State)
}

func (s *LocalShellJobService) writeTerminal(j *localJob, exit localsandbox.ExitStatus) {
	j.mu.Lock()
	status := j.status
	cause := j.cause
	j.mu.Unlock()
	ended := time.Now().UTC()
	status.EndedAt = &ended
	switch {
	case cause == "lost":
		status.State = interfaces.WorkStateLost
		status.ExitCode = nil
	case cause == "cancelled" || exit.Killed:
		status.State = interfaces.WorkStateCancelled
		code := exit.Code
		if code == 0 {
			code = 143
		}
		status.ExitCode = &code
	case exit.Code == 0:
		status.State = interfaces.WorkStateSucceeded
		code := 0
		status.ExitCode = &code
	default:
		status.State = interfaces.WorkStateFailed
		code := exit.Code
		status.ExitCode = &code
	}
	path := filepath.Join(s.root, j.meta.ID, "status.json")
	if err := sandbox.WriteShellJobJSON(path, status); err != nil {
		logger.Warnf(s.appCtx, "[LocalShellJob] status id=%s: %v", j.meta.ID, err)
	}
	j.mu.Lock()
	j.status = status
	j.mu.Unlock()
}

func (s *LocalShellJobService) report(j *localJob) {
	j.reportMu.Lock()
	defer j.reportMu.Unlock()
	if j.suppress {
		return
	}
	snap := s.snapshot(j)
	result := applyHostOutputFiles(ShellJobResult(snap, true), snap.Meta)
	if result.ID == "" {
		result.ID = j.meta.ID
	}
	s.skillCapture.settle(context.Background(), localRef(j.meta), j.meta.ID, result.State)
	notice := shellJobNotice(j.meta.ID, snap, result.State, result.OutputFiles)
	ctx := context.WithoutCancel(s.appCtx)
	if err := s.lifecycle.Finish(ctx, localRef(j.meta), notice, result, j.meta.ParentMessageID); err != nil {
		logger.Warnf(ctx, "[LocalShellJob] finish id=%s: %v", j.meta.ID, err)
	}
}

func (s *LocalShellJobService) snapshot(j *localJob) sandbox.ShellJobSnapshot {
	snap, err := sandbox.ReadShellJob(s.root, sandbox.ShellJobRead{ID: j.meta.ID})
	if err != nil || snap.Meta.ID == "" {
		snap.Meta = j.meta
	}
	if err != nil || snap.Status.State == "" {
		j.mu.Lock()
		snap.Status = j.status
		j.mu.Unlock()
	}
	return snap
}

// Status returns one result per id. An empty RunID reads head plus tail
// and does not move the submitting run's cursor.
func (s *LocalShellJobService) Status(
	_ context.Context, ref interfaces.SessionRef, ids []string, opts interfaces.StatusOptions,
) ([]interfaces.WorkResult, error) {
	if err := validateShellJobIDs(ids); err != nil {
		return nil, err
	}
	out := make([]interfaces.WorkResult, len(ids))
	for i, id := range ids {
		j, err := s.ownedJob(ref, id)
		if err != nil {
			out[i] = lostResult(sandbox.ShellJobMeta{ID: id}, "job not found")
			continue
		}
		if !opts.Detailed {
			out[i] = s.memoryResult(j)
			continue
		}
		from := s.cursorFrom(ref, opts.RunID, id)
		snap, err := sandbox.ReadShellJob(s.root, sandbox.ShellJobRead{
			ID: id, From: from, OutputBudget: opts.OutputBudget,
		})
		if err != nil || snap.Missing || belongsToOtherSession(snap.Meta, ref) {
			out[i] = lostResult(sandbox.ShellJobMeta{ID: id}, "job unreadable")
			continue
		}
		if snap.Meta.ID == "" {
			snap.Meta = j.meta
		}
		result := applyHostOutputFiles(ShellJobResult(snap, true), snap.Meta)
		if result.ID == "" {
			result.ID = id
		}
		s.noteObservedCursor(ref, opts.RunID, j.originRun, id, snap)
		out[i] = result
	}
	return out, nil
}

func (s *LocalShellJobService) memoryResult(j *localJob) interfaces.WorkResult {
	j.mu.Lock()
	defer j.mu.Unlock()
	state := j.status.State
	if state == "" {
		state = interfaces.WorkStateRunning
	}
	return interfaces.WorkResult{
		ID: j.meta.ID, State: state, Title: j.meta.Title,
		Terminal: interfaces.IsTerminalWorkState(state),
	}
}

func (s *LocalShellJobService) ownedJob(ref interfaces.SessionRef, id string) (*localJob, error) {
	s.mu.Lock()
	j := s.jobs[id]
	s.mu.Unlock()
	if j == nil || j.meta.TenantID != ref.TenantID || j.meta.SessionID != ref.SessionID {
		return nil, interfaces.ErrWorkNotFound
	}
	return j, nil
}

// Cancel asks the watcher to kill each id. The returned state is whatever was
// recorded at this moment; the terminal state lands in the notice.
func (s *LocalShellJobService) Cancel(
	_ context.Context, ref interfaces.SessionRef, ids []string,
) ([]interfaces.WorkResult, error) {
	if err := validateShellJobIDs(ids); err != nil {
		return nil, err
	}
	out := make([]interfaces.WorkResult, len(ids))
	for i, id := range ids {
		j, err := s.ownedJob(ref, id)
		if err != nil {
			out[i] = lostResult(sandbox.ShellJobMeta{ID: id}, "job not found")
			continue
		}
		j.requestStop("cancelled")
		out[i] = s.memoryResult(j)
	}
	return out, nil
}

// OnDeleteAborted reopens a session whose repository deletion failed.
func (s *LocalShellJobService) OnDeleteAborted(_ context.Context, ref interfaces.SessionRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blocked, ref)
	return nil
}

// OnDelete refuses later submits for the session, suppresses a late notice,
// and cancels every live job. The shared listener clears the inbox afterwards.
func (s *LocalShellJobService) OnDelete(ctx context.Context, ref interfaces.SessionRef) error {
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	s.mu.Lock()
	s.blocked[ref] = true
	s.skillCapture.dropSession(ref)
	jobs := s.sessionJobsLocked(ref)
	s.dropSessionCursorsLocked(ref)
	s.mu.Unlock()
	for _, j := range jobs {
		j.reportMu.Lock()
		j.suppress = true
		j.reportMu.Unlock()
		j.requestStop("cancelled")
	}
	return s.waitReported(ctx, jobs)
}

// Shutdown kills every live child and records lost. A second call is a no-op.
// The wait is bounded by ctx, or by 5s when ctx has no deadline.
func (s *LocalShellJobService) Shutdown(ctx context.Context) error {
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	jobs := make([]*localJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	s.mu.Unlock()
	s.cancel()
	for _, j := range jobs {
		j.reportMu.Lock()
		j.suppress = true
		j.reportMu.Unlock()
		j.requestStop("lost")
	}
	return s.waitReported(ctx, jobs)
}

// EndShellJobRun keeps the run's output cursors for one hour after the run
// ends, rather than for one hour after the last read.
func (s *LocalShellJobService) EndShellJobRun(ref interfaces.SessionRef, runID string) {
	if s == nil || runID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := fmt.Sprintf("%d\x00%s\x00%s\x00", ref.TenantID, ref.SessionID, runID)
	exp := time.Now().Add(time.Hour)
	for key, cursor := range s.cursors {
		if strings.HasPrefix(key, prefix) {
			cursor.expires = exp
			s.cursors[key] = cursor
		}
	}
}

func (s *LocalShellJobService) sessionJobsLocked(ref interfaces.SessionRef) []*localJob {
	out := make([]*localJob, 0)
	for _, j := range s.jobs {
		if j.meta.TenantID == ref.TenantID && j.meta.SessionID == ref.SessionID {
			out = append(out, j)
		}
	}
	return out
}

func (s *LocalShellJobService) dropSessionCursorsLocked(ref interfaces.SessionRef) {
	prefix := fmt.Sprintf("%d\x00%s\x00", ref.TenantID, ref.SessionID)
	for key := range s.cursors {
		if strings.HasPrefix(key, prefix) {
			delete(s.cursors, key)
		}
	}
}

func (s *LocalShellJobService) waitReported(ctx context.Context, jobs []*localJob) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, localShellJobStopWait)
		defer cancel()
	}
	for _, j := range jobs {
		if j == nil || j.reported == nil {
			continue
		}
		select {
		case <-j.reported:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func localCursorKey(ref interfaces.SessionRef, runID, workID string) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s", ref.TenantID, ref.SessionID, runID, workID)
}

func (s *LocalShellJobService) cursorFrom(
	ref interfaces.SessionRef, runID, id string,
) *sandbox.ShellJobOffsets {
	if s == nil || runID == "" || id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneCursorsLocked(time.Now())
	cursor, ok := s.cursors[localCursorKey(ref, runID, id)]
	if !ok {
		return nil
	}
	from := cursor.from
	return &from
}

// noteObservedCursor moves a run cursor to the end of the bytes just observed.
// An empty RunID is an out-of-run read (HTTP detail, chip "view output") and
// must not move any run's cursor.
func (s *LocalShellJobService) noteObservedCursor(
	ref interfaces.SessionRef, runID, originRun, id string, snap sandbox.ShellJobSnapshot,
) {
	if runID == "" || id == "" || snap.Missing {
		return
	}
	_ = originRun
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneCursorsLocked(time.Now())
	s.cursors[localCursorKey(ref, runID, id)] = localCursor{
		from: sandbox.ShellJobOffsets{
			Stdout: nextStreamOffset(snap.Stdout),
			Stderr: nextStreamOffset(snap.Stderr),
		},
		expires: time.Now().Add(time.Hour),
	}
}

func (s *LocalShellJobService) pruneCursorsLocked(now time.Time) {
	for key, cursor := range s.cursors {
		if !cursor.expires.After(now) {
			delete(s.cursors, key)
		}
	}
}

func applyHostOutputFiles(result interfaces.WorkResult, meta sandbox.ShellJobMeta) interfaces.WorkResult {
	if !result.Terminal || !meta.OutputSeen || meta.OutputDir == "" {
		return result
	}
	after, err := listHostOutputFiles(meta.OutputDir)
	if err != nil {
		return result
	}
	links := tools.ChangedOutputFileLinks(meta.OutputDir, sandbox.EntriesFromStamps(meta.OutputBefore), after)
	result.OutputFiles = links
	if payload, ok := result.Payload.(ShellJobPayload); ok {
		payload.OutputFiles = links
		result.Payload = payload
	}
	return result
}

func listHostOutputFiles(dir string) ([]sandbox.RemoteDirEntry, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("output dir is empty")
	}
	var out []sandbox.RemoteDirEntry
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if p == dir && os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if len(out) > 200 {
			return errors.New("output dir listing is too large")
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		out = append(out, sandbox.RemoteDirEntry{
			Name: d.Name(), Path: p, Type: sandbox.RemoteEntryFile,
			Size: info.Size(), ModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return out, nil
}
