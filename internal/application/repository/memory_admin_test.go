package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type memoryAdminWrites interface {
	SaveItemWithPendingLimit(context.Context, interfaces.MemoryScope, *types.MemoryItem, string, int) error
	ClearSubjectMemory(context.Context, interfaces.MemoryScope) (int64, error)
}

type memoryAdminReads interface {
	ListSubjects(context.Context, uint64, int, int) ([]*types.MemorySubjectSummary, int64, error)
	GetSubjectByID(context.Context, uint64, string) (*types.MemorySubject, error)
}

type memoryPendingRejecter interface {
	RejectPendingItem(context.Context, interfaces.MemoryScope, string) error
}

func TestMemoryAdminRejectPendingAtomic(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure_%t", fail), func(t *testing.T) {
			repo, db := newMemoryAdminHarness(t)
			rejecter, ok := any(repo).(memoryPendingRejecter)
			require.True(t, ok, "pending rejection must validate and delete inside one subject transaction")
			scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:a"}
			_, err := repo.EnsureSubject(t.Context(), scope)
			require.NoError(t, err)
			proposal := adminItem("proposal", types.MemoryStatusPending, time.Now())
			proposal.TenantID, proposal.SubjectID = scope.TenantID, scope.SubjectID
			proposal.Topic, proposal.SourceMessageID = "topic", "message"
			require.NoError(t, repo.CreateItem(t.Context(), proposal))
			dependent := adminItem("dependent", types.MemoryStatusPending, time.Now())
			dependent.TenantID, dependent.SubjectID, dependent.ReplacesID = scope.TenantID, scope.SubjectID, proposal.ID
			require.NoError(t, repo.CreateItem(t.Context(), dependent))
			require.NoError(t, db.Create(&types.MemoryItemEmbedding{
				ItemID: proposal.ID, TenantID: scope.TenantID, SubjectID: scope.SubjectID,
			}).Error)
			if fail {
				require.NoError(t, db.Exec(`CREATE TRIGGER fail_rejection BEFORE DELETE ON memory_items
					BEGIN SELECT RAISE(ABORT, 'injected rejection failure'); END`).Error)
			}
			err = rejecter.RejectPendingItem(t.Context(), scope, proposal.ID)
			if fail {
				require.ErrorContains(t, err, "injected rejection failure")
			} else {
				require.NoError(t, err)
			}
			got, err := repo.GetItem(t.Context(), scope, proposal.ID)
			require.NoError(t, err)
			deps, err := repo.GetItem(t.Context(), scope, dependent.ID)
			require.NoError(t, err)
			tombstones, err := repo.ListTombstones(t.Context(), scope, 10)
			require.NoError(t, err)
			var vectors int64
			require.NoError(t, db.Model(&types.MemoryItemEmbedding{}).Count(&vectors).Error)
			if fail {
				require.NotNil(t, got)
				require.Equal(t, types.MemoryStatusPending, deps.Status)
				require.Empty(t, tombstones)
				require.EqualValues(t, 1, vectors)
				return
			}
			require.Nil(t, got)
			require.Equal(t, types.MemoryStatusSuperseded, deps.Status)
			require.Zero(t, vectors)
			require.Len(t, tombstones, 1)
			require.Equal(t, proposal.Topic, tombstones[0].Topic)
			require.Equal(t, proposal.SourceMessageID, tombstones[0].SourceMessageID)
			require.Equal(t, types.MemoryFingerprint(proposal.Content), tombstones[0].Fingerprint)
			require.ErrorIs(t, repo.ConfirmPendingItem(t.Context(), scope, proposal.ID), gorm.ErrRecordNotFound)
		})
	}
}

