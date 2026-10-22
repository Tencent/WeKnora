package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	shellJobYieldDefault = 10 * time.Second
	shellJobYieldFloor   = 250 * time.Millisecond
	shellJobYieldMax     = 30 * time.Second
	shellJobKindCap      = 64
	// shellJobRenewLease outlives several 5s sweeper ticks so one missed round
	// does not drop rewind / rebuild protection.
	shellJobRenewLease = 30 * time.Second
	shellJobHandoff    = 5 * time.Second
	shellJobIDAttempts = 8
)

const (
	shellJobNoteRunning = "Still running. Keep working on other parts of the task; " +
		"you will be told when it finishes. " +
		"Call work_wait only when you cannot continue without its output. work_cancel stops it. " +
		"It shares the workspace with your other tools: do not edit files it is writing. " +
		"Cancel it once you no longer need it. " +
		"Do not pipe the command through tail or head: the job log stays empty until that pipe closes."
	shellJobNoteRunningLite = " It runs in the user's project directory and stops if WeKnora Lite is closed. " +
		"Other chats on the same project can still write to this directory."
	shellJobNoteLost = "The sandbox was replaced or the job's watcher died, so the result is unknown. " +
		"Re-run it, or use shell_exec if it is short."
	shellJobNoteLostLite = "WeKnora Lite was closed while it was running."
)

// LocalShellJobs is the Lite host implementation contract. The remote service
// delegates to it for host sessions.
type LocalShellJobs interface {
	SubmitShellJob(
		context.Context, interfaces.SessionRef, tools.ShellJobSubmitRequest,
	) (tools.ShellJobSubmission, error)
	Status(context.Context, interfaces.SessionRef, []string, interfaces.StatusOptions) ([]interfaces.WorkResult, error)
	Cancel(context.Context, interfaces.SessionRef, []string) ([]interfaces.WorkResult, error)
	OnDelete(context.Context, interfaces.SessionRef) error
	Shutdown(context.Context) error
	EndShellJobRun(interfaces.SessionRef, string)
}

// ShellJobPayload is the model- and UI-facing body of one shell job result.
type ShellJobPayload struct {
	DisplayType  string                  `json:"display_type"`
	WorkID       string                  `json:"work_id"`
	State        string                  `json:"state"`
	SessionID    string                  `json:"session_id"`
	Command      string                  `json:"command"`
	WorkDir      string                  `json:"work_dir"`
	ExitCode     *int                    `json:"exit_code,omitempty"`
	DurationMS   int64                   `json:"duration_ms"`
	StartedAt    time.Time               `json:"started_at"`
	Stdout       string                  `json:"stdout"`
	Stderr       string                  `json:"stderr"`
	FromBytes    sandbox.ShellJobOffsets `json:"from_bytes"`
	TotalBytes   sandbox.ShellJobOffsets `json:"total_bytes"`
	OmittedBytes sandbox.ShellJobOffsets `json:"omitted_bytes"`
	Truncated    bool                    `json:"truncated"`
	Note         string                  `json:"note,omitempty"`
	OutputFiles  []string                `json:"output_files,omitempty"`
}

// ShellJobService runs remote shell jobs: submit, status, cancel, and the
// per-replica sweeper.
type ShellJobService struct {
	pinned *PinnedSessionSandbox
	bg     *BackgroundWork
	index  *ShellJobIndex
	events *BackgroundEventEmitter
	local  LocalShellJobs
	// keeper is the same value stored on lifecycle.KeepAlive. E2B exec does
	// not extend the provider timer, so submit and the sweeper both need it.
	// A nil keeper fails SubmitShellJob before anything is registered.
	keeper    interfaces.SessionKeepAliver
	lifecycle background.Lifecycle
	// remoteRead, remoteExec and boundSandbox are the seams tests replace.
	// Production installs them from pinned. The command is a separate argument
	// of remoteExec; ShellExecOptions has no Command field.
	newJobID          func() (string, error)
	heartbeatInterval time.Duration
	remoteRead        remoteReadFunc
	remoteExec        remoteExecFunc
	boundSandbox      boundSandboxFunc
	// listFiles overrides output-directory listing in tests.
	listFiles    func(context.Context, string, string) ([]sandbox.RemoteDirEntry, error)
	skillCapture skillEnvCaptures
	replica      string
	swept        sync.Mutex
	mu           sync.Mutex
	cursors      map[string]shellJobCursor // key: tenant\0session\0run\0work
}

type shellJobCursor struct {
	from    sandbox.ShellJobOffsets
	expires time.Time
}

// remoteRead returns one snapshot per id, in order. errs carries per-id
// failures. Preserve typed errors so callers can distinguish confirmed loss
// from a transport failure without acknowledging an unread completion.
type remoteReadFunc func(ctx context.Context, s interfaces.SessionRef,
	ids []string, sandboxIDs map[string]string) ([]sandbox.ShellJobSnapshot, map[string]error)

type remoteExecFunc func(ctx context.Context, sessionID string, command string,
	opts sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error)

type boundSandboxFunc func(ctx context.Context, sessionID string) (string, bool)

// shellJobLease covers the yield window plus the 10s exec slack and a few
// seconds for the sweeper to take over. Begin's own keep-alive runs only
// after start returns, so the lease has to outlive that wait.
func shellJobLease(yield time.Duration) time.Duration {
	return yield + 15*time.Second
}

var (
	_ interfaces.WorkHandler              = (*ShellJobService)(nil)
	_ interfaces.SessionLifecycleListener = (*ShellJobService)(nil)
)

// NewShellJobService wires the remote shell-job service. keeper is the only
// way a keep-aliver enters: BackgroundWork has no KeepAliver field. events
// is stored on the lifecycle only when the pointer is non-nil, so a typed
// nil never sits in the interface.
func NewShellJobService(pinned *PinnedSessionSandbox, bg *BackgroundWork,
	index *ShellJobIndex, events *BackgroundEventEmitter,
	keeper interfaces.SessionKeepAliver,
) *ShellJobService {
	svc := &ShellJobService{
		pinned:            pinned,
		bg:                bg,
		index:             index,
		events:            events,
		keeper:            keeper,
		replica:           fmt.Sprintf("%s-%d", hostname(), osGetpid()),
		cursors:           make(map[string]shellJobCursor),
		newJobID:          sandbox.NewShellJobID,
		heartbeatInterval: 5 * time.Second,
	}
	if bg != nil {
		svc.lifecycle = background.Lifecycle{
			Registry:  bg.Registry,
			Inbox:     bg.Inbox,
			KeepAlive: keeper,
			Logf:      logger.Warnf,
		}
		if events != nil {
			svc.lifecycle.Events = events
		}
		if bg.Listeners != nil {
			bg.Listeners.Register(svc)
		}
	}
	if pinned != nil {
		svc.remoteRead = svc.batchReadViaPinned
		svc.remoteExec = func(ctx context.Context, sessionID, command string,
			opts sandbox.ShellExecOptions,
		) (*sandbox.ExecuteResult, error) {
			return pinned.ExecShellCommandWithOptions(ctx, sessionID, command, opts)
		}
		svc.boundSandbox = pinned.BoundSandboxID
	}
	return svc
}

// WithLocal installs the host implementation used for Lite sessions.
func (s *ShellJobService) WithLocal(local LocalShellJobs) *ShellJobService {
	if s == nil {
		return nil
	}
	s.local = local
	return s
}

// RemoteAvailable reports whether a remote submit can run. An injected
// remoteExec counts: tests construct the service with a nil pin and then
// install the seam.
func (s *ShellJobService) RemoteAvailable() bool {
	return s != nil && s.index != nil && (s.pinned != nil || s.remoteExec != nil)
}

// LocalAvailable reports whether a host implementation is installed.
func (s *ShellJobService) LocalAvailable() bool { return s != nil && s.local != nil }

// SubmitShellJob starts a remote shell job, or delegates to the host
// implementation when this session lives on the user's machine.
func (s *ShellJobService) SubmitShellJob(ctx context.Context, ref interfaces.SessionRef,
	req tools.ShellJobSubmitRequest,
) (tools.ShellJobSubmission, error) {
	if s.local != nil && s.onHostSession(ctx, ref) {
		return s.local.SubmitShellJob(ctx, ref, req)
	}
	if !s.RemoteAvailable() {
		return tools.ShellJobSubmission{}, fmt.Errorf("remote shell jobs are unavailable")
	}
	var result tools.ShellJobSubmission
	err := s.index.WithSessionLock(ctx, ref, func(lockCtx context.Context) error {
		closed, err := s.index.SessionClosed(lockCtx, ref)
		if err != nil {
			return err
		}
		if closed {
			return interfaces.ErrWorkNotFound
		}
		submitCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(lockCtx, cancel)
		defer stop()
		defer cancel()
		result, err = s.submitRemoteShellJob(submitCtx, ref, req)
		return err
	})
	return result, err
}

