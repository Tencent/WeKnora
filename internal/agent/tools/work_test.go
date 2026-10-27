package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

var workSession = interfaces.SessionRef{TenantID: 1, SessionID: "s"}

// fakeWorkHandler serves a scripted sequence of cheap states per ID; the last
// entry repeats. Detailed reads return detailed[id] (default "running").
type fakeWorkHandler struct {
	mu          sync.Mutex
	cheap       map[string][]string
	detailed    map[string]string
	calls       []interfaces.StatusOptions
	err         error
	detailedErr error
	reverse     bool
	omitLast    bool
	cancelled   [][]string
}

func (h *fakeWorkHandler) Status(
	ctx context.Context, _ interfaces.SessionRef, ids []string, opts interfaces.StatusOptions,
) ([]interfaces.WorkResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, opts)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if h.err != nil {
		return nil, h.err
	}
	if opts.Detailed && h.detailedErr != nil {
		return nil, h.detailedErr
	}
	out := make([]interfaces.WorkResult, 0, len(ids))
	for _, id := range ids {
		state := interfaces.WorkStateRunning
		if opts.Detailed {
			if s, ok := h.detailed[id]; ok {
				state = s
			}
		} else if seq := h.cheap[id]; len(seq) > 0 {
			state = seq[0]
			if len(seq) > 1 {
				h.cheap[id] = seq[1:]
			}
		}
		out = append(out, interfaces.WorkResult{
			ID: id, State: state, Title: "job " + id,
			Payload: map[string]any{"stdout": "out-" + id},
		})
	}
	if h.reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if h.omitLast && len(out) > 0 {
		out = out[:len(out)-1]
	}
	return out, nil
}

func (h *fakeWorkHandler) Cancel(
	ctx context.Context, _ interfaces.SessionRef, ids []string,
) ([]interfaces.WorkResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if h.err != nil {
		return nil, h.err
	}
	h.cancelled = append(h.cancelled, ids)
	out := make([]interfaces.WorkResult, 0, len(ids))
	for _, id := range ids {
		state := interfaces.WorkStateRunning
		if id == "sj_done" {
			state = interfaces.WorkStateSucceeded
		}
		out = append(out, interfaces.WorkResult{ID: id, State: state})
	}
	return out, nil
}

type recordingMarks struct {
	seen    []string
	started []string
}

func (m *recordingMarks) MarkSeen(ids ...string) { m.seen = append(m.seen, ids...) }

func (m *recordingMarks) MarkStarted(id string) { m.started = append(m.started, id) }

// newTestWorkWait returns a tool on a fake clock: sleeping advances time.
func newTestWorkWait(h *fakeWorkHandler, marks WorkMarks, pending func(context.Context) bool) (*WorkWaitTool, *int) {
	tool := NewWorkWaitTool(h, workSession, "run-1", marks, pending, []string{"shell_exec_async"})
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	sleeps := 0
	tool.now = func() time.Time { return now }
	tool.sleep = func(_ context.Context, d time.Duration) bool {
		sleeps++
		now = now.Add(d)
		return true
	}
	return tool, &sleeps
}

func decodeWork(t *testing.T, output string) workView {
	t.Helper()
	var view workView
	require.NoError(t, json.Unmarshal([]byte(output), &view))
	return view
}

func TestWorkWaitZeroReadsOnceInDetail(t *testing.T) {
	h := &fakeWorkHandler{detailed: map[string]string{"sj_1": interfaces.WorkStateSucceeded}}
	marks := &recordingMarks{}
	tool, sleeps := newTestWorkWait(h, marks, nil)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"work_ids":["sj_1"],"wait_sec":0}`))

	require.NoError(t, err)
	require.True(t, res.Success)
	require.Zero(t, *sleeps)
	require.Equal(t, []interfaces.StatusOptions{{
		Detailed: true, RunID: "run-1", OutputBudget: workOutputBudget,
	}}, h.calls)
	view := decodeWork(t, res.Output)
	require.Equal(t, interfaces.WorkStateSucceeded, view.Items[0].State)
	require.Empty(t, view.Note)
	require.Equal(t, []string{"sj_1"}, marks.seen)
}

func TestWorkWaitAllWaitsForEveryID(t *testing.T) {
	h := &fakeWorkHandler{
		cheap: map[string][]string{
			"sj_1": {interfaces.WorkStateRunning, interfaces.WorkStateSucceeded},
			"sj_2": {interfaces.WorkStateRunning, interfaces.WorkStateRunning, interfaces.WorkStateFailed},
		},
		detailed: map[string]string{"sj_1": interfaces.WorkStateSucceeded, "sj_2": interfaces.WorkStateFailed},
	}
	marks := &recordingMarks{}
	tool, sleeps := newTestWorkWait(h, marks, nil)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"work_ids":["sj_1","sj_2"],"wait_sec":30}`))

	require.NoError(t, err)
	require.Equal(t, 2, *sleeps)
	view := decodeWork(t, res.Output)
	require.False(t, view.TimedOut)
	require.Equal(t, []string{"sj_1", "sj_2"}, marks.seen)
	final := h.calls[len(h.calls)-1]
	require.Equal(t, interfaces.StatusOptions{
		Detailed: true, RunID: "run-1", OutputBudget: workOutputBudget / 2,
	}, final)
}