func TestMemoryAdminRejectRechecksStatusAndScope(t *testing.T) {
	repo, _ := newMemoryAdminHarness(t)
	rejecter, ok := any(repo).(memoryPendingRejecter)
	require.True(t, ok)
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:a"}
	other := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:other"}
	foreign := interfaces.MemoryScope{TenantID: 2, SubjectID: scope.SubjectID}
	for _, s := range []interfaces.MemoryScope{scope, other, foreign} {
		_, err := repo.EnsureSubject(t.Context(), s)
		require.NoError(t, err)
	}
	item := adminItem("proposal", types.MemoryStatusPending, time.Now())
	require.NoError(t, repo.SaveItem(t.Context(), scope, item, ""))
	for _, s := range []interfaces.MemoryScope{other, foreign} {
		require.ErrorIs(t, rejecter.RejectPendingItem(t.Context(), s, item.ID), gorm.ErrRecordNotFound)
	}
	// The administrator may have read pending before confirmation committed.
	// Rejection must recheck the persisted state while holding the subject lock.
	require.NoError(t, repo.ConfirmPendingItem(t.Context(), scope, item.ID))
	require.ErrorIs(t, rejecter.RejectPendingItem(t.Context(), scope, item.ID), types.ErrMemoryConflict)
	got, err := repo.GetItem(t.Context(), scope, item.ID)
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusActive, got.Status)
	tombstones, err := repo.ListTombstones(t.Context(), scope, 10)
	require.NoError(t, err)
	require.Empty(t, tombstones)
}

func TestMemoryAdminListAndLookup(t *testing.T) {
	repo, db := newMemoryAdminHarness(t)
	reads, ok := any(repo).(memoryAdminReads)
	require.True(t, ok, "repository must expose tenant-scoped subject summaries and UUID lookup")
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	for _, subject := range []*types.MemorySubject{
		{ID: "b", TenantID: 1, SubjectID: "im_user:channel/someone", UpdatedAt: now},
		{ID: "a", TenantID: 1, SubjectID: "web_user:alice", UpdatedAt: now},
		{ID: "foreign", TenantID: 2, SubjectID: "web_user:alice", UpdatedAt: now.Add(time.Hour)},
	} {
		require.NoError(t, db.Create(subject).Error)
	}
	require.NoError(t, db.Model(&types.MemorySubject{}).Where("id = ?", "a").
		Updates(map[string]any{"enabled": false, "updated_at": now}).Error)
	for i, status := range []string{
		types.MemoryStatusActive, types.MemoryStatusPending, types.MemoryStatusArchived, types.MemoryStatusSuperseded,
	} {
		item := adminItem(fmt.Sprintf("a-%d", i), status, now)
		item.TenantID, item.SubjectID, item.UpdatedAt = 1, "web_user:alice", now
		require.NoError(t, db.Create(item).Error)
	}
	foreignItem := adminItem("foreign-item", types.MemoryStatusActive, now.Add(2*time.Hour))
	foreignItem.TenantID, foreignItem.SubjectID = 2, "web_user:alice"
	require.NoError(t, db.Create(foreignItem).Error)
	first, total, err := reads.ListSubjects(ctx, 1, 1, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, first, 1)
	require.Equal(t, "b", first[0].ID, "equal activity timestamps use UUID descending as a stable tie-breaker")
	require.Zero(t, first[0].ItemCount)
	require.Equal(t, types.PrincipalIMUser, first[0].PrincipalType)
	second, total, err := reads.ListSubjects(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, second, 1)
	require.Equal(t, "a", second[0].ID)
	require.False(t, second[0].Enabled)
	require.EqualValues(t, 4, second[0].ItemCount)
	require.EqualValues(t, 1, second[0].ActiveCount)
	require.EqualValues(t, 1, second[0].PendingCount)
	require.Equal(t, types.PrincipalWebUser, second[0].PrincipalType)
	require.Equal(t, now, second[0].LastActivityAt)
	all, _, err := reads.ListSubjects(ctx, 1, 0, -1)
	require.NoError(t, err)
	require.Len(t, all, 2)
	for _, id := range []string{"foreign", "missing", "web_user:alice"} {
		subject, e := reads.GetSubjectByID(ctx, 1, id)
		require.NoError(t, e)
		require.Nil(t, subject)
	}
	subject, err := reads.GetSubjectByID(ctx, 1, "a")
	require.NoError(t, err)
	require.Equal(t, "web_user:alice", subject.SubjectID)
	empty, n, err := reads.ListSubjects(ctx, 999, 50, 0)
	require.NoError(t, err)
	require.Empty(t, empty)
	require.Zero(t, n)
	later := now.Add(time.Hour)
	require.NoError(t, db.Model(&types.MemoryItem{}).Where("id = ?", "a-0").Update("updated_at", later).Error)
	newest, _, err := reads.ListSubjects(ctx, 1, 1, 0)
	require.NoError(t, err)
	require.Equal(t, "a", newest[0].ID)
	require.Equal(t, later, newest[0].LastActivityAt)
}

