package repository

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const knowledgeFolderMovePendingTestDDL = `
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

const knowledgeFolderMoveTestTimeout = 5 * time.Second

func setupKnowledgeFolderMoveTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupKnowledgeFolderTestDB(t)
	require.NoError(t, db.Exec(
		`ALTER TABLE knowledges ADD COLUMN updated_at DATETIME DEFAULT CURRENT_TIMESTAMP`,
	).Error)
	require.NoError(t, db.Exec(
		`ALTER TABLE knowledges ADD COLUMN parse_status VARCHAR(32) NOT NULL DEFAULT 'completed'`,
	).Error)
	require.NoError(t, db.Exec(knowledgeFolderMovePendingTestDDL).Error)
	return db
}

func setupConcurrentKnowledgeFolderMoveTestDBs(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "knowledge-folder-move.db")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=1"
	dbA, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	dbB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDBA, err := dbA.DB()
	require.NoError(t, err)
	sqlDBB, err := dbB.DB()
	require.NoError(t, err)
	sqlDBA.SetMaxOpenConns(1)
	sqlDBB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDBA.Close()
		_ = sqlDBB.Close()
	})
	require.NoError(t, dbA.Exec(knowledgeFolderTestDDL).Error)
	require.NoError(t, dbA.Exec(
		`ALTER TABLE knowledges ADD COLUMN updated_at DATETIME DEFAULT CURRENT_TIMESTAMP`,
	).Error)
	require.NoError(t, dbA.Exec(
		`ALTER TABLE knowledges ADD COLUMN parse_status VARCHAR(32) NOT NULL DEFAULT 'completed'`,
	).Error)
	require.NoError(t, dbA.Exec(knowledgeFolderMovePendingTestDDL).Error)
	return dbA, dbB
}

func insertKnowledgeFolderMoveRow(
	t *testing.T,
	db *gorm.DB,
	id string,
	tenantID uint64,
	kbID string,
	folderID string,
	folderVersion uint64,
	folderIndexedVersion uint64,
	deleted bool,
) {
	t.Helper()
	var deletedAt interface{}
	if deleted {
		deletedAt = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	}
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (
			id,
			tenant_id,
			knowledge_base_id,
			folder_id,
			folder_version,
			folder_indexed_version,
			deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, tenantID, kbID, folderID, folderVersion, folderIndexedVersion, deletedAt).Error)
}

func setKnowledgeFolderMoveParseStatus(
	t *testing.T,
	db *gorm.DB,
	id string,
	parseStatus string,
) {
	t.Helper()
	require.NoError(t, db.Unscoped().
		Model(&types.Knowledge{}).
		Where("id = ?", id).
		Update("parse_status", parseStatus).Error)
}

func readKnowledgeFolderMoveState(
	t *testing.T,
	db *gorm.DB,
	id string,
) (folderID string, folderVersion uint64, folderIndexedVersion uint64) {
	t.Helper()
	row := db.Raw(`
		SELECT folder_id, folder_version, folder_indexed_version
		FROM knowledges
		WHERE id = ?
	`, id).Row()
	require.NoError(t, row.Scan(&folderID, &folderVersion, &folderIndexedVersion))
	return folderID, folderVersion, folderIndexedVersion
}

func moveLockedKnowledgeForTest(
	ctx context.Context,
	txRepo interfaces.KnowledgeFolderMoveTxRepository,
	tenantID uint64,
	kbID string,
	knowledgeID string,
	targetFolderID string,
	pendingID string,
) error {
	locked, err := txRepo.LockKnowledgeForFolderMove(
		ctx,
		tenantID,
		kbID,
		[]string{knowledgeID},
	)
	if err != nil {
		return err
	}
	if len(locked) != 1 || locked[0] == nil || locked[0].ID != knowledgeID {
		return ErrKnowledgeFolderMoveDataIntegrity
	}
	if err := txRepo.UpdateKnowledgeFolderForMove(
		ctx,
		interfaces.KnowledgeFolderMoveUpdate{
			TenantID:              tenantID,
			KnowledgeBaseID:       kbID,
			KnowledgeID:           knowledgeID,
			ExpectedFolderID:      locked[0].FolderID,
			ExpectedFolderVersion: locked[0].FolderVersion,
			TargetFolderID:        targetFolderID,
		},
	); err != nil {
		return err
	}
	return txRepo.UpsertKnowledgeFolderIndexPending(
		ctx,
		&types.KnowledgeFolderIndexPending{
			ID:               pendingID,
			TenantID:         tenantID,
			KnowledgeBaseID:  kbID,
			KnowledgeID:      knowledgeID,
			TargetFolderID:   targetFolderID,
			RequestedVersion: locked[0].FolderVersion + 1,
		},
	)
}

func TestKnowledgeFolderMoveLockUsesScopedSortedPostgresRowLocks(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	repo := newKnowledgeFolderMoveWriteRepository(db)
	input := []string{"knowledge-b", "knowledge-a", "knowledge-a"}
	original := append([]string(nil), input...)

	mock.ExpectQuery(
		`SELECT .* FROM "knowledges".*tenant_id = \$1 AND knowledge_base_id = \$2 AND id IN \(\$3,\$4\).*`+
			regexp.QuoteMeta(`ORDER BY tenant_id ASC,knowledge_base_id ASC,id ASC FOR UPDATE`),
	).
		WithArgs(uint64(7), "kb-1", "knowledge-a", "knowledge-b").
		WillReturnRows(sqlmock.NewRows([]string{
			"id",
			"tenant_id",
			"knowledge_base_id",
			"folder_id",
			"folder_version",
			"folder_indexed_version",
			"parse_status",
		}).
			AddRow("knowledge-a", 7, "kb-1", "", 1, 0, types.ParseStatusCompleted).
			AddRow(
				"knowledge-b",
				7,
				"kb-1",
				"folder-old",
				4,
				3,
				types.ParseStatusDeleting,
			))

	got, err := repo.LockKnowledgeForFolderMove(
		context.Background(),
		7,
		"kb-1",
		input,
	)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, []string{"knowledge-a", "knowledge-b"}, []string{got[0].ID, got[1].ID})
	assert.Equal(t, types.ParseStatusCompleted, got[0].ParseStatus)
	assert.Equal(t, types.ParseStatusDeleting, got[1].ParseStatus)
	assert.Equal(t, original, input, "repository sorting must not mutate caller input")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderMoveLockReturnsDeletingStatusForServiceValidation(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := newKnowledgeFolderMoveWriteRepository(db)
	insertKnowledgeFolderMoveRow(
		t,
		db,
		"knowledge-deleting",
		7,
		"kb-1",
		"folder-old",
		4,
		2,
		false,
	)
	setKnowledgeFolderMoveParseStatus(
		t,
		db,
		"knowledge-deleting",
		types.ParseStatusDeleting,
	)

	locked, err := repo.LockKnowledgeForFolderMove(
		context.Background(),
		7,
		"kb-1",
		[]string{"knowledge-deleting"},
	)

	require.NoError(t, err)
	require.Len(t, locked, 1)
	require.NotNil(t, locked[0])
	assert.Equal(t, types.ParseStatusDeleting, locked[0].ParseStatus)
}

func TestKnowledgeFolderMoveLockRejectsMissingDeletedOrOutOfScopeRows(t *testing.T) {
	tests := []struct {
		name        string
		requestedID string
		seed        func(*testing.T, *gorm.DB)
	}{
		{
			name:        "missing",
			requestedID: "knowledge-missing",
			seed:        func(*testing.T, *gorm.DB) {},
		},
		{
			name:        "soft deleted",
			requestedID: "knowledge-deleted",
			seed: func(t *testing.T, db *gorm.DB) {
				insertKnowledgeFolderMoveRow(t, db, "knowledge-deleted", 7, "kb-1", "", 1, 0, true)
			},
		},
		{
			name:        "other tenant",
			requestedID: "knowledge-other-tenant",
			seed: func(t *testing.T, db *gorm.DB) {
				insertKnowledgeFolderMoveRow(t, db, "knowledge-other-tenant", 8, "kb-1", "", 1, 0, false)
			},
		},
		{
			name:        "other knowledge base",
			requestedID: "knowledge-other-kb",
			seed: func(t *testing.T, db *gorm.DB) {
				insertKnowledgeFolderMoveRow(t, db, "knowledge-other-kb", 7, "kb-2", "", 1, 0, false)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeFolderMoveTestDB(t)
			repo := newKnowledgeFolderMoveWriteRepository(db)
			insertKnowledgeFolderMoveRow(t, db, "knowledge-active", 7, "kb-1", "", 1, 0, false)
			test.seed(t, db)

			got, err := repo.LockKnowledgeForFolderMove(
				context.Background(),
				7,
				"kb-1",
				[]string{"knowledge-active", test.requestedID},
			)
			require.ErrorIs(t, err, ErrKnowledgeFolderMoveKnowledgeNotFound)
			assert.Nil(t, got, "partial locked collections must never escape the repository")
		})
	}
}

func TestKnowledgeFolderMoveLockRejectsEmptyIDCollections(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := newKnowledgeFolderMoveWriteRepository(db)

	for _, knowledgeIDs := range [][]string{nil, []string{}} {
		locked, err := repo.LockKnowledgeForFolderMove(
			context.Background(),
			7,
			"kb-1",
			knowledgeIDs,
		)
		require.ErrorIs(t, err, ErrKnowledgeFolderMoveInvalid)
		assert.Nil(t, locked)
	}
}

func TestKnowledgeFolderMoveUpdateIsScopedConditionalAndPreservesIndexedVersion(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := newKnowledgeFolderMoveWriteRepository(db)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-1", 7, "kb-1", "folder-old", 7, 6, false)

	err := repo.UpdateKnowledgeFolderForMove(
		context.Background(),
		interfaces.KnowledgeFolderMoveUpdate{
			TenantID:              7,
			KnowledgeBaseID:       "kb-1",
			KnowledgeID:           "knowledge-1",
			ExpectedFolderID:      "folder-old",
			ExpectedFolderVersion: 7,
			TargetFolderID:        "folder-new",
			UpdatedAt:             time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC),
		},
	)
	require.NoError(t, err)

	folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, db, "knowledge-1")
	assert.Equal(t, "folder-new", folderID)
	assert.Equal(t, uint64(8), folderVersion)
	assert.Equal(t, uint64(6), folderIndexedVersion, "move must leave the index checkpoint pending")
}

func TestKnowledgeFolderMoveUpdateRejectsDeletingKnowledge(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := newKnowledgeFolderMoveWriteRepository(db)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-1", 7, "kb-1", "folder-old", 7, 6, false)
	setKnowledgeFolderMoveParseStatus(t, db, "knowledge-1", types.ParseStatusDeleting)

	err := repo.UpdateKnowledgeFolderForMove(
		context.Background(),
		interfaces.KnowledgeFolderMoveUpdate{
			TenantID:              7,
			KnowledgeBaseID:       "kb-1",
			KnowledgeID:           "knowledge-1",
			ExpectedFolderID:      "folder-old",
			ExpectedFolderVersion: 7,
			TargetFolderID:        "folder-new",
		},
	)
	require.ErrorIs(t, err, ErrKnowledgeFolderMoveConflict)

	folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, db, "knowledge-1")
	assert.Equal(t, "folder-old", folderID)
	assert.Equal(t, uint64(7), folderVersion)
	assert.Equal(t, uint64(6), folderIndexedVersion)
}

func TestKnowledgeFolderMoveUpdateAllowsProcessingKnowledge(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := newKnowledgeFolderMoveWriteRepository(db)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-1", 7, "kb-1", "folder-old", 7, 6, false)
	setKnowledgeFolderMoveParseStatus(t, db, "knowledge-1", types.ParseStatusProcessing)

	err := repo.UpdateKnowledgeFolderForMove(
		context.Background(),
		interfaces.KnowledgeFolderMoveUpdate{
			TenantID:              7,
			KnowledgeBaseID:       "kb-1",
			KnowledgeID:           "knowledge-1",
			ExpectedFolderID:      "folder-old",
			ExpectedFolderVersion: 7,
			TargetFolderID:        "folder-new",
		},
	)
	require.NoError(t, err)

	folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, db, "knowledge-1")
	assert.Equal(t, "folder-new", folderID)
	assert.Equal(t, uint64(8), folderVersion)
	assert.Equal(t, uint64(6), folderIndexedVersion)
}

func TestKnowledgeFolderMoveUpdateRequiresExactlyOneMatchingActiveRow(t *testing.T) {
	tests := []struct {
		name   string
		update interfaces.KnowledgeFolderMoveUpdate
	}{
		{
			name: "wrong tenant",
			update: interfaces.KnowledgeFolderMoveUpdate{
				TenantID:              8,
				KnowledgeBaseID:       "kb-1",
				KnowledgeID:           "knowledge-1",
				ExpectedFolderID:      "folder-old",
				ExpectedFolderVersion: 7,
				TargetFolderID:        "folder-new",
			},
		},
		{
			name: "wrong knowledge base",
			update: interfaces.KnowledgeFolderMoveUpdate{
				TenantID:              7,
				KnowledgeBaseID:       "kb-2",
				KnowledgeID:           "knowledge-1",
				ExpectedFolderID:      "folder-old",
				ExpectedFolderVersion: 7,
				TargetFolderID:        "folder-new",
			},
		},
		{
			name: "missing knowledge",
			update: interfaces.KnowledgeFolderMoveUpdate{
				TenantID:              7,
				KnowledgeBaseID:       "kb-1",
				KnowledgeID:           "knowledge-missing",
				ExpectedFolderID:      "folder-old",
				ExpectedFolderVersion: 7,
				TargetFolderID:        "folder-new",
			},
		},
		{
			name: "stale folder",
			update: interfaces.KnowledgeFolderMoveUpdate{
				TenantID:              7,
				KnowledgeBaseID:       "kb-1",
				KnowledgeID:           "knowledge-1",
				ExpectedFolderID:      "folder-stale",
				ExpectedFolderVersion: 7,
				TargetFolderID:        "folder-new",
			},
		},
		{
			name: "stale version",
			update: interfaces.KnowledgeFolderMoveUpdate{
				TenantID:              7,
				KnowledgeBaseID:       "kb-1",
				KnowledgeID:           "knowledge-1",
				ExpectedFolderID:      "folder-old",
				ExpectedFolderVersion: 6,
				TargetFolderID:        "folder-new",
			},
		},
		{
			name: "soft deleted",
			update: interfaces.KnowledgeFolderMoveUpdate{
				TenantID:              7,
				KnowledgeBaseID:       "kb-1",
				KnowledgeID:           "knowledge-deleted",
				ExpectedFolderID:      "folder-old",
				ExpectedFolderVersion: 7,
				TargetFolderID:        "folder-new",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeFolderMoveTestDB(t)
			repo := newKnowledgeFolderMoveWriteRepository(db)
			insertKnowledgeFolderMoveRow(t, db, "knowledge-1", 7, "kb-1", "folder-old", 7, 6, false)
			insertKnowledgeFolderMoveRow(t, db, "knowledge-deleted", 7, "kb-1", "folder-old", 7, 6, true)

			err := repo.UpdateKnowledgeFolderForMove(context.Background(), test.update)
			require.ErrorIs(t, err, ErrKnowledgeFolderMoveConflict)

			readID := "knowledge-1"
			if test.name == "soft deleted" {
				readID = "knowledge-deleted"
			}
			folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, db, readID)
			assert.Equal(t, "folder-old", folderID)
			assert.Equal(t, uint64(7), folderVersion)
			assert.Equal(t, uint64(6), folderIndexedVersion)
		})
	}
}

func TestKnowledgeFolderMoveUpdateRejectsCorruptOrOverflowVersions(t *testing.T) {
	tests := []struct {
		name    string
		version uint64
	}{
		{name: "zero", version: 0},
		{name: "signed bigint overflow boundary", version: uint64(math.MaxInt64)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeFolderMoveTestDB(t)
			repo := newKnowledgeFolderMoveWriteRepository(db)
			insertKnowledgeFolderMoveRow(t, db, "knowledge-1", 7, "kb-1", "folder-old", 7, 6, false)

			err := repo.UpdateKnowledgeFolderForMove(
				context.Background(),
				interfaces.KnowledgeFolderMoveUpdate{
					TenantID:              7,
					KnowledgeBaseID:       "kb-1",
					KnowledgeID:           "knowledge-1",
					ExpectedFolderID:      "folder-old",
					ExpectedFolderVersion: test.version,
					TargetFolderID:        "folder-new",
				},
			)
			require.ErrorIs(t, err, ErrKnowledgeFolderMoveDataIntegrity)

			folderID, folderVersion, folderIndexedVersion :=
				readKnowledgeFolderMoveState(t, db, "knowledge-1")
			assert.Equal(t, "folder-old", folderID)
			assert.Equal(t, uint64(7), folderVersion)
			assert.Equal(t, uint64(6), folderIndexedVersion)
		})
	}
}

func TestKnowledgeFolderMoveUpdateRejectsNoOpBeforeWriting(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := newKnowledgeFolderMoveWriteRepository(db)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-1", 7, "kb-1", "folder-a", 7, 6, false)

	err := repo.UpdateKnowledgeFolderForMove(
		context.Background(),
		interfaces.KnowledgeFolderMoveUpdate{
			TenantID:              7,
			KnowledgeBaseID:       "kb-1",
			KnowledgeID:           "knowledge-1",
			ExpectedFolderID:      "folder-a",
			ExpectedFolderVersion: 7,
			TargetFolderID:        "folder-a",
			UpdatedAt:             time.Now().UTC(),
		},
	)
	require.ErrorIs(t, err, ErrKnowledgeFolderMoveInvalid)

	folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, db, "knowledge-1")
	assert.Equal(t, "folder-a", folderID)
	assert.Equal(t, uint64(7), folderVersion)
	assert.Equal(t, uint64(6), folderIndexedVersion)
}

func TestKnowledgeFolderIndexPendingUpsertPreservesIdentityAndCreatedAt(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := newKnowledgeFolderMoveWriteRepository(db)
	firstCreatedAt := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	firstUpdatedAt := firstCreatedAt.Add(time.Minute)
	secondCreatedAt := firstCreatedAt.Add(2 * time.Hour)
	secondUpdatedAt := firstUpdatedAt.Add(3 * time.Hour)
	firstID := uuid.NewString()

	first := &types.KnowledgeFolderIndexPending{
		ID:               firstID,
		TenantID:         7,
		KnowledgeBaseID:  "kb-1",
		KnowledgeID:      "knowledge-1",
		TargetFolderID:   "folder-a",
		RequestedVersion: 2,
		CreatedAt:        firstCreatedAt,
		UpdatedAt:        firstUpdatedAt,
	}
	require.NoError(t, repo.UpsertKnowledgeFolderIndexPending(context.Background(), first))

	second := &types.KnowledgeFolderIndexPending{
		ID:               uuid.NewString(),
		TenantID:         7,
		KnowledgeBaseID:  "kb-1",
		KnowledgeID:      "knowledge-1",
		TargetFolderID:   "folder-b",
		RequestedVersion: 3,
		CreatedAt:        secondCreatedAt,
		UpdatedAt:        secondUpdatedAt,
	}
	require.NoError(t, repo.UpsertKnowledgeFolderIndexPending(context.Background(), second))

	var rows []*types.KnowledgeFolderIndexPending
	require.NoError(t, db.Order("knowledge_id ASC").Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, firstID, rows[0].ID)
	assert.True(t, rows[0].CreatedAt.Equal(firstCreatedAt), rows[0].CreatedAt)
	assert.Equal(t, "folder-b", rows[0].TargetFolderID)
	assert.Equal(t, uint64(3), rows[0].RequestedVersion)
	assert.True(t, rows[0].UpdatedAt.Equal(secondUpdatedAt), rows[0].UpdatedAt)
}

func TestKnowledgeFolderIndexPendingRejectsInvalidIdentityOrVersion(t *testing.T) {
	tests := []struct {
		name             string
		id               string
		requestedVersion uint64
	}{
		{name: "missing id", requestedVersion: 2},
		{name: "not uuid", id: "not-a-uuid", requestedVersion: 2},
		{
			name:             "uppercase id",
			id:               "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
			requestedVersion: 2,
		},
		{
			name:             "compact id",
			id:               "aaaaaaaaaaaa4aaa8aaaaaaaaaaaaaaa",
			requestedVersion: 2,
		},
		{name: "zero version", id: uuid.NewString(), requestedVersion: 0},
		{
			name:             "signed bigint overflow",
			id:               uuid.NewString(),
			requestedVersion: uint64(math.MaxInt64) + 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeFolderMoveTestDB(t)
			repo := newKnowledgeFolderMoveWriteRepository(db)
			pending := &types.KnowledgeFolderIndexPending{
				ID:               test.id,
				TenantID:         7,
				KnowledgeBaseID:  "kb-1",
				KnowledgeID:      "knowledge-1",
				TargetFolderID:   types.KnowledgeFolderRootID,
				RequestedVersion: test.requestedVersion,
			}

			err := repo.UpsertKnowledgeFolderIndexPending(context.Background(), pending)
			require.ErrorIs(t, err, ErrKnowledgeFolderIndexPendingInvalid)

			var count int64
			require.NoError(t, db.Model(&types.KnowledgeFolderIndexPending{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestKnowledgeFolderMoveTransactionReusesPostgresKnowledgeBaseLocks(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	repo := NewKnowledgeFolderMoveRepository(db)
	lockKey := knowledgeFolderAdvisoryLockKey(7, "kb-1")

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).
		WithArgs(lockKey).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT .* FROM "knowledge_bases".*FOR UPDATE`).
		WithArgs(uint64(7), "kb-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("kb-1"))
	mock.ExpectCommit()

	called := false
	err = repo.RunKnowledgeFolderMoveTransaction(
		context.Background(),
		7,
		"kb-1",
		func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
			called = true
			require.NotNil(t, txRepo)
			return nil
		},
	)
	require.NoError(t, err)
	assert.True(t, called)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderMoveSQLiteTransactionReplaysCallbackBusyErrors(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := NewKnowledgeFolderMoveRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 7)`,
	).Error)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-1", 7, "kb-1", "folder-old", 7, 6, false)
	pendingID := uuid.NewString()

	callbackCalls := 0
	err := repo.RunKnowledgeFolderMoveTransaction(
		context.Background(),
		7,
		"kb-1",
		func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
			callbackCalls++
			locked, lockErr := txRepo.LockKnowledgeForFolderMove(
				context.Background(),
				7,
				"kb-1",
				[]string{"knowledge-1"},
			)
			if lockErr != nil {
				return lockErr
			}
			if len(locked) != 1 {
				return ErrKnowledgeFolderMoveDataIntegrity
			}
			if updateErr := txRepo.UpdateKnowledgeFolderForMove(
				context.Background(),
				interfaces.KnowledgeFolderMoveUpdate{
					TenantID:              7,
					KnowledgeBaseID:       "kb-1",
					KnowledgeID:           locked[0].ID,
					ExpectedFolderID:      locked[0].FolderID,
					ExpectedFolderVersion: locked[0].FolderVersion,
					TargetFolderID:        "folder-new",
				},
			); updateErr != nil {
				return updateErr
			}
			if pendingErr := txRepo.UpsertKnowledgeFolderIndexPending(
				context.Background(),
				&types.KnowledgeFolderIndexPending{
					ID:               pendingID,
					TenantID:         7,
					KnowledgeBaseID:  "kb-1",
					KnowledgeID:      locked[0].ID,
					TargetFolderID:   "folder-new",
					RequestedVersion: locked[0].FolderVersion + 1,
				},
			); pendingErr != nil {
				return pendingErr
			}
			switch callbackCalls {
			case 1:
				return sqlite3.Error{Code: sqlite3.ErrBusy}
			case 2:
				return sqlite3.Error{Code: sqlite3.ErrLocked}
			default:
				return nil
			}
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 3, callbackCalls)

	folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, db, "knowledge-1")
	assert.Equal(t, "folder-new", folderID)
	assert.Equal(t, uint64(8), folderVersion, "rolled-back retries must not increment more than once")
	assert.Equal(t, uint64(6), folderIndexedVersion)

	var pendingRows []*types.KnowledgeFolderIndexPending
	require.NoError(t, db.Find(&pendingRows).Error)
	require.Len(t, pendingRows, 1)
	assert.Equal(t, pendingID, pendingRows[0].ID)
	assert.Equal(t, "folder-new", pendingRows[0].TargetFolderID)
	assert.Equal(t, uint64(8), pendingRows[0].RequestedVersion)
}

func TestKnowledgeFolderMoveTransactionRejectsNilContextBeforeCallback(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := NewKnowledgeFolderMoveRepository(db)
	callbackCalls := 0

	err := repo.RunKnowledgeFolderMoveTransaction(
		nil,
		7,
		"kb-1",
		func(interfaces.KnowledgeFolderMoveTxRepository) error {
			callbackCalls++
			return nil
		},
	)
	require.ErrorIs(t, err, ErrKnowledgeFolderMoveInvalid)
	assert.Zero(t, callbackCalls)
}

func TestKnowledgeFolderMoveTransactionCommitsKnowledgeAndPendingTogether(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := NewKnowledgeFolderMoveRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 7)`,
	).Error)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-1", 7, "kb-1", "folder-old", 7, 6, false)
	pendingID := uuid.NewString()
	requestedAt := time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC)

	err := repo.RunKnowledgeFolderMoveTransaction(
		context.Background(),
		7,
		"kb-1",
		func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
			locked, lockErr := txRepo.LockKnowledgeForFolderMove(
				context.Background(),
				7,
				"kb-1",
				[]string{"knowledge-1"},
			)
			if lockErr != nil {
				return lockErr
			}
			if len(locked) != 1 {
				return ErrKnowledgeFolderMoveDataIntegrity
			}
			if updateErr := txRepo.UpdateKnowledgeFolderForMove(
				context.Background(),
				interfaces.KnowledgeFolderMoveUpdate{
					TenantID:              7,
					KnowledgeBaseID:       "kb-1",
					KnowledgeID:           locked[0].ID,
					ExpectedFolderID:      locked[0].FolderID,
					ExpectedFolderVersion: locked[0].FolderVersion,
					TargetFolderID:        "folder-new",
					UpdatedAt:             requestedAt,
				},
			); updateErr != nil {
				return updateErr
			}
			return txRepo.UpsertKnowledgeFolderIndexPending(
				context.Background(),
				&types.KnowledgeFolderIndexPending{
					ID:               pendingID,
					TenantID:         7,
					KnowledgeBaseID:  "kb-1",
					KnowledgeID:      locked[0].ID,
					TargetFolderID:   "folder-new",
					RequestedVersion: locked[0].FolderVersion + 1,
					CreatedAt:        requestedAt,
					UpdatedAt:        requestedAt,
				},
			)
		},
	)
	require.NoError(t, err)

	folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, db, "knowledge-1")
	assert.Equal(t, "folder-new", folderID)
	assert.Equal(t, uint64(8), folderVersion)
	assert.Equal(t, uint64(6), folderIndexedVersion)

	var pending types.KnowledgeFolderIndexPending
	require.NoError(t, db.Where(
		"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ?",
		7,
		"kb-1",
		"knowledge-1",
	).Take(&pending).Error)
	assert.Equal(t, pendingID, pending.ID)
	assert.Equal(t, "folder-new", pending.TargetFolderID)
	assert.Equal(t, uint64(8), pending.RequestedVersion)
}