func (s *ShellJobService) submitRemoteShellJob(ctx context.Context, ref interfaces.SessionRef,
	req tools.ShellJobSubmitRequest,
) (tools.ShellJobSubmission, error) {
	// E2B exec does not extend the provider timer. Missing wiring has to fail
	// here, before Begin, or the sandbox pauses and the job shows up later as lost.
	if s.keeper == nil {
		return tools.ShellJobSubmission{}, errors.New("shell job service has no keep-aliver wired")
	}
	yield := clampYieldTime(req.YieldTime)
	now := time.Now().UTC()
	bindCtx, bindSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
		Name:  "shelljob.ensure_bound_sandbox",
		Input: map[string]interface{}{"session_id": ref.SessionID},
	})
	sandboxID, err := s.ensureBoundSandbox(bindCtx, ref)
	bindSpan.Finish(map[string]interface{}{"sandbox_id": sandboxID}, nil, err)
	if err != nil {
		return tools.ShellJobSubmission{}, err
	}
	docker := false
	if s.pinned != nil {
		dockerCtx, dockerSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
			Name:  "shelljob.backend_check",
			Input: map[string]interface{}{"session_id": ref.SessionID},
		})
		docker = s.pinned.BackendIsDocker(maintenanceCtx(dockerCtx, ref), ref.SessionID)
		dockerSpan.Finish(map[string]interface{}{"docker": docker}, nil, nil)
	}
	var (
		jobID     string
		meta      sandbox.ShellJobMeta
		activity  interfaces.BackgroundActivity
		preview   shellJobPreview
		forwarded sandbox.ShellJobOffsets
		finished  *tools.ShellJobSubmission
		degraded  bool
		// unparsed records that the submit exec ended without its epilogue: the
		// marker line never reached the stream, so the job's state at the end of
		// the yield window is unknown.
		unparsed bool
	)
	for range shellJobIDAttempts {
		preview = shellJobPreview{}
		forwarded = sandbox.ShellJobOffsets{}
		finished = nil
		degraded = false
		unparsed = false
		jobID, err = s.newJobID()
		if err != nil {
			return tools.ShellJobSubmission{}, err
		}
		reserved, reserveErr := s.index.ReserveID(ctx, ref, jobID)
		if reserveErr != nil {
			return tools.ShellJobSubmission{}, reserveErr
		}
		if !reserved {
			err = errShellJobIDTaken
			continue
		}
		meta = sandbox.ShellJobMeta{
			ID: jobID, TenantID: ref.TenantID, SandboxTenantID: ref.TenantID,
			SessionID: ref.SessionID, Command: req.MaskedCommand(),
			WorkDir: req.WorkDir, SandboxID: sandboxID, CreatedAt: now,
			YieldUntil: now.Add(yield), ParentMessageID: req.ParentMessageID,
			ToolCallID: req.ToolCallID, Title: req.Description,
			OutputDir: req.OutputDir, OutputSeen: req.OutputSeen, OutputBefore: req.OutputBefore,
		}
		script, marker := sandbox.ShellJobSubmitScriptWithBackend(meta, req.Command, int(yield.Milliseconds()), docker)
		activity = interfaces.BackgroundActivity{
			ID: jobID, Kind: "shell", Title: req.Description, StartedAt: now,
		}
		stream := sandbox.NewShellJobMarkerStream(marker)
		beginCtx, beginSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
			Name: "shelljob.lifecycle_begin",
			Input: map[string]interface{}{
				"work_id": jobID, "yield_ms": yield.Milliseconds(),
			},
		})
		err = s.lifecycle.Begin(beginCtx, ref, activity, shellJobLease(yield), shellJobKindCap,
			func(ctx context.Context) error {
				idxCtx, idxSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
					Name:  "shelljob.index_add",
					Input: map[string]interface{}{"work_id": jobID, "sandbox_id": sandboxID},
				})
				err := s.index.Add(idxCtx, ShellJobIndexEntry{
					SessionRef: ref, WorkID: jobID, SandboxID: sandboxID, YieldUntil: meta.YieldUntil,
				})
				idxSpan.Finish(nil, nil, err)
				if err != nil {
					return err
				}
				// Begin's own keep-alive runs only after start returns. start blocks
				// for the whole yield window, so the sandbox has to be refreshed now.
				keepCtx, keepSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
					Name:  "shelljob.keep_alive",
					Input: map[string]interface{}{"session_id": ref.SessionID},
				})
				err = s.keeper.KeepAlive(keepCtx, ref)
				keepSpan.Finish(nil, nil, err)
				if err != nil {
					logger.Warnf(ctx, "[ShellJob] keep-alive at submit id=%s: %v", jobID, err)
				}
				execCtx, execSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
					Name: "shelljob.submit_exec",
					Input: map[string]interface{}{
						"work_id": jobID, "yield_ms": yield.Milliseconds(),
						"command": langfuse.TruncateRunes(script, 256),
					},
				})
				var sawStdout, sawStderr atomic.Bool
				execRes, err := s.remoteExec(execCtx, ref.SessionID, script, sandbox.ShellExecOptions{
					// No WorkDir: shared machines admit only the session layout
					// root as cwd, and the manager's empty default is exactly
					// that root. The script uses absolute paths, so it does not
					// care where it starts.
					ExpectedSandboxID: sandboxID,
					Timeout:           yield + 10*time.Second,
					Env:               req.Env,
					OnOutput: func(streamName string, chunk []byte) {
						// stderr is never part of the epilogue. A line that
						// looks like "1 2" would otherwise replace the offsets.
						if streamName == "stderr" {
							sawStderr.Store(true)
							if len(chunk) == 0 {
								return
							}
							preview.add("stderr", chunk)
							if req.OnOutput != nil {
								req.OnOutput("stderr", chunk)
							}
							return
						}
						sawStdout.Store(true)
						body, _ := stream.Feed(chunk)
						if len(body) == 0 {
							return
						}
						preview.add("stdout", body)
						if req.OnOutput != nil {
							req.OnOutput("stdout", body)
						}
					},
				})
				execOut := map[string]interface{}{}
				if execRes != nil {
					execOut["exit_code"] = execRes.ExitCode
					execOut["killed"] = execRes.Killed
				}
				execSpan.Finish(execOut, nil, err)
				// A Go error means the wrapper never started (lookup, prepare, or
				// an early cancel). Roll the index member back so Begin can drop
				// the registration without leaving a sweeper ghost.
				if err != nil {
					if _, rmErr := s.index.ClaimWork(context.WithoutCancel(ctx), ref, jobID); rmErr != nil {
						logger.Warnf(ctx, "[ShellJob] rollback index after pre-launch failure id=%s: %v", jobID, rmErr)
					}
					return err
				}
				if execRes != nil && execRes.ExitCode == sandbox.ShellJobDirExistsExit {
					if _, rmErr := s.index.ClaimWork(context.WithoutCancel(ctx), ref, jobID); rmErr != nil {
						logger.Warnf(ctx, "[ShellJob] rollback index after id collision id=%s: %v", jobID, rmErr)
					}
					return errShellJobIDTaken
				}
				// A nil Go error with Error or Killed means the exec was cut after
				// the wrapper may already be running. Keep the registration and the
				// index entry. Do not read status: the caller context may already
				// be canceled, and a miss would look like lost.
				if execRes != nil && (execRes.Error != "" || execRes.Killed) {
					logger.Warnf(ctx, "[ShellJob] submit exec degraded id=%s: %s", jobID, execRes.Error)
					degraded = true
					return nil
				}
				// Cube buffers the whole command and does not call OnOutput.
				// Feed that buffer once. Docker and E2B already called it, so
				// feeding the same bytes again would duplicate the epilogue.
				if !sawStdout.Load() && execRes != nil && execRes.Stdout != "" {
					body, _ := stream.Feed([]byte(execRes.Stdout))
					if len(body) > 0 {
						preview.add("stdout", body)
						if req.OnOutput != nil {
							req.OnOutput("stdout", body)
						}
					}
				}
				if !sawStderr.Load() && execRes != nil && execRes.Stderr != "" {
					chunk := []byte(execRes.Stderr)
					preview.add("stderr", chunk)
					if req.OnOutput != nil {
						req.OnOutput("stderr", chunk)
					}
				}
				_, epilogue := stream.Final()
				status, offsets, parsed := sandbox.ParseShellJobEpilogue(epilogue)
				if !parsed {
					// The marker or the final status was lost. Re-check the state
					// before registering: a job that already finished must be
					// answered here, or the sweeper reports it a second time as a
					// completion notice after the model already has its output.
					unparsed = true
					logger.Warnf(ctx, "[ShellJob] submit epilogue missing id=%s", jobID)
					return nil
				}
				forwarded = offsets
				if status.State != interfaces.WorkStateRunning {
					sub := tools.ShellJobSubmission{
						Result:     shellTerminalResult(meta, status, offsets, &preview, false),
						Background: false,
					}
					finished = &sub
				}
				return nil
			})
		beginSpan.Finish(map[string]interface{}{
			"finished": finished != nil, "degraded": degraded, "unparsed": unparsed,
		}, nil, err)
		if errors.Is(err, errShellJobIDTaken) {
			continue
		}
		break
	}
	if err != nil {
		return tools.ShellJobSubmission{}, err
	}
	if finished != nil {
		if forwarded.Stdout > preview.stdout.total || forwarded.Stderr > preview.stderr.total {
			results, readErr := s.Status(ctx, ref, []string{jobID}, interfaces.StatusOptions{Detailed: true})
			if readErr == nil && len(results) == 1 && results[0].Terminal &&
				results[0].State != interfaces.WorkStateLost {
				finished.Result = results[0]
			}
		}
		finCtx, finSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
			Name:  "shelljob.finish_cleanup",
			Input: map[string]interface{}{"work_id": jobID},
		})
		removed, rmErr := s.index.ClaimWork(context.WithoutCancel(finCtx), ref, jobID)
		if rmErr != nil {
			logger.Warnf(ctx, "[ShellJob] drop finished index id=%s: %v", jobID, rmErr)
		} else if !removed {
			// The sweeper won the claim, so it may already have queued a notice
			// for a job whose result this call is also returning inline.
			logger.Warnf(ctx, "[ShellJob] finished job %s was already claimed", jobID)
		}
		if dErr := s.lifecycle.Discard(context.WithoutCancel(finCtx), ref, jobID); dErr != nil {
			logger.Warnf(ctx, "[ShellJob] discard id=%s: %v", jobID, dErr)
		}
		finished.Result = s.applyOutputFiles(finCtx, ref, finished.Result, meta)
		finSpan.Finish(map[string]interface{}{"removed": removed}, nil, rmErr)
		return *finished, nil
	}
	// The epilogue never arrived, so we do not know whether the job finished
	// inside the yield window. Registering a finished job as background makes
	// the sweeper deliver a completion notice for a result the model already
	// has; check the state once before committing to that path.
	if unparsed && !degraded {
		if submission, ok := s.finishedBeforeRegistering(ctx, ref, jobID, meta, &preview); ok {
			return submission, nil
		}
	}
	s.noteRunning(ctx, ref, req, activity, jobID, forwarded)
	if degraded {
		return runningSubmission(meta, &preview), nil
	}
	statusCtx, statusSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
		Name:  "shelljob.status",
		Input: map[string]interface{}{"work_id": jobID, "detailed": true},
	})
	first, err := s.Status(statusCtx, ref, []string{jobID}, interfaces.StatusOptions{
		Detailed: true, RunID: req.RunID,
	})
	statusSpan.Finish(map[string]interface{}{"results": len(first)}, nil, err)
	if err != nil {
		return runningSubmission(meta, &preview), nil
	}
	if len(first) == 0 {
		return tools.ShellJobSubmission{}, fmt.Errorf("shell job %s has no status", jobID)
	}
	first[0] = fillShellJobPreview(first[0], &preview)
	return tools.ShellJobSubmission{Result: first[0], Background: true}, nil
}

