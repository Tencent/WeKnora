package service

import (
	"context"
	"errors"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
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
		name        string
		setup       func(h *replaceFileHarness)
		overrides   *types.KnowledgeProcessOverrides
		wantErr     string
		startStatus string
		endStatus   string
	}{
		{
			name: "override validation fails",
			setup: func(h *replaceFileHarness) {
				// An image needs a VLM model, which the KB does not have.
				h.repo.row.FileName = "a.png"
				h.repo.row.FileType = "png"
			},
			overrides: &types.KnowledgeProcessOverrides{},
			wantErr:   "VLM", startStatus: types.ParseStatusFinalizing, endStatus: types.ParseStatusFinalizing,
		},
		{
			name: "resource cleanup fails",
			setup: func(h *replaceFileHarness) {
				h.svc.chunkRepo = failingCleanupChunkRepo{err: errors.New("database unavailable")}
			},
			wantErr: "database unavailable", startStatus: types.ParseStatusCompleted,
			endStatus: types.ParseStatusFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newReparseHarness(t, test.startStatus)
			test.setup(h)

			_, err := h.svc.ReparseKnowledge(h.ctx, h.original.ID, test.overrides)

			require.ErrorContains(t, err, test.wantErr)
			assert.Empty(t, h.events, "no dequeue, no new attempt, no wiki scrub")
			assert.Zero(t, h.svc.spanTracker.(*attemptTracker).opened)
			assert.Equal(t, test.endStatus, h.repo.row.ParseStatus)
			assert.Empty(t, h.tasks.payloads)
		})
	}
}

// Active runs reject a new reparse without cancelling their work. Finished
// runs can claim a new attempt and enqueue it.
func TestReparseKnowledgeDequeuesOnlyInFlightRuns(t *testing.T) {
	scrub := "scrub:" + WikiOpIngest + ":knowledge-1"
	tests := []struct {
		status string
		want   []string
	}{
		{types.ParseStatusPending, []string{"dequeue:knowledge-1", "open-attempt", scrub, "enqueue"}},
		{types.ParseStatusProcessing, []string{"dequeue:knowledge-1", "open-attempt", scrub, "enqueue"}},
		{types.ParseStatusFinalizing, []string{"dequeue:knowledge-1", "open-attempt", scrub, "enqueue"}},
		{types.ParseStatusCompleted, []string{"open-attempt", scrub, "enqueue"}},
		{types.ParseStatusFailed, []string{"open-attempt", scrub, "enqueue"}},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			h := newReparseHarness(t, test.status)

			_, err := h.svc.ReparseKnowledge(h.ctx, h.original.ID, nil)
			if test.status == types.ParseStatusPending ||
				test.status == types.ParseStatusProcessing ||
				test.status == types.ParseStatusFinalizing {
				var appErr *apperrors.AppError
				require.ErrorAs(t, err, &appErr)
				require.Equal(t, 409, appErr.HTTPCode)
				require.Empty(t, h.events, "busy admission must not cancel or clean another attempt")
				require.Empty(t, h.tasks.payloads)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, h.events)
			require.Len(t, h.tasks.payloads, 1)
			assert.Equal(t, 1, h.tasks.payloads[0].Attempt, "the task carries the attempt opened for it")
			assert.Equal(t, types.ParseStatusPending, h.repo.row.ParseStatus)
		})
	}
}

// Replacement cannot take ownership of an active parse or cancel its tasks.
func TestReplaceKnowledgeFileRejectsInFlightParseBeforeDequeue(t *testing.T) {
	h := newReparseHarness(t, types.ParseStatusProcessing)
	_, err := h.replace(t, "# new body", "notes/a.md", nil)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, 409, appErr.HTTPCode)
	for _, event := range h.events {
		assert.NotEqual(t, "dequeue:knowledge-1", event)
	}
	assert.Equal(t, types.ParseStatusProcessing, h.repo.row.ParseStatus)
	assert.Empty(t, h.tasks.payloads)
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
