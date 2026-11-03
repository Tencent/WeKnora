package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reparseScrubPendingRepo records the wiki pending-op scrub in the shared
// event log so a test can see where it falls relative to dequeue and
// OpenAttempt.
type reparseScrubPendingRepo struct {
	interfaces.TaskPendingOpsRepository
	events *[]string
	ops    []*types.TaskPendingOp
}

func (r *reparseScrubPendingRepo) Enqueue(_ context.Context, op *types.TaskPendingOp) error {
	r.ops = append(r.ops, op)
	*r.events = append(*r.events, "pending-enqueue")
	return nil
}

func (r *reparseScrubPendingRepo) DeleteByDedupKey(_ context.Context, _, _, _, dedupKey, op string) error {
	*r.events = append(*r.events, "scrub:"+op+":"+dedupKey)
	return nil
}

type failingCleanupChunkRepo struct {
	replaceFileChunks
	err error
}

func (r failingCleanupChunkRepo) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	return r.err
}

// newReparseHarness reuses the replace-file harness (single-row repo, event
// log, recording inspector and enqueuer) with a tracker and a wiki pending
// repo that write into the same event log.
func newReparseHarness(t *testing.T, status string) *replaceFileHarness {
	t.Helper()
	h := newReplaceFileHarness(t)
	h.repo.row.ParseStatus = status
	tracker := newAttemptTracker()
	tracker.events = &h.events
	h.svc.spanTracker = tracker
	h.svc.taskPendingRepo = &reparseScrubPendingRepo{events: &h.events}
	return h
}

// A rejected reparse must leave the previous run alone: opening a newer
// attempt made that run's subtasks drop themselves as superseded without
// draining, stranding the row in "finalizing".
func TestReparseKnowledgeRejectedLeavesPreviousRunUntouched(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(h *replaceFileHarness)
		overrides *types.KnowledgeProcessOverrides
		wantErr   string
	}{
		{
			name: "override validation fails",
			setup: func(h *replaceFileHarness) {
				// An image needs a VLM model, which the KB does not have.
				h.repo.row.FileName = "a.png"
				h.repo.row.FileType = "png"
			},
			overrides: &types.KnowledgeProcessOverrides{},
			wantErr:   "VLM",
		},
		{
			name: "resource cleanup fails",
			setup: func(h *replaceFileHarness) {
				h.svc.chunkRepo = failingCleanupChunkRepo{err: errors.New("database unavailable")}
			},
			wantErr: "database unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newReparseHarness(t, types.ParseStatusFinalizing)
			test.setup(h)

			_, err := h.svc.ReparseKnowledge(h.ctx, h.original.ID, test.overrides)

			require.ErrorContains(t, err, test.wantErr)
			assert.Empty(t, h.events, "no dequeue, no new attempt, no wiki scrub")
			assert.Zero(t, h.svc.spanTracker.(*attemptTracker).opened)
			assert.Equal(t, types.ParseStatusFinalizing, h.repo.row.ParseStatus)
			assert.Empty(t, h.tasks.payloads)
		})
	}
}

// A reparse over a run that may still own queued tasks drops them before it
// announces the new attempt; a finished run has nothing to drop.
func TestReparseKnowledgeDequeuesOnlyInFlightRuns(t *testing.T) {
	scrub := "scrub:" + WikiOpIngest + ":knowledge-1"
	tests := []struct {
		status string
		want   []string
	}{
		{types.ParseStatusPending, []string{"dequeue:knowledge-1", "open-attempt", scrub, "pending-enqueue", "dispatch"}},
		{types.ParseStatusProcessing, []string{"dequeue:knowledge-1", "open-attempt", scrub, "pending-enqueue", "dispatch"}},
		{types.ParseStatusFinalizing, []string{"dequeue:knowledge-1", "open-attempt", scrub, "pending-enqueue", "dispatch"}},
		{types.ParseStatusCompleted, []string{"open-attempt", scrub, "pending-enqueue", "dispatch"}},
		{types.ParseStatusFailed, []string{"open-attempt", scrub, "pending-enqueue", "dispatch"}},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			h := newReparseHarness(t, test.status)

			_, err := h.svc.ReparseKnowledge(h.ctx, h.original.ID, nil)

			require.NoError(t, err)
			assert.Equal(t, test.want, h.events)
			pending := h.svc.taskPendingRepo.(*reparseScrubPendingRepo)
			require.Len(t, pending.ops, 1)
			var payload types.DocumentProcessPayload
			require.NoError(t, json.Unmarshal(pending.ops[0].Payload, &payload))
			assert.Equal(t, 1, payload.Attempt, "the pending op carries the attempt opened for it")
			require.Len(t, h.tasks.dispatches, 1)
			assert.Equal(t, types.ParseStatusPending, h.repo.row.ParseStatus)
		})
	}
}

// ReplaceKnowledgeFile already dropped the queued tasks before swapping the
// file, so its reparse must not scan the queues a second time.
func TestReplaceKnowledgeFileDequeuesInFlightParseOnce(t *testing.T) {
	h := newReparseHarness(t, types.ParseStatusProcessing)

	_, err := h.replace(t, "# new body", "notes/a.md", nil)

	require.NoError(t, err)
	dequeues := 0
	for _, event := range h.events {
		if event == "dequeue:knowledge-1" {
			dequeues++
		}
	}
	assert.Equal(t, 1, dequeues)
}

// An ingest op left by an earlier wiki-enabled run would drain a slot of the
// new attempt, so the scrub runs even when the KB has wiki turned off now.
func TestReparseKnowledgeScrubsWikiOpWhenWikiDisabled(t *testing.T) {
	h := newReparseHarness(t, types.ParseStatusCompleted)
	kb, err := h.svc.kbService.GetKnowledgeBaseByID(h.ctx, "kb-1")
	require.NoError(t, err)
	require.False(t, kb.IsWikiEnabled())

	_, err = h.svc.ReparseKnowledge(h.ctx, h.original.ID, nil)

	require.NoError(t, err)
	assert.Contains(t, h.events, "scrub:"+WikiOpIngest+":knowledge-1")
}
