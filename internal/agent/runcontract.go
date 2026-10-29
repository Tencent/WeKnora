package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// RunStopReason is defined in types so the completion event and persisted
// state can carry it without importing the engine.
type RunStopReason = types.RunStopReason

const (
	RunStopTokenBudget RunStopReason = types.RunStopTokenBudget
	RunStopTimeBudget  RunStopReason = types.RunStopTimeBudget
	RunStopWriteBudget RunStopReason = types.RunStopWriteBudget
)

// RunBudget and RunBudgetUsage are aliases for the wire types.
type (
	RunBudget      = types.RunBudget
	RunBudgetUsage = types.RunBudgetUsage
	RunBudgetUsed  = types.RunBudgetUsed
)

// runBudgetState is the concurrency-safe counter for one turn. Tool calls can
// run in parallel, so writes reserve a slot atomically before executing.
type runBudgetState struct {
	mu         sync.Mutex
	limit      RunBudget
	startedAt  time.Time
	writesUsed int
	stopReason RunStopReason
}

func newRunBudgetState(limit RunBudget, startedAt time.Time) *runBudgetState {
	return &runBudgetState{limit: limit, startedAt: startedAt}
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
	if s.stopReason != "" {
		return s.stopReason
	}
	if s.limit.MaxTotalTokens > 0 && tokensUsed >= s.limit.MaxTotalTokens {
		s.stopReason = RunStopTokenBudget
		return s.stopReason
	}
	if s.limit.MaxWallTimeSec > 0 && time.Since(s.startedAt) >= time.Duration(s.limit.MaxWallTimeSec)*time.Second {
		s.stopReason = RunStopTimeBudget
		return s.stopReason
	}
	return ""
}

// reserveWrite atomically spends one write action. It returns false when the
// action would exceed the limit; the caller must not execute the tool.
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

// snapshot reports limits and usage without mutating the stop reason.
func (s *runBudgetState) snapshot(tokensUsed int) RunBudgetUsage {

	if s == nil {
		return RunBudgetUsage{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := int(time.Since(s.startedAt) / time.Second)
	return RunBudgetUsage{
		Limit: s.limit,
		Used: RunBudgetUsed{
			TotalTokens:  tokensUsed,
			WallTimeSec:  elapsed,
			WriteActions: s.writesUsed,
		},
		StopReason: s.stopReason,
	}
}

// budgetStopNotice is the deterministic user-facing reason a RunContract
// ended the turn. It is also persisted as the final answer, so it must not
// imply results that were never produced.
func budgetStopNotice(reason RunStopReason, usage RunBudgetUsage) string {
	switch reason {
	case RunStopTokenBudget:
		return "Stopped at the run token budget (" +
			fmt.Sprintf("%d/%d tokens", usage.Used.TotalTokens, usage.Limit.MaxTotalTokens) +
			"). The turn stopped before another model round or tool could run."
	case RunStopTimeBudget:
		return "Stopped at the run time budget (" +
			fmt.Sprintf("%ds/%ds", usage.Used.WallTimeSec, usage.Limit.MaxWallTimeSec) +
			"). The turn stopped before another model round or tool could run."
	case RunStopWriteBudget:
		return "Stopped at the run write-action budget (" +
			fmt.Sprintf("%d/%d writes", usage.Used.WriteActions, usage.Limit.MaxWriteActions) +
			"). No further changes were made in this turn."
	default:
		return ""
	}
}

// BudgetFromAgentConfig reads the configured contract.
func BudgetFromAgentConfig(cfg *types.AgentConfig) RunBudget {
	return cfg.RunBudget()
}

// ptrBudgetUsage returns the report by pointer so an empty contract can stay absent.
func ptrBudgetUsage(u RunBudgetUsage) *RunBudgetUsage {
	return &u
}

// budgetStopReason and budgetSnapshot are nil-safe: executeLoop is also
// called directly by tests and internal adapters that do not go through
// Execute, and those paths must retain the pre-contract behaviour.
func (e *AgentEngine) budgetStopReason(tokensUsed int) RunStopReason {
	if e == nil || e.runBudget == nil {
		return ""
	}
	return e.runBudget.stopReasonNow(tokensUsed)
}

func (e *AgentEngine) budgetSnapshot(tokensUsed int) RunBudgetUsage {
	if e == nil || e.runBudget == nil {
		return RunBudgetUsage{}
	}
	return e.runBudget.snapshot(tokensUsed)
}
