package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func withRunBudget(budget RunBudget) testEngineOption {
	return func(cfg *types.AgentConfig) {
		cfg.MaxRunTokens = budget.MaxTotalTokens
		cfg.MaxRunSeconds = budget.MaxWallTimeSec
		cfg.MaxWriteActions = budget.MaxWriteActions
	}
}

func toolCallResponse(name, args string, tokens int) mockResponse {
	return mockResponse{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "working",
		ToolCalls: []types.LLMToolCall{{
			ID: "call-1", Function: types.FunctionCall{Name: name, Arguments: args},
		}},
		Done: true, FinishReason: "tool_calls", Usage: &types.TokenUsage{TotalTokens: tokens},
	}}}
}

func stopResponse(content string) mockResponse {
	return mockResponse{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      content, Done: true, FinishReason: "stop",
	}}}
}

type fakeBudgetPauser struct {
	decision types.BudgetPauseDecision
	err      error
	requests []types.BudgetPauseRequest
}

func (p *fakeBudgetPauser) PauseForBudget(
	_ context.Context, req types.BudgetPauseRequest,
) (types.BudgetPauseDecision, error) {
	p.requests = append(p.requests, req)
	return p.decision, p.err
}

type fakeSharedBudgetSink struct {
	mu           sync.Mutex
	reserved     map[string]RunBudgetUsed
	actual       map[string]RunBudgetUsed
	limit        RunBudget
	extension    RunBudget
	extensions   map[string]RunBudget
	reserveCalls []string
	reserveErr   error
}

func (s *fakeSharedBudgetSink) ReserveBudget(_ context.Context, _ string, key string, used RunBudgetUsed) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reserveErr != nil {
		return s.reserveErr
	}
	if s.reserved == nil {
		s.reserved = map[string]RunBudgetUsed{}
	}
	if s.actual == nil {
		s.actual = map[string]RunBudgetUsed{}
	}
	old := s.reserved[key]
	s.reserveCalls = append(s.reserveCalls, key)
	next := RunBudgetUsed{
		TotalTokens: max(old.TotalTokens, used.TotalTokens), WallTimeSec: max(old.WallTimeSec, used.WallTimeSec),
		WriteActions: max(old.WriteActions, used.WriteActions),
	}
	total := 0
	for k, v := range s.reserved {
		if k != key {
			total += max(v.TotalTokens, s.actual[k].TotalTokens)
		}
	}
	if s.limit.MaxTotalTokens > 0 && total+next.TotalTokens > s.limit.MaxTotalTokens {
		return collaboration.ErrRunBudgetExhausted
	}
	s.reserved[key] = next
	return nil
}

func (s *fakeSharedBudgetSink) SettleBudget(_ context.Context, _ string, key string, used RunBudgetUsed) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.actual == nil {
		s.actual = map[string]RunBudgetUsed{}
	}
	s.actual[key] = used
	return nil
}
func (s *fakeSharedBudgetSink) ExtendBudget(_ context.Context, _ string, key string, extra RunBudget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.extensions == nil {
		s.extensions = map[string]RunBudget{}
	}
	if _, ok := s.extensions[key]; ok {
		return nil
	}
	s.extensions[key] = extra
	s.extension = extra
	s.limit.MaxTotalTokens += extra.MaxTotalTokens
	return nil
}
func (s *fakeSharedBudgetSink) UsageBudget(context.Context, string) (RunBudgetUsage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RunBudgetUsage{Limit: s.limit}, nil
}

func TestSharedModelBudgetRejectsBeforeCallingModel(t *testing.T) {
	model := &mockChat{responses: []mockResponse{stopResponse("must not run")}}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 100_000}))
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.runBudget = newRunBudgetState(RunBudget{MaxTotalTokens: 100_000}, time.Now())
	engine.budgetSink = &fakeSharedBudgetSink{limit: RunBudget{MaxTotalTokens: 1}}
	engine.budgetTurnID = "turn"
	engine.budgetOwnerKey = "child"
	state, err := engine.Execute(context.Background(), "s", "m", "hello", nil)
	require.NoError(t, err)
	require.NotNil(t, state)
	require.True(t, state.IsComplete, "the run must stop deterministically")
	require.Empty(t, model.calls, "shared reservation must reject before the provider is called")
}