func (s *ShellJobService) noteRunning(ctx context.Context, ref interfaces.SessionRef,
	req tools.ShellJobSubmitRequest, activity interfaces.BackgroundActivity,
	jobID string, from sandbox.ShellJobOffsets,
) {
	if s.events != nil {
		if err := s.events.EmitWorkStarted(ctx, ref, req.ParentMessageID, activity, req.ToolCallID); err != nil {
			logger.Warnf(ctx, "[ShellJob] work_started id=%s: %v", jobID, err)
		}
	}
	s.mu.Lock()
	s.setCursorsLocked(ref, req.RunID, jobID, from)
	s.mu.Unlock()
}

func runningSubmission(meta sandbox.ShellJobMeta, preview *shellJobPreview) tools.ShellJobSubmission {
	snap := sandbox.ShellJobSnapshot{
		Meta: meta,
		Status: sandbox.ShellJobStatus{
			State: interfaces.WorkStateRunning, StartedAt: meta.CreatedAt,
		},
	}
	if preview != nil {
		snap.Stdout = preview.stdout.stream(preview.stdout.total)
		snap.Stderr = preview.stderr.stream(preview.stderr.total)
	}
	return tools.ShellJobSubmission{Result: ShellJobResult(snap, false), Background: true}
}

// finishedBeforeRegistering answers a job whose submit epilogue was lost but
// which has already reached a terminal state. It drops the index member and the
// activity registration so the sweeper cannot report the same job again as a
// completion notice for a result this call is returning inline.
//
// The read deliberately carries no RunID: this is not a run, and it must not
// move the incremental cursor a later work_wait relies on.
func (s *ShellJobService) finishedBeforeRegistering(
	ctx context.Context, ref interfaces.SessionRef, jobID string,
	meta sandbox.ShellJobMeta, preview *shellJobPreview,
) (tools.ShellJobSubmission, bool) {
	results, err := s.Status(ctx, ref, []string{jobID}, interfaces.StatusOptions{Detailed: true})
	if err != nil {
		logger.Warnf(ctx, "[ShellJob] check state before registering id=%s: %v", jobID, err)
		return tools.ShellJobSubmission{}, false
	}
	if len(results) != 1 || !interfaces.IsTerminalWorkState(results[0].State) {
		return tools.ShellJobSubmission{}, false
	}
	// Only a readable terminal record can produce an inline result. Leave an
	// absent record to the sweeper, which also owns process cleanup.
	if results[0].State == interfaces.WorkStateLost {
		return tools.ShellJobSubmission{}, false
	}
	result := fillShellJobPreview(results[0], preview)
	if removed, rmErr := s.index.ClaimWork(context.WithoutCancel(ctx), ref, jobID); rmErr != nil {
		logger.Warnf(ctx, "[ShellJob] drop finished index id=%s: %v", jobID, rmErr)
	} else if !removed {
		logger.Warnf(ctx, "[ShellJob] finished job %s was already claimed", jobID)
	}
	if dErr := s.lifecycle.Discard(context.WithoutCancel(ctx), ref, jobID); dErr != nil {
		logger.Warnf(ctx, "[ShellJob] discard id=%s: %v", jobID, dErr)
	}
	result = s.applyOutputFiles(ctx, ref, result, meta)
	return tools.ShellJobSubmission{Result: result, Background: false}, true
}

// shellJobPreview retains a bounded head and tail of the bytes forwarded
// during the yield window, keeping short jobs to a single exec.
type shellJobPreview struct {
	stdout shellPreviewStream
	stderr shellPreviewStream
}

type shellPreviewStream struct {
	head  []byte
	tail  []byte
	total int64
}

func (p *shellPreviewStream) add(body []byte, budget int) {
	p.total += int64(len(body))
	headN := min(len(body), budget/4-len(p.head))
	p.head = append(p.head, body[:headN]...)
	p.tail = append(p.tail, body[headN:]...)
	if len(p.tail) > budget-budget/4 {
		p.tail = append([]byte(nil), p.tail[len(p.tail)-(budget-budget/4):]...)
	}
}

func (p *shellPreviewStream) String() string {
	return strings.ToValidUTF8(string(p.head)+string(p.tail), "")
}

func (p *shellPreviewStream) stream(total int64) sandbox.ShellJobStream {
	omitted := max(0, total-int64(len(p.head)+len(p.tail)))
	return sandbox.ShellJobStream{
		Text: p.String(), TotalBytes: total,
		OmittedBytes: omitted, Truncated: omitted > 0,
	}
}

func (p *shellJobPreview) add(stream string, body []byte) {
	if stream == "stderr" {
		p.stderr.add(body, 8*1024)
		return
	}
	p.stdout.add(body, 16*1024)
}

func fillShellJobPreview(result interfaces.WorkResult, preview *shellJobPreview) interfaces.WorkResult {
	if preview == nil {
		return result
	}
	payload, ok := result.Payload.(ShellJobPayload)
	if !ok {
		return result
	}
	if payload.Stdout == "" {
		payload.Stdout = preview.stdout.String()
	}
	if payload.Stderr == "" {
		payload.Stderr = preview.stderr.String()
	}
	result.Payload = payload
	return result
}