func TestKnowledgeFolderMoveTransactionMixedNoOpWritesOnlyChangedRows(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := NewKnowledgeFolderMoveRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 7)`,
	).Error)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-a", 7, "kb-1", "folder-old", 7, 6, false)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-b", 7, "kb-1", "folder-target", 4, 3, false)
	pendingID := uuid.NewString()

	err := repo.RunKnowledgeFolderMoveTransaction(
		context.Background(),
		7,
		"kb-1",
		func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
			locked, lockErr := txRepo.LockKnowledgeForFolderMove(
				context.Background(),
				7,
				"kb-1",
				[]string{"knowledge-b", "knowledge-a"},
			)
			if lockErr != nil {
				return lockErr
			}
			if len(locked) != 2 {
				return ErrKnowledgeFolderMoveDataIntegrity
			}
			for _, knowledge := range locked {
				if knowledge.FolderID == "folder-target" {
					continue
				}
				if updateErr := txRepo.UpdateKnowledgeFolderForMove(
					context.Background(),
					interfaces.KnowledgeFolderMoveUpdate{
						TenantID:              7,
						KnowledgeBaseID:       "kb-1",
						KnowledgeID:           knowledge.ID,
						ExpectedFolderID:      knowledge.FolderID,
						ExpectedFolderVersion: knowledge.FolderVersion,
						TargetFolderID:        "folder-target",
					},
				); updateErr != nil {
					return updateErr
				}
				if pendingErr := txRepo.UpsertKnowledgeFolderIndexPending(
					context.Background(),
					&types.KnowledgeFolderIndexPending{
						ID:               pendingID,
						TenantID:         7,
						KnowledgeBaseID:  "kb-1",
						KnowledgeID:      knowledge.ID,
						TargetFolderID:   "folder-target",
						RequestedVersion: knowledge.FolderVersion + 1,
					},
				); pendingErr != nil {
					return pendingErr
				}
			}
			return nil
		},
	)
	require.NoError(t, err)

	changedFolderID, changedVersion, changedIndexedVersion :=
		readKnowledgeFolderMoveState(t, db, "knowledge-a")
	assert.Equal(t, "folder-target", changedFolderID)
	assert.Equal(t, uint64(8), changedVersion)
	assert.Equal(t, uint64(6), changedIndexedVersion)

	unchangedFolderID, unchangedVersion, unchangedIndexedVersion :=
		readKnowledgeFolderMoveState(t, db, "knowledge-b")
	assert.Equal(t, "folder-target", unchangedFolderID)
	assert.Equal(t, uint64(4), unchangedVersion)
	assert.Equal(t, uint64(3), unchangedIndexedVersion)

	var pendingRows []*types.KnowledgeFolderIndexPending
	require.NoError(t, db.Find(&pendingRows).Error)
	require.Len(t, pendingRows, 1)
	assert.Equal(t, "knowledge-a", pendingRows[0].KnowledgeID)
	assert.Equal(t, uint64(8), pendingRows[0].RequestedVersion)
}

func TestKnowledgeFolderMoveTransactionAllNoOpWritesNoPendingRows(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := NewKnowledgeFolderMoveRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 7)`,
	).Error)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-a", 7, "kb-1", "folder-target", 4, 3, false)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-b", 7, "kb-1", "folder-target", 9, 8, false)

	err := repo.RunKnowledgeFolderMoveTransaction(
		context.Background(),
		7,
		"kb-1",
		func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
			locked, lockErr := txRepo.LockKnowledgeForFolderMove(
				context.Background(),
				7,
				"kb-1",
				[]string{"knowledge-b", "knowledge-a"},
			)
			if lockErr != nil {
				return lockErr
			}
			if len(locked) != 2 {
				return ErrKnowledgeFolderMoveDataIntegrity
			}
			for _, knowledge := range locked {
				if knowledge.FolderID != "folder-target" {
					return ErrKnowledgeFolderMoveDataIntegrity
				}
			}
			return nil
		},
	)
	require.NoError(t, err)

	for knowledgeID, expected := range map[string][2]uint64{
		"knowledge-a": [2]uint64{4, 3},
		"knowledge-b": [2]uint64{9, 8},
	} {
		folderID, folderVersion, folderIndexedVersion :=
			readKnowledgeFolderMoveState(t, db, knowledgeID)
		assert.Equal(t, "folder-target", folderID)
		assert.Equal(t, expected[0], folderVersion)
		assert.Equal(t, expected[1], folderIndexedVersion)
	}
	var pendingCount int64
	require.NoError(t, db.Model(&types.KnowledgeFolderIndexPending{}).Count(&pendingCount).Error)
	assert.Zero(t, pendingCount)
}