func TestMemoryAdminPaginationBounds(t *testing.T) {
	repo, db := newMemoryAdminHarness(t)
	reads, ok := any(repo).(memoryAdminReads)
	require.True(t, ok)
	now := time.Now().UTC().Truncate(time.Second)
	subjects := make([]types.MemorySubject, 201)
	for i := range subjects {
		subjects[i] = types.MemorySubject{
			ID: fmt.Sprintf("subject-%03d", i), TenantID: 1,
			SubjectID: fmt.Sprintf("web_user:%03d", i), UpdatedAt: now,
		}
	}
	require.NoError(t, db.CreateInBatches(subjects, 50).Error)
	for _, tc := range []struct{ limit, offset, count int }{{0, -1, 50}, {999, 0, 200}, {200, 200, 1}} {
		rows, total, err := reads.ListSubjects(t.Context(), 1, tc.limit, tc.offset)
		require.NoError(t, err)
		require.EqualValues(t, 201, total)
		require.Len(t, rows, tc.count)
	}
}

func TestMemoryAdminSaveItemDefaultPendingLimit(t *testing.T) {
	repo, db := newMemoryAdminHarness(t)
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:a"}
	_, err := repo.EnsureSubject(t.Context(), scope)
	require.NoError(t, err)
	now := time.Now().Add(-time.Hour)
	items := make([]types.MemoryItem, types.DefaultMemoryMaxItems)
	for i := range items {
		createdAt := now.Add(time.Duration(i) * time.Second)
		items[i] = *adminItem(fmt.Sprintf("p-%03d", i), types.MemoryStatusPending, createdAt)
		items[i].TenantID, items[i].SubjectID = scope.TenantID, scope.SubjectID
	}
	require.NoError(t, db.CreateInBatches(items, 20).Error)
	require.NoError(t, repo.SaveItem(t.Context(), scope, adminItem("new", types.MemoryStatusPending, time.Now()), ""))
	_, pending, err := repo.ListItems(t.Context(), scope, types.MemoryStatusPending, 1, 0)
	require.NoError(t, err)
	require.EqualValues(t, types.DefaultMemoryMaxItems, pending)
	oldest, err := repo.GetItem(t.Context(), scope, "p-000")
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusArchived, oldest.Status)
}

func TestMemoryAdminPendingReplayConvergesToReducedLimit(t *testing.T) {
	for _, replayReplacement := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacement_%t", replayReplacement), func(t *testing.T) {
			repo, _ := newMemoryAdminHarness(t)
			writes := adminWrites(t, repo)
			scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:a"}
			_, err := repo.EnsureSubject(t.Context(), scope)
			require.NoError(t, err)
			now := time.Now().Add(-time.Hour)
			for i := 0; i < 3; i++ {
				createdAt := now.Add(time.Duration(i) * time.Second)
				item := adminItem(fmt.Sprintf("p-%d", i), types.MemoryStatusPending, createdAt)
				item.TenantID, item.SubjectID = scope.TenantID, scope.SubjectID
				require.NoError(t, repo.CreateItem(t.Context(), item))
			}
			replacesID := ""
			if replayReplacement {
				old := adminItem("superseded", types.MemoryStatusSuperseded, now)
				old.TenantID, old.SubjectID, old.SupersededBy = scope.TenantID, scope.SubjectID, "p-0"
				require.NoError(t, repo.CreateItem(t.Context(), old))
				replacesID = old.ID
			}
			duplicate := adminItem("duplicate", types.MemoryStatusPending, time.Now())
			duplicate.Content, duplicate.NormalizedKey = "p-0", "p-0"
			require.NoError(t, writes.SaveItemWithPendingLimit(t.Context(), scope, duplicate, replacesID, 1))
			_, count, err := repo.ListItems(t.Context(), scope, types.MemoryStatusPending, 10, 0)
			require.NoError(t, err)
			require.EqualValues(t, 1, count)
			require.Equal(t, "p-0", duplicate.ID)
			require.Equal(t, types.MemoryStatusArchived, duplicate.Status, "returned replay reflects archive result")
		})
	}
}