// Status returns one result per id. Detailed=false never execs.
func (s *ShellJobService) Status(ctx context.Context, ref interfaces.SessionRef,
	ids []string, opts interfaces.StatusOptions,
) ([]interfaces.WorkResult, error) {
	if s.local != nil && s.onHostSession(ctx, ref) {
		return s.local.Status(ctx, ref, ids, opts)
	}
	if !s.RemoteAvailable() {
		return nil, interfaces.ErrWorkNotFound
	}
	if err := validateShellJobIDs(ids); err != nil {
		return nil, err
	}
	if !opts.Detailed {
		return s.cheapStatus(ctx, ref, ids)
	}
	if s.remoteRead == nil {
		return nil, fmt.Errorf("remote shell jobs are unavailable")
	}
	readCtx := context.WithValue(maintenanceCtx(ctx, ref), shellReadRunKey{}, opts.RunID)
	readCtx = context.WithValue(readCtx, shellReadBudgetKey{}, opts.OutputBudget)
	snapshots, errs := s.remoteRead(readCtx, ref, ids, s.sandboxFor(ctx, ref, ids))
	if len(snapshots) != len(ids) {
		return nil, fmt.Errorf("shell job batch read returned %d of %d", len(snapshots), len(ids))
	}
	for i, snap := range snapshots {
		if err := errs[ids[i]]; err != nil && !shellSandboxGone(err) {
			return nil, fmt.Errorf("read shell job %s: %w", ids[i], err)
		}
		if errs[ids[i]] == nil && !snap.Missing && !belongsToOtherSession(snap.Meta, ref) &&
			snap.Status.State != interfaces.WorkStateRunning && !interfaces.IsTerminalWorkState(snap.Status.State) {
			return nil, fmt.Errorf("read shell job %s: unreadable status", ids[i])
		}
	}
	out := make([]interfaces.WorkResult, len(ids))
	for i, snap := range snapshots {
		if errs != nil && errs[ids[i]] != nil {
			out[i] = lostResult(sandbox.ShellJobMeta{ID: ids[i]}, errs[ids[i]].Error())
			continue
		}
		if snap.Missing || belongsToOtherSession(snap.Meta, ref) {
			out[i] = lostResult(sandbox.ShellJobMeta{ID: ids[i]}, "job directory missing or belongs to another session")
			continue
		}
		out[i] = s.applyOutputFiles(ctx, ref, ShellJobResult(snap, false), snap.Meta)
		if out[i].ID == "" {
			out[i].ID = ids[i]
		}
		s.noteReadCursor(ref, opts.RunID, ids[i], snap)
	}
	return out, nil
}

// cheapStatus is the work_wait poll source: one result per id, unknown when
// neither the registry nor the inbox can decide. A bad id is the only whole-call error.
func (s *ShellJobService) cheapStatus(ctx context.Context, ref interfaces.SessionRef,
	ids []string,
) ([]interfaces.WorkResult, error) {
	if err := validateShellJobIDs(ids); err != nil {
		return nil, err
	}
	if s.bg == nil || s.bg.Registry == nil || s.bg.Inbox == nil {
		return nil, interfaces.ErrWorkNotFound
	}
	live, err := s.bg.Registry.List(ctx, ref)
	if err != nil {
		return nil, err
	}
	liveSet := make(map[string]interfaces.BackgroundActivity, len(live))
	for _, a := range live {
		liveSet[a.ID] = a
	}
	notices, err := s.bg.Inbox.Pending(ctx, ref)
	if err != nil {
		return nil, err
	}
	noticeState := make(map[string]string, len(notices))
	for _, n := range notices {
		if state := ParseShellJobNoticeState(n.Text); state != "" {
			noticeState[n.ID] = state
		}
	}
	out := make([]interfaces.WorkResult, len(ids))
	for i, id := range ids {
		switch {
		case liveSet[id].ID == id:
			a := liveSet[id]
			out[i] = interfaces.WorkResult{
				ID: id, State: interfaces.WorkStateRunning, Title: a.Title, Terminal: false,
			}
		case noticeState[id] != "":
			out[i] = interfaces.WorkResult{
				ID: id, State: noticeState[id], Terminal: interfaces.IsTerminalWorkState(noticeState[id]),
			}
		default:
			out[i] = interfaces.WorkResult{ID: id, State: interfaces.WorkStateUnknown}
		}
	}
	return out, nil
}

// ParseShellJobNoticeState reads the terminal state out of a shell-job notice.
// The text is "Background shell <id> <state>: <title>. …".
func ParseShellJobNoticeState(text string) string {
	const prefix = "Background shell "
	if !strings.HasPrefix(text, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(text, prefix)
	space := strings.IndexByte(rest, ' ')
	if space <= 0 || space+1 >= len(rest) {
		return ""
	}
	stateAndMore := rest[space+1:]
	colon := strings.IndexByte(stateAndMore, ':')
	if colon <= 0 {
		return ""
	}
	state := stateAndMore[:colon]
	switch state {
	case interfaces.WorkStateRunning, interfaces.WorkStateSucceeded, interfaces.WorkStateFailed,
		interfaces.WorkStateCancelled, interfaces.WorkStateLost:
		return state
	default:
		return ""
	}
}

// Cancel asks the watcher of each id to stop. The returned state is whatever
// status.json said at that moment; the durable terminal state arrives as a notice.
func (s *ShellJobService) Cancel(ctx context.Context, ref interfaces.SessionRef,
	ids []string,
) ([]interfaces.WorkResult, error) {
	if s.local != nil && s.onHostSession(ctx, ref) {
		return s.local.Cancel(ctx, ref, ids)
	}
	if !s.RemoteAvailable() {
		return nil, interfaces.ErrWorkNotFound
	}
	if err := validateShellJobIDs(ids); err != nil {
		return nil, err
	}
	sandboxIDs := s.sandboxFor(ctx, ref, ids)
	out := make([]interfaces.WorkResult, len(ids))
	for i, id := range ids {
		result, err := s.cancelRemoteJob(ctx, ref, id, sandboxIDs[id], false)
		if err != nil {
			return nil, err
		}
		out[i] = result
	}
	return out, nil
}

func (s *ShellJobService) cancelRemoteJob(ctx context.Context, ref interfaces.SessionRef,
	id, sandboxID string, force bool,
) (interfaces.WorkResult, error) {
	if sandboxID == "" {
		return interfaces.WorkResult{}, errors.New("session has no readable bound sandbox")
	}
	if s.remoteRead == nil || s.remoteExec == nil {
		return interfaces.WorkResult{}, fmt.Errorf("remote shell jobs are unavailable")
	}
	snapshots, errs := s.remoteRead(maintenanceCtx(ctx, ref), ref, []string{id}, map[string]string{id: sandboxID})
	if err := errs[id]; err != nil {
		return interfaces.WorkResult{}, err
	}
	if len(snapshots) != 1 {
		return interfaces.WorkResult{}, fmt.Errorf("unreadable cancellation status for %s", id)
	}
	snap := snapshots[0]
	if snap.Missing || snap.Meta.ID != id || snap.Meta.TenantID != ref.TenantID ||
		snap.Meta.SessionID != ref.SessionID || snap.Meta.SandboxID != sandboxID {
		return interfaces.WorkResult{}, interfaces.ErrWorkNotFound
	}
	if interfaces.IsTerminalWorkState(snap.Status.State) {
		return interfaces.WorkResult{ID: id, State: snap.Status.State, Terminal: true}, nil
	}
	if snap.Status.State != interfaces.WorkStateRunning {
		return interfaces.WorkResult{}, fmt.Errorf("unreadable cancellation status for %s", id)
	}
	res, err := s.remoteExec(maintenanceCtx(ctx, ref), ref.SessionID,
		sandbox.ShellJobCancelScript(snap.Meta, force), sandbox.ShellExecOptions{
			ExpectedSandboxID: sandboxID, Timeout: 10 * time.Second, SkipWorkspacePrep: true,
		})
	if err != nil {
		return interfaces.WorkResult{}, err
	}
	if res != nil && res.ExitCode == 3 {
		return interfaces.WorkResult{}, interfaces.ErrWorkNotFound
	}
	if res == nil || res.ExitCode != 0 || res.Error != "" || res.Killed {
		return interfaces.WorkResult{}, fmt.Errorf("cancel exec failed for %s; retry cancellation", id)
	}
	var status sandbox.ShellJobStatus
	if json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &status) != nil ||
		(status.State != interfaces.WorkStateRunning && !interfaces.IsTerminalWorkState(status.State)) {
		return interfaces.WorkResult{}, fmt.Errorf("cancel status unreadable for %s; retry cancellation", id)
	}
	return interfaces.WorkResult{
		ID: id, State: status.State, Title: snap.Meta.Title,
		Terminal: interfaces.IsTerminalWorkState(status.State),
	}, nil
}