func TestKnowledgeFolderMoveTransactionRollsBackEarlierRowsOnLaterDeletingCASConflict(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := NewKnowledgeFolderMoveRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 7)`,
	).Error)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-a", 7, "kb-1", "folder-old", 7, 6, false)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-b", 7, "kb-1", "folder-old", 7, 6, false)
	setKnowledgeFolderMoveParseStatus(t, db, "knowledge-b", types.ParseStatusDeleting)
	pendingID := uuid.NewString()

	err := repo.RunKnowledgeFolderMoveTransaction(
		context.Background(),
		7,
		"kb-1",
		func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
			locked, lockErr := txRepo.LockKnowledgeForFolderMove(
				context.Background(),
				7,
				"kb-1",
				[]string{"knowledge-b", "knowledge-a"},
			)
			if lockErr != nil {
				return lockErr
			}
			if len(locked) != 2 {
				return ErrKnowledgeFolderMoveDataIntegrity
			}
			if locked[0].ID != "knowledge-a" ||
				locked[1].ID != "knowledge-b" ||
				locked[1].ParseStatus != types.ParseStatusDeleting {
				return ErrKnowledgeFolderMoveDataIntegrity
			}
			if updateErr := txRepo.UpdateKnowledgeFolderForMove(
				context.Background(),
				interfaces.KnowledgeFolderMoveUpdate{
					TenantID:              7,
					KnowledgeBaseID:       "kb-1",
					KnowledgeID:           locked[0].ID,
					ExpectedFolderID:      locked[0].FolderID,
					ExpectedFolderVersion: locked[0].FolderVersion,
					TargetFolderID:        "folder-new",
				},
			); updateErr != nil {
				return updateErr
			}
			if pendingErr := txRepo.UpsertKnowledgeFolderIndexPending(
				context.Background(),
				&types.KnowledgeFolderIndexPending{
					ID:               pendingID,
					TenantID:         7,
					KnowledgeBaseID:  "kb-1",
					KnowledgeID:      locked[0].ID,
					TargetFolderID:   "folder-new",
					RequestedVersion: locked[0].FolderVersion + 1,
				},
			); pendingErr != nil {
				return pendingErr
			}
			return txRepo.UpdateKnowledgeFolderForMove(
				context.Background(),
				interfaces.KnowledgeFolderMoveUpdate{
					TenantID:              7,
					KnowledgeBaseID:       "kb-1",
					KnowledgeID:           locked[1].ID,
					ExpectedFolderID:      locked[1].FolderID,
					ExpectedFolderVersion: locked[1].FolderVersion,
					TargetFolderID:        "folder-new",
				},
			)
		},
	)
	require.ErrorIs(t, err, ErrKnowledgeFolderMoveConflict)

	for _, knowledgeID := range []string{"knowledge-a", "knowledge-b"} {
		folderID, folderVersion, folderIndexedVersion :=
			readKnowledgeFolderMoveState(t, db, knowledgeID)
		assert.Equal(t, "folder-old", folderID)
		assert.Equal(t, uint64(7), folderVersion)
		assert.Equal(t, uint64(6), folderIndexedVersion)
	}
	var pendingCount int64
	require.NoError(t, db.Model(&types.KnowledgeFolderIndexPending{}).Count(&pendingCount).Error)
	assert.Zero(t, pendingCount)
}

func TestKnowledgeFolderMoveTransactionRollsBackKnowledgeAndPendingTogether(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := NewKnowledgeFolderMoveRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 7)`,
	).Error)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-1", 7, "kb-1", "folder-old", 7, 6, false)
	rollbackErr := errors.New("rollback move")
	pendingID := uuid.NewString()

	err := repo.RunKnowledgeFolderMoveTransaction(
		context.Background(),
		7,
		"kb-1",
		func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
			locked, lockErr := txRepo.LockKnowledgeForFolderMove(
				context.Background(),
				7,
				"kb-1",
				[]string{"knowledge-1"},
			)
			require.NoError(t, lockErr)
			require.Len(t, locked, 1)
			require.NoError(t, txRepo.UpdateKnowledgeFolderForMove(
				context.Background(),
				interfaces.KnowledgeFolderMoveUpdate{
					TenantID:              7,
					KnowledgeBaseID:       "kb-1",
					KnowledgeID:           "knowledge-1",
					ExpectedFolderID:      "folder-old",
					ExpectedFolderVersion: 7,
					TargetFolderID:        "folder-new",
					UpdatedAt:             time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC),
				},
			))
			require.NoError(t, txRepo.UpsertKnowledgeFolderIndexPending(
				context.Background(),
				&types.KnowledgeFolderIndexPending{
					ID:               pendingID,
					TenantID:         7,
					KnowledgeBaseID:  "kb-1",
					KnowledgeID:      "knowledge-1",
					TargetFolderID:   "folder-new",
					RequestedVersion: 8,
				},
			))
			return rollbackErr
		},
	)
	require.ErrorIs(t, err, rollbackErr)

	folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, db, "knowledge-1")
	assert.Equal(t, "folder-old", folderID)
	assert.Equal(t, uint64(7), folderVersion)
	assert.Equal(t, uint64(6), folderIndexedVersion)
	var pendingCount int64
	require.NoError(t, db.Model(&types.KnowledgeFolderIndexPending{}).Count(&pendingCount).Error)
	assert.Zero(t, pendingCount)
}

