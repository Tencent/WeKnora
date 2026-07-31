package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const knowledgeFolderMoveServicePendingTestDDL = `
CREATE TABLE knowledge_folder_index_pending (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id      VARCHAR(36) NOT NULL,
    target_folder_id  VARCHAR(36) NOT NULL DEFAULT '',
    requested_version INTEGER NOT NULL CHECK (requested_version > 0),
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, knowledge_base_id, knowledge_id)
);
`

func setupKnowledgeFolderMoveServiceIntegrationTest(
	t *testing.T,
) (interfaces.KnowledgeFolderMoveService, *gorm.DB, context.Context) {
	t.Helper()
	_, _, db, ctx := setupKnowledgeFolderServiceTest(t)
	require.NoError(t, db.Exec(
		`ALTER TABLE knowledges ADD COLUMN updated_at DATETIME`,
	).Error)
	require.NoError(t, db.Exec(
		`ALTER TABLE knowledges ADD COLUMN parse_status VARCHAR(32) NOT NULL DEFAULT 'completed'`,
	).Error)
	require.NoError(t, db.Exec(knowledgeFolderMoveServicePendingTestDDL).Error)
	return NewKnowledgeFolderMoveService(
		repository.NewKnowledgeFolderMoveRepository(db),
	), db, ctx
}

func setKnowledgeFolderMoveServiceIntegrationParseStatus(
	t *testing.T,
	db *gorm.DB,
	knowledgeID string,
	parseStatus string,
) {
	t.Helper()
	require.NoError(t, db.Model(&types.Knowledge{}).
		Where("id = ?", knowledgeID).
		Update("parse_status", parseStatus).Error)
}