func TestWorkWaitAnyReturnsOnTheFirstFinish(t *testing.T) {
	h := &fakeWorkHandler{
		cheap: map[string][]string{
			"sj_1": {interfaces.WorkStateRunning},
			"sj_2": {interfaces.WorkStateRunning, interfaces.WorkStateSucceeded},
		},
		detailed: map[string]string{"sj_2": interfaces.WorkStateSucceeded},
	}
	tool, sleeps := newTestWorkWait(h, &recordingMarks{}, nil)

	res, err := tool.Execute(context.Background(),
		json.RawMessage(`{"work_ids":["sj_1","sj_2"],"wait_sec":30,"mode":"any"}`))

	require.NoError(t, err)
	require.Equal(t, 1, *sleeps)
	view := decodeWork(t, res.Output)
	require.Equal(t, "Still running; you will be told when it finishes.", view.Note)
}

func TestWorkWaitDoesNotBlockAgainForTheSameRunningID(t *testing.T) {
	h := &fakeWorkHandler{}
	tool, sleeps := newTestWorkWait(h, &recordingMarks{}, nil)

	first, err := tool.Execute(context.Background(), json.RawMessage(`{"work_ids":["sj_1"],"wait_sec":3}`))
	require.NoError(t, err)
	require.Equal(t, 3, *sleeps)
	require.Equal(t, workNoteRunning, decodeWork(t, first.Output).Note)

	second, err := tool.Execute(context.Background(), json.RawMessage(`{"work_ids":["sj_1"],"wait_sec":60}`))
	require.NoError(t, err)
	require.Equal(t, 3, *sleeps, "a second blocking wait must not sleep")
	view := decodeWork(t, second.Output)
	require.False(t, view.TimedOut)
	require.Equal(t, workNoteRunning, view.Note)
	require.Equal(t, interfaces.WorkStateRunning, view.Items[0].State)
}

func TestWorkWaitTimesOut(t *testing.T) {
	h := &fakeWorkHandler{}
	marks := &recordingMarks{}
	tool, sleeps := newTestWorkWait(h, marks, nil)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"work_ids":["sj_1"],"wait_sec":3}`))

	require.NoError(t, err)
	require.Equal(t, 3, *sleeps)
	view := decodeWork(t, res.Output)
	require.True(t, view.TimedOut)
	require.Equal(t, "Still running; you will be told when it finishes.", view.Note)
	require.Empty(t, marks.seen)
}

func TestWorkWaitReturnsEarlyWhenTheUserTypes(t *testing.T) {
	h := &fakeWorkHandler{}
	tool, sleeps := newTestWorkWait(h, &recordingMarks{}, func(context.Context) bool { return true })

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"work_ids":["sj_1"],"wait_sec":300}`))

	require.NoError(t, err)
	require.Zero(t, *sleeps)
	view := decodeWork(t, res.Output)
	require.True(t, view.InterruptedByInput)
	require.Equal(t, "The user sent a new message; it is shown to you next. The work keeps running.", view.Note)
}