func TestKnowledgeFolderMoveTransactionRollsBackKnowledgeWhenPendingUpsertFails(t *testing.T) {
	db := setupKnowledgeFolderMoveTestDB(t)
	repo := NewKnowledgeFolderMoveRepository(db)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 7)`,
	).Error)
	insertKnowledgeFolderMoveRow(t, db, "knowledge-1", 7, "kb-1", "folder-old", 7, 6, false)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_knowledge_folder_index_pending_insert
		BEFORE INSERT ON knowledge_folder_index_pending
		BEGIN
			SELECT RAISE(ABORT, 'forced pending insert failure');
		END
	`).Error)
	pendingID := uuid.NewString()

	err := repo.RunKnowledgeFolderMoveTransaction(
		context.Background(),
		7,
		"kb-1",
		func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
			updateErr := txRepo.UpdateKnowledgeFolderForMove(
				context.Background(),
				interfaces.KnowledgeFolderMoveUpdate{
					TenantID:              7,
					KnowledgeBaseID:       "kb-1",
					KnowledgeID:           "knowledge-1",
					ExpectedFolderID:      "folder-old",
					ExpectedFolderVersion: 7,
					TargetFolderID:        "folder-new",
					UpdatedAt:             time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC),
				},
			)
			require.NoError(t, updateErr)

			return txRepo.UpsertKnowledgeFolderIndexPending(
				context.Background(),
				&types.KnowledgeFolderIndexPending{
					ID:               pendingID,
					TenantID:         7,
					KnowledgeBaseID:  "kb-1",
					KnowledgeID:      "knowledge-1",
					TargetFolderID:   "folder-new",
					RequestedVersion: 8,
				},
			)
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forced pending insert failure")

	folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, db, "knowledge-1")
	assert.Equal(t, "folder-old", folderID)
	assert.Equal(t, uint64(7), folderVersion)
	assert.Equal(t, uint64(6), folderIndexedVersion)

	var pendingCount int64
	require.NoError(t, db.Model(&types.KnowledgeFolderIndexPending{}).Count(&pendingCount).Error)
	assert.Zero(t, pendingCount)
}