func insertKnowledgeFolderMoveServiceIntegrationRows(
	t *testing.T,
	db *gorm.DB,
	rows ...[]interface{},
) {
	t.Helper()
	for _, row := range rows {
		require.Len(t, row, 4)
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges (
				id,
				tenant_id,
				knowledge_base_id,
				folder_id,
				folder_version,
				folder_indexed_version
			) VALUES (?, 1, 'kb-1', ?, ?, ?)
		`, row...).Error)
	}
}

func readKnowledgeFolderMoveServiceIntegrationState(
	t *testing.T,
	db *gorm.DB,
	knowledgeID string,
) (folderID string, folderVersion uint64, indexedVersion uint64) {
	t.Helper()
	row := db.Raw(`
		SELECT folder_id, folder_version, folder_indexed_version
		FROM knowledges
		WHERE id = ?
	`, knowledgeID).Row()
	require.NoError(t, row.Scan(&folderID, &folderVersion, &indexedVersion))
	return folderID, folderVersion, indexedVersion
}

func TestKnowledgeFolderMoveServiceAllNoOpLeavesDatabaseAndPendingUnchanged(
	t *testing.T,
) {
	moveService, db, ctx := setupKnowledgeFolderMoveServiceIntegrationTest(t)
	insertKnowledgeFolderMoveServiceIntegrationRows(
		t,
		db,
		[]interface{}{knowledgeFolderMoveTestKnowledgeA, "", uint64(4), uint64(3)},
		[]interface{}{knowledgeFolderMoveTestKnowledgeB, "", uint64(7), uint64(5)},
	)

	result, err := moveService.MoveKnowledge(ctx, &types.KnowledgeFolderMoveInput{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		KnowledgeIDs: []string{
			knowledgeFolderMoveTestKnowledgeA,
			knowledgeFolderMoveTestKnowledgeB,
		},
		TargetFolderID: types.KnowledgeFolderRootID,
	})

	require.NoError(t, err)
	assert.Equal(t, &types.KnowledgeFolderMoveResult{
		ChangedCount:   0,
		UnchangedCount: 2,
	}, result)
	for knowledgeID, expectedVersion := range map[string]uint64{
		knowledgeFolderMoveTestKnowledgeA: 4,
		knowledgeFolderMoveTestKnowledgeB: 7,
	} {
		folderID, version, indexedVersion :=
			readKnowledgeFolderMoveServiceIntegrationState(t, db, knowledgeID)
		assert.Empty(t, folderID)
		assert.Equal(t, expectedVersion, version)
		if knowledgeID == knowledgeFolderMoveTestKnowledgeA {
			assert.Equal(t, uint64(3), indexedVersion)
		} else {
			assert.Equal(t, uint64(5), indexedVersion)
		}
	}
	var pendingCount int64
	require.NoError(t, db.Model(&types.KnowledgeFolderIndexPending{}).
		Count(&pendingCount).Error)
	assert.Zero(t, pendingCount)
}

func TestKnowledgeFolderMoveServicePartialNoOpCommitsOnlyChangedKnowledgeAndPending(
	t *testing.T,
) {
	moveService, db, ctx := setupKnowledgeFolderMoveServiceIntegrationTest(t)
	insertKnowledgeFolderMoveServiceIntegrationRows(
		t,
		db,
		[]interface{}{knowledgeFolderMoveTestKnowledgeA, "", uint64(4), uint64(3)},
		[]interface{}{
			knowledgeFolderMoveTestKnowledgeB,
			knowledgeFolderMoveTestFolderA,
			uint64(7),
			uint64(5),
		},
	)

	result, err := moveService.MoveKnowledge(ctx, &types.KnowledgeFolderMoveInput{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		KnowledgeIDs: []string{
			knowledgeFolderMoveTestKnowledgeB,
			knowledgeFolderMoveTestKnowledgeA,
		},
		TargetFolderID: types.KnowledgeFolderRootID,
	})

	require.NoError(t, err)
	assert.Equal(t, &types.KnowledgeFolderMoveResult{
		ChangedCount:   1,
		UnchangedCount: 1,
	}, result)
	folderA, versionA, indexedA := readKnowledgeFolderMoveServiceIntegrationState(
		t,
		db,
		knowledgeFolderMoveTestKnowledgeA,
	)
	assert.Empty(t, folderA)
	assert.Equal(t, uint64(4), versionA)
	assert.Equal(t, uint64(3), indexedA)
	folderB, versionB, indexedB := readKnowledgeFolderMoveServiceIntegrationState(
		t,
		db,
		knowledgeFolderMoveTestKnowledgeB,
	)
	assert.Empty(t, folderB)
	assert.Equal(t, uint64(8), versionB)
	assert.Equal(t, uint64(5), indexedB)

	var pendingRows []*types.KnowledgeFolderIndexPending
	require.NoError(t, db.Order("knowledge_id ASC").Find(&pendingRows).Error)
	require.Len(t, pendingRows, 1)
	assert.Equal(t, knowledgeFolderMoveTestKnowledgeB, pendingRows[0].KnowledgeID)
	assert.Empty(t, pendingRows[0].TargetFolderID)
	assert.Equal(t, uint64(8), pendingRows[0].RequestedVersion)
}

func TestKnowledgeFolderMoveServiceDeletingBatchLeavesDatabaseAndPendingUnchanged(
	t *testing.T,
) {
	moveService, db, ctx := setupKnowledgeFolderMoveServiceIntegrationTest(t)
	insertKnowledgeFolderMoveServiceIntegrationRows(
		t,
		db,
		[]interface{}{
			knowledgeFolderMoveTestKnowledgeA,
			knowledgeFolderMoveTestFolderA,
			uint64(4),
			uint64(3),
		},
		[]interface{}{
			knowledgeFolderMoveTestKnowledgeB,
			types.KnowledgeFolderRootID,
			uint64(7),
			uint64(5),
		},
	)
	setKnowledgeFolderMoveServiceIntegrationParseStatus(
		t,
		db,
		knowledgeFolderMoveTestKnowledgeB,
		types.ParseStatusDeleting,
	)

	result, err := moveService.MoveKnowledge(ctx, &types.KnowledgeFolderMoveInput{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		KnowledgeIDs: []string{
			knowledgeFolderMoveTestKnowledgeA,
			knowledgeFolderMoveTestKnowledgeB,
		},
		TargetFolderID: types.KnowledgeFolderRootID,
	})

	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrKnowledgeFolderMoveKnowledgeNotFound)
	folderA, versionA, indexedA := readKnowledgeFolderMoveServiceIntegrationState(
		t,
		db,
		knowledgeFolderMoveTestKnowledgeA,
	)
	assert.Equal(t, knowledgeFolderMoveTestFolderA, folderA)
	assert.Equal(t, uint64(4), versionA)
	assert.Equal(t, uint64(3), indexedA)
	folderB, versionB, indexedB := readKnowledgeFolderMoveServiceIntegrationState(
		t,
		db,
		knowledgeFolderMoveTestKnowledgeB,
	)
	assert.Empty(t, folderB)
	assert.Equal(t, uint64(7), versionB)
	assert.Equal(t, uint64(5), indexedB)
	var pendingCount int64
	require.NoError(t, db.Model(&types.KnowledgeFolderIndexPending{}).
		Count(&pendingCount).Error)
	assert.Zero(t, pendingCount)
}

func TestKnowledgeFolderMoveServiceIndexedVersionAheadRollsBackWholeBatch(
	t *testing.T,
) {
	moveService, db, ctx := setupKnowledgeFolderMoveServiceIntegrationTest(t)
	insertKnowledgeFolderMoveServiceIntegrationRows(
		t,
		db,
		[]interface{}{
			knowledgeFolderMoveTestKnowledgeA,
			knowledgeFolderMoveTestFolderA,
			uint64(4),
			uint64(3),
		},
		[]interface{}{
			knowledgeFolderMoveTestKnowledgeB,
			knowledgeFolderMoveTestFolderB,
			uint64(7),
			uint64(8),
		},
	)

	result, err := moveService.MoveKnowledge(ctx, &types.KnowledgeFolderMoveInput{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		KnowledgeIDs: []string{
			knowledgeFolderMoveTestKnowledgeA,
			knowledgeFolderMoveTestKnowledgeB,
		},
		TargetFolderID: types.KnowledgeFolderRootID,
	})

	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
	for _, expected := range []struct {
		id             string
		folderID       string
		version        uint64
		indexedVersion uint64
	}{
		{
			id:             knowledgeFolderMoveTestKnowledgeA,
			folderID:       knowledgeFolderMoveTestFolderA,
			version:        4,
			indexedVersion: 3,
		},
		{
			id:             knowledgeFolderMoveTestKnowledgeB,
			folderID:       knowledgeFolderMoveTestFolderB,
			version:        7,
			indexedVersion: 8,
		},
	} {
		folderID, version, indexedVersion :=
			readKnowledgeFolderMoveServiceIntegrationState(t, db, expected.id)
		assert.Equal(t, expected.folderID, folderID)
		assert.Equal(t, expected.version, version)
		assert.Equal(t, expected.indexedVersion, indexedVersion)
	}
	var pendingCount int64
	require.NoError(t, db.Model(&types.KnowledgeFolderIndexPending{}).
		Count(&pendingCount).Error)
	assert.Zero(t, pendingCount)
}

func TestKnowledgeFolderMoveServiceEqualIndexedVersionRemainsUnchanged(
	t *testing.T,
) {
	moveService, db, ctx := setupKnowledgeFolderMoveServiceIntegrationTest(t)
	insertKnowledgeFolderMoveServiceIntegrationRows(
		t,
		db,
		[]interface{}{
			knowledgeFolderMoveTestKnowledgeA,
			knowledgeFolderMoveTestFolderA,
			uint64(4),
			uint64(4),
		},
	)

	result, err := moveService.MoveKnowledge(ctx, &types.KnowledgeFolderMoveInput{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		KnowledgeIDs:    []string{knowledgeFolderMoveTestKnowledgeA},
		TargetFolderID:  types.KnowledgeFolderRootID,
	})

	require.NoError(t, err)
	assert.Equal(t, &types.KnowledgeFolderMoveResult{
		ChangedCount:   1,
		UnchangedCount: 0,
	}, result)
	folderID, version, indexedVersion :=
		readKnowledgeFolderMoveServiceIntegrationState(
			t,
			db,
			knowledgeFolderMoveTestKnowledgeA,
		)
	assert.Empty(t, folderID)
	assert.Equal(t, uint64(5), version)
	assert.Equal(t, uint64(4), indexedVersion)
	var pending types.KnowledgeFolderIndexPending
	require.NoError(t, db.Where(
		"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ?",
		1,
		"kb-1",
		knowledgeFolderMoveTestKnowledgeA,
	).Take(&pending).Error)
	assert.Equal(t, uint64(5), pending.RequestedVersion)
}