// sandboxFor is the expected sandbox of each id. The index member wins.
// Anything the index does not know falls back to the current binding, which
// is lookup-only. The map is never nil; an empty string means "do not exec".
func (s *ShellJobService) sandboxFor(ctx context.Context, ref interfaces.SessionRef,
	ids []string,
) map[string]string {
	out := make(map[string]string, len(ids))
	indexed := map[string]string{}
	if s.index != nil {
		indexed = s.index.SandboxIDs(ctx, ref, ids)
	}
	fallback := ""
	looked := false
	for _, id := range ids {
		if sbx := indexed[id]; sbx != "" {
			out[id] = sbx
			continue
		}
		if !looked {
			looked = true
			if s.boundSandbox != nil {
				if current, ok := s.boundSandbox(maintenanceCtx(ctx, ref), ref.SessionID); ok {
					fallback = current
				}
			}
		}
		out[id] = fallback
	}
	return out
}

// batchReadViaPinned groups ids by sandbox and runs one maintenance exec per
// group. Every exec sets ExpectedSandboxID and SkipWorkspacePrep. An empty
// sandbox id is a per-id loss and does not exec. A Go error marks only that
// group's ids.
func (s *ShellJobService) batchReadViaPinned(ctx context.Context, ref interfaces.SessionRef,
	ids []string, sandboxIDs map[string]string,
) ([]sandbox.ShellJobSnapshot, map[string]error) {
	groups := make(map[string][]string)
	for _, id := range ids {
		sbx := ""
		if sandboxIDs != nil {
			sbx = sandboxIDs[id]
		}
		groups[sbx] = append(groups[sbx], id)
	}
	snapshots := make([]sandbox.ShellJobSnapshot, len(ids))
	byID := make(map[string]int, len(ids))
	for i, id := range ids {
		byID[id] = i
		snapshots[i] = sandbox.ShellJobSnapshot{Missing: true, Meta: sandbox.ShellJobMeta{ID: id}}
	}
	errs := make(map[string]error)
	for sandboxID, groupIDs := range groups {
		if sandboxID == "" {
			for _, id := range groupIDs {
				// The lookup-only binding API can also return false on a storage
				// outage. Only a pinned maintenance exec can confirm replacement.
				errs[id] = errors.New("session has no bound sandbox")
			}
			continue
		}
		if s.remoteExec == nil {
			for _, id := range groupIDs {
				errs[id] = errors.New("remote shell jobs are unavailable")
			}
			continue
		}
		budget, _ := ctx.Value(shellReadBudgetKey{}).(int)
		script := sandbox.ShellJobBatchReadScriptWithBudget(
			sandboxID, groupIDs, s.cursorsFor(ctx, ref, groupIDs), budget,
		)
		res, err := s.remoteExec(ctx, ref.SessionID, script, sandbox.ShellExecOptions{
			ExpectedSandboxID: sandboxID,
			Timeout:           30 * time.Second,
			SkipWorkspacePrep: true,
		})
		if err != nil || res == nil || res.ExitCode != 0 || res.Error != "" || res.Killed {
			reason := fmt.Sprintf("batch read on sandbox %s failed", sandboxID)
			if err != nil {
				reason = err.Error()
			} else if res != nil && res.Error != "" {
				reason = res.Error
			}
			for _, id := range groupIDs {
				if err != nil {
					errs[id] = fmt.Errorf("%s: %w", reason, err)
				} else {
					errs[id] = errors.New(reason)
				}
			}
			continue
		}
		seen := make(map[string]bool, len(groupIDs))
		for _, snap := range sandbox.ParseShellJobBatch(res.Stdout) {
			i, ok := byID[snap.Meta.ID]
			if !ok {
				continue
			}
			snapshots[i] = snap
			seen[snap.Meta.ID] = true
		}
		for _, id := range groupIDs {
			if !seen[id] {
				errs[id] = fmt.Errorf("unreadable batch response for shell job %s", id)
			}
		}
	}
	return snapshots, errs
}

// StartSweeper polls due index members until ctx is canceled. A service that
// cannot reach a remote sandbox does not start a goroutine.
func (s *ShellJobService) StartSweeper(ctx context.Context, interval time.Duration) {
	if s == nil || !s.RemoteAvailable() || interval <= 0 || ctx == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.SweepOnce(ctx); err != nil {
					logger.Warnf(ctx, "[ShellJob] sweep: %v", err)
				}
			}
		}
	}()
}

// protectScheduledJobs renews leases independently of remote execs and session
// locks. A blocked read must not leave any queued session unprotected.
func (s *ShellJobService) protectScheduledJobs(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	var providers sync.WaitGroup
	var mu sync.Mutex
	inFlight := make(map[interfaces.SessionRef]bool)
	refreshProvider := func(ref interfaces.SessionRef) {
		if s.keeper == nil || ctx.Err() != nil {
			return
		}
		mu.Lock()
		if inFlight[ref] {
			mu.Unlock()
			return
		}
		inFlight[ref] = true
		mu.Unlock()
		providers.Add(1)
		go func() {
			defer providers.Done()
			defer func() {
				mu.Lock()
				delete(inFlight, ref)
				mu.Unlock()
			}()
			refreshCtx, stop := context.WithTimeout(ctx, backgroundKeepAliveTimeout)
			defer stop()
			if err := s.keeper.KeepAlive(refreshCtx, ref); err != nil && refreshCtx.Err() == nil {
				logger.Warnf(ctx, "[ShellJob] provider heartbeat session=%s: %v", ref.SessionID, err)
			}
		}()
	}
	renew := func() {
		entries, err := s.index.Entries(ctx)
		if err != nil || s.bg == nil || s.bg.Registry == nil {
			return
		}
		sessions := make(map[interfaces.SessionRef]bool)
		for _, e := range entries {
			// Renew only: a snapshot taken before a terminal claim must not
			// resurrect a discarded activity or a deleted session.
			found, err := s.bg.Registry.Renew(ctx, e.SessionRef, e.WorkID, shellJobRenewLease)
			if err != nil {
				logger.Warnf(ctx, "[ShellJob] heartbeat id=%s: %v", e.WorkID, err)
			}
			if found {
				sessions[e.SessionRef] = true
			}
			_ = s.index.client.Expire(
				ctx, s.index.reservationKey(e.SessionRef, e.WorkID), interfaces.NoticeRetention,
			).Err()
		}
		// Provider TTLs are independent of Redis leases. A slow provider must
		// not block lease renewal or keep another session from being refreshed.
		for ref := range sessions {
			refreshProvider(ref)
		}
	}
	renew()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renew()
			}
		}
	}()
	return func() {
		cancel()
		<-done
		providers.Wait()
	}
}

// SweepOnce claims due jobs, grouped by session, and reports or renews each one.
func (s *ShellJobService) SweepOnce(ctx context.Context) error {
	if s == nil || s.index == nil {
		return nil
	}
	s.swept.Lock()
	defer s.swept.Unlock()
	stopHeartbeat := s.protectScheduledJobs(ctx)
	defer stopHeartbeat()
	s.settleCapturedOutcomes(ctx)
	now := time.Now().UTC()
	claimed, err := s.index.Claim(ctx, now, s.replica)
	if err != nil {
		return err
	}
	for ref, entries := range claimed {
		if err := s.index.WithSessionLock(ctx, ref, func(lockCtx context.Context) error {
			closed, err := s.index.SessionClosed(lockCtx, ref)
			if err != nil || closed {
				return err
			}
			return s.sweepSession(lockCtx, ref, entries)
		}); err != nil {
			logger.Warnf(ctx, "[ShellJob] sweep session=%s: %v", ref.SessionID, err)
		}
		if err := s.index.Release(ctx, ref, s.replica); err != nil {
			logger.Warnf(ctx, "[ShellJob] release sweep lock session=%s: %v", ref.SessionID, err)
		}
	}
	return nil
}