func TestKnowledgeFolderMoveAndDeleteEmptyShareKnowledgeBaseSerialization(t *testing.T) {
	dbA, dbB := setupConcurrentKnowledgeFolderMoveTestDBs(t)
	moveRepo := newKnowledgeFolderRepository(dbA)
	treeRepo := newKnowledgeFolderRepository(dbB)
	require.NoError(t, dbA.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 7)`,
	).Error)
	sourceFolder := knowledgeFolderFixture(
		"move-source",
		7,
		"kb-1",
		"",
		"Move source",
		"/move-source/",
		1,
	)
	targetFolder := knowledgeFolderFixture(
		"move-target",
		7,
		"kb-1",
		"",
		"Move target",
		"/move-target/",
		1,
	)
	require.NoError(t, dbA.Create(sourceFolder).Error)
	require.NoError(t, dbA.Create(targetFolder).Error)
	insertKnowledgeFolderMoveRow(
		t,
		dbA,
		"knowledge-1",
		7,
		"kb-1",
		sourceFolder.ID,
		7,
		6,
		false,
	)

	moveEntered := make(chan struct{}, 1)
	releaseMove := make(chan struct{})
	t.Cleanup(func() { close(releaseMove) })
	moveErr := make(chan error, 1)
	pendingID := uuid.NewString()
	go func() {
		moveErr <- moveRepo.RunKnowledgeFolderMoveTransaction(
			context.Background(),
			7,
			"kb-1",
			func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
				if err := moveLockedKnowledgeForTest(
					context.Background(),
					txRepo,
					7,
					"kb-1",
					"knowledge-1",
					targetFolder.ID,
					pendingID,
				); err != nil {
					return err
				}
				moveEntered <- struct{}{}
				<-releaseMove
				return nil
			},
		)
	}()

	select {
	case <-moveEntered:
	case <-time.After(knowledgeFolderMoveTestTimeout):
		t.Fatal("move callback did not reach its controlled commit point")
	}

	deleteBlocked := make(chan struct{}, 1)
	allowDeleteRetry := make(chan struct{})
	t.Cleanup(func() { close(allowDeleteRetry) })
	treeRepo.sqliteRetryWait = func(ctx context.Context, attempt int) error {
		if attempt != 0 {
			return ErrKnowledgeFolderMoveDataIntegrity
		}
		deleteBlocked <- struct{}{}
		select {
		case <-allowDeleteRetry:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	deleteEntered := make(chan struct{}, 1)
	deleteErr := make(chan error, 1)
	go func() {
		deleteErr <- treeRepo.RunTreeWriteTransaction(
			context.Background(),
			7,
			"kb-1",
			func(txRepo interfaces.KnowledgeFolderTreeRepository) error {
				deleteEntered <- struct{}{}
				return txRepo.DeleteEmpty(
					context.Background(),
					7,
					"kb-1",
					sourceFolder.ID,
				)
			},
		)
	}()
	select {
	case <-deleteBlocked:
	case <-time.After(knowledgeFolderMoveTestTimeout):
		t.Fatal("DeleteEmpty did not contend on the shared knowledge-base writer lock")
	}
	select {
	case <-deleteEntered:
		t.Fatal("DeleteEmpty callback entered before acquiring the shared knowledge-base writer lock")
	default:
	}

	releaseMove <- struct{}{}
	select {
	case err := <-moveErr:
		require.NoError(t, err)
	case <-time.After(knowledgeFolderMoveTestTimeout):
		t.Fatal("move transaction did not finish")
	}
	allowDeleteRetry <- struct{}{}
	select {
	case err := <-deleteErr:
		require.NoError(t, err)
	case <-time.After(knowledgeFolderMoveTestTimeout):
		t.Fatal("DeleteEmpty transaction did not finish")
	}

	folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, dbA, "knowledge-1")
	assert.Equal(t, targetFolder.ID, folderID)
	assert.Equal(t, uint64(8), folderVersion)
	assert.Equal(t, uint64(6), folderIndexedVersion)

	var deletedSource types.KnowledgeFolder
	require.NoError(t, dbA.Unscoped().Where("id = ?", sourceFolder.ID).Take(&deletedSource).Error)
	assert.True(t, deletedSource.DeletedAt.Valid)

	var pending types.KnowledgeFolderIndexPending
	require.NoError(t, dbA.Where("knowledge_id = ?", "knowledge-1").Take(&pending).Error)
	assert.Equal(t, targetFolder.ID, pending.TargetFolderID)
	assert.Equal(t, uint64(8), pending.RequestedVersion)
}

func TestConcurrentKnowledgeFolderMovesCommitInDeterministicOrder(t *testing.T) {
	dbA, dbB := setupConcurrentKnowledgeFolderMoveTestDBs(t)
	firstRepo := newKnowledgeFolderRepository(dbA)
	secondRepo := newKnowledgeFolderRepository(dbB)
	require.NoError(t, dbA.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 7)`,
	).Error)
	insertKnowledgeFolderMoveRow(t, dbA, "knowledge-1", 7, "kb-1", "", 1, 0, false)
	targetA := knowledgeFolderTestID("concurrent-target-a")
	targetB := knowledgeFolderTestID("concurrent-target-b")
	firstPendingID := uuid.NewString()
	secondPendingID := uuid.NewString()

	firstEntered := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	t.Cleanup(func() { close(releaseFirst) })
	firstErr := make(chan error, 1)
	go func() {
		firstErr <- firstRepo.RunKnowledgeFolderMoveTransaction(
			context.Background(),
			7,
			"kb-1",
			func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
				if err := moveLockedKnowledgeForTest(
					context.Background(),
					txRepo,
					7,
					"kb-1",
					"knowledge-1",
					targetA,
					firstPendingID,
				); err != nil {
					return err
				}
				firstEntered <- struct{}{}
				<-releaseFirst
				return nil
			},
		)
	}()
	select {
	case <-firstEntered:
	case <-time.After(knowledgeFolderMoveTestTimeout):
		t.Fatal("first move callback did not reach its controlled commit point")
	}

	secondBlocked := make(chan struct{}, 1)
	allowSecondRetry := make(chan struct{})
	t.Cleanup(func() { close(allowSecondRetry) })
	secondRepo.sqliteRetryWait = func(ctx context.Context, attempt int) error {
		if attempt != 0 {
			return ErrKnowledgeFolderMoveDataIntegrity
		}
		secondBlocked <- struct{}{}
		select {
		case <-allowSecondRetry:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	secondEntered := make(chan struct{}, 1)
	secondErr := make(chan error, 1)
	go func() {
		secondErr <- secondRepo.RunKnowledgeFolderMoveTransaction(
			context.Background(),
			7,
			"kb-1",
			func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
				secondEntered <- struct{}{}
				return moveLockedKnowledgeForTest(
					context.Background(),
					txRepo,
					7,
					"kb-1",
					"knowledge-1",
					targetB,
					secondPendingID,
				)
			},
		)
	}()
	select {
	case <-secondBlocked:
	case <-time.After(knowledgeFolderMoveTestTimeout):
		t.Fatal("second move did not contend on the knowledge-base writer lock")
	}
	select {
	case <-secondEntered:
		t.Fatal("second move callback entered before acquiring the knowledge-base writer lock")
	default:
	}

	releaseFirst <- struct{}{}
	select {
	case err := <-firstErr:
		require.NoError(t, err)
	case <-time.After(knowledgeFolderMoveTestTimeout):
		t.Fatal("first move transaction did not finish")
	}
	allowSecondRetry <- struct{}{}
	select {
	case err := <-secondErr:
		require.NoError(t, err)
	case <-time.After(knowledgeFolderMoveTestTimeout):
		t.Fatal("second move transaction did not finish")
	}

	folderID, folderVersion, folderIndexedVersion := readKnowledgeFolderMoveState(t, dbA, "knowledge-1")
	assert.Equal(t, targetB, folderID)
	assert.Equal(t, uint64(3), folderVersion)
	assert.Zero(t, folderIndexedVersion)

	var pendingRows []*types.KnowledgeFolderIndexPending
	require.NoError(t, dbA.Find(&pendingRows).Error)
	require.Len(t, pendingRows, 1)
	assert.Equal(t, firstPendingID, pendingRows[0].ID,
		"latest-wins upsert must preserve the first row identity")
	assert.NotEqual(t, secondPendingID, pendingRows[0].ID)
	assert.Equal(t, targetB, pendingRows[0].TargetFolderID)
	assert.Equal(t, uint64(3), pendingRows[0].RequestedVersion)
}