func TestSharedBudgetExtensionIsIdempotent(t *testing.T) {
	sink := &fakeSharedBudgetSink{limit: RunBudget{MaxTotalTokens: 10}}
	engine := newTestEngine(t, &mockChat{}, withRunBudget(RunBudget{MaxTotalTokens: 10}))
	engine.budgetSink = sink
	engine.budgetTurnID = "turn"
	engine.budgetOwnerKey = "child"
	decision := types.BudgetPauseDecision{PromptID: "prompt-1", Extension: RunBudget{MaxTotalTokens: 5}}
	engine.extendSharedBudget(context.Background(), decision)
	engine.extendSharedBudget(context.Background(), decision)
	require.Equal(t, 15, sink.limit.MaxTotalTokens)
}

func TestRunContractTokenExhaustionPausesAtCompletedRound(t *testing.T) {
	tool := newCountingTool(agenttools.ToolWriteSandboxFile)
	model := &mockChat{responses: []mockResponse{toolCallResponse(
		agenttools.ToolWriteSandboxFile, `{"path":"a.txt","content":"a"}`, 3_000,
	)}}
	pauser := &fakeBudgetPauser{decision: types.BudgetPauseDecision{Suspended: &types.PromptSuspension{
		PromptID: "budget-1", Kind: types.AgentPromptKindBudget,
	}}}
	// The preflight leaves room for this response; its reported usage then
	// reaches the limit before any tool in the response runs.
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 3_000}))
	engine.mode = types.RunModeAttended
	engine.suspendable = true
	engine.SetBudgetPauser(pauser)
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	state, err := engine.Execute(context.Background(), "s", "m", "write a file", nil)
	require.NoError(t, err)
	require.NotNil(t, state.Suspended)
	require.False(t, state.IsComplete)
	require.Zero(t, tool.calls, "a budget pause must not execute the pending tool batch")
	require.Empty(t, state.RoundSteps)
	require.Len(t, model.calls, 1, "no next model call after the limit")
	require.Len(t, pauser.requests, 1)
	snap, err := DecodeRunSnapshot(pauser.requests[0].Snapshot)
	require.NoError(t, err)
	require.True(t, snap.PendingToolBatch)
	require.NotNil(t, snap.Budget)
	require.Equal(t, RunStopTokenBudget, snap.Budget.StopReason)
	require.Empty(t, snap.CompletedSteps)
	require.Len(t, snap.PendingRound.Calls, 1)
}

func TestRunContractWriteBatchIsAtomicAcrossParallelTools(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withRunBudget(RunBudget{MaxWriteActions: 2}))
	engine.runBudget = newRunBudgetState(RunBudget{MaxWriteActions: 2}, time.Now())
	allowed, reason := engine.runBudget.reserveWrites(4)
	require.False(t, allowed)
	require.Equal(t, RunStopWriteBudget, reason)
	require.Equal(t, 0, engine.runBudget.snapshot(0).Used.WriteActions)
}

func TestRunContractTimeBudgetStopsBeforeNextRound(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withRunBudget(RunBudget{MaxWallTimeSec: 1}))
	engine.runBudget = newRunBudgetState(RunBudget{MaxWallTimeSec: 1}, time.Now().Add(-2*time.Second))
	require.Equal(t, RunStopTimeBudget, engine.budgetStopReason(0))
}

func TestRunContractZeroValuesPreserveLegacyExecution(t *testing.T) {
	tool := newCountingTool(agenttools.ToolWriteSandboxFile)
	model := &mockChat{responses: []mockResponse{
		toolCallResponse(agenttools.ToolWriteSandboxFile, `{"path":"a","content":"a"}`, 1),
		stopResponse("done"),
	}}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{}))
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	state, err := engine.Execute(context.Background(), "s", "m", "write", nil)
	require.NoError(t, err)
	require.Nil(t, state.Budget)
	require.Equal(t, 1, tool.calls)
}

func TestRunBudgetExtensionUsesCumulativeUsage(t *testing.T) {
	usage := RunBudgetUsage{
		Limit: RunBudget{MaxTotalTokens: 100, MaxWallTimeSec: 10, MaxWriteActions: 2},
		Used:  RunBudgetUsed{TotalTokens: 100, WallTimeSec: 10, WriteActions: 2},
	}
	state := restoreRunBudgetState(usage)
	state.extend(RunBudget{MaxTotalTokens: 50, MaxWallTimeSec: 5, MaxWriteActions: 1})
	report := state.snapshot(100)
	require.Equal(t, 150, report.Limit.MaxTotalTokens)
	require.Equal(t, 15, report.Limit.MaxWallTimeSec)
	require.Equal(t, 3, report.Limit.MaxWriteActions)
	require.Empty(t, report.StopReason)
	require.Equal(t, 100, report.Used.TotalTokens)
}

