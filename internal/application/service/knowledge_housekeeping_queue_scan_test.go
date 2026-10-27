package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queuePassCountingInspector counts every walk over the queues. Against asynq
// a per-document probe pages through the queues until it finds the document,
// so with a large backlog each probe costs about as much as a full scan.
type queuePassCountingInspector struct {
	countingTaskInspector
	probes int
}

func (f *queuePassCountingInspector) HasQueuedTasksForKnowledge(
	_ context.Context, knowledgeID string,
) (bool, error) {
	f.probes++
	if f.err != nil {
		return false, f.err
	}
	return f.queued[knowledgeID], nil
}

func (f *queuePassCountingInspector) passes() int { return f.scans + f.probes }

// Under a large import every document waiting for enrichment goes stale at
// once. Probing the queues per document made one sweep cost candidates ×
// queue length and kept Redis saturated for hours; a sweep must walk the
// queues at most once however many rows it checks.
func TestHousekeeping_SweepScansQueuesOnce(t *testing.T) {
	db := setupHousekeepingDB(t)
	inspector := &queuePassCountingInspector{}
	inspector.queued = map[string]bool{}
	svc := newHousekeepingSvcWithInspector(db, inspector)
	stale := time.Now().Add(-3 * time.Hour)

	const rows = 20
	for i := 0; i < rows; i++ {
		backlogged := fmt.Sprintf("k-backlogged-%d", i)
		insertKnowledge(t, db, backlogged, types.ParseStatusFinalizing, stale)
		inspector.queued[backlogged] = true
		insertKnowledge(t, db, fmt.Sprintf("k-orphan-%d", i), types.ParseStatusProcessing, stale)

		queuedSummary := fmt.Sprintf("k-summary-queued-%d", i)
		insertKnowledgeWithSummary(t, db, queuedSummary,
			types.ParseStatusCompleted, types.SummaryStatusPending, stale)
		inspector.queued[queuedSummary] = true
		insertKnowledgeWithSummary(t, db, fmt.Sprintf("k-summary-lost-%d", i),
			types.ParseStatusCompleted, types.SummaryStatusPending, stale)
	}

	svc.runSweep(context.Background())

	assert.LessOrEqual(t, inspector.passes(), 1,
		"one sweep walks the queues at most once (scans=%d, per-row probes=%d)",
		inspector.scans, inspector.probes)
	for i := 0; i < rows; i++ {
		status, _ := readKnowledgeStatus(t, db, fmt.Sprintf("k-backlogged-%d", i))
		assert.Equal(t, types.ParseStatusFinalizing, status, "backlogged row %d", i)
		status, _ = readKnowledgeStatus(t, db, fmt.Sprintf("k-orphan-%d", i))
		assert.Equal(t, types.ParseStatusFailed, status, "orphaned row %d", i)
		assert.Equal(t, types.SummaryStatusPending,
			readSummaryStatus(t, db, fmt.Sprintf("k-summary-queued-%d", i)), "queued summary %d", i)
		assert.Equal(t, types.SummaryStatusFailed,
			readSummaryStatus(t, db, fmt.Sprintf("k-summary-lost-%d", i)), "lost summary %d", i)
	}
}

// When the queue scan fails, Sweep A keeps treating its candidates as stuck
// (TestHousekeeping_QueueProbeError_FailsSafe), but a pending summary is only
// a spinner and waits for the next sweep instead of being failed blind.
func TestHousekeeping_QueueScanErrorDefersStrandedSummaries(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcWithInspector(db, fakeTaskInspector{err: errors.New("redis unavailable")})
	stale := time.Now().Add(-2 * time.Hour)
	insertKnowledgeWithSummary(t, db, "k-summary", types.ParseStatusCompleted, types.SummaryStatusPending, stale)

	svc.recoverStrandedPendingSummaries(context.Background(), time.Now().Add(-time.Hour))

	assert.Equal(t, types.SummaryStatusPending, readSummaryStatus(t, db, "k-summary"))
}

// The sweep must not walk the queues when it has nothing to check: no stale
// row, or only rows the durable Wiki table already holds.
func TestHousekeeping_NoQueueScanWithoutCandidates(t *testing.T) {
	db := setupHousekeepingDB(t)
	inspector := &queuePassCountingInspector{}
	svc := newHousekeepingSvcWithInspector(db, inspector)
	insertKnowledge(t, db, "k-fresh", types.ParseStatusProcessing, time.Now())
	insertKnowledge(t, db, "k-wiki", types.ParseStatusFinalizing, time.Now().Add(-3*time.Hour))
	insertWikiPendingOp(t, db, "kb-1", "k-wiki")

	svc.runSweep(context.Background())

	require.Zero(t, inspector.passes())
	status, _ := readKnowledgeStatus(t, db, "k-wiki")
	assert.Equal(t, types.ParseStatusFinalizing, status)
}
