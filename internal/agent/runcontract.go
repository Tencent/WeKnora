package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// RunStopReason is defined in types so the completion event and persisted
// state can carry it without importing the engine.
type RunStopReason = types.RunStopReason

const (
	// RunStopTokenBudget is the token contract boundary.
	RunStopTokenBudget RunStopReason = types.RunStopTokenBudget
	// RunStopTimeBudget is the active-time contract boundary.
	RunStopTimeBudget RunStopReason = types.RunStopTimeBudget
	// RunStopWriteBudget is the mutating-attempt contract boundary.
	RunStopWriteBudget RunStopReason = types.RunStopWriteBudget
)

// RunBudget is this package's alias for the public contract shape.
type RunBudget = types.RunBudget

// RunBudgetUsage is this package's alias for a contract report.
type RunBudgetUsage = types.RunBudgetUsage

// RunBudgetUsed is this package's alias for cumulative usage.
type RunBudgetUsed = types.RunBudgetUsed

const (
	// minTurnCompletionTokens is the smallest response budget worth sending.
	// A smaller allowance cannot complete a useful answer or a valid tool call,
	// so the run pauses instead of spending it.
	minTurnCompletionTokens = 256
	// budgetUsageSafetyTokens covers estimator drift when a provider does not
	// report usage. It is charged only on the fallback accounting path.
	budgetUsageSafetyTokens = 64
)

var errTurnTokenBudgetInsufficient = errors.New("turn token budget leaves no room for another model response")

// runBudgetState is the concurrency-safe ledger for one logical run. A pause
// and resume preserve the same ledger; the wait itself does not consume the
// wall-time allowance.
type runBudgetState struct {
	mu          sync.Mutex
	limit       RunBudget
	startedAt   time.Time
	writesUsed  int
	batchWrites int
	stopReason  RunStopReason
	tokensUsed  int
}

func newRunBudgetState(limit RunBudget, startedAt time.Time) *runBudgetState {
	return &runBudgetState{limit: limit, startedAt: startedAt}
}

// restoreRunBudgetState rebuilds a paused run's cumulative ledger. The pause
// duration is deliberately excluded: startedAt is shifted back by active
// usage, not by the wall time spent waiting for the user.
func restoreRunBudgetState(usage RunBudgetUsage) *runBudgetState {
	return &runBudgetState{
		limit:      usage.Limit,
		startedAt:  time.Now().Add(-time.Duration(max(usage.Used.WallTimeSec, 0)) * time.Second),
		tokensUsed: max(usage.Used.TotalTokens, 0),
		writesUsed: max(usage.Used.WriteActions, 0),
		// An extension is an explicit decision to continue. A stale stop
		// reason must not survive the resume.
	}
}

// extend adds room from the paused usage. The wire contract describes the
// extension as additional capacity, not a replacement of the configured
// limit; the effective limit therefore grows.
func (s *runBudgetState) extend(extra RunBudget) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if extra.MaxTotalTokens > 0 {
		if s.limit.MaxTotalTokens > 0 {
			s.limit.MaxTotalTokens = safeAddInt(s.limit.MaxTotalTokens, extra.MaxTotalTokens)
		} else {
			s.limit.MaxTotalTokens = safeAddInt(s.tokensUsed, extra.MaxTotalTokens)
		}
	}
	if extra.MaxWallTimeSec > 0 {
		if s.limit.MaxWallTimeSec > 0 {
			s.limit.MaxWallTimeSec = safeAddInt(s.limit.MaxWallTimeSec, extra.MaxWallTimeSec)
		} else {
			s.limit.MaxWallTimeSec = safeAddInt(int(time.Since(s.startedAt)/time.Second), extra.MaxWallTimeSec)
		}
	}
	if extra.MaxWriteActions > 0 {
		if s.limit.MaxWriteActions > 0 {
			s.limit.MaxWriteActions = safeAddInt(s.limit.MaxWriteActions, extra.MaxWriteActions)
		} else {
			s.limit.MaxWriteActions = safeAddInt(s.writesUsed, extra.MaxWriteActions)
		}
	}
	s.stopReason = ""
}