func (s *ShellJobService) sweepSession(ctx context.Context, ref interfaces.SessionRef,
	entries []ShellJobIndexEntry,
) error {
	ids := make([]string, 0, len(entries))
	sandboxIDs := make(map[string]string, len(entries))
	entryByID := make(map[string]ShellJobIndexEntry, len(entries))
	now := time.Now().UTC()
	for _, e := range entries {
		if e.YieldUntil.Add(shellJobHandoff).After(now) {
			continue
		}
		ids = append(ids, e.WorkID)
		sandboxIDs[e.WorkID] = e.SandboxID
		entryByID[e.WorkID] = e
	}
	if len(ids) == 0 {
		return nil
	}
	if s.remoteRead == nil {
		return fmt.Errorf("remote shell jobs are unavailable")
	}
	snapshots, errs := s.remoteRead(maintenanceCtx(ctx, ref), ref, ids, sandboxIDs)
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(snapshots) != len(ids) {
		return fmt.Errorf("sweep batch read returned %d of %d", len(snapshots), len(ids))
	}
	byID := make(map[string]sandbox.ShellJobSnapshot, len(snapshots))
	for i, snap := range snapshots {
		key := snap.Meta.ID
		if key == "" && i < len(ids) {
			key = ids[i]
		}
		if key != "" {
			byID[key] = snap
		}
	}
	for _, id := range ids {
		snap := byID[id]
		entry := entryByID[id]
		parentMessageID := snap.Meta.ParentMessageID
		if belongsToOtherSession(snap.Meta, ref) {
			parentMessageID = ""
		}
		switch {
		case errs != nil && errs[id] != nil && (entry.SandboxID == "" || shellSandboxGone(errs[id])):
			s.finishLost(ctx, ref, entry, errs[id].Error(), parentMessageID)
		case errs != nil && errs[id] != nil:
			// A timed-out or paused sandbox read is not evidence the watcher
			// died. Dropping the index member and the activity lease here lets
			// the next resolve rebuild the sandbox under a command that is
			// still running. Leave both in place and renew the lease so the
			// next sweep can read again. An index member with no sandbox id
			// can never be read, so that case is still lost.
			logger.Warnf(ctx, "[ShellJob] sweep read id=%s: %v", id, errs[id])
			s.renewRunning(ctx, ref, id, snap)
		case snap.Missing || belongsToOtherSession(snap.Meta, ref) || snap.Meta.SandboxID == "" ||
			(entry.SandboxID != "" && snap.Meta.SandboxID != entry.SandboxID):
			s.finishLost(ctx, ref, entry, "job directory missing or sandbox replaced", parentMessageID)
		case interfaces.IsTerminalWorkState(snap.Status.State):
			s.finishTerminal(ctx, ref, entry, snap)
		case snap.Status.State == interfaces.WorkStateRunning && !snap.WatcherAlive:
			s.stopOrphanedJob(ctx, ref, entry, snap.Status)
			s.finishLost(ctx, ref, entry, "watcher process is gone", parentMessageID)
		default:
			s.renewRunning(ctx, ref, id, snap)
		}
	}
	return nil
}

func (s *ShellJobService) settleCapturedOutcomes(ctx context.Context) {
	if s.index == nil {
		return
	}
	for _, item := range s.skillCapture.pending() {
		state, ok, err := s.index.Outcome(ctx, item.ref, item.id)
		if err != nil {
			logger.Warnf(ctx, "[ShellJob] outcome id=%s: %v", item.id, err)
			continue
		}
		if !ok {
			continue
		}
		s.skillCapture.settle(ctx, item.ref, item.id, state)
	}
}

func (s *ShellJobService) recordShellJobOutcome(ctx context.Context, ref interfaces.SessionRef, workID, state string) {
	if s.index == nil {
		return
	}
	if err := s.index.RecordOutcome(ctx, ref, workID, state); err != nil {
		logger.Warnf(ctx, "[ShellJob] record outcome id=%s: %v", workID, err)
	}
}

// stopOrphanedJob kills the command process group and the docker activity
// loop after the watcher itself is gone. A missing process is logged and
// still reported lost. A replaced sandbox is not passed here: its processes
// are already gone, and exec would land in the new sandbox.
func (s *ShellJobService) stopOrphanedJob(ctx context.Context, ref interfaces.SessionRef,
	entry ShellJobIndexEntry, status sandbox.ShellJobStatus,
) {
	if s.remoteExec == nil || entry.SandboxID == "" {
		return
	}
	if status.PGID <= 1 && status.TouchPID <= 1 {
		return
	}
	script := sandbox.ShellJobKillOrphansScript(status.PGID, status.TouchPID)
	execCtx, cancel := context.WithTimeout(maintenanceCtx(ctx, ref), 10*time.Second)
	defer cancel()
	res, err := s.remoteExec(execCtx, ref.SessionID, script, sandbox.ShellExecOptions{
		ExpectedSandboxID: entry.SandboxID,
		Timeout:           10 * time.Second,
		SkipWorkspacePrep: true,
	})
	if err != nil || (res != nil && res.Error != "") {
		reason := ""
		if err != nil {
			reason = err.Error()
		} else if res != nil {
			reason = res.Error
		}
		logger.Warnf(ctx, "[ShellJob] kill orphans id=%s: %s", entry.WorkID, reason)
	}
}

func (s *ShellJobService) finishTerminal(ctx context.Context, ref interfaces.SessionRef,
	entry ShellJobIndexEntry, snap sandbox.ShellJobSnapshot,
) {
	s.skillCapture.settle(ctx, ref, entry.WorkID, snap.Status.State)
	claimed, err := s.index.ClaimWork(ctx, ref, entry.WorkID)
	if err != nil {
		logger.Warnf(ctx, "[ShellJob] claim terminal id=%s: %v", entry.WorkID, err)
		return
	}
	if !claimed {
		return
	}
	s.recordShellJobOutcome(ctx, ref, entry.WorkID, snap.Status.State)
	result := s.applyOutputFiles(ctx, ref, ShellJobResult(snap, false), snap.Meta)
	if result.ID == "" {
		result.ID = entry.WorkID
	}
	if err := s.lifecycle.Finish(ctx, ref, shellJobNotice(entry.WorkID, snap, result.State, result.OutputFiles), result,
		snap.Meta.ParentMessageID); err != nil {
		logger.Warnf(ctx, "[ShellJob] finish id=%s: %v", entry.WorkID, err)
		if rqErr := s.index.Requeue(ctx, entry); rqErr != nil {
			logger.Warnf(ctx, "[ShellJob] requeue id=%s: %v", entry.WorkID, rqErr)
		}
	}
}

func (s *ShellJobService) finishLost(ctx context.Context, ref interfaces.SessionRef,
	entry ShellJobIndexEntry, reason, parentMessageID string,
) {
	s.skillCapture.drop(ref, entry.WorkID)
	claimed, err := s.index.ClaimWork(ctx, ref, entry.WorkID)
	if err != nil {
		logger.Warnf(ctx, "[ShellJob] claim lost id=%s: %v", entry.WorkID, err)
		return
	}
	if !claimed {
		return
	}
	s.recordShellJobOutcome(ctx, ref, entry.WorkID, interfaces.WorkStateLost)
	meta := sandbox.ShellJobMeta{
		ID: entry.WorkID, TenantID: ref.TenantID, SessionID: ref.SessionID,
		SandboxID: entry.SandboxID, Title: reason,
	}
	result := lostResult(meta, reason)
	snap := sandbox.ShellJobSnapshot{
		Meta:   meta,
		Status: sandbox.ShellJobStatus{State: interfaces.WorkStateLost, EndedAt: ptrTime(time.Now().UTC())},
	}
	if err := s.lifecycle.Finish(ctx, ref, shellJobNotice(entry.WorkID, snap, interfaces.WorkStateLost, nil), result,
		parentMessageID); err != nil {
		logger.Warnf(ctx, "[ShellJob] finish lost id=%s: %v", entry.WorkID, err)
		if rqErr := s.index.Requeue(ctx, entry); rqErr != nil {
			logger.Warnf(ctx, "[ShellJob] requeue id=%s: %v", entry.WorkID, rqErr)
		}
	}
}

func (s *ShellJobService) renewRunning(ctx context.Context, ref interfaces.SessionRef,
	id string, snap sandbox.ShellJobSnapshot,
) {
	found, err := s.lifecycle.Alive(ctx, ref, id, shellJobRenewLease)
	if err != nil {
		logger.Warnf(ctx, "[ShellJob] renew id=%s: %v", id, err)
		return
	}
	if found {
		return
	}
	if s.bg == nil || s.bg.Registry == nil {
		return
	}
	started := snap.Status.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	activity := interfaces.BackgroundActivity{
		ID: id, Kind: "shell", Title: snap.Meta.Title, StartedAt: started,
	}
	if err := s.bg.Registry.Add(ctx, ref, activity, shellJobRenewLease, 0); err != nil {
		logger.Warnf(ctx, "[ShellJob] re-add id=%s: %v", id, err)
		return
	}
	if s.keeper != nil {
		if err := s.keeper.KeepAlive(ctx, ref); err != nil {
			logger.Warnf(ctx, "[ShellJob] keep-alive after re-add id=%s: %v", id, err)
		}
	}
}

// OnStop does not cancel shell jobs. Stopping the turn only returns the
// submit tool early; the watcher keeps running.
func (s *ShellJobService) OnStop(context.Context, interfaces.SessionRef) error { return nil }

