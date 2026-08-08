package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const knowledgeFileUpdateSlotsTestDDL = `
CREATE TABLE knowledge_file_update_slots (
    knowledge_id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    latest_version INTEGER NOT NULL DEFAULT 0,
    active_version INTEGER,
    active_state VARCHAR(16) NOT NULL DEFAULT 'idle',
    active_payload TEXT,
    pending_version INTEGER,
    pending_payload TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (active_state IN ('idle', 'waiting', 'applying', 'retry_wait', 'failed')),
    CHECK ((active_state = 'idle') = (active_version IS NULL)),
    CHECK ((active_version IS NULL) = (active_payload IS NULL)),
    CHECK ((pending_version IS NULL) = (pending_payload IS NULL))
);`

func setupKnowledgeFileUpdateSlotTestDB(t *testing.T) (*gorm.DB, *knowledgeRepository) {
	t.Helper()
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec(knowledgeFileUpdateSlotsTestDDL).Error)
	return db, NewKnowledgeRepository(db).(*knowledgeRepository)
}

func updatePayload(t *testing.T, path, hash string) types.JSON {
	t.Helper()
	payload, err := json.Marshal(types.KnowledgeFileUpdatePayload{
		NewFilePath: path,
		NewFileHash: hash,
		NewFileName: "document.md",
		NewFileType: "md",
	})
	require.NoError(t, err)
	return types.JSON(payload)
}

func decodeUpdatePayload(t *testing.T, payload types.JSON) types.KnowledgeFileUpdatePayload {
	t.Helper()
	var decoded types.KnowledgeFileUpdatePayload
	require.NoError(t, json.Unmarshal(payload, &decoded))
	return decoded
}

func TestStageKnowledgeFileUpdateLatestWinsAndPromotes(t *testing.T) {
	db, repo := setupKnowledgeFileUpdateSlotTestDB(t)
	ctx := context.Background()
	kbID := uuid.New().String()
	knowledgeID := insertFileKnowledge(
		t, db, 1, kbID, types.ParseStatusCompleted, "old/path.md", "old-hash",
	)

	first, err := repo.StageKnowledgeFileUpdate(ctx, 1, knowledgeID, kbID, updatePayload(t, "a", "ha"), nil)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), first.Version)
	assert.Equal(t, types.KnowledgeFileUpdateResultActive, first.State)

	second, err := repo.StageKnowledgeFileUpdate(ctx, 1, knowledgeID, kbID, updatePayload(t, "b", "hb"), nil)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), second.Version)
	assert.Equal(t, types.KnowledgeFileUpdateResultPending, second.State)
	assert.Empty(t, second.ReplacedPendingPayload)

	third, err := repo.StageKnowledgeFileUpdate(ctx, 1, knowledgeID, kbID, updatePayload(t, "c", "hc"), nil)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), third.Version)
	assert.Equal(t, "b", decodeUpdatePayload(t, third.ReplacedPendingPayload).NewFilePath)

	slot, err := repo.GetKnowledgeFileUpdateSlot(ctx, 1, knowledgeID)
	require.NoError(t, err)
	require.NotNil(t, slot.ActiveVersion)
	require.NotNil(t, slot.PendingVersion)
	assert.Equal(t, uint64(1), *slot.ActiveVersion)
	assert.Equal(t, uint64(3), *slot.PendingVersion)
	assert.Equal(t, "a", decodeUpdatePayload(t, slot.ActivePayload).NewFilePath)
	assert.Equal(t, "c", decodeUpdatePayload(t, slot.PendingPayload).NewFilePath)

	slot, err = repo.CompleteKnowledgeFileUpdate(ctx, 1, knowledgeID, 1)
	require.NoError(t, err)
	require.NotNil(t, slot.ActiveVersion)
	assert.Equal(t, uint64(3), *slot.ActiveVersion)
	assert.Nil(t, slot.PendingVersion)
	assert.Equal(t, types.KnowledgeFileUpdateStateWaiting, slot.ActiveState)
	assert.Equal(t, "c", decodeUpdatePayload(t, slot.ActivePayload).NewFilePath)
}

func TestStageKnowledgeFileUpdateExpectedVersion(t *testing.T) {
	db, repo := setupKnowledgeFileUpdateSlotTestDB(t)
	ctx := context.Background()
	kbID := uuid.New().String()
	knowledgeID := insertFileKnowledge(
		t, db, 1, kbID, types.ParseStatusCompleted, "old/path.md", "old-hash",
	)

	_, err := repo.StageKnowledgeFileUpdate(ctx, 1, knowledgeID, kbID, updatePayload(t, "a", "ha"), nil)
	require.NoError(t, err)
	stale := uint64(0)
	_, err = repo.StageKnowledgeFileUpdate(ctx, 1, knowledgeID, kbID, updatePayload(t, "b", "hb"), &stale)
	require.ErrorIs(t, err, ErrKnowledgeFileUpdateVersionConflict)

	slot, err := repo.GetKnowledgeFileUpdateSlot(ctx, 1, knowledgeID)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), slot.LatestVersion)
}

