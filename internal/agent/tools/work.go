package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	workWaitDefaultSec = 60
	workWaitMaxSec     = 300
	workWaitPoll       = time.Second
	// workMaxIDs bounds one call so each ID's share of the output budget stays useful.
	workMaxIDs = 16
	// workOutputBudget caps the stdout and stderr bodies of one work_wait call (D6).
	workOutputBudget = 32 * 1024
	// workWaitOutputLimit is the rune cap ToolRegistry applies to the marshaled
	// work_wait body. JSON escaping can turn one payload byte into \u00XX, and
	// the envelope adds ids, states, and the note. The generic 24000-rune cap
	// would cut a full result into invalid JSON.
	workWaitOutputLimit = workOutputBudget*6 + 8*1024
	// workProbeBudget asks a state-only probe for as little output as possible.
	workProbeBudget = 1
)

const workWaitDescription = `Wait for background work only when you cannot continue without its result.
work_ids come from %s. Do not call it merely because work is still running; you will
be told when it finishes. wait_sec: seconds to wait (default 60, max 300); 0 returns
the current state and new output immediately. mode: all (default) or any. Returns
early if the user sends a message.`

const workCancelDescription = `Stop background work by work_id.`

const (
	workNoteRunning     = "Still running; you will be told when it finishes."
	workNoteInterrupted = "The user sent a new message; it is shown to you next. The work keeps running."
	workNoteCancelling  = "Cancellation requested; you will be told when it stops."
)

// backgroundSubmissionTools are the tools that start background work. A run
// gets work_wait and work_cancel only when it has at least one of them.
var backgroundSubmissionTools []string

// RegisteredBackgroundSubmitters returns the background submission tools
// registered in r, in declaration order.
func RegisteredBackgroundSubmitters(r *ToolRegistry) []string {
	var out []string
	for _, name := range backgroundSubmissionTools {
		if _, err := r.GetTool(name); err == nil {
			out = append(out, name)
		}
	}
	return out
}

// WorkMarks is the run-scoped tracker the work tools report to.
// *background.RunNotices implements it.
type WorkMarks interface {
	MarkSeen(ids ...string)
}

// WorkWaitInput documents the work_wait arguments for the schema.
type WorkWaitInput struct {
	WorkIDs []string `json:"work_ids" jsonschema:"IDs of the background work to wait for"`
	WaitSec int      `json:"wait_sec,omitempty" jsonschema:"Seconds to wait, default 60, max 300; 0 returns at once"`
	Mode    string   `json:"mode,omitempty" jsonschema:"all (default) waits for every ID; any returns when one ends"`
}

// WorkCancelInput documents the work_cancel arguments for the schema.
type WorkCancelInput struct {
	WorkIDs []string `json:"work_ids" jsonschema:"IDs of the background work to stop"`
}

type workWaitArgs struct {
	WorkIDs []string `json:"work_ids"`
	WaitSec *int     `json:"wait_sec"`
	Mode    string   `json:"mode"`
}