func newMemoryAdminHarness(t *testing.T) (*memoryRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(
		&types.MemorySubject{}, &types.MemoryItem{}, &types.MemoryItemEmbedding{},
		&types.MemoryTopicStat{}, &types.MemoryDocAffinity{},
		&types.MemoryTombstone{}, &types.MemoryExtractionSession{},
	))
	return &memoryRepository{db: db}, db
}

func adminWrites(t *testing.T, repo *memoryRepository) memoryAdminWrites {
	t.Helper()
	writes, ok := any(repo).(memoryAdminWrites)
	require.True(t, ok, "repository must implement atomic admin clear and bounded pending writes")
	return writes
}

func adminItem(id, status string, at time.Time) *types.MemoryItem {
	return &types.MemoryItem{
		ID: id, Kind: types.MemoryKindFact, Content: id, NormalizedKey: id,
		Status: status, ValidFrom: at, CreatedAt: at,
	}
}

func TestMemoryAdminPendingLimit(t *testing.T) {
	repo, db := newMemoryAdminHarness(t)
	writes := adminWrites(t, repo)
	ctx := t.Context()
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "im_user:some/user"}
	other := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:other"}
	foreign := interfaces.MemoryScope{TenantID: 2, SubjectID: scope.SubjectID}
	for _, s := range []interfaces.MemoryScope{scope, other, foreign} {
		_, err := repo.EnsureSubject(ctx, s)
		require.NoError(t, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	active := adminItem("active", types.MemoryStatusActive, now)
	require.NoError(t, repo.SaveItem(ctx, scope, active, ""))
	for i, s := range []interfaces.MemoryScope{other, foreign} {
		item := adminItem(fmt.Sprintf("isolated-%d", i), types.MemoryStatusPending, now)
		require.NoError(t, writes.SaveItemWithPendingLimit(ctx, s, item, "", 2))
	}
	for i := 0; i < 3; i++ {
		item := adminItem(fmt.Sprintf("p-%d", i), types.MemoryStatusPending, now.Add(time.Duration(i)*time.Second))
		require.NoError(t, writes.SaveItemWithPendingLimit(ctx, scope, item, "", 2))
	}
	oldest, err := repo.GetItem(ctx, scope, "p-0")
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusArchived, oldest.Status)
	pending, total, err := repo.ListItems(ctx, scope, types.MemoryStatusPending, 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Equal(t, "p-2", pending[0].ID)
	require.Equal(t, "p-1", pending[1].ID)
	duplicate := adminItem("duplicate", types.MemoryStatusPending, now.Add(time.Hour))
	duplicate.Content, duplicate.NormalizedKey = "p-2", "p-2"
	require.NoError(t, writes.SaveItemWithPendingLimit(ctx, scope, duplicate, "", 2))
	require.Equal(t, "p-2", duplicate.ID)
	var count int64
	require.NoError(t, db.Model(&types.MemoryItem{}).Count(&count).Error)
	require.EqualValues(t, 6, count)
	for _, s := range []interfaces.MemoryScope{other, foreign} {
		_, n, e := repo.ListItems(ctx, s, types.MemoryStatusPending, 10, 0)
		require.NoError(t, e)
		require.EqualValues(t, 1, n)
	}
	got, err := repo.GetItem(ctx, scope, active.ID)
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusActive, got.Status)
}

