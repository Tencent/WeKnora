package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCreateAndAssignReparseSubmissionAllowsDisjointGroupsAndRejectsOverlap(t *testing.T) {
	ctx := context.Background()
	db := setupKnowledgeTestDB(t)
	require.NoError(
		t,
		db.AutoMigrate(
			&types.Knowledge{},
			&types.ReparseBatch{},
			&types.ReparseBatchMember{},
			&types.ReparseBatchSubmission{},
		),
	)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	batches, submissions := repo.ReparseBatchStore(), repo.ReparseSubmissionStore()
	for _, id := range []string{"a", "b", "c"} {
		require.NoError(
			t,
			db.Create(
				&types.Knowledge{ID: id, TenantID: 1, KnowledgeBaseID: "kb", Type: "file", Title: id, Source: "test"},
			).Error,
		)
	}
	batch := &types.ReparseBatch{
		ID:              "batch",
		TenantID:        1,
		KnowledgeBaseID: "kb",
		Mode:            "managed",
		ManifestSHA256:  "manifest",
	}
	require.NoError(t, batches.CreateReparseBatch(ctx, batch, []string{"a", "b", "c"}))

	first := &types.ReparseBatchSubmission{
		ID:            "one",
		BatchID:       batch.ID,
		TenantID:      1,
		RequestSHA256: "one",
		State:         "created",
		TaskID:        "task-one",
	}
	second := &types.ReparseBatchSubmission{
		ID:            "two",
		BatchID:       batch.ID,
		TenantID:      1,
		RequestSHA256: "two",
		State:         "created",
		TaskID:        "task-two",
	}
	require.NoError(t, submissions.CreateAndAssignReparseSubmission(ctx, first, []string{"a", "b"}))
	require.NoError(t, submissions.CreateAndAssignReparseSubmission(ctx, second, []string{"c"}))

	overlap := &types.ReparseBatchSubmission{
		ID:            "three",
		BatchID:       batch.ID,
		TenantID:      1,
		RequestSHA256: "three",
		State:         "created",
		TaskID:        "task-three",
	}
	require.ErrorIs(
		t,
		submissions.CreateAndAssignReparseSubmission(ctx, overlap, []string{"b", "c"}),
		ErrReparseSubmissionMembersUnavailable,
	)
	var count int64
	require.NoError(t, db.Model(&types.ReparseBatchSubmission{}).Count(&count).Error)
	require.Equal(t, int64(2), count, "failed overlap rolls back its receipt")

	var members []types.ReparseBatchMember
	require.NoError(t, db.Where("batch_id = ?", batch.ID).Order("knowledge_id").Find(&members).Error)
	require.Equal(
		t,
		[]string{"one", "one", "two"},
		[]string{members[0].SubmissionID, members[1].SubmissionID, members[2].SubmissionID},
	)
}

