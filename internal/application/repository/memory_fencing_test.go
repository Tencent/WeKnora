package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestMemoryClearFencesEveryLateMutation(t *testing.T) {
	type mutation func(interfaces.MemoryRepository, interfaces.MemoryScope) error
	mutations := map[string]mutation{
		"block": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.UpdateSubjectBlock(t.Context(), s, "stale biography", 99)
		},
		"topic": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			_, err := r.BumpTopic(t.Context(), s, "old topic", "topic", "")
			return err
		},
		"rename": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			_, err := r.RenameTopic(t.Context(), s, "topic", "renamed", "old label")
			return err
		},
		"promote": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.MarkTopicPromoted(t.Context(), s, "topic")
		},
		"affinity": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			refs := []types.MemoryDocAffinity{{KnowledgeID: "doc", Title: "old title"}}
			return r.BumpDocAffinity(t.Context(), s, refs)
		},
		"consolidated": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.MarkConsolidated(t.Context(), s)
		},
		"forced consolidated": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.MarkForcedConsolidated(t.Context(), s)
		},
		"tombstone": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.AddTombstone(t.Context(), s, "old topic", "old fingerprint", "old message")
		},
		"archive": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			_, err := r.ArchiveLowestRanked(t.Context(), s, 1)
			return err
		},
		"expire": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			_, err := r.ExpireOverdue(t.Context(), s)
			return err
		},
		"save": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.SaveItem(t.Context(), s, adminItem("late", types.MemoryStatusActive, time.Now()), "")
		},
		"supersede": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.SupersedeItem(t.Context(), s, "fresh", "late")
		},
		"edit": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.UpdateItemContent(t.Context(), s, "fresh", "old content", "key", 1)
		},
		"delete": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.DeleteItem(t.Context(), s, "fresh")
		},
		"confirm": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.ConfirmPendingItem(t.Context(), s, "proposal")
		},
		"reject": func(r interfaces.MemoryRepository, s interfaces.MemoryScope) error {
			return r.RejectPendingItem(t.Context(), s, "proposal")
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			repo, _ := newMemoryAdminHarness(t)
			scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:a"}
			subject, err := repo.EnsureSubject(t.Context(), scope)
			require.NoError(t, err)
			stale := scope.WithGeneration(subject.ExtractionState.Generation)
			_, err = repo.ClearSubjectMemory(t.Context(), scope)
			require.NoError(t, err)
			subject, err = repo.GetSubject(t.Context(), scope)
			require.NoError(t, err)
			fresh := scope.WithGeneration(subject.ExtractionState.Generation)
			freshItem := adminItem("fresh", types.MemoryStatusActive, time.Now())
			require.NoError(t, repo.SaveItem(t.Context(), fresh, freshItem, ""))
			require.NoError(t, repo.SaveItem(
				t.Context(), fresh, adminItem("proposal", types.MemoryStatusPending, time.Now()), "",
			))
			_, err = repo.BumpTopic(t.Context(), fresh, "new topic", "topic", "")
			require.NoError(t, err)
			require.ErrorIs(t, mutate(repo, stale), types.ErrMemoryGenerationChanged)
			item, err := repo.GetItem(t.Context(), scope, "fresh")
			require.NoError(t, err)
			require.NotNil(t, item)
			require.Equal(t, types.MemoryStatusActive, item.Status)
		})
	}
}

func TestMemoryClearCutoffAcrossTimezonesAndPages(t *testing.T) {
	repo, db := newMemoryAdminHarness(t)
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "mixed-timezones"}
	_, err := repo.EnsureSubject(t.Context(), scope)
	require.NoError(t, err)
	var sessions []types.MemoryExtractionSession
	for i := range 205 {
		at := time.Now().Add(-time.Hour).In(time.FixedZone("ahead", 14*60*60))
		if i%2 == 0 {
			at = time.Now().UTC().Add(time.Hour)
		}
		sessions = append(sessions, types.MemoryExtractionSession{
			TenantID: scope.TenantID, SubjectID: scope.SubjectID, SessionID: fmt.Sprintf("session-%03d", i),
			Cursor: types.MemoryMessageCursor{At: at, ID: "old-id"}, Pending: true,
		})
	}
	require.NoError(t, db.Create(&sessions).Error)
	before := time.Now()
	_, err = repo.ClearSubjectMemory(t.Context(), scope)
	require.NoError(t, err)
	var got []types.MemoryExtractionSession
	require.NoError(t, extractionRows(db, scope).Order("session_id").Find(&got).Error)
	require.Len(t, got, len(sessions))
	for i, row := range got {
		require.False(t, row.Pending)
		if i%2 == 0 {
			require.True(t, row.Cursor.At.Equal(sessions[i].Cursor.At),
				"future cursor %s must not regress", row.SessionID)
			require.Equal(t, "old-id", row.Cursor.ID)
		} else {
			require.False(t, row.Cursor.At.Before(before),
				"past cursor %s must advance despite its offset", row.SessionID)
			require.Empty(t, row.Cursor.ID)
		}
	}
}

func TestMemoryClearCutoffIsMonotonicAndTenantLocal(t *testing.T) {
	repo, db := newMemoryAdminHarness(t)
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "same"}
	foreign := interfaces.MemoryScope{TenantID: 2, SubjectID: "same"}
	for _, s := range []interfaces.MemoryScope{scope, foreign} {
		_, err := repo.EnsureSubject(t.Context(), s)
		require.NoError(t, err)
		_, _, err = repo.EnqueuePendingSession(t.Context(), s, "session", time.Minute)
		require.NoError(t, err)
	}
	future := types.MemoryMessageCursor{At: time.Now().UTC().Add(time.Hour), ID: "z"}
	require.NoError(t, db.Model(&types.MemoryExtractionSession{}).Where("tenant_id = ?", scope.TenantID).
		Updates(map[string]any{"cursor_at": future.At, "cursor_id": future.ID}).Error)
	before := time.Now()
	_, err := repo.ClearSubjectMemory(t.Context(), scope)
	require.NoError(t, err)
	subject, err := repo.GetSubject(t.Context(), scope)
	require.NoError(t, err)
	require.NotNil(t, subject.ExtractCursor)
	require.False(t, subject.ExtractCursor.Before(before))
	var sessions []types.MemoryExtractionSession
	require.NoError(t, db.Order("tenant_id").Find(&sessions).Error)
	require.True(t, sessions[0].Cursor.At.Equal(future.At))
	require.Equal(t, future.ID, sessions[0].Cursor.ID)
	require.False(t, sessions[0].Pending)
	require.True(t, sessions[1].Pending)
	require.True(t, sessions[1].Cursor.At.IsZero())
	other, err := repo.GetSubject(t.Context(), foreign)
	require.NoError(t, err)
	require.Empty(t, other.ExtractionState.Generation)
	require.NoError(t, repo.UpdateSubjectBlock(t.Context(), foreign.WithGeneration(""), "foreign profile", 1))
}