func TestBudgetUsageEstimateWhenProviderOmitsUsage(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withRunBudget(RunBudget{MaxTotalTokens: 1_000}))
	engine.runBudget = newRunBudgetState(RunBudget{MaxTotalTokens: 1_000}, time.Now())
	engine.recordRoundBudgetUsage(&types.ChatResponse{
		Content: "hello", ToolCalls: []types.LLMToolCall{{
			Function: types.FunctionCall{Name: "x", Arguments: `{"a":1}`},
		}},
	}, 100, 0)
	require.Positive(t, engine.budgetSnapshot(0).Used.TotalTokens)
}

func TestBudgetPauseDoesNotWaitForUnattendedRun(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withRunBudget(RunBudget{MaxTotalTokens: 10}))
	engine.mode = types.RunModeUnattended
	require.False(t, engine.shouldPauseForBudget())
}

func TestBudgetCompletionEventCarriesNoSuspendedFlagOnHardStop(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withRunBudget(RunBudget{MaxTotalTokens: 1}))
	var completed event.AgentCompleteData
	engine.eventBus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		completed = evt.Data.(event.AgentCompleteData)
		return nil
	})
	engine.runBudget = newRunBudgetState(RunBudget{MaxTotalTokens: 1}, time.Now())
	state := &types.AgentState{}
	require.NoError(t, engine.finishBudgetStop(context.Background(), state, "s"))
	require.True(t, state.IsComplete)
	engine.emitCompletionEvent(context.Background(), state, "s", "m", time.Now())
	require.Nil(t, completed.Suspended)
	require.True(t, state.IsComplete)
}

func TestBudgetRejectsInvalidDecision(t *testing.T) {
	require.Error(t, types.ValidateBudgetPauseResponse(nil, json.RawMessage(`{"action":"extend"}`)))
	require.Error(t, types.ValidateBudgetPauseResponse(nil, json.RawMessage(`{"action":"unknown"}`)))
	require.NoError(t, types.ValidateBudgetPauseResponse(nil, json.RawMessage(`{"action":"finish"}`)))
	require.NoError(t, types.ValidateBudgetPauseResponse(nil, json.RawMessage(
		`{"action":"extend","extension":{"max_total_tokens":100}}`,
	)))
}

func budgetPausedSnapshot(t *testing.T) ([]byte, *countingTool) {
	t.Helper()
	tool := newCountingTool(agenttools.ToolWriteSandboxFile)
	model := &mockChat{responses: []mockResponse{toolCallResponse(
		agenttools.ToolWriteSandboxFile, `{"path":"a.txt","content":"a"}`, 3_000,
	)}}
	pauser := &fakeBudgetPauser{decision: types.BudgetPauseDecision{Suspended: &types.PromptSuspension{
		PromptID: "budget-1", Kind: types.AgentPromptKindBudget,
	}}}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 3_000}))
	engine.mode = types.RunModeAttended
	engine.suspendable = true
	engine.SetBudgetPauser(pauser)
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)
	state, err := engine.Execute(context.Background(), "s", "m", "write a file", nil)
	require.NoError(t, err)
	require.NotNil(t, state.Suspended)
	require.Len(t, pauser.requests, 1)
	return pauser.requests[0].Snapshot, tool
}

func TestBudgetResumeExtendContinuesWithoutReplayingCommittedTool(t *testing.T) {
	snapshot, first := budgetPausedSnapshot(t)
	model := &mockChat{responses: []mockResponse{stopResponse("finished")}}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 3_000}))
	engine.mode = types.RunModeAttended
	engine.suspendable = true
	engine.toolRegistry = agenttools.NewToolRegistry()
	tool := newCountingTool(agenttools.ToolWriteSandboxFile)
	engine.toolRegistry.RegisterTool(tool)
	answered := &types.AgentPrompt{
		ID: "budget-1", Kind: types.AgentPromptKindBudget, Status: types.AgentPromptAnswered,
		Response: types.JSON(`{"action":"extend","extension":{"max_total_tokens":3000}}`),
	}
	state, err := engine.Resume(context.Background(), "s", "m", nil, snapshot, answered)
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Nil(t, state.Suspended)
	require.Equal(t, "finished", state.FinalAnswer)
	require.Zero(t, first.calls, "the paused engine must not execute the pending batch")
	require.Equal(t, 1, tool.calls, "resume executes the original pending batch exactly once")
	require.Len(t, model.calls, 1)
	require.Zero(t, state.RoundSteps[0].Iteration)
	require.Equal(t, 1, state.RoundSteps[1].Iteration)
}