func TestClaimReparseMemberDoesNotReplaySubmittedOrAmbiguousWork(t *testing.T) {
	ctx := context.Background()
	db := setupKnowledgeTestDB(t)
	require.NoError(
		t,
		db.AutoMigrate(
			&types.Knowledge{},
			&types.ReparseBatch{},
			&types.ReparseBatchMember{},
			&types.ReparseBatchSubmission{},
		),
	)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	batches, submissions := repo.ReparseBatchStore(), repo.ReparseSubmissionStore()
	require.NoError(
		t,
		db.Create(
			&types.Knowledge{ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", Type: "file", Title: "doc", Source: "test"},
		).Error,
	)
	batch := &types.ReparseBatch{
		ID:              "batch",
		TenantID:        1,
		KnowledgeBaseID: "kb",
		Mode:            "managed",
		ManifestSHA256:  "manifest",
	}
	require.NoError(t, batches.CreateReparseBatch(ctx, batch, []string{"doc"}))
	receipt := &types.ReparseBatchSubmission{
		ID:            "receipt",
		BatchID:       batch.ID,
		TenantID:      1,
		RequestSHA256: "hash",
		State:         "queued",
		TaskID:        "task",
	}
	require.NoError(t, submissions.CreateAndAssignReparseSubmission(ctx, receipt, []string{"doc"}))

	state, err := submissions.ClaimReparseMember(ctx, batch.ID, "doc", receipt.ID)
	require.NoError(t, err)
	require.Equal(t, "claimed", state)
	state, err = submissions.ClaimReparseMember(ctx, batch.ID, "doc", receipt.ID)
	require.NoError(t, err)
	require.Equal(t, "submitting", state, "crash after durable claim must not reparse again")
	require.NoError(t, batches.SetReparseMemberState(ctx, batch.ID, "doc", "submitted", ""))
	state, err = submissions.ClaimReparseMember(ctx, batch.ID, "doc", receipt.ID)
	require.NoError(t, err)
	require.Equal(t, "submitted", state)
}

func TestQueueReceiptStateDoesNotOverwriteFastWorker(t *testing.T) {
	ctx := context.Background()
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.ReparseBatchSubmission{}))
	store := NewKnowledgeRepository(db).(*knowledgeRepository).ReparseSubmissionStore()
	receipt := &types.ReparseBatchSubmission{
		ID:            "receipt",
		BatchID:       "batch",
		TenantID:      1,
		RequestSHA256: "hash",
		State:         "created",
		TaskID:        "task",
	}
	require.NoError(t, store.CreateReparseSubmission(ctx, receipt))
	require.NoError(t, store.MarkReparseSubmissionProcessing(ctx, receipt.ID))
	require.NoError(t, store.MarkReparseSubmissionQueued(ctx, receipt.ID, receipt.TaskID))

	var got types.ReparseBatchSubmission
	require.NoError(t, db.First(&got, "id = ?", receipt.ID).Error)
	require.Equal(t, "processing", got.State, "late enqueue acknowledgement cannot regress worker state")
	require.NoError(t, store.SetReparseSubmissionState(ctx, receipt.ID, "completed", ""))
	require.NoError(t, store.MarkReparseSubmissionProcessing(ctx, receipt.ID))
	require.NoError(t, db.First(&got, "id = ?", receipt.ID).Error)
	require.Equal(t, "completed", got.State, "late duplicate worker cannot reactivate a terminal receipt")
}