func TestWorkWaitProbesUnknownOnceWithoutMovingTheReadPosition(t *testing.T) {
	h := &fakeWorkHandler{
		cheap:    map[string][]string{"sj_1": {interfaces.WorkStateUnknown}},
		detailed: map[string]string{"sj_1": interfaces.WorkStateSucceeded},
	}
	tool, _ := newTestWorkWait(h, &recordingMarks{}, nil)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"work_ids":["sj_1"],"wait_sec":30}`))

	require.NoError(t, err)
	require.Equal(t, []interfaces.StatusOptions{
		{RunID: "run-1"},
		{Detailed: true, OutputBudget: workProbeBudget},
		{Detailed: true, RunID: "run-1", OutputBudget: workOutputBudget},
	}, h.calls)
}

func TestWorkWaitFailedReadsDoNotAcknowledgeNotices(t *testing.T) {
	for name, h := range map[string]*fakeWorkHandler{
		"error":      {err: errors.New("sandbox unreachable")},
		"not found":  {err: fmt.Errorf("wrap: %w", interfaces.ErrWorkNotFound)},
		"reordered":  {reverse: true},
		"incomplete": {omitLast: true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			inbox := background.NewMemoryNoticeInbox(nil)
			marks := background.NewRunNotices(workSession, background.NewMemoryActivityRegistry(nil), inbox)
			tool, _ := newTestWorkWait(h, marks, nil)

			res, err := tool.Execute(ctx, json.RawMessage(`{"work_ids":["sj_1","sj_2"],"wait_sec":0}`))

			if errors.Is(h.err, interfaces.ErrWorkNotFound) {
				require.NoError(t, err)
				require.False(t, res.Success)
				require.Empty(t, res.Output, "a missing ID must not turn unrelated IDs into lost jobs")
			} else {
				require.Error(t, err)
				require.Nil(t, res)
			}
			require.Empty(t, marks.TakeSeen(), "a failed batch must not acknowledge any requested ID")
			_, err = inbox.Push(ctx, workSession, interfaces.BackgroundNotice{
				ID: "sj_1", Text: "build succeeded", FinishedAt: time.Now(),
			})
			require.NoError(t, err)
			delivery, err := marks.RoundStart(ctx)
			require.NoError(t, err)
			require.Equal(t, "build succeeded", delivery.Text, "the later real completion must still reach the model")
		})
	}
}

func TestWorkWaitMissingWorkReturnsWithoutSleeping(t *testing.T) {
	for _, mode := range []string{"all", "any"} {
		for _, source := range []string{"cheap", "probe"} {
			t.Run(mode+"/"+source, func(t *testing.T) {
				missing := fmt.Errorf("status: %w", interfaces.ErrWorkNotFound)
				h := &fakeWorkHandler{err: missing}
				wantCalls := 2
				if source == "probe" {
					h = &fakeWorkHandler{
						cheap: map[string][]string{"missing": {interfaces.WorkStateUnknown}}, detailedErr: missing,
					}
					wantCalls = 3
				}
				marks := &recordingMarks{}
				tool, sleeps := newTestWorkWait(h, marks, nil)
				res, err := tool.Execute(context.Background(), json.RawMessage(
					fmt.Sprintf(`{"work_ids":["missing"],"wait_sec":300,"mode":%q}`, mode)))
				require.NoError(t, err)
				require.Zero(t, *sleeps)
				require.Len(t, h.calls, wantCalls)
				require.True(t, h.calls[wantCalls-1].Detailed)
				require.Equal(t, "run-1", h.calls[wantCalls-1].RunID, "always finish with the run's detailed read")
				require.Equal(t, interfaces.WorkStateLost, decodeWork(t, res.Output).Items[0].State)
				require.Empty(t, marks.seen)
			})
		}
	}
}

func TestWorkCancelFailedReadsDoNotAcknowledgeNotices(t *testing.T) {
	for _, callErr := range []error{errors.New("connection interrupted"), interfaces.ErrWorkNotFound} {
		marks := &recordingMarks{}
		tool := NewWorkCancelTool(&fakeWorkHandler{err: callErr}, workSession, marks)
		res, err := tool.Execute(context.Background(), json.RawMessage(`{"work_ids":["sj_1","sj_2"]}`))
		if errors.Is(callErr, interfaces.ErrWorkNotFound) {
			require.NoError(t, err)
			require.False(t, res.Success)
			require.Empty(t, res.Output)
		} else {
			require.ErrorIs(t, err, callErr)
		}
		require.Empty(t, marks.seen)
	}
}

func TestWorkWaitCancelIsAnErrorAndIsNotSeen(t *testing.T) {
	marks := &recordingMarks{}
	tool, _ := newTestWorkWait(&fakeWorkHandler{}, marks, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := tool.Execute(ctx, json.RawMessage(`{"work_ids":["sj_1"],"wait_sec":30}`))

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, res)
	require.Empty(t, marks.seen)
}

func TestWorkCancelContextIsAnErrorAndIsNotSeen(t *testing.T) {
	marks := &recordingMarks{}
	tool := NewWorkCancelTool(&fakeWorkHandler{}, workSession, marks)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := tool.Execute(ctx, json.RawMessage(`{"work_ids":["sj_1"]}`))

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, res)
	require.Empty(t, marks.seen)
}

func TestWorkWaitInterruptedNoteOnlyWhileSomethingRuns(t *testing.T) {
	h := &fakeWorkHandler{detailed: map[string]string{"sj_1": interfaces.WorkStateSucceeded}}
	marks := &recordingMarks{}
	tool, _ := newTestWorkWait(h, marks, func(context.Context) bool { return true })

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"work_ids":["sj_1"],"wait_sec":30}`))

	require.NoError(t, err)
	view := decodeWork(t, res.Output)
	require.True(t, view.InterruptedByInput)
	require.Empty(t, view.Note)
	require.Equal(t, []string{"sj_1"}, marks.seen)
}