func safeAddInt(a, b int) int {
	if b > 0 && a > int(^uint(0)>>1)-b {
		return int(^uint(0) >> 1)
	}
	return a + b
}

func (s *runBudgetState) tokensUsedSnapshot() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokensUsed
}

// recordTokens adds one model round's usage to the ledger. Providers that do
// not report usage pass an estimate, so an unlimited free call cannot evade an
// explicit token budget.
func (s *runBudgetState) recordTokens(tokens int) {
	if s == nil || tokens <= 0 {
		return
	}
	s.mu.Lock()
	s.tokensUsed = safeAddInt(s.tokensUsed, tokens)
	s.mu.Unlock()
}

func (s *runBudgetState) remainingTokens(tokensUsed int) (int, bool) {
	if s == nil || s.limit.MaxTotalTokens <= 0 {
		return 0, false
	}
	remaining := s.limit.MaxTotalTokens - tokensUsed
	if remaining < 0 {
		remaining = 0
	}
	return remaining, true
}

// excludeWallTime keeps an interactive budget decision from consuming the
// allowance it is deciding about. A suspended run is restored from Used, so
// this adjustment is needed only for an answer delivered during the live wait.
func (s *runBudgetState) excludeWallTime(wait time.Duration) {
	if s == nil || wait <= 0 {
		return
	}
	s.mu.Lock()
	s.startedAt = s.startedAt.Add(wait)
	s.mu.Unlock()
}

func (s *runBudgetState) clearStop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopReason = ""
	s.mu.Unlock()
}
func (s *runBudgetState) markStop(reason RunStopReason) RunStopReason {
	if s == nil {
		return reason
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopReason == "" {
		s.stopReason = reason
	}
	return s.stopReason
}

// stopReasonNow returns the first budget that has already been exhausted.
// Time is checked live; token and write exhaustion are latched when first
// observed so later calls cannot "recover" past the limit.
func (s *runBudgetState) stopReasonNow(tokensUsed int) RunStopReason {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tokensUsed > s.tokensUsed {
		s.tokensUsed = tokensUsed
	}
	if s.stopReason != "" {
		return s.stopReason
	}
	if s.limit.MaxTotalTokens > 0 && s.tokensUsed >= s.limit.MaxTotalTokens {
		s.stopReason = RunStopTokenBudget
		return s.stopReason
	}
	if s.limit.MaxWallTimeSec > 0 &&
		time.Since(s.startedAt) >= time.Duration(s.limit.MaxWallTimeSec)*time.Second {
		s.stopReason = RunStopTimeBudget
		return s.stopReason
	}
	return ""
}

// beginWriteBatch marks that a model response paid for its mutating calls in
// one reservation. The marker is process-local and cleared at round end.
func (s *runBudgetState) beginWriteBatch() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.batchWrites++
	s.mu.Unlock()
}

func (s *runBudgetState) endWriteBatch() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.batchWrites > 0 {
		s.batchWrites--
	}
	s.mu.Unlock()
}

func (s *runBudgetState) writeBatchActive() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.batchWrites > 0
}