func TestKnowledgeCrossKBMovePersistsTargetAndResetFolderStateInOneUpdate(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	baseRepo := NewKnowledgeRepository(db)
	moveRepo, ok := baseRepo.(interfaces.KnowledgeCrossKBMoveRepository)
	require.True(t, ok)
	ctx := context.Background()
	sourceKBID := uuid.NewString()
	targetKBID := uuid.NewString()
	knowledge := &types.Knowledge{
		TenantID:             7,
		KnowledgeBaseID:      sourceKBID,
		FolderID:             uuid.NewString(),
		FolderVersion:        9,
		FolderIndexedVersion: 8,
		Type:                 "document",
		Title:                "source title",
		Description:          "source description",
		Source:               "upload",
		ParseStatus:          types.ParseStatusProcessing,
		PendingSubtasksCount: 5,
		EnableStatus:         "enabled",
		FileSize:             42,
	}
	require.NoError(t, baseRepo.CreateKnowledge(ctx, knowledge))

	loaded, err := baseRepo.GetKnowledgeByID(ctx, 7, knowledge.ID)
	require.NoError(t, err)
	loaded.KnowledgeBaseID = targetKBID
	loaded.FolderID = types.KnowledgeFolderRootID
	loaded.FolderVersion = 1
	loaded.FolderIndexedVersion = 0
	loaded.Title = "target title"
	loaded.Description = ""
	loaded.FileSize = 0
	loaded.PendingSubtasksCount = 0
	loaded.ParseStatus = types.ParseStatusCompleted

	updateCalls := 0
	var updateSQL string
	var updateVars []interface{}
	require.NoError(t, db.Callback().Update().
		After("gorm:update").
		Register("phase35:count_cross_kb_move_update", func(*gorm.DB) {
			updateCalls++
		}))
	require.NoError(t, db.Callback().Update().
		After("phase35:count_cross_kb_move_update").
		Register("phase35:capture_cross_kb_move_update", func(tx *gorm.DB) {
			updateSQL = tx.Statement.SQL.String()
			updateVars = append([]interface{}(nil), tx.Statement.Vars...)
		}))

	require.NoError(t, moveRepo.UpdateKnowledgeForCrossKBMove(ctx, loaded, sourceKBID))
	assert.Equal(t, 1, updateCalls, "cross-KB move must persist target and folder reset in one UPDATE")
	whereParts := strings.SplitN(updateSQL, " WHERE ", 2)
	require.Len(t, whereParts, 2)
	assert.Contains(t, whereParts[1], "parse_status")
	assert.Contains(t, updateVars, types.ParseStatusProcessing)

	reloaded, err := baseRepo.GetKnowledgeByID(ctx, 7, knowledge.ID)
	require.NoError(t, err)
	assert.Equal(t, targetKBID, reloaded.KnowledgeBaseID)
	assert.Equal(t, types.KnowledgeFolderRootID, reloaded.FolderID)
	assert.Equal(t, uint64(1), reloaded.FolderVersion)
	assert.Zero(t, reloaded.FolderIndexedVersion)
	assert.Equal(t, "target title", reloaded.Title)
	assert.Empty(t, reloaded.Description, "zero-value fields must retain generic Save semantics")
	assert.Zero(t, reloaded.FileSize, "zero-value fields must retain generic Save semantics")
	assert.Equal(t, types.ParseStatusCompleted, reloaded.ParseStatus)
	assert.Equal(t, 5, reloaded.PendingSubtasksCount,
		"cross-KB move must not overwrite the independently managed orchestration counter")

	var sourceCount int64
	require.NoError(t, db.Model(&types.Knowledge{}).
		Where("tenant_id = ? AND id = ? AND knowledge_base_id = ?", 7, knowledge.ID, sourceKBID).
		Count(&sourceCount).Error)
	assert.Zero(t, sourceCount, "the moved row must no longer remain in the source KB scope")

	var targetCount int64
	require.NoError(t, db.Model(&types.Knowledge{}).
		Where("tenant_id = ? AND id = ? AND knowledge_base_id = ?", 7, knowledge.ID, targetKBID).
		Count(&targetCount).Error)
	assert.Equal(t, int64(1), targetCount)
}