func TestStageKnowledgeFileUpdateConcurrentQueueIsBounded(t *testing.T) {
	db, repo := setupKnowledgeFileUpdateSlotTestDB(t)
	ctx := context.Background()
	kbID := uuid.New().String()
	knowledgeID := insertFileKnowledge(
		t, db, 1, kbID, types.ParseStatusCompleted, "old/path.md", "old-hash",
	)

	const requests = 100
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := repo.StageKnowledgeFileUpdate(
				ctx, 1, knowledgeID, kbID,
				updatePayload(t, "path-"+string(rune('a'+i)), "hash"), nil,
			)
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	slot, err := repo.GetKnowledgeFileUpdateSlot(ctx, 1, knowledgeID)
	require.NoError(t, err)
	assert.Equal(t, uint64(requests), slot.LatestVersion)
	require.NotNil(t, slot.ActiveVersion)
	require.NotNil(t, slot.PendingVersion)
	assert.NotEmpty(t, slot.ActivePayload)
	assert.NotEmpty(t, slot.PendingPayload)
}

func TestStageKnowledgeFileUpdateReplacesFailedActiveAndPending(t *testing.T) {
	db, repo := setupKnowledgeFileUpdateSlotTestDB(t)
	ctx := context.Background()
	kbID := uuid.New().String()
	knowledgeID := insertFileKnowledge(
		t, db, 1, kbID, types.ParseStatusCompleted, "old/path.md", "old-hash",
	)

	first, err := repo.StageKnowledgeFileUpdate(ctx, 1, knowledgeID, kbID, updatePayload(t, "a", "ha"), nil)
	require.NoError(t, err)
	_, err = repo.StageKnowledgeFileUpdate(ctx, 1, knowledgeID, kbID, updatePayload(t, "b", "hb"), nil)
	require.NoError(t, err)
	failed, err := repo.TransitionKnowledgeFileUpdateState(
		ctx, 1, knowledgeID, first.ActiveVersion,
		types.KnowledgeFileUpdateStateWaiting, types.KnowledgeFileUpdateStateFailed, "apply failed",
	)
	require.NoError(t, err)
	require.True(t, failed)

	latest, err := repo.StageKnowledgeFileUpdate(ctx, 1, knowledgeID, kbID, updatePayload(t, "c", "hc"), nil)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), latest.Version)
	assert.Equal(t, types.KnowledgeFileUpdateResultActive, latest.State)
	assert.Equal(t, "a", decodeUpdatePayload(t, latest.ReplacedActivePayload).NewFilePath)
	assert.Equal(t, "b", decodeUpdatePayload(t, latest.ReplacedPendingPayload).NewFilePath)

	slot, err := repo.GetKnowledgeFileUpdateSlot(ctx, 1, knowledgeID)
	require.NoError(t, err)
	require.NotNil(t, slot.ActiveVersion)
	assert.Equal(t, uint64(3), *slot.ActiveVersion)
	assert.Nil(t, slot.PendingVersion)
	assert.Equal(t, types.KnowledgeFileUpdateStateWaiting, slot.ActiveState)
	assert.Equal(t, "c", decodeUpdatePayload(t, slot.ActivePayload).NewFilePath)
}

func TestKnowledgeQueryProjectsLatestFileUpdateState(t *testing.T) {
	db, repo := setupKnowledgeFileUpdateSlotTestDB(t)
	ctx := context.Background()
	kbID := uuid.New().String()
	knowledgeID := insertFileKnowledge(
		t, db, 1, kbID, types.ParseStatusCompleted, "old/path.md", "old-hash",
	)

	_, err := repo.StageKnowledgeFileUpdate(ctx, 1, knowledgeID, kbID, updatePayload(t, "a", "ha"), nil)
	require.NoError(t, err)
	_, err = repo.StageKnowledgeFileUpdate(ctx, 1, knowledgeID, kbID, updatePayload(t, "b", "hb"), nil)
	require.NoError(t, err)

	knowledge, err := repo.GetKnowledgeByID(ctx, 1, knowledgeID)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), knowledge.FileUpdateVersion)
	assert.Equal(t, types.KnowledgeFileUpdateResultPending, knowledge.FileUpdateState)
}