func TestBudgetResumeFinishDoesNotCallModel(t *testing.T) {
	snapshot, _ := budgetPausedSnapshot(t)
	model := &mockChat{}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 3_000}))
	engine.mode = types.RunModeAttended
	engine.suspendable = true
	engine.toolRegistry = agenttools.NewToolRegistry()
	answered := &types.AgentPrompt{
		ID: "budget-1", Kind: types.AgentPromptKindBudget, Status: types.AgentPromptAnswered,
		Response: types.JSON(`{"action":"finish"}`),
	}
	state, err := engine.Resume(context.Background(), "s", "m", nil, snapshot, answered)
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Empty(t, model.calls)
	require.Equal(t, "finish", state.Budget.Action)
	require.Contains(t, state.FinalAnswer, "finish")
}

func TestBudgetResumeCancelDoesNotCallModel(t *testing.T) {
	snapshot, _ := budgetPausedSnapshot(t)
	model := &mockChat{}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 3_000}))
	engine.mode = types.RunModeAttended
	engine.suspendable = true
	engine.toolRegistry = agenttools.NewToolRegistry()
	answered := &types.AgentPrompt{
		ID: "budget-1", Kind: types.AgentPromptKindBudget, Status: types.AgentPromptAnswered,
		Response: types.JSON(`{"action":"cancel"}`),
	}
	state, err := engine.Resume(context.Background(), "s", "m", nil, snapshot, answered)
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Empty(t, model.calls)
	require.Equal(t, "cancel", state.Budget.Action)
	require.Contains(t, strings.ToLower(state.FinalAnswer), "cancel")
}

func TestBudgetHotExtensionContinuesCapturedToolBatch(t *testing.T) {
	tool := newCountingTool(agenttools.ToolWriteSandboxFile)
	model := &mockChat{responses: []mockResponse{
		toolCallResponse(agenttools.ToolWriteSandboxFile, `{"path":"a.txt","content":"a"}`, 3_000),
		stopResponse("finished"),
	}}
	pauser := &fakeBudgetPauser{decision: types.BudgetPauseDecision{
		Action: "extend", Extension: RunBudget{MaxTotalTokens: 3_000},
	}}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 3_000}))
	engine.mode = types.RunModeAttended
	engine.suspendable = true
	engine.SetBudgetPauser(pauser)
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	state, err := engine.Execute(context.Background(), "s", "m", "write", nil)
	require.NoError(t, err)
	require.Nil(t, state.Suspended, "a hot-wait answer is not a suspended prompt")
	require.True(t, state.IsComplete)
	require.Equal(t, 1, tool.calls)
	require.Equal(t, "finished", state.FinalAnswer)
}

func TestBudgetResumeTracksWriteReservationOnce(t *testing.T) {
	snapshot, _ := budgetPausedSnapshot(t)
	model := &mockChat{responses: []mockResponse{stopResponse("finished")}}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 3_000, MaxWriteActions: 1}))
	engine.mode = types.RunModeAttended
	engine.suspendable = true
	engine.toolRegistry = agenttools.NewToolRegistry()
	tool := newCountingTool(agenttools.ToolWriteSandboxFile)
	engine.toolRegistry.RegisterTool(tool)
	answered := &types.AgentPrompt{
		ID: "budget-1", Kind: types.AgentPromptKindBudget, Status: types.AgentPromptAnswered,
		Response: types.JSON(`{"action":"extend","extension":{"max_total_tokens":3000,"max_write_actions":1}}`),
	}
	state, err := engine.Resume(context.Background(), "s", "m", nil, snapshot, answered)
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Equal(t, 1, tool.calls)
	require.Equal(t, 1, state.Budget.Used.WriteActions, "the resumed batch must be charged exactly once")
}