func TestKnowledgeCrossKBMoveAllowsProcessingSourceToPendingTarget(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	baseRepo := NewKnowledgeRepository(db)
	moveRepo, ok := baseRepo.(interfaces.KnowledgeCrossKBMoveRepository)
	require.True(t, ok)
	ctx := context.Background()
	sourceKBID := uuid.NewString()
	targetKBID := uuid.NewString()
	source := &types.Knowledge{
		TenantID:             7,
		KnowledgeBaseID:      sourceKBID,
		FolderID:             uuid.NewString(),
		FolderVersion:        4,
		FolderIndexedVersion: 2,
		Type:                 "document",
		Title:                "source",
		Source:               "upload",
		ParseStatus:          types.ParseStatusProcessing,
	}
	require.NoError(t, baseRepo.CreateKnowledge(ctx, source))

	target := *source
	target.KnowledgeBaseID = targetKBID
	target.FolderID = types.KnowledgeFolderRootID
	target.FolderVersion = 1
	target.FolderIndexedVersion = 0
	target.ParseStatus = types.ParseStatusPending

	require.NoError(t, moveRepo.UpdateKnowledgeForCrossKBMove(
		ctx,
		&target,
		sourceKBID,
	))

	reloaded, err := baseRepo.GetKnowledgeByID(ctx, 7, source.ID)
	require.NoError(t, err)
	assert.Equal(t, targetKBID, reloaded.KnowledgeBaseID)
	assert.Equal(t, types.ParseStatusPending, reloaded.ParseStatus)
	assert.Equal(t, types.KnowledgeFolderRootID, reloaded.FolderID)
	assert.Equal(t, uint64(1), reloaded.FolderVersion)
	assert.Zero(t, reloaded.FolderIndexedVersion)
}