// WorkWaitSeconds returns the wait of a work_wait call: default 60, clamped to
// 0–300. Malformed arguments get the default. The engine derives the tool
// timeout from it (D3).
func WorkWaitSeconds(arguments string) int {
	var args struct {
		WaitSec *int `json:"wait_sec"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil || args.WaitSec == nil {
		return workWaitDefaultSec
	}
	return min(max(*args.WaitSec, 0), workWaitMaxSec)
}

type workItemView struct {
	WorkID string `json:"work_id"`
	State  string `json:"state"`
	Title  string `json:"title,omitempty"`
	Result any    `json:"result,omitempty"`
}

type workView struct {
	Items              []workItemView `json:"items"`
	TimedOut           bool           `json:"timed_out,omitempty"`
	InterruptedByInput bool           `json:"interrupted_by_input,omitempty"`
	Note               string         `json:"note,omitempty"`
}

// WorkWaitTool waits on background work of the run's session.
type WorkWaitTool struct {
	BaseTool
	handler interfaces.WorkHandler
	session interfaces.SessionRef
	runID   string
	marks   WorkMarks
	pending func(context.Context) bool
	now     func() time.Time
	sleep   func(context.Context, time.Duration) bool

	// waitedRunning records IDs that already had a blocking wait and were still
	// running. A later blocking wait in this run does not sleep on them again.
	mu            sync.Mutex
	waitedRunning map[string]struct{}
}

// NewWorkWaitTool returns work_wait bound to one run. runID scopes incremental
// output reads; pending reports a waiting user message; submitters are named
// in the description.
func NewWorkWaitTool(
	handler interfaces.WorkHandler, session interfaces.SessionRef, runID string,
	marks WorkMarks, pending func(context.Context) bool, submitters []string,
) *WorkWaitTool {
	description := fmt.Sprintf(workWaitDescription, strings.Join(submitters, ", "))
	return &WorkWaitTool{
		BaseTool: NewBaseTool(ToolWorkWait, description, utils.GenerateSchema[WorkWaitInput]()),
		handler:  handler,
		session:  session,
		runID:    runID,
		marks:    marks,
		pending:  pending,
		now:      time.Now,
		sleep:    workSleep,
	}
}

// Execute implements types.Tool.
func (t *WorkWaitTool) Execute(ctx context.Context, raw json.RawMessage) (*types.ToolResult, error) {
	var args workWaitArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return workError("invalid arguments: " + err.Error()), nil
	}
	ids, err := normalizeWorkIDs(args.WorkIDs)
	if err != nil {
		return workError(err.Error()), nil
	}
	mode := strings.ToLower(strings.TrimSpace(args.Mode))
	if mode == "" {
		mode = "all"
	}
	if mode != "all" && mode != "any" {
		return workError(`mode must be "all" or "any"`), nil
	}
	waitSec := WorkWaitSeconds(string(raw))

	timedOut, interrupted := false, false
	if waitSec > 0 {
		fresh := t.unwaited(ids)
		if len(fresh) > 0 {
			var err error
			timedOut, interrupted, err = t.poll(ctx, fresh, mode, time.Duration(waitSec)*time.Second)
			if err != nil {
				return nil, err
			}
		}
	}
	results, err := t.status(ctx, ids, interfaces.StatusOptions{
		Detailed: true, RunID: t.runID, OutputBudget: workOutputBudget / len(ids),
	})
	if err != nil {
		return workReadError(ids, err)
	}
	if waitSec > 0 {
		t.rememberRunning(results)
	}
	markTerminal(t.marks, results)

	note := ""
	switch {
	case interrupted && anyRunning(results):
		note = workNoteInterrupted
	case anyRunning(results):
		note = workNoteRunning
	}
	rendered := renderWork(results, note, timedOut && anyRunning(results), interrupted)
	rendered.OutputFiles = collectOutputFiles(results)
	return rendered, nil
}

func collectOutputFiles(results []interfaces.WorkResult) []string {
	inspected := false
	var links []string
	for _, result := range results {
		if result.OutputFiles == nil {
			continue
		}
		inspected = true
		links = append(links, result.OutputFiles...)
	}
	if !inspected {
		return nil
	}
	if links == nil {
		return []string{}
	}
	return links
}

// OutputLimitChars keeps a full work_wait body, including JSON framing and
// escaping, inside the registry's truncation ceiling.
func (t *WorkWaitTool) OutputLimitChars(json.RawMessage) int {
	return workWaitOutputLimit
}

// poll waits with cheap reads until mode is satisfied, the user sends a
// message, or wait elapses.
func (t *WorkWaitTool) poll(
	ctx context.Context, ids []string, mode string, wait time.Duration,
) (timedOut, interrupted bool, err error) {
	deadline := t.now().Add(wait)
	probed := make(map[string]bool, len(ids))
	for {
		results, callErr := t.handler.Status(ctx, t.session, ids, interfaces.StatusOptions{RunID: t.runID})
		if workStopped(callErr) {
			return false, false, callErr
		}
		if errors.Is(callErr, interfaces.ErrWorkNotFound) {
			return false, false, nil
		}
		// A failed cheap read is not "everything finished". Keep waiting; the
		// final detailed read is what the model sees.
		if callErr == nil {
			results, callErr = checkWorkResults(ids, results, nil)
			if callErr != nil {
				return false, false, callErr
			}
			var unknown []string
			for _, r := range results {
				if r.State == interfaces.WorkStateUnknown && !probed[r.ID] {
					probed[r.ID] = true
					unknown = append(unknown, r.ID)
				}
			}
			if len(unknown) > 0 {
				// No RunID: a probe must not move the read position the final
				// detailed read hands to the model.
				probe, probeErr := t.handler.Status(ctx, t.session, unknown, interfaces.StatusOptions{
					Detailed: true, OutputBudget: workProbeBudget,
				})
				if workStopped(probeErr) {
					return false, false, probeErr
				}
				if errors.Is(probeErr, interfaces.ErrWorkNotFound) {
					return false, false, nil
				}
				if probeErr == nil {
					probe, probeErr = checkWorkResults(unknown, probe, nil)
					if probeErr != nil {
						return false, false, probeErr
					}
					byID := make(map[string]interfaces.WorkResult, len(probe))
					for _, r := range probe {
						byID[r.ID] = r
					}
					for i, r := range results {
						if p, ok := byID[r.ID]; ok {
							results[i] = p
						}
					}
				}
			}
			if workSatisfied(results, mode) {
				return false, false, nil
			}
		}
		if t.pending != nil && t.pending(ctx) {
			return false, true, nil
		}
		if !t.now().Before(deadline) {
			return true, false, nil
		}
		if !t.sleep(ctx, workWaitPoll) {
			return false, false, ctx.Err()
		}
	}
}

func (t *WorkWaitTool) unwaited(ids []string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	fresh := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, waited := t.waitedRunning[id]; !waited {
			fresh = append(fresh, id)
		}
	}
	return fresh
}

func (t *WorkWaitTool) rememberRunning(results []interfaces.WorkResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.waitedRunning == nil {
		t.waitedRunning = map[string]struct{}{}
	}
	for _, result := range results {
		if !result.Terminal && result.ID != "" {
			t.waitedRunning[result.ID] = struct{}{}
		}
	}
}

func (t *WorkWaitTool) status(
	ctx context.Context, ids []string, opts interfaces.StatusOptions,
) ([]interfaces.WorkResult, error) {
	results, err := t.handler.Status(ctx, t.session, ids, opts)
	return checkWorkResults(ids, results, err)
}

// workStopped reports a cancelled or timed-out call. Those are not a lost
// batch: Execute returns the error, and the IDs are not marked seen.
func workStopped(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// WorkCancelTool stops background work of the run's session.
type WorkCancelTool struct {
	BaseTool
	handler interfaces.WorkHandler
	session interfaces.SessionRef
	marks   WorkMarks
}

// NewWorkCancelTool returns work_cancel bound to one run.
func NewWorkCancelTool(handler interfaces.WorkHandler, session interfaces.SessionRef, marks WorkMarks) *WorkCancelTool {
	return &WorkCancelTool{
		BaseTool: NewBaseTool(ToolWorkCancel, workCancelDescription, utils.GenerateSchema[WorkCancelInput]()),
		handler:  handler,
		session:  session,
		marks:    marks,
	}
}

// Execute implements types.Tool.
func (t *WorkCancelTool) Execute(ctx context.Context, raw json.RawMessage) (*types.ToolResult, error) {
	var args WorkCancelInput
	if err := json.Unmarshal(raw, &args); err != nil {
		return workError("invalid arguments: " + err.Error()), nil
	}
	ids, err := normalizeWorkIDs(args.WorkIDs)
	if err != nil {
		return workError(err.Error()), nil
	}
	results, err := t.handler.Cancel(ctx, t.session, ids)
	results, err = checkWorkResults(ids, results, err)
	if err != nil {
		return workReadError(ids, err)
	}
	markTerminal(t.marks, results)
	note := ""
	if anyRunning(results) {
		note = workNoteCancelling
	}
	return renderWork(results, note, false, false), nil
}

// checkWorkResults enforces the WorkHandler contract of one result per ID in
// input order. Results are matched by position, so a reordered reply would
// hand one item's state to another; it fails the batch instead (D14).
func checkWorkResults(ids []string, results []interfaces.WorkResult, err error) ([]interfaces.WorkResult, error) {
	if err == nil && len(results) != len(ids) {
		err = fmt.Errorf("handler returned %d results for %d ids", len(results), len(ids))
	}
	for i := 0; err == nil && i < len(ids); i++ {
		if results[i].ID != ids[i] {
			err = fmt.Errorf("handler returned id %q at position %d, want %q", results[i].ID, i, ids[i])
		}
	}
	if err != nil {
		return nil, err
	}
	for i := range results {
		results[i].Terminal = interfaces.IsTerminalWorkState(results[i].State)
	}
	return results, nil
}

// workReadError never acknowledges IDs from a failed batch. Even
// ErrWorkNotFound may refer to only one of several requested IDs; it does not
// confirm that the other jobs ended or that their results reached the model.
func workReadError(ids []string, err error) (*types.ToolResult, error) {
	if !errors.Is(err, interfaces.ErrWorkNotFound) {
		return nil, err
	}
	if len(ids) > 1 {
		return workError("One or more work IDs were not found in this session; check them individually."), nil
	}
	results := make([]interfaces.WorkResult, len(ids))
	for i, id := range ids {
		results[i] = interfaces.WorkResult{
			ID: id, State: interfaces.WorkStateLost, Terminal: true,
			Payload: map[string]any{"error": "work not found in this session"},
		}
	}
	return renderWork(results, "", false, false), nil
}

func normalizeWorkIDs(raw []string) ([]string, error) {
	ids := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New("work_ids must name at least one work_id")
	}
	if len(ids) > workMaxIDs {
		return nil, fmt.Errorf("work_ids may name at most %d work_ids", workMaxIDs)
	}
	return ids, nil
}

func workSatisfied(results []interfaces.WorkResult, mode string) bool {
	finished := 0
	for _, r := range results {
		if r.Terminal {
			finished++
		}
	}
	if mode == "any" {
		return finished > 0
	}
	return finished == len(results)
}

func anyRunning(results []interfaces.WorkResult) bool {
	for _, r := range results {
		if !r.Terminal {
			return true
		}
	}
	return false
}

func markTerminal(marks WorkMarks, results []interfaces.WorkResult) {
	if marks == nil {
		return
	}
	var ids []string
	for _, r := range results {
		if r.Terminal {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) > 0 {
		marks.MarkSeen(ids...)
	}
}

func renderWork(results []interfaces.WorkResult, note string, timedOut, interrupted bool) *types.ToolResult {
	view := workView{
		Items:              make([]workItemView, 0, len(results)),
		TimedOut:           timedOut,
		InterruptedByInput: interrupted,
		Note:               note,
	}
	for _, r := range results {
		view.Items = append(view.Items, workItemView{WorkID: r.ID, State: r.State, Title: r.Title, Result: r.Payload})
	}
	raw, err := json.Marshal(view)
	if err != nil {
		return workError("encode work results: " + err.Error())
	}
	return &types.ToolResult{Success: true, Output: string(raw), Data: map[string]interface{}{"work": view}}
}

func workError(msg string) *types.ToolResult {
	return &types.ToolResult{Success: false, Error: msg}
}

// workSleep waits d, returning false if ctx ends first.
func workSleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