// reserveWrites reserves a complete mutating tool batch atomically. All or
// nothing is deliberate: a model response is replayed as one unit after a
// pause, so executing a prefix would either lose the suffix or duplicate the
// prefix on resume.
func (s *runBudgetState) reserveWrites(count int) (bool, RunStopReason) {
	if s == nil || count <= 0 {
		return true, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopReason != "" {
		return false, s.stopReason
	}
	if s.limit.MaxWriteActions <= 0 {
		return true, ""
	}
	if count > s.limit.MaxWriteActions-s.writesUsed {
		s.stopReason = RunStopWriteBudget
		return false, s.stopReason
	}
	s.writesUsed += count
	return true, ""
}

// reserveWrite atomically spends one mutating-tool attempt. It returns false
// when the action would exceed the limit; the caller must not execute the tool.
func (s *runBudgetState) reserveWrite() (bool, RunStopReason) {
	if s == nil {
		return true, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopReason != "" {
		return false, s.stopReason
	}
	if s.limit.MaxWriteActions <= 0 {
		return true, ""
	}
	if s.writesUsed >= s.limit.MaxWriteActions {
		s.stopReason = RunStopWriteBudget
		return false, s.stopReason
	}
	s.writesUsed++
	return true, ""
}

// snapshot reports limits and cumulative usage. tokensUsed is accepted for
// legacy callers/tests; the ledger keeps the larger of the two values.
func (s *runBudgetState) snapshot(tokensUsed int) RunBudgetUsage {
	if s == nil {
		return RunBudgetUsage{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tokensUsed > s.tokensUsed {
		s.tokensUsed = tokensUsed
	}
	elapsed := int(time.Since(s.startedAt) / time.Second)
	return RunBudgetUsage{
		Limit: s.limit,
		Used: RunBudgetUsed{
			TotalTokens:  s.tokensUsed,
			WallTimeSec:  elapsed,
			WriteActions: s.writesUsed,
		},
		StopReason: s.stopReason,
	}
}

func budgetPauseNotice(reason RunStopReason, usage RunBudgetUsage) string {
	switch reason {
	case RunStopTokenBudget:
		return fmt.Sprintf("Paused at the run token budget (%d/%d tokens). "+
			"The completed work is preserved and can continue after extending the budget.",
			usage.Used.TotalTokens, usage.Limit.MaxTotalTokens)
	case RunStopTimeBudget:
		return fmt.Sprintf("Paused at the run time budget (%ds/%ds). "+
			"The completed work is preserved and can continue after extending the budget.",
			usage.Used.WallTimeSec, usage.Limit.MaxWallTimeSec)
	case RunStopWriteBudget:
		return fmt.Sprintf("Paused at the run write-attempt budget (%d/%d attempts). "+
			"No additional mutating tool call was started in that round.",
			usage.Used.WriteActions, usage.Limit.MaxWriteActions)
	default:
		return ""
	}
}

func budgetHardStopNotice(reason RunStopReason, usage RunBudgetUsage) string {
	switch reason {
	case RunStopTokenBudget:
		return fmt.Sprintf("Run stopped at the token budget (%d/%d tokens). "+
			"Completed work is preserved; no further model or tool work was started.",
			usage.Used.TotalTokens, usage.Limit.MaxTotalTokens)
	case RunStopTimeBudget:
		return fmt.Sprintf("Run stopped at the time budget (%ds/%ds). "+
			"Completed work is preserved; no further model or tool work was started.",
			usage.Used.WallTimeSec, usage.Limit.MaxWallTimeSec)
	case RunStopWriteBudget:
		return fmt.Sprintf("Run stopped at the write-attempt budget (%d/%d attempts). "+
			"No mutating tool call from the refused batch was started.",
			usage.Used.WriteActions, usage.Limit.MaxWriteActions)
	default:
		return "Run stopped at its configured budget."
	}
}

// mutatingToolCallCount classifies a model response once, before any call in
// it runs. Unknown and MCP/proxy calls fail closed under a write contract.
func mutatingToolCallCount(ctx context.Context, calls []types.LLMToolCall) int {
	count := 0
	for _, call := range calls {
		risk := agenttools.CallRisk(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
		if risk.Mutates() || !risk.Valid() {
			count++
		}
	}
	return count
}

// SetSharedRunBudgetSink wires the engine to a durable collaboration account.
// A nil sink keeps a standalone turn-local ledger.
func (e *AgentEngine) SetSharedRunBudgetSink(sink BudgetSink, turnID, ownerKey string) {
	if e == nil {
		return
	}
	e.budgetSink = sink
	e.budgetTurnID = turnID
	e.budgetOwnerKey = ownerKey
}

func (e *AgentEngine) reserveSharedWrites(
	ctx context.Context, key string, count int,
) (bool, RunStopReason) {
	if e == nil || e.runBudget == nil || count <= 0 {
		return true, ""
	}
	if e.budgetSink != nil && e.budgetTurnID != "" {
		if err := e.budgetSink.ReserveBudget(ctx, e.budgetTurnID, e.budgetKey(key),
			types.RunBudgetUsed{WriteActions: count}); err != nil {
			e.runBudget.markStop(RunStopWriteBudget)
			return false, RunStopWriteBudget
		}
	}
	allowed, reason := e.runBudget.reserveWrites(count)
	if !allowed {
		if e.budgetSink != nil && e.budgetTurnID != "" {
			_ = e.budgetSink.SettleBudget(ctx, e.budgetTurnID, e.budgetKey(key), types.RunBudgetUsed{})
		}
		return false, reason
	}
	return true, ""
}

// reserveSharedModel atomically reserves the provider request before the call.
// Reserve and settle use the same owner-scoped key so a retry is idempotent.
func (e *AgentEngine) reserveSharedModel(
	ctx context.Context, estimated int,
) (key string, ok bool) {
	if e == nil || e.budgetSink == nil || e.budgetTurnID == "" {
		return "", true
	}
	call := e.budgetModelCall.Add(1) - 1
	key = e.budgetKey(fmt.Sprintf("model:%d", call))
	if estimated < 0 {
		estimated = 0
	}
	if err := e.budgetSink.ReserveBudget(ctx, e.budgetTurnID, key,
		types.RunBudgetUsed{TotalTokens: estimated}); err != nil {
		return key, false
	}
	return key, true
}

func (e *AgentEngine) settleSharedModel(ctx context.Context, key string, actual int) {
	if e == nil || e.budgetSink == nil || e.budgetTurnID == "" || key == "" || actual < 0 {
		return
	}
	if err := e.budgetSink.SettleBudget(ctx, e.budgetTurnID, key,
		types.RunBudgetUsed{TotalTokens: actual}); err != nil {
		logger.Warnf(ctx, "[Agent] Settle shared model budget: %v", err)
	}
}

func (e *AgentEngine) budgetKey(key string) string {
	if e == nil || e.budgetOwnerKey == "" {
		return key
	}
	return e.budgetOwnerKey + "/" + key
}

func (e *AgentEngine) extendSharedBudget(ctx context.Context, decision types.BudgetPauseDecision) {
	if e == nil || e.budgetSink == nil || e.budgetTurnID == "" || !decision.Extension.Active() {
		return
	}
	key := decision.PromptID
	if key == "" {
		key = fmt.Sprintf("%s:%d", e.budgetKey("extension"), e.budgetModelCall.Load())
	}
	if err := e.budgetSink.ExtendBudget(ctx, e.budgetTurnID, e.budgetKey(key), decision.Extension); err != nil {
		logger.Warnf(ctx, "[Agent] Extend shared run budget: %v", err)
	}
}

func (e *AgentEngine) settleSharedWrites(ctx context.Context, key string, count int) {
	if e == nil || e.budgetSink == nil || e.budgetTurnID == "" || key == "" || count <= 0 {
		return
	}
	err := e.budgetSink.SettleBudget(
		ctx, e.budgetTurnID, e.budgetKey(key), types.RunBudgetUsed{WriteActions: count},
	)
	if err != nil {
		logger.Warnf(ctx, "[Agent] Settle shared write budget: %v", err)
	}
}

// SetSharedRunBudget attaches a parent-owned cumulative ledger to this engine.
// The engine's local limits are capped by the shared remaining allowance so a
// child cannot spend more than the collaboration has left.
func (e *AgentEngine) SetSharedRunBudget(usage *types.RunBudgetUsage) {
	if e == nil || usage == nil {
		return
	}
	// The shared sink is authoritative for cumulative usage. The local ledger
	// only enforces this execution's slice of the same limit; seeding it with
	// shared usage and then recording local calls would double-count them.
	e.runBudget = newRunBudgetState(usage.Limit, time.Now())
}

// SharedRunBudget returns a snapshot for a collaboration owner to persist.
func (e *AgentEngine) SharedRunBudget(state *types.AgentState) types.RunBudgetUsage {
	if e == nil || state == nil {
		return types.RunBudgetUsage{}
	}
	return e.budgetSnapshot(state.TurnUsage.TotalTokens)
}

// SetBudgetPauser installs the human decision adapter for contract pauses.
func (e *AgentEngine) SetBudgetPauser(p BudgetPauser) {
	e.budgetPauser = p
}

func (e *AgentEngine) collaborationExcludeWallTime(ctx context.Context, wait time.Duration) error {
	if e == nil || e.budgetSink == nil || e.budgetTurnID == "" || wait <= 0 {
		return nil
	}
	if clock, ok := e.budgetSink.(interface {
		ExcludeWallTime(context.Context, string, string, time.Duration) error
	}); ok {
		return clock.ExcludeWallTime(ctx, e.budgetTurnID, e.budgetOwnerKey, wait)
	}
	return nil
}

func (e *AgentEngine) shouldPauseForBudget() bool {
	return e != nil && e.budgetPauser != nil && e.suspendable &&
		e.mode.Normalize() != types.RunModeUnattended
}

// budgetPromptRequestForContext fills identity from the live execution
// context. Kept separate from the pure state helper so callers cannot
// accidentally persist a principal captured from a background context.
func (e *AgentEngine) budgetPromptRequestForContext(
	ctx context.Context, state *types.AgentState, sessionID, messageID string, reason RunStopReason,
) types.BudgetPauseRequest {
	usage := e.budgetUsageForContext(ctx, state)
	usage.StopReason = reason
	req := types.BudgetPauseRequest{
		SessionID: sessionID, AssistantMessageID: messageID,
		Reason: reason, Limit: usage.Limit, Used: usage.Used,
		SuggestedExtension: suggestedBudgetExtension(usage.Limit),
	}
	req.TenantID = types.CallerFromContext(ctx).TenantID
	principal, _ := types.PrincipalFromContext(ctx)
	req.UserID = principal.StorageID()
	if requestID, ok := types.RequestIDFromContext(ctx); ok {
		req.RequestID = requestID
	}
	return req
}

func suggestedBudgetExtension(limit RunBudget) RunBudget {
	extra := RunBudget{}
	if limit.MaxTotalTokens > 0 {
		extra.MaxTotalTokens = max(limit.MaxTotalTokens/2, minTurnCompletionTokens)
	}
	if limit.MaxWallTimeSec > 0 {
		extra.MaxWallTimeSec = max(limit.MaxWallTimeSec/2, 30)
	}
	if limit.MaxWriteActions > 0 {
		extra.MaxWriteActions = max(limit.MaxWriteActions/2, 1)
	}
	return extra
}

type budgetBoundaryResult int

const (
	budgetContinue budgetBoundaryResult = iota
	budgetSuspended
	budgetFinished
)

type budgetBatchResult int

const (
	budgetBatchContinue budgetBatchResult = iota
	budgetBatchSuspended
	budgetBatchFinished
)

// handleBudgetBoundary is the single safe decision point for a contract stop.
// It runs only after a complete round or before starting the next model call;
// it never pauses in the middle of a tool batch.
func (e *AgentEngine) handleBudgetBoundary(
	ctx context.Context, state *types.AgentState, reason RunStopReason, sessionID, messageID string,
) budgetBoundaryResult {
	if e == nil || state == nil {
		return budgetFinished
	}
	usage := e.budgetUsageForContext(ctx, state)
	usage.StopReason = reason
	state.Budget = ptrBudgetUsage(usage)

	// MaxIterations is a separate contract. When it is exhausted there is no
	// next round to fund, so do not offer a misleading extension.
	if e.shouldPauseForBudget() && e.withinIterationBudget(state.CurrentRound) {
		decision, err := e.pauseForBudget(ctx, state, reason, sessionID, messageID)
		if err == nil {
			switch decision.Action {
			case "extend":
				e.runBudget.extend(decision.Extension)
				e.runBudget.clearStop()
				e.extendSharedBudget(ctx, decision)
				extended := e.budgetSnapshot(state.TurnUsage.TotalTokens)
				extended.StopReason = ""
				state.Budget = ptrBudgetUsage(extended)
				logger.Infof(ctx, "[Agent] Run budget extended: tokens=%d time=%ds writes=%d",
					extended.Limit.MaxTotalTokens, extended.Limit.MaxWallTimeSec, extended.Limit.MaxWriteActions)
				return budgetContinue
			case "finish", "cancel":
				_, _ = e.finishPausedBudget(ctx, state, sessionID, messageID, decision.Action)
				return budgetFinished
			}
			if decision.Suspended != nil {
				state.Suspended = decision.Suspended
				return budgetSuspended
			}
		} else if ctx.Err() == nil {
			logger.Warnf(ctx, "[Agent] Budget prompt failed; finishing deterministically: %v", err)
		}
	}

	_ = e.finishBudgetStop(ctx, state, sessionID)
	return budgetFinished
}

func (e *AgentEngine) pauseForBudget(
	ctx context.Context, state *types.AgentState, reason RunStopReason, sessionID, messageID string,
) (types.BudgetPauseDecision, error) {
	if e == nil || e.budgetPauser == nil || state == nil {
		return types.BudgetPauseDecision{}, nil
	}
	snapshot, version, err := e.betweenRoundSnapshot(state)
	if err != nil {
		return types.BudgetPauseDecision{}, fmt.Errorf("capture budget snapshot: %w", err)
	}
	req := e.budgetPromptRequestForContext(ctx, state, sessionID, messageID, reason)
	req.Snapshot, req.SnapshotVersion = snapshot, version
	waitStarted := time.Now()
	decision, err := e.budgetPauser.PauseForBudget(ctx, req)
	waited := time.Since(waitStarted)
	e.runBudget.excludeWallTime(waited)
	if e.collaborationExcludeWallTime(ctx, waited) != nil {
		logger.Warnf(ctx, "[Agent] Exclude shared budget wait time: %v", err)
	}
	return decision, err
}

// pauseBeforeToolBatch stores a model response whose tools have not executed.
// The first call is the resume point, so extending executes the exact batch;
// finish or cancel performs no tool at all. The explicit result avoids
// inventing a prompt ID for an answer already delivered during the hot wait.
func (e *AgentEngine) pauseBeforeToolBatch(
	ctx context.Context, state *types.AgentState, step types.AgentStep,
	calls []types.LLMToolCall, reason RunStopReason, sessionID, messageID string,
) budgetBatchResult {
	if e == nil || state == nil || len(calls) == 0 {
		return budgetBatchFinished
	}
	if !e.shouldPauseForBudget() {
		_ = e.finishBudgetStop(ctx, state, sessionID)
		return budgetBatchFinished
	}
	usage := e.budgetUsageForContext(ctx, state)
	usage.StopReason = reason
	state.Budget = ptrBudgetUsage(usage)
	snapshot, version, err := e.pendingToolBatchSnapshot(state, step, calls)
	if err != nil {
		logger.Warnf(ctx, "[Agent] Pending tool snapshot failed: %v", err)
		_ = e.finishBudgetStop(ctx, state, sessionID)
		return budgetBatchFinished
	}
	req := e.budgetPromptRequestForContext(ctx, state, sessionID, messageID, reason)
	req.Snapshot, req.SnapshotVersion = snapshot, version
	waitStarted := time.Now()
	decision, err := e.budgetPauser.PauseForBudget(ctx, req)
	waited := time.Since(waitStarted)
	e.runBudget.excludeWallTime(waited)
	if e.collaborationExcludeWallTime(ctx, waited) != nil {
		logger.Warnf(ctx, "[Agent] Exclude shared budget wait time: %v", err)
	}
	if err != nil {
		logger.Warnf(ctx, "[Agent] Budget prompt failed; stopping deterministically: %v", err)
		_ = e.finishBudgetStop(ctx, state, sessionID)
		return budgetBatchFinished
	}
	if decision.Suspended != nil {
		state.Suspended = decision.Suspended
		return budgetBatchSuspended
	}
	switch decision.Action {
	case "extend":
		e.runBudget.extend(decision.Extension)
		e.runBudget.clearStop()
		e.extendSharedBudget(ctx, decision)
		state.Budget = nil
		return budgetBatchContinue
	case "finish", "cancel":
		_, _ = e.finishPausedBudget(ctx, state, sessionID, messageID, decision.Action)
		return budgetBatchFinished
	}
	_ = e.finishBudgetStop(ctx, state, sessionID)
	return budgetBatchFinished
}

// budgetStopReason is nil-safe; executeLoop is also called directly by tests
// and internal adapters that do not go through Execute.
func (e *AgentEngine) budgetStopReason(tokensUsed int) RunStopReason {
	if e == nil || e.runBudget == nil {
		return ""
	}
	return e.runBudget.stopReasonNow(tokensUsed)
}

func (e *AgentEngine) estimateBudgetRequestTokens(messages []chat.Message, tools []chat.Tool, notice string) int {
	total := e.tokenEstimator.EstimateMessages(e.modelContext.EncodeMessages(messages))
	total += e.tokenEstimator.EstimateTools(tools)
	if notice != "" {
		total += e.tokenEstimator.EstimateString(notice)
	}
	return total
}

func (e *AgentEngine) completionBudgetForRunTokens(requestTokens int, configured int) int {
	if e == nil || e.runBudget == nil || e.runBudget.limit.MaxTotalTokens <= 0 {
		return configured
	}
	remaining, _ := e.runBudget.remainingTokens(e.runBudget.tokensUsedSnapshot())
	return min(configured, max(remaining-requestTokens, 0))
}

// recordRoundBudgetUsage charges one model request. Provider totals are
// authoritative; when a provider omits usage, estimate both halves so a
// contract cannot be bypassed by a streaming-compatible endpoint.
func (e *AgentEngine) recordRoundBudgetUsage(response *types.ChatResponse, requestTokens int, _ int) {
	if e == nil || e.runBudget == nil || !e.runBudget.limit.Active() || response == nil {
		return
	}
	used := contextTokensFromUsage(response.Usage)
	if used <= 0 {
		used = requestTokens + e.estimateResponseTokens(response)
		used += budgetUsageSafetyTokens
	}
	e.runBudget.recordTokens(used)
	// Shared model usage is settled by the model-call owner, using the same
	// reservation key. Settling again here would double-charge the turn.
}

func (e *AgentEngine) estimateResponseTokens(response *types.ChatResponse) int {
	if response == nil {
		return 0
	}
	total := e.tokenEstimator.EstimateString(response.Content)
	total += e.tokenEstimator.EstimateString(response.ReasoningContent)
	for _, call := range response.ToolCalls {
		total += e.tokenEstimator.EstimateString(call.Function.Name)
		total += e.tokenEstimator.EstimateString(call.Function.Arguments)
		total += 8
	}
	return total
}

// budgetUsageForContext reports the durable shared account when available.
// The local ledger remains the fallback for standalone runs and storage errors.
func (e *AgentEngine) budgetUsageForContext(ctx context.Context, state *types.AgentState) RunBudgetUsage {
	local := e.budgetSnapshot(state.TurnUsage.TotalTokens)
	if e == nil || e.budgetSink == nil || e.budgetTurnID == "" {
		return local
	}
	shared, err := e.budgetSink.UsageBudget(ctx, e.budgetTurnID)
	if err != nil || !shared.Limit.Active() {
		return local
	}
	if local.StopReason != "" {
		shared.StopReason = local.StopReason
	}
	if state != nil && state.Budget != nil && state.Budget.Action != "" {
		shared.Action = state.Budget.Action
	}
	return shared
}

func (e *AgentEngine) budgetSnapshot(tokensUsed int) RunBudgetUsage {
	if e == nil || e.runBudget == nil {
		return RunBudgetUsage{}
	}
	return e.runBudget.snapshot(tokensUsed)
}

// BudgetFromAgentConfig returns the effective contract configured on an agent.
func BudgetFromAgentConfig(cfg *types.AgentConfig) RunBudget {
	return cfg.RunBudget()
}

func ptrBudgetUsage(u RunBudgetUsage) *RunBudgetUsage {
	return &u
}