func TestKnowledgeCrossKBMoveRejectsSourceStatusOtherThanProcessing(t *testing.T) {
	tests := []struct {
		name         string
		sourceStatus string
		targetStatus string
	}{
		{
			name:         "deleting source",
			sourceStatus: types.ParseStatusDeleting,
			targetStatus: types.ParseStatusCompleted,
		},
		{
			name:         "completed source",
			sourceStatus: types.ParseStatusCompleted,
			targetStatus: types.ParseStatusPending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupKnowledgeTestDB(t)
			baseRepo := NewKnowledgeRepository(db)
			moveRepo, ok := baseRepo.(interfaces.KnowledgeCrossKBMoveRepository)
			require.True(t, ok)
			source := &types.Knowledge{
				TenantID:             7,
				KnowledgeBaseID:      "kb-source",
				FolderID:             "folder-source",
				FolderVersion:        4,
				FolderIndexedVersion: 3,
				Type:                 "document",
				Title:                "source",
				Source:               "upload",
				ParseStatus:          tt.sourceStatus,
			}
			require.NoError(t, baseRepo.CreateKnowledge(context.Background(), source))

			target := *source
			target.KnowledgeBaseID = "kb-target"
			target.FolderID = types.KnowledgeFolderRootID
			target.FolderVersion = 1
			target.FolderIndexedVersion = 0
			target.ParseStatus = tt.targetStatus

			err := moveRepo.UpdateKnowledgeForCrossKBMove(
				context.Background(),
				&target,
				"kb-source",
			)
			require.ErrorIs(t, err, ErrKnowledgeCrossKBMoveConflict)

			reloaded, reloadErr := baseRepo.GetKnowledgeByID(
				context.Background(),
				7,
				source.ID,
			)
			require.NoError(t, reloadErr)
			assert.Equal(t, "kb-source", reloaded.KnowledgeBaseID)
			assert.Equal(t, "folder-source", reloaded.FolderID)
			assert.Equal(t, uint64(4), reloaded.FolderVersion)
			assert.Equal(t, uint64(3), reloaded.FolderIndexedVersion)
			assert.Equal(t, tt.sourceStatus, reloaded.ParseStatus)
		})
	}
}

func TestKnowledgeCrossKBMoveRejectsSameKnowledgeBaseBeforeUpdate(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	baseRepo := NewKnowledgeRepository(db)
	moveRepo, ok := baseRepo.(interfaces.KnowledgeCrossKBMoveRepository)
	require.True(t, ok)
	knowledge := &types.Knowledge{
		ID:                   uuid.NewString(),
		TenantID:             7,
		KnowledgeBaseID:      uuid.NewString(),
		FolderID:             types.KnowledgeFolderRootID,
		FolderVersion:        1,
		FolderIndexedVersion: 0,
	}
	updateCalls := 0
	require.NoError(t, db.Callback().Update().
		Before("gorm:update").
		Register("phase35:reject_same_cross_kb_move", func(*gorm.DB) {
			updateCalls++
		}))

	err := moveRepo.UpdateKnowledgeForCrossKBMove(
		context.Background(),
		knowledge,
		knowledge.KnowledgeBaseID,
	)
	require.ErrorIs(t, err, ErrKnowledgeCrossKBMoveInvalid)
	assert.Zero(t, updateCalls)
}

func TestKnowledgeCrossKBMoveRejectsNonRootFolderStateBeforeUpdate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.Knowledge)
	}{
		{
			name: "folder id",
			mutate: func(knowledge *types.Knowledge) {
				knowledge.FolderID = "source-folder"
			},
		},
		{
			name: "folder version",
			mutate: func(knowledge *types.Knowledge) {
				knowledge.FolderVersion = 2
			},
		},
		{
			name: "folder indexed version",
			mutate: func(knowledge *types.Knowledge) {
				knowledge.FolderIndexedVersion = 1
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeTestDB(t)
			baseRepo := NewKnowledgeRepository(db)
			moveRepo, ok := baseRepo.(interfaces.KnowledgeCrossKBMoveRepository)
			require.True(t, ok)
			knowledge := &types.Knowledge{
				ID:                   uuid.NewString(),
				TenantID:             7,
				KnowledgeBaseID:      uuid.NewString(),
				FolderID:             types.KnowledgeFolderRootID,
				FolderVersion:        1,
				FolderIndexedVersion: 0,
			}
			test.mutate(knowledge)
			updateCalls := 0
			require.NoError(t, db.Callback().Update().
				Before("gorm:update").
				Register("phase35:reject_invalid_cross_kb_folder_state", func(*gorm.DB) {
					updateCalls++
				}))

			err := moveRepo.UpdateKnowledgeForCrossKBMove(
				context.Background(),
				knowledge,
				uuid.NewString(),
			)
			require.ErrorIs(t, err, ErrKnowledgeCrossKBMoveInvalidFolderState)
			assert.Zero(t, updateCalls)
		})
	}
}

func TestKnowledgeCrossKBMoveRequiresExactlyOneScopedActiveSourceRow(t *testing.T) {
	tests := []struct {
		name           string
		inputTenantID  uint64
		sourceKBID     string
		softDeleteSeed bool
	}{
		{
			name:          "wrong tenant",
			inputTenantID: 8,
			sourceKBID:    "kb-source",
		},
		{
			name:          "wrong source knowledge base",
			inputTenantID: 7,
			sourceKBID:    "kb-other",
		},
		{
			name:           "soft deleted source",
			inputTenantID:  7,
			sourceKBID:     "kb-source",
			softDeleteSeed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeTestDB(t)
			baseRepo := NewKnowledgeRepository(db)
			moveRepo, ok := baseRepo.(interfaces.KnowledgeCrossKBMoveRepository)
			require.True(t, ok)
			seed := &types.Knowledge{
				TenantID:             7,
				KnowledgeBaseID:      "kb-source",
				FolderID:             "folder-source",
				FolderVersion:        4,
				FolderIndexedVersion: 3,
				Type:                 "document",
				Title:                "source",
				Source:               "upload",
				ParseStatus:          types.ParseStatusProcessing,
			}
			require.NoError(t, baseRepo.CreateKnowledge(context.Background(), seed))
			if test.softDeleteSeed {
				require.NoError(t, db.Delete(&types.Knowledge{}, "id = ?", seed.ID).Error)
			}

			target := *seed
			target.TenantID = test.inputTenantID
			target.KnowledgeBaseID = "kb-target"
			target.FolderID = types.KnowledgeFolderRootID
			target.FolderVersion = 1
			target.FolderIndexedVersion = 0
			target.ParseStatus = types.ParseStatusCompleted

			err := moveRepo.UpdateKnowledgeForCrossKBMove(
				context.Background(),
				&target,
				test.sourceKBID,
			)
			require.ErrorIs(t, err, ErrKnowledgeCrossKBMoveConflict)

			var sourceRow types.Knowledge
			require.NoError(t, db.Unscoped().Where("id = ?", seed.ID).Take(&sourceRow).Error)
			assert.Equal(t, "kb-source", sourceRow.KnowledgeBaseID)
			assert.Equal(t, "folder-source", sourceRow.FolderID)
			assert.Equal(t, uint64(4), sourceRow.FolderVersion)
			assert.Equal(t, uint64(3), sourceRow.FolderIndexedVersion)
		})
	}
}