// OnDelete stops detached jobs before the session's shared-machine workspace
// or pin disappears. Failed cancellation leaves tracking intact and aborts deletion.
func (s *ShellJobService) OnDelete(ctx context.Context, ref interfaces.SessionRef) error {
	if s.local != nil && s.onHostSession(ctx, ref) {
		return s.local.OnDelete(ctx, ref)
	}
	if s.index == nil {
		return nil
	}
	return s.index.WithSessionLock(ctx, ref, func(ctx context.Context) error {
		entries, err := s.index.Entries(ctx)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.SessionRef != ref {
				continue
			}
			result, err := s.cancelRemoteJob(ctx, ref, e.WorkID, e.SandboxID, true)
			if err != nil && !errors.Is(err, interfaces.ErrWorkNotFound) && !shellSandboxGone(err) {
				return err
			}
			if err == nil && !result.Terminal {
				return fmt.Errorf("shell job %s is still stopping; retry session deletion", e.WorkID)
			}
		}
		if err := s.index.client.Set(ctx, s.index.closedKey(ref), "1", interfaces.NoticeRetention).Err(); err != nil {
			return err
		}
		if err := s.index.RemoveSession(ctx, ref); err != nil {
			return err
		}
		if err := s.index.DeleteSessionCursors(ctx, ref); err != nil {
			return err
		}
		s.skillCapture.dropSession(ref)
		s.mu.Lock()
		defer s.mu.Unlock()
		prefix := fmt.Sprintf("%d\x00%s\x00", ref.TenantID, ref.SessionID)
		for key := range s.cursors {
			if strings.HasPrefix(key, prefix) {
				delete(s.cursors, key)
			}
		}
		return nil
	})
}

// OnDeleteAborted allows new jobs after a failed session deletion.
func (s *ShellJobService) OnDeleteAborted(ctx context.Context, ref interfaces.SessionRef) error {
	if s.local != nil && s.onHostSession(ctx, ref) {
		if rollback, ok := s.local.(interface {
			OnDeleteAborted(context.Context, interfaces.SessionRef) error
		}); ok {
			return rollback.OnDeleteAborted(ctx, ref)
		}
		return nil
	}
	if s.index == nil {
		return nil
	}
	return s.index.WithSessionLock(ctx, ref, func(ctx context.Context) error {
		return s.index.client.Del(ctx, s.index.closedKey(ref)).Err()
	})
}

// EndShellJobRun keeps the run's output cursors for another hour so a late
// read can still be incremental.
func (s *ShellJobService) EndShellJobRun(ref interfaces.SessionRef, runID string) {
	if s == nil || runID == "" {
		return
	}
	if s.local != nil && s.onHostSession(context.Background(), ref) {
		s.local.EndShellJobRun(ref, runID)
		return
	}
	if s.index != nil {
		if err := s.index.TouchRunCursors(context.Background(), ref, runID, time.Hour); err != nil {
			logger.Warnf(context.Background(), "[ShellJob] touch cursors run=%s: %v", runID, err)
		}
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

// ShellJobResult maps a directory snapshot into the shared work result.
// lite selects the host-session wording of the running and lost notes.
func ShellJobResult(snap sandbox.ShellJobSnapshot, lite bool) interfaces.WorkResult {
	state := snap.Status.State
	if state == "" {
		state = interfaces.WorkStateUnknown
	}
	payload := ShellJobPayload{
		DisplayType: "shell_exec",
		WorkID:      snap.Meta.ID,
		State:       state,
		SessionID:   snap.Meta.SessionID,
		Command:     snap.Meta.Command,
		WorkDir:     snap.Meta.WorkDir,
		ExitCode:    snap.Status.ExitCode,
		DurationMS:  shellJobDurationMS(snap.Status),
		StartedAt:   snap.Status.StartedAt,
		Stdout:      snap.Stdout.Text,
		Stderr:      snap.Stderr.Text,
		FromBytes: sandbox.ShellJobOffsets{
			Stdout: snap.Stdout.FromBytes, Stderr: snap.Stderr.FromBytes,
		},
		TotalBytes: sandbox.ShellJobOffsets{
			Stdout: snap.Stdout.TotalBytes, Stderr: snap.Stderr.TotalBytes,
		},
		OmittedBytes: sandbox.ShellJobOffsets{
			Stdout: snap.Stdout.OmittedBytes, Stderr: snap.Stderr.OmittedBytes,
		},
		Truncated: snap.Stdout.Truncated || snap.Stderr.Truncated ||
			snap.Stdout.OmittedBytes > 0 || snap.Stderr.OmittedBytes > 0,
		Note: shellJobNote(snap, lite),
	}
	return interfaces.WorkResult{
		ID: snap.Meta.ID, State: state, Title: snap.Meta.Title,
		Terminal: interfaces.IsTerminalWorkState(state), Payload: payload,
	}
}

func shellJobNote(snap sandbox.ShellJobSnapshot, lite bool) string {
	var b strings.Builder
	switch snap.Status.State {
	case interfaces.WorkStateRunning:
		b.WriteString(shellJobNoteRunning)
		if lite {
			b.WriteString(shellJobNoteRunningLite)
		}
	case interfaces.WorkStateLost:
		if lite {
			b.WriteString(shellJobNoteLostLite)
		} else {
			b.WriteString(shellJobNoteLost)
		}
	}
	if extra := shellJobTruncationNote(snap); extra != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(extra)
	}
	return b.String()
}

func shellJobTruncationNote(snap sandbox.ShellJobSnapshot) string {
	omitted := snap.Stdout.OmittedBytes + snap.Stderr.OmittedBytes
	truncated := omitted > 0 || snap.Stdout.Truncated || snap.Stderr.Truncated
	if !truncated {
		return ""
	}
	if snap.Stdout.FromBytes > 0 || snap.Stderr.FromBytes > 0 {
		return fmt.Sprintf("%d bytes of older output were rotated away.", omitted)
	}
	return fmt.Sprintf("Showed the first and last part; %d bytes in between were omitted.", omitted)
}

func shellTerminalResult(meta sandbox.ShellJobMeta, status sandbox.ShellJobStatus,
	offsets sandbox.ShellJobOffsets, preview *shellJobPreview, lite bool,
) interfaces.WorkResult {
	snap := sandbox.ShellJobSnapshot{
		Meta:   meta,
		Status: status,
		Stdout: sandbox.ShellJobStream{TotalBytes: offsets.Stdout},
		Stderr: sandbox.ShellJobStream{TotalBytes: offsets.Stderr},
	}
	if preview != nil {
		snap.Stdout = preview.stdout.stream(offsets.Stdout)
		snap.Stderr = preview.stderr.stream(offsets.Stderr)
	}
	return ShellJobResult(snap, lite)
}

func lostResult(meta sandbox.ShellJobMeta, _ string) interfaces.WorkResult {
	snap := sandbox.ShellJobSnapshot{
		Meta:    meta,
		Status:  sandbox.ShellJobStatus{State: interfaces.WorkStateLost},
		Missing: true,
	}
	return ShellJobResult(snap, false)
}

func shellJobNotice(
	id string, snap sandbox.ShellJobSnapshot, state string, links []string,
) interfaces.BackgroundNotice {
	title := snap.Meta.Title
	if strings.TrimSpace(title) == "" {
		title = id
	}
	finished := time.Now().UTC()
	if snap.Status.EndedAt != nil && !snap.Status.EndedAt.IsZero() {
		finished = snap.Status.EndedAt.UTC()
	}
	text := fmt.Sprintf("Background shell %s %s: %s. Use work_wait(wait_sec=0) to read its output.",
		id, state, title)
	if len(links) > 0 {
		text += " Output files: `" + strings.Join(links, "`, `") + "`."
	}
	return interfaces.BackgroundNotice{
		ID: id, Kind: "shell", FinishedAt: finished, Text: text,
	}
}

// HoldSkillEnvCapture keeps a credential write-back until this process sees
// the job succeed. The pairs never go into the job files or Redis.
func (s *ShellJobService) HoldSkillEnvCapture(
	ctx context.Context, ref interfaces.SessionRef, jobID string, run func(context.Context),
) {
	if s == nil || run == nil || jobID == "" {
		return
	}
	if s.local != nil && s.onHostSession(ctx, ref) {
		if holder, ok := s.local.(interface {
			HoldSkillEnvCapture(interfaces.SessionRef, string, func(context.Context))
		}); ok {
			holder.HoldSkillEnvCapture(ref, jobID, run)
			return
		}
	}
	s.skillCapture.put(ref, jobID, run)
}

func (s *ShellJobService) applyOutputFiles(
	ctx context.Context, ref interfaces.SessionRef, result interfaces.WorkResult, meta sandbox.ShellJobMeta,
) interfaces.WorkResult {
	if !result.Terminal || !meta.OutputSeen || meta.OutputDir == "" {
		return result
	}
	after, err := s.listOutputFiles(ctx, ref.SessionID, meta.OutputDir)
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

func (s *ShellJobService) listOutputFiles(
	ctx context.Context, sessionID, dir string,
) ([]sandbox.RemoteDirEntry, error) {
	if s.listFiles != nil {
		return s.listFiles(ctx, sessionID, dir)
	}
	if s.pinned == nil {
		return nil, errors.New("shell job service has no sandbox file listing")
	}
	return s.pinned.ListSessionFiles(ctx, sessionID, dir)
}

// skillEnvCaptures holds in-memory credential write-backs keyed by session and job.
type skillEnvCaptures struct {
	mu  sync.Mutex
	fns map[string]func(context.Context)
}

func skillCaptureKey(ref interfaces.SessionRef, jobID string) string {
	return fmt.Sprintf("%d\x00%s\x00%s", ref.TenantID, ref.SessionID, jobID)
}

func (c *skillEnvCaptures) put(ref interfaces.SessionRef, jobID string, run func(context.Context)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fns == nil {
		c.fns = map[string]func(context.Context){}
	}
	c.fns[skillCaptureKey(ref, jobID)] = run
}

func (c *skillEnvCaptures) settle(ctx context.Context, ref interfaces.SessionRef, jobID, state string) {
	run := c.take(ref, jobID)
	if run == nil || state != interfaces.WorkStateSucceeded {
		return
	}
	run(ctx)
}

func (c *skillEnvCaptures) drop(ref interfaces.SessionRef, jobID string) {
	_ = c.take(ref, jobID)
}

func (c *skillEnvCaptures) dropSession(ref interfaces.SessionRef) {
	prefix := fmt.Sprintf("%d\x00%s\x00", ref.TenantID, ref.SessionID)
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.fns {
		if strings.HasPrefix(key, prefix) {
			delete(c.fns, key)
		}
	}
}

type pendingSkillCapture struct {
	ref interfaces.SessionRef
	id  string
}

func (c *skillEnvCaptures) pending() []pendingSkillCapture {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]pendingSkillCapture, 0, len(c.fns))
	for key := range c.fns {
		parts := strings.Split(key, "\x00")
		if len(parts) != 3 {
			continue
		}
		var tenant uint64
		if _, err := fmt.Sscanf(parts[0], "%d", &tenant); err != nil {
			continue
		}
		out = append(out, pendingSkillCapture{
			ref: interfaces.SessionRef{TenantID: tenant, SessionID: parts[1]},
			id:  parts[2],
		})
	}
	return out
}

