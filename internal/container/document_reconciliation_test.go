package container

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type documentShutdownProbe struct {
	interfaces.KnowledgeService
	entered chan struct{}
	exited  chan struct{}
	stopped bool
	ordered bool
}

func (p *documentShutdownProbe) ReconcileDocuments(ctx context.Context) error {
	close(p.entered)
	<-ctx.Done()
	close(p.exited)
	return ctx.Err()
}

func (p *documentShutdownProbe) StopDocumentDispatches(context.Context) error {
	p.stopped = true
	select {
	case <-p.exited:
		p.ordered = true
	default:
	}
	return nil
}

func TestDocumentReconciliationStopsWorkersAfterLoop(t *testing.T) {
	p := &documentShutdownProbe{entered: make(chan struct{}), exited: make(chan struct{})}
	cleaner := NewResourceCleaner()
	startDocumentReconciliation(p, cleaner)
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("no startup reconciliation")
	}
	require.Empty(t, cleaner.Cleanup(context.Background()))
	require.True(t, p.stopped, "Lite and Standard must cancel active dispatcher contexts before DB cleanup")
	require.True(t, p.ordered, "stop the replenisher before stopping its workers")
}

func TestDocumentReconciliationRunsAtStartupAndStops(t *testing.T) {
	var calls atomic.Int64
	started := make(chan struct{}, 1)
	stop := runDocumentReconciliation(func(context.Context) error {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		return nil
	}, time.Millisecond)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no startup reconciliation")
	}
	require.Eventually(t, func() bool { return calls.Load() >= 2 }, time.Second, time.Millisecond)
	require.NoError(t, stop())
	count := calls.Load()
	time.Sleep(5 * time.Millisecond)
	require.Equal(t, count, calls.Load(), "shutdown waits for the background loop")
	require.NoError(t, stop(), "cleanup is idempotent")
}

func TestDocumentReconciliationCancelsInFlightSweep(t *testing.T) {
	entered := make(chan struct{})
	stop := runDocumentReconciliation(func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}, time.Hour)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no startup reconciliation")
	}
	require.NoError(t, stop())
}