func TestBudgetFinishEmitsCompletionExactlyOnce(t *testing.T) {
	snapshot, _ := budgetPausedSnapshot(t)
	engine := newTestEngine(t, &mockChat{})
	engine.toolRegistry = agenttools.NewToolRegistry()
	var completes int
	engine.eventBus.On(event.EventAgentComplete, func(context.Context, event.Event) error {
		completes++
		return nil
	})
	answered := &types.AgentPrompt{
		ID: "budget-1", Kind: types.AgentPromptKindBudget, Status: types.AgentPromptAnswered,
		Response: types.JSON(`{"action":"finish"}`),
	}
	state, err := engine.Resume(context.Background(), "s", "m", nil, snapshot, answered)
	require.NoError(t, err)
	require.True(t, state.IsComplete)
	require.Equal(t, 1, completes, "finish must produce exactly one completion event")
}

func TestBudgetStopNoticeDistinguishesHardStopFromPause(t *testing.T) {
	usage := RunBudgetUsage{
		Limit: RunBudget{MaxTotalTokens: 100}, Used: RunBudgetUsed{TotalTokens: 100},
	}
	require.Contains(t, strings.ToLower(budgetPauseNotice(RunStopTokenBudget, usage)), "paused")
	hard := strings.ToLower(budgetHardStopNotice(RunStopTokenBudget, usage))
	require.Contains(t, hard, "stopped")
	require.NotContains(t, hard, "paused")
}

func TestToolEffectStateClassifiesCommittedFailedAndUnknown(t *testing.T) {
	write := types.LLMToolCall{Function: types.FunctionCall{
		Name: agenttools.ToolWriteSandboxFile, Arguments: `{"path":"a","content":"a"}`,
	}}
	require.Equal(t, types.EffectCommitted, toolEffectState(
		context.Background(), write, &types.ToolResult{Success: true}, nil,
	))
	require.Equal(t, types.EffectNotStarted, toolEffectState(
		context.Background(), write, &types.ToolResult{Success: false, Error: "permission denied"}, nil,
	))
	require.Equal(t, types.EffectUnknown, toolEffectState(
		context.Background(), write, &types.ToolResult{Success: false, Error: "process exited with status 1"}, nil,
	))
	require.Equal(t, types.EffectUnknown, toolEffectState(
		context.Background(), write, &types.ToolResult{Success: false, Error: "MCP tool execution outcome is unknown"}, nil,
	))
	require.Equal(t, types.EffectUnknown, toolEffectState(
		context.Background(), write, nil, errors.New("transport closed after dispatch"),
	))
	read := types.LLMToolCall{Function: types.FunctionCall{
		Name: agenttools.ToolSearchKnowledge, Arguments: `{"query":"a"}`,
	}}
	require.Equal(t, types.EffectNone, toolEffectState(
		context.Background(), read, &types.ToolResult{Success: false, Error: "no result"}, nil,
	))
}

func TestToolEffectStateIsVisibleInModelAndHistory(t *testing.T) {
	result := &types.ToolResult{
		Success: false, Error: "connection closed after dispatch",
		EffectState: types.EffectUnknown,
	}
	modelOutput := modelcontext.NewRegistry(true).ModelToolResultForTool(
		agenttools.ToolCallMCPTool, result,
	)
	require.Contains(t, modelOutput, "effect: unknown")
	require.Contains(t, modelOutput, "do not replay blindly")
	require.Contains(t, agenttools.CompactToolOutputForHistory(
		agenttools.ToolCallMCPTool, result,
	), "effect: unknown")
}

func TestExecutePreservesSharedBudgetLedger(t *testing.T) {
	model := &mockChat{responses: []mockResponse{stopResponse("done")}}
	engine := newTestEngine(t, model, withRunBudget(RunBudget{MaxTotalTokens: 10_000}))
	engine.toolRegistry = agenttools.NewToolRegistry()
	shared := &types.RunBudgetUsage{
		Limit: RunBudget{MaxTotalTokens: 10_000},
		Used:  RunBudgetUsed{TotalTokens: 7_500},
	}
	sink := &fakeSharedBudgetSink{limit: RunBudget{MaxTotalTokens: 10_000}}
	engine.SetSharedRunBudget(shared)
	engine.SetSharedRunBudgetSink(sink, "turn", "child")
	state, err := engine.Execute(context.Background(), "s", "m", "hello", nil)
	require.NoError(t, err)
	require.NotNil(t, state.Budget)
	require.Contains(t, sink.reserveCalls, "child/model:0",
		"Execute must preserve and consume the shared account loaded by attach")
	require.NotZero(t, sink.actual["child/model:0"].TotalTokens,
		"the live model call must settle against the shared account")
}