func TestBeginKnowledgeDeletionMarksDeletingAndRevokesSlot(t *testing.T) {
	db, repo := setupKnowledgeFileUpdateSlotTestDB(t)
	ctx := context.Background()
	kbID := uuid.NewString()
	knowledgeID := insertFileKnowledge(
		t, db, 1, kbID, types.ParseStatusCompleted, "old/path.md", "old-hash",
	)
	_, err := repo.StageKnowledgeFileUpdate(
		ctx, 1, knowledgeID, kbID, updatePayload(t, "staged/a.md", "ha"), nil,
	)
	require.NoError(t, err)
	_, err = repo.StageKnowledgeFileUpdate(
		ctx, 1, knowledgeID, kbID, updatePayload(t, "staged/b.md", "hb"), nil,
	)
	require.NoError(t, err)

	cancelled, err := repo.BeginKnowledgeDeletion(ctx, 1, []string{knowledgeID})
	require.NoError(t, err)
	require.Len(t, cancelled, 1)
	assert.Equal(t, "staged/a.md", decodeUpdatePayload(t, cancelled[0].ActivePayload).NewFilePath)
	assert.Equal(t, "staged/b.md", decodeUpdatePayload(t, cancelled[0].PendingPayload).NewFilePath)

	status, _, _ := reloadFileVersion(t, db, knowledgeID)
	assert.Equal(t, types.ParseStatusDeleting, status)
	_, err = repo.GetKnowledgeFileUpdateSlot(ctx, 1, knowledgeID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	_, err = repo.StageKnowledgeFileUpdate(
		ctx, 1, knowledgeID, kbID, updatePayload(t, "staged/late.md", "late"), nil,
	)
	require.ErrorIs(t, err, ErrKnowledgeFileUpdateDeleting)
	_, err = repo.GetKnowledgeFileUpdateSlot(ctx, 1, knowledgeID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestBeginKnowledgeDeletionRollsBackStatusWhenSlotDeleteFails(t *testing.T) {
	db, repo := setupKnowledgeFileUpdateSlotTestDB(t)
	ctx := context.Background()
	kbID := uuid.NewString()
	knowledgeID := insertFileKnowledge(
		t, db, 1, kbID, types.ParseStatusCompleted, "old/path.md", "old-hash",
	)
	_, err := repo.StageKnowledgeFileUpdate(
		ctx, 1, knowledgeID, kbID, updatePayload(t, "staged/a.md", "ha"), nil,
	)
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_file_update_slot_delete
		BEFORE DELETE ON knowledge_file_update_slots
		BEGIN
			SELECT RAISE(ABORT, 'slot delete rejected');
		END;
	`).Error)

	_, err = repo.BeginKnowledgeDeletion(ctx, 1, []string{knowledgeID})
	require.Error(t, err)
	status, _, _ := reloadFileVersion(t, db, knowledgeID)
	assert.Equal(t, types.ParseStatusCompleted, status)
	_, err = repo.GetKnowledgeFileUpdateSlot(ctx, 1, knowledgeID)
	require.NoError(t, err)
}

func TestCancelFailedKnowledgeFileUpdateDoesNotRemoveNewerActive(t *testing.T) {
	db, repo := setupKnowledgeFileUpdateSlotTestDB(t)
	ctx := context.Background()
	kbID := uuid.NewString()
	knowledgeID := insertFileKnowledge(
		t, db, 1, kbID, types.ParseStatusCompleted, "old/path.md", "old-hash",
	)
	first, err := repo.StageKnowledgeFileUpdate(
		ctx, 1, knowledgeID, kbID, updatePayload(t, "staged/a.md", "ha"), nil,
	)
	require.NoError(t, err)
	failed, err := repo.TransitionKnowledgeFileUpdateState(
		ctx, 1, knowledgeID, first.ActiveVersion,
		types.KnowledgeFileUpdateStateWaiting, types.KnowledgeFileUpdateStateFailed, "failed",
	)
	require.NoError(t, err)
	require.True(t, failed)
	latest, err := repo.StageKnowledgeFileUpdate(
		ctx, 1, knowledgeID, kbID, updatePayload(t, "staged/b.md", "hb"), nil,
	)
	require.NoError(t, err)

	_, err = repo.CancelFailedKnowledgeFileUpdate(ctx, 1, knowledgeID, first.ActiveVersion)
	require.ErrorIs(t, err, ErrKnowledgeFileUpdateStateConflict)
	slot, err := repo.GetKnowledgeFileUpdateSlot(ctx, 1, knowledgeID)
	require.NoError(t, err)
	require.NotNil(t, slot.ActiveVersion)
	assert.Equal(t, latest.ActiveVersion, *slot.ActiveVersion)
	assert.Equal(t, "staged/b.md", decodeUpdatePayload(t, slot.ActivePayload).NewFilePath)
}