func (c *skillEnvCaptures) take(ref interfaces.SessionRef, jobID string) func(context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := skillCaptureKey(ref, jobID)
	run := c.fns[key]
	delete(c.fns, key)
	return run
}

func (s *ShellJobService) setCursorsLocked(ref interfaces.SessionRef, runID, workID string,
	from sandbox.ShellJobOffsets,
) {
	if runID == "" || workID == "" {
		return
	}
	if s.index != nil {
		if err := s.index.PutCursor(context.Background(), ref, runID, workID, from, time.Hour); err != nil {
			logger.Warnf(context.Background(), "[ShellJob] cursor id=%s: %v", workID, err)
		}
		return
	}
	s.pruneCursorsLocked(time.Now())
	s.cursors[cursorKey(ref, runID, workID)] = shellJobCursor{
		from: from, expires: time.Now().Add(time.Hour),
	}
}

func (s *ShellJobService) pruneCursorsLocked(now time.Time) {
	for key, cursor := range s.cursors {
		if !cursor.expires.After(now) {
			delete(s.cursors, key)
		}
	}
}

func cursorKey(ref interfaces.SessionRef, runID, workID string) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s", ref.TenantID, ref.SessionID, runID, workID)
}

type (
	shellReadRunKey    struct{}
	shellReadBudgetKey struct{}
)

func shellSandboxGone(err error) bool {
	return errors.Is(err, sandbox.ErrBoundSandboxChanged) || errors.Is(err, sandbox.ErrSandboxSessionDeleted)
}

func (s *ShellJobService) cursorsFor(
	ctx context.Context, ref interfaces.SessionRef, ids []string,
) map[string]sandbox.ShellJobOffsets {
	runID, _ := ctx.Value(shellReadRunKey{}).(string)
	if s == nil || runID == "" || len(ids) == 0 {
		return nil
	}
	if s.index != nil {
		out := make(map[string]sandbox.ShellJobOffsets, len(ids))
		for _, id := range ids {
			from, ok, err := s.index.GetCursor(ctx, ref, runID, id)
			if err != nil {
				logger.Warnf(ctx, "[ShellJob] cursor id=%s: %v", id, err)
				continue
			}
			if !ok {
				continue
			}
			out[id] = from
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneCursorsLocked(now)
	out := make(map[string]sandbox.ShellJobOffsets, len(ids))
	for _, id := range ids {
		cursor, ok := s.cursors[cursorKey(ref, runID, id)]
		if !ok {
			continue
		}
		out[id] = cursor.from
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *ShellJobService) noteReadCursor(
	ref interfaces.SessionRef, runID, workID string, snap sandbox.ShellJobSnapshot,
) {
	if s == nil || runID == "" || workID == "" || snap.Missing {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCursorsLocked(ref, runID, workID, sandbox.ShellJobOffsets{
		Stdout: nextStreamOffset(snap.Stdout),
		Stderr: nextStreamOffset(snap.Stderr),
	})
}

func nextStreamOffset(st sandbox.ShellJobStream) int64 {
	if st.NextBytes != nil {
		return *st.NextBytes
	}
	end := st.FromBytes + int64(len(st.Text))
	if st.OmittedBytes > 0 && st.FromBytes == 0 {
		return st.TotalBytes
	}
	if end > 0 && end < st.TotalBytes && st.Truncated && st.OmittedBytes == 0 {
		return end
	}
	if end > st.TotalBytes {
		return st.TotalBytes
	}
	if end == 0 {
		return st.TotalBytes
	}
	return st.TotalBytes
}

// ensureBoundSandbox returns the session's sandbox id. A first command has
// no binding yet: one ordinary exec (empty ExpectedSandboxID) takes the same
// resolveSession path as shell_exec and creates it. The job script itself
// still runs against the id this returns.
func (s *ShellJobService) ensureBoundSandbox(ctx context.Context, ref interfaces.SessionRef) (string, error) {
	if s.boundSandbox == nil {
		return "", fmt.Errorf("session has no bound sandbox")
	}
	lookupCtx := maintenanceCtx(ctx, ref)
	if id, ok := s.boundSandbox(lookupCtx, ref.SessionID); ok && id != "" {
		return id, nil
	}
	if s.remoteExec == nil {
		return "", fmt.Errorf("session has no bound sandbox")
	}
	if _, err := s.remoteExec(lookupCtx, ref.SessionID, "true", sandbox.ShellExecOptions{
		// No WorkDir: the warm-up must take the same resolveSession path as
		// shell_exec, whose cwd is the manager's layout-root default. A pinned
		// legacy "/workspace" is outside a shared machine's WorkDirRoots.
	}); err != nil {
		return "", err
	}
	if id, ok := s.boundSandbox(lookupCtx, ref.SessionID); ok && id != "" {
		return id, nil
	}
	return "", fmt.Errorf("session has no bound sandbox")
}

func (s *ShellJobService) onHostSession(ctx context.Context, ref interfaces.SessionRef) bool {
	if s == nil || s.pinned == nil {
		return false
	}
	return !s.pinned.VersionsWorkspace(ctx, ref.SessionID)
}

func maintenanceCtx(ctx context.Context, ref interfaces.SessionRef) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return types.WithSandboxTenantID(
		types.WithExecutionTenant(context.WithoutCancel(ctx), ref.TenantID),
		ref.TenantID,
	)
}

func clampYieldTime(d time.Duration) time.Duration {
	if d <= 0 {
		return shellJobYieldDefault
	}
	if d < shellJobYieldFloor {
		return shellJobYieldFloor
	}
	if d > shellJobYieldMax {
		return shellJobYieldMax
	}
	return d
}

func validateShellJobIDs(ids []string) error {
	for _, id := range ids {
		if !sandbox.ValidShellJobID(id) {
			return interfaces.ErrWorkNotFound
		}
	}
	return nil
}

func belongsToOtherSession(meta sandbox.ShellJobMeta, ref interfaces.SessionRef) bool {
	if meta.SessionID != "" && meta.SessionID != ref.SessionID {
		return true
	}
	if meta.TenantID != 0 && meta.TenantID != ref.TenantID {
		return true
	}
	return false
}

func shellJobDurationMS(status sandbox.ShellJobStatus) int64 {
	if status.StartedAt.IsZero() {
		return 0
	}
	end := time.Now()
	if status.EndedAt != nil && !status.EndedAt.IsZero() {
		end = *status.EndedAt
	}
	d := end.Sub(status.StartedAt)
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}

var errShellJobIDTaken = errors.New("shell job id already exists")

// allocateShellJobID draws a handle, skipping ids taken reports as in use.
func allocateShellJobID(taken func(string) bool) (string, error) {
	for range shellJobIDAttempts {
		id, err := sandbox.NewShellJobID()
		if err != nil {
			return "", err
		}
		if taken != nil && taken(id) {
			continue
		}
		return id, nil
	}
	return "", errShellJobIDTaken
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "unknown"
	}
	return name
}

func osGetpid() int { return os.Getpid() }

func ptrTime(t time.Time) *time.Time { return &t }