func TestWorkWaitOutputLimitCoversTheMarshaledBudget(t *testing.T) {
	tool, _ := newTestWorkWait(&fakeWorkHandler{}, nil, nil)
	limit := tool.OutputLimitChars(json.RawMessage(`{"work_ids":["sj_1"]}`))
	require.Greater(t, limit, DefaultMaxToolOutput)
	require.Equal(t, workWaitOutputLimit, limit)
}

func TestWorkWaitRejectsBadArguments(t *testing.T) {
	tool, _ := newTestWorkWait(&fakeWorkHandler{}, &recordingMarks{}, nil)
	tooMany := `{"work_ids":["a","b","c","d","e","f","g","h","i","j","k","l","m","n","o","p","q"]}`
	for _, args := range []string{
		`{"work_ids":[]}`, `{"work_ids":["  "]}`, tooMany, `{"work_ids":["a"],"mode":"some"}`,
	} {
		res, err := tool.Execute(context.Background(), json.RawMessage(args))
		require.NoError(t, err)
		require.False(t, res.Success, args)
	}
}

func TestWorkWaitSecondsClamps(t *testing.T) {
	require.Equal(t, 60, WorkWaitSeconds(`{"work_ids":["a"]}`))
	require.Equal(t, 0, WorkWaitSeconds(`{"wait_sec":-5}`))
	require.Equal(t, 300, WorkWaitSeconds(`{"wait_sec":999}`))
	require.Equal(t, 0, WorkWaitSeconds(`{"wait_sec":0}`))
	require.Equal(t, 60, WorkWaitSeconds(`{`))
}

func TestWorkCancelReportsAndMarksTerminal(t *testing.T) {
	h := &fakeWorkHandler{}
	marks := &recordingMarks{}
	tool := NewWorkCancelTool(h, workSession, marks)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"work_ids":["sj_run","sj_done","sj_run"]}`))

	require.NoError(t, err)
	require.Equal(t, [][]string{{"sj_run", "sj_done"}}, h.cancelled, "IDs are de-duplicated in order")
	require.Equal(t, []string{"sj_done"}, marks.seen)
	view := decodeWork(t, res.Output)
	require.Equal(t, "Cancellation requested; you will be told when it stops.", view.Note)
}

func TestWorkToolDescriptionsNameTheSubmitters(t *testing.T) {
	tool := NewWorkWaitTool(&fakeWorkHandler{}, workSession, "run-1", nil, nil, []string{"shell_exec_async"})
	require.Contains(t, tool.Description(), "work_ids come from shell_exec_async.")
	require.False(t, CanRunConcurrently(ToolWorkWait))
	require.False(t, CanRunConcurrently(ToolWorkCancel))
}

func TestRegisteredBackgroundSubmitters(t *testing.T) {
	saved := backgroundSubmissionTools
	t.Cleanup(func() { backgroundSubmissionTools = saved })
	backgroundSubmissionTools = []string{"submit_a", "submit_b"}
	registry := NewToolRegistry()
	registry.RegisterTool(&fakeNamedTool{BaseTool: NewBaseTool("submit_b", "x", json.RawMessage(`{"type":"object"}`))})

	require.Equal(t, []string{"submit_b"}, RegisteredBackgroundSubmitters(registry))
}

type fakeNamedTool struct{ BaseTool }

func (*fakeNamedTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	return nil, nil
}