func TestCreateAndAssignRejectsOverlappingAutomaticBatches(t *testing.T) {
	ctx := context.Background()
	db := setupKnowledgeTestDB(t)
	require.NoError(
		t,
		db.AutoMigrate(
			&types.Knowledge{},
			&types.ReparseBatch{},
			&types.ReparseBatchMember{},
			&types.ReparseBatchSubmission{},
		),
	)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	batches, submissions := repo.ReparseBatchStore(), repo.ReparseSubmissionStore()
	require.NoError(
		t,
		db.Create(
			&types.Knowledge{ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", Type: "file", Title: "doc", Source: "test"},
		).Error,
	)
	firstBatch := &types.ReparseBatch{
		ID:              "auto-one",
		TenantID:        1,
		KnowledgeBaseID: "kb",
		Mode:            "managed",
		ManifestSHA256:  "one",
	}
	secondBatch := &types.ReparseBatch{
		ID:              "auto-two",
		TenantID:        1,
		KnowledgeBaseID: "kb",
		Mode:            "managed",
		ManifestSHA256:  "two",
	}
	require.NoError(t, batches.CreateReparseBatch(ctx, firstBatch, []string{"doc"}))
	require.NoError(t, batches.CreateReparseBatch(ctx, secondBatch, []string{"doc"}))
	require.NoError(
		t,
		submissions.CreateAndAssignReparseSubmission(
			ctx,
			&types.ReparseBatchSubmission{
				ID:            "first",
				BatchID:       firstBatch.ID,
				TenantID:      1,
				RequestSHA256: "one",
				State:         "created",
			},
			[]string{"doc"},
		),
	)
	require.ErrorIs(
		t,
		submissions.CreateAndAssignReparseSubmission(
			ctx,
			&types.ReparseBatchSubmission{
				ID:            "second",
				BatchID:       secondBatch.ID,
				TenantID:      1,
				RequestSHA256: "two",
				State:         "created",
			},
			[]string{"doc"},
		),
		ErrReparseSubmissionMembersUnavailable,
	)
}

func TestReceiptMemberTransitionsExposeQueuedUnknownAndLateWorkerRecovery(t *testing.T) {
	ctx := context.Background()
	db := setupKnowledgeTestDB(t)
	require.NoError(
		t,
		db.AutoMigrate(
			&types.Knowledge{},
			&types.ReparseBatch{},
			&types.ReparseBatchMember{},
			&types.ReparseBatchSubmission{},
		),
	)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	batches, submissions := repo.ReparseBatchStore(), repo.ReparseSubmissionStore()
	for _, id := range []string{"queued", "unknown", "old-failed"} {
		require.NoError(
			t,
			db.Create(
				&types.Knowledge{
					ID:              id,
					TenantID:        1,
					KnowledgeBaseID: "kb",
					Type:            "file",
					Title:           id,
					Source:          "test",
					ParseStatus:     types.ParseStatusFailed,
				},
			).Error,
		)
	}
	batch := &types.ReparseBatch{ID: "batch", TenantID: 1, KnowledgeBaseID: "kb", Mode: "managed", ManifestSHA256: "m"}
	require.NoError(t, batches.CreateReparseBatch(ctx, batch, []string{"queued", "unknown", "old-failed"}))
	receipts := []*types.ReparseBatchSubmission{
		{ID: "q", BatchID: batch.ID, TenantID: 1, RequestSHA256: "q", State: "created"},
		{ID: "u", BatchID: batch.ID, TenantID: 1, RequestSHA256: "u", State: "created"},
	}
	for _, row := range receipts {
		require.NoError(
			t,
			submissions.CreateAndAssignReparseSubmission(
				ctx,
				row,
				[]string{map[string]string{"q": "queued", "u": "unknown"}[row.ID]},
			),
		)
	}
	require.NoError(t, submissions.MarkReparseSubmissionQueued(ctx, "q", "task-q"))
	require.NoError(t, submissions.MarkReparseSubmissionEnqueueUnknown(ctx, "u", "task-u"))
	view, err := batches.GetReparseBatchView(ctx, 1, batch.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), view.Counts.Queued)
	require.Equal(t, int64(1), view.Counts.Unknown)
	require.NoError(t, submissions.MarkReparseSubmissionProcessing(ctx, "u"))
	claim, err := submissions.ClaimReparseMember(ctx, batch.ID, "unknown", "u")
	require.NoError(t, err)
	require.Equal(t, "claimed", claim, "a worker delivered after an ambiguous enqueue can safely dispatch once")
}

func TestScopeFailureTerminalizesReceiptAndReleasesActiveOverlap(t *testing.T) {
	ctx := context.Background()
	db := setupKnowledgeTestDB(t)
	require.NoError(
		t,
		db.AutoMigrate(
			&types.Knowledge{},
			&types.ReparseBatch{},
			&types.ReparseBatchMember{},
			&types.ReparseBatchSubmission{},
		),
	)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	batches, submissions := repo.ReparseBatchStore(), repo.ReparseSubmissionStore()
	require.NoError(
		t,
		db.Create(
			&types.Knowledge{ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", Type: "file", Title: "doc", Source: "test"},
		).Error,
	)
	batch := &types.ReparseBatch{ID: "batch", TenantID: 1, KnowledgeBaseID: "kb", Mode: "managed", ManifestSHA256: "m"}
	require.NoError(t, batches.CreateReparseBatch(ctx, batch, []string{"doc"}))
	receipt := &types.ReparseBatchSubmission{
		ID:            "receipt",
		BatchID:       batch.ID,
		TenantID:      1,
		RequestSHA256: "hash",
		State:         "queued",
	}
	require.NoError(t, submissions.CreateAndAssignReparseSubmission(ctx, receipt, []string{"doc"}))
	require.NoError(t, submissions.FailReparseSubmissionScope(ctx, receipt.ID))
	active, err := submissions.HasActiveReparseSubmissionOverlap(ctx, 1, []string{"doc"}, "")
	require.NoError(t, err)
	require.False(t, active)
	var member types.ReparseBatchMember
	require.NoError(t, db.Where("batch_id = ? AND knowledge_id = ?", batch.ID, "doc").First(&member).Error)
	require.Equal(t, "failed", member.SubmissionState)
}
