package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunBudgetZeroLeavesEveryDimensionUncapped(t *testing.T) {
	budget := newRunBudgetState(RunBudget{}, time.Now())
	require.Empty(t, budget.stopReasonNow(10_000_000))
	ok, reason := budget.reserveWrite()
	require.True(t, ok)
	require.Empty(t, reason)
	require.Zero(t, budget.snapshot(10_000_000).Limit.MaxTotalTokens)
}

func TestRunBudgetTokenAndTimeStopReasons(t *testing.T) {
	token := newRunBudgetState(RunBudget{MaxTotalTokens: 100}, time.Now())
	require.Empty(t, token.stopReasonNow(99))
	require.Equal(t, RunStopTokenBudget, token.stopReasonNow(100))
	require.Equal(t, RunStopTokenBudget, token.stopReasonNow(100), "the reason is latched")

	timed := newRunBudgetState(RunBudget{MaxWallTimeSec: 1}, time.Now().Add(-2*time.Second))
	require.Equal(t, RunStopTimeBudget, timed.stopReasonNow(0))
}

func TestRunBudgetWriteReservationIsAtomic(t *testing.T) {
	budget := newRunBudgetState(RunBudget{MaxWriteActions: 3}, time.Now())
	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := budget.reserveWrite(); ok {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 3, granted)
	require.Equal(t, 3, budget.snapshot(0).Used.WriteActions)
	require.Equal(t, RunStopWriteBudget, budget.snapshot(0).StopReason)
}

func TestRunBudgetReportSeparatesLimitAndUsage(t *testing.T) {
	budget := newRunBudgetState(RunBudget{MaxTotalTokens: 100, MaxWallTimeSec: 30, MaxWriteActions: 2}, time.Now())
	_, _ = budget.reserveWrite()
	report := budget.snapshot(84)
	require.Equal(t, 100, report.Limit.MaxTotalTokens)
	require.Equal(t, 84, report.Used.TotalTokens)
	require.Equal(t, 1, report.Used.WriteActions)
	require.Empty(t, report.StopReason)
}