func TestMemoryAdminPendingReplacementAndRollback(t *testing.T) {
	repo, db := newMemoryAdminHarness(t)
	writes := adminWrites(t, repo)
	ctx := t.Context()
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:a"}
	_, err := repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	now := time.Now()
	active := adminItem("active", types.MemoryStatusActive, now)
	require.NoError(t, repo.SaveItem(ctx, scope, active, ""))
	first := adminItem("first", types.MemoryStatusPending, now)
	first.NormalizedKey = active.NormalizedKey
	require.NoError(t, writes.SaveItemWithPendingLimit(ctx, scope, first, active.ID, 1))
	second := adminItem("second", types.MemoryStatusPending, now.Add(time.Second))
	second.NormalizedKey = active.NormalizedKey
	require.NoError(t, writes.SaveItemWithPendingLimit(ctx, scope, second, first.ID, 1))
	require.Equal(t, active.ID, second.ReplacesID)
	old, err := repo.GetItem(ctx, scope, first.ID)
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusSuperseded, old.Status)
	require.Equal(t, second.ID, old.SupersededBy)
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_pending_archive BEFORE UPDATE OF status ON memory_items
		WHEN NEW.status = 'archived' BEGIN SELECT RAISE(ABORT, 'injected archive failure'); END`).Error)
	rollback := adminItem("rollback", types.MemoryStatusPending, now.Add(2*time.Second))
	err = writes.SaveItemWithPendingLimit(ctx, scope, rollback, "", 1)
	require.ErrorContains(t, err, "injected archive failure")
	missing, err := repo.GetItem(ctx, scope, "rollback")
	require.NoError(t, err)
	require.Nil(t, missing)
	require.NoError(t, repo.ConfirmPendingItem(ctx, scope, second.ID))
	old, err = repo.GetItem(ctx, scope, active.ID)
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusSuperseded, old.Status)
}

func TestMemoryAdminClearAtomicScopeAndProgress(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("delete_failure_%t", fail), func(t *testing.T) {
			repo, db := newMemoryAdminHarness(t)
			writes := adminWrites(t, repo)
			ctx := t.Context()
			scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "embed_visitor:some/path"}
			other := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:other"}
			foreign := interfaces.MemoryScope{TenantID: 2, SubjectID: scope.SubjectID}
			now := time.Now().UTC().Truncate(time.Second)
			var original *types.MemorySubject
			for i, s := range []interfaces.MemoryScope{scope, other, foreign} {
				subject, err := repo.EnsureSubject(ctx, s)
				require.NoError(t, err)
				if i == 0 {
					original = subject
				}
				id := fmt.Sprintf("item-%d", i)
				require.NoError(t, repo.CreateItem(ctx, &types.MemoryItem{
					ID: id, TenantID: s.TenantID, SubjectID: s.SubjectID, Kind: types.MemoryKindFact,
					Content: "private", Status: types.MemoryStatusActive,
				}))
				for _, row := range []any{
					&types.MemoryItemEmbedding{ItemID: id, TenantID: s.TenantID, SubjectID: s.SubjectID},
					&types.MemoryTopicStat{
						ID: id, TenantID: s.TenantID, SubjectID: s.SubjectID, NormalizedKey: "topic",
					},
					&types.MemoryDocAffinity{ID: id, TenantID: s.TenantID, SubjectID: s.SubjectID, KnowledgeID: "doc"},
					&types.MemoryTombstone{
						ID: id, TenantID: s.TenantID, SubjectID: s.SubjectID, Fingerprint: "fingerprint",
					},
					&types.MemoryExtractionSession{
						TenantID: s.TenantID, SubjectID: s.SubjectID, SessionID: "session", Revision: 7, Pending: true,
						Cursor:       types.MemoryMessageCursor{At: now, ID: "cursor"},
						FailureCount: 2, FailureCode: "invalid",
					},
				} {
					require.NoError(t, db.Create(row).Error)
				}
			}
			lease := types.MemoryExtractionState{LeaseID: "old-lease", LeaseUntil: now.Add(time.Hour)}
			require.NoError(t, db.Model(original).Updates(map[string]any{
				"enabled": false, "block_text": "private block", "item_count": 1,
				"block_updated_at": now, "extract_cursor": now, "last_extracted_at": now, "consolidated_at": now,
				"forced_consolidated_at": now, "extraction_state": lease,
				"pending_sessions": types.MemoryPendingSessions{"legacy"}, "extract_scheduled_at": now,
			}).Error)
			if fail {
				require.NoError(t, db.Exec(`CREATE TRIGGER fail_clear BEFORE DELETE ON memory_items
					BEGIN SELECT RAISE(ABORT, 'injected clear failure'); END`).Error)
			}
			n, err := writes.ClearSubjectMemory(ctx, scope)
			if fail {
				require.ErrorContains(t, err, "injected clear failure")
				require.Zero(t, n)
			} else {
				require.NoError(t, err)
				require.EqualValues(t, 1, n)
			}
			for _, model := range []any{
				&types.MemoryItem{}, &types.MemoryItemEmbedding{}, &types.MemoryTopicStat{},
				&types.MemoryDocAffinity{}, &types.MemoryTombstone{},
			} {
				for _, s := range []interfaces.MemoryScope{scope, other, foreign} {
					var count int64
					require.NoError(t, db.Model(model).
						Where("tenant_id = ? AND subject_id = ?", s.TenantID, s.SubjectID).
						Count(&count).Error)
					want := int64(1)
					if s == scope && !fail {
						want = 0
					}
					if count != want {
						t.Fatalf("%T scope %+v count=%d want=%d", model, s, count, want)
					}
				}
			}
			got, err := repo.GetSubject(ctx, scope)
			require.NoError(t, err)
			require.False(t, got.Enabled)
			require.Equal(t, original.ID, got.ID)
			require.Equal(t, now, *got.LastExtractedAt)
			var progress types.MemoryExtractionSession
			require.NoError(t, extractionRows(db, scope).Where("session_id = ?", "session").First(&progress).Error)
			require.EqualValues(t, 7, progress.Revision)
			require.Equal(t, 2, progress.FailureCount)
			if fail {
				require.Equal(t, now, *got.ExtractCursor)
				require.Equal(t, "cursor", progress.Cursor.ID)
				require.Equal(t, "private block", got.BlockText)
				require.Equal(t, "old-lease", got.ExtractionState.LeaseID)
				require.True(t, progress.Pending)
				return
			}
			require.True(t, got.ExtractCursor.After(now))
			require.True(t, progress.Cursor.At.Equal(*got.ExtractCursor))
			require.Empty(t, progress.Cursor.ID)
			require.NotEmpty(t, got.ExtractionState.Generation)
			require.Empty(t, got.BlockText)
			require.Zero(t, got.ItemCount)
			require.Nil(t, got.BlockUpdatedAt)
			require.Nil(t, got.ConsolidatedAt)
			require.Nil(t, got.ForcedConsolidatedAt)
			require.Empty(t, got.ExtractionState.LeaseID)
			require.Nil(t, got.ExtractScheduledAt)
			require.Empty(t, got.PendingSessions)
			require.False(t, progress.Pending)
			err = repo.CheckpointExtraction(ctx, scope, "old-lease", progress,
				types.MemoryMessageCursor{At: now.Add(time.Hour), ID: "stale"}, true)
			require.ErrorIs(t, err, types.ErrMemoryExtractionLeaseLost)
			for _, session := range []string{"session", "legacy"} {
				batch, e := repo.ClaimPendingSessions(ctx, scope, session, "stale-queued", time.Minute)
				require.NoError(t, e)
				require.Nil(t, batch)
			}
			_, enqueue, err := repo.EnqueuePendingSession(ctx, scope, "session", time.Minute)
			require.NoError(t, err)
			require.True(t, enqueue, "fresh conversation activity can still schedule extraction")
		})
	}
}

func TestMemoryAdminClearMissingSubject(t *testing.T) {
	repo, _ := newMemoryAdminHarness(t)
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "missing"}
	n, err := adminWrites(t, repo).ClearSubjectMemory(t.Context(), scope)
	require.True(t, errors.Is(err, gorm.ErrRecordNotFound))
	require.Zero(t, n)
}
