package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestKnowledgeFolderTreeRepositoryCreateIfAbsentRowsAffected(t *testing.T) {
	tests := []struct {
		name        string
		rows        int64
		wantCreated bool
		wantErr     error
	}{
		{name: "insert success", rows: 1, wantCreated: true},
		{name: "conflict does nothing", rows: 0, wantCreated: false},
		{
			name:    "unexpected rows fail closed",
			rows:    2,
			wantErr: ErrKnowledgeFolderDataIntegrity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := newKnowledgeFolderCreateIfAbsentSQLMock(t)
			folder := knowledgeFolderCreateIfAbsentFixture("candidate", "Candidate")
			mock.ExpectExec(knowledgeFolderCreateIfAbsentInsertPattern()).
				WillReturnResult(sqlmock.NewResult(0, tt.rows))

			created, err := repo.CreateIfAbsent(context.Background(), folder)

			if tt.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.wantErr)
			}
			assert.Equal(t, tt.wantCreated, created)
			assert.Equal(t, knowledgeFolderTestID("candidate"), folder.ID)
			assert.Equal(t, "/"+folder.ID+"/", folder.Path)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKnowledgeFolderTreeRepositoryCreateIfAbsentRejectsInvalidInputBeforeSQL(t *testing.T) {
	t.Run("nil context", func(t *testing.T) {
		repo, mock := newKnowledgeFolderCreateIfAbsentSQLMock(t)
		created, err := repo.CreateIfAbsent(
			nil,
			knowledgeFolderCreateIfAbsentFixture("nil-context", "Folder"),
		)
		require.ErrorIs(t, err, ErrKnowledgeFolderInvalid)
		assert.False(t, created)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid folder", func(t *testing.T) {
		repo, mock := newKnowledgeFolderCreateIfAbsentSQLMock(t)
		created, err := repo.CreateIfAbsent(context.Background(), nil)
		require.ErrorIs(t, err, ErrKnowledgeFolderInvalid)
		assert.False(t, created)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("canceled context", func(t *testing.T) {
		repo, mock := newKnowledgeFolderCreateIfAbsentSQLMock(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		created, err := repo.CreateIfAbsent(
			ctx,
			knowledgeFolderCreateIfAbsentFixture("canceled", "Folder"),
		)
		require.ErrorIs(t, err, context.Canceled)
		assert.False(t, created)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestKnowledgeFolderTreeRepositoryCreateIfAbsentPropagatesDatabaseError(t *testing.T) {
	repo, mock := newKnowledgeFolderCreateIfAbsentSQLMock(t)
	databaseErr := errors.New("database unavailable")
	mock.ExpectExec(knowledgeFolderCreateIfAbsentInsertPattern()).WillReturnError(databaseErr)

	created, err := repo.CreateIfAbsent(
		context.Background(),
		knowledgeFolderCreateIfAbsentFixture("database-error", "Folder"),
	)

	require.ErrorIs(t, err, databaseErr)
	assert.False(t, created)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderTreeRepositoryCreateIfAbsentKeepsSQLiteTransactionUsable(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	ctx := context.Background()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES (?, ?)`,
		"kb-1",
		uint64(1),
	).Error)

	existing := knowledgeFolderCreateIfAbsentFixture("existing", "Shared")
	require.NoError(t, newKnowledgeFolderTreeRepository(db).Create(ctx, existing))
	candidate := knowledgeFolderCreateIfAbsentFixture("candidate", "Shared")
	repo := newKnowledgeFolderRepository(db)
	var rereadID string

	err := repo.RunTreeWriteTransaction(
		ctx,
		1,
		"kb-1",
		func(txRepo interfaces.KnowledgeFolderTreeRepository) error {
			created, createErr := txRepo.CreateIfAbsent(ctx, candidate)
			if createErr != nil {
				return createErr
			}
			if created {
				return fmt.Errorf("conflicting sibling was unexpectedly inserted")
			}
			folder, getErr := txRepo.GetByParentAndName(ctx, 1, "kb-1", "", "Shared")
			if getErr != nil {
				return getErr
			}
			if folder == nil {
				return fmt.Errorf("conflicting sibling reread returned nil")
			}
			rereadID = folder.ID
			return nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, existing.ID, rereadID)
	var activeCount int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND parent_id = ? AND name = ?",
			uint64(1),
			"kb-1",
			"",
			"Shared",
		).
		Count(&activeCount).Error)
	assert.Equal(t, int64(1), activeCount)
}

func TestKnowledgeFolderRepositoryListByParentAndNamesEmptySkipsQuery(t *testing.T) {
	repo, mock := newKnowledgeFolderNamesSQLMock(t)

	folders, err := repo.ListByParentAndNames(context.Background(), 1, "kb-1", "", nil)
	require.NoError(t, err)
	require.NotNil(t, folders)
	assert.Empty(t, folders)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderRepositoryListByParentAndNamesScopesActiveRows(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderReader(db)
	ctx := context.Background()
	parentID := knowledgeFolderTestID("parent")

	target := knowledgeFolderFixture("target", 1, "kb-1", "parent", "target", "/parent/target/", 2)
	deleted := knowledgeFolderFixture("deleted", 1, "kb-1", "parent", "deleted", "/parent/deleted/", 2)
	for _, folder := range []*types.KnowledgeFolder{
		target,
		deleted,
		knowledgeFolderFixture("other-tenant", 2, "kb-1", "parent", "other-tenant", "/parent/other-tenant/", 2),
		knowledgeFolderFixture("other-kb", 1, "kb-2", "parent", "other-kb", "/parent/other-kb/", 2),
		knowledgeFolderFixture("other-parent", 1, "kb-1", "elsewhere", "other-parent", "/elsewhere/other-parent/", 2),
	} {
		require.NoError(t, db.Create(folder).Error)
	}
	require.NoError(t, db.Delete(deleted).Error)

	names := []string{"target", "deleted", "other-tenant", "other-kb", "other-parent"}
	before := append([]string(nil), names...)
	folders, err := repo.ListByParentAndNames(ctx, 1, "kb-1", parentID, names)
	require.NoError(t, err)
	require.Len(t, folders, 1)
	assert.Equal(t, target.ID, folders[0].ID)
	assert.Equal(t, before, names)
}

func TestKnowledgeFolderRepositoryListByParentAndNamesDeduplicatesAndBindsNames(t *testing.T) {
	repo, mock := newKnowledgeFolderNamesSQLMock(t)
	parentID := knowledgeFolderTestID("parent")
	injectionLikeName := "x') OR 1=1 --"
	names := []string{"safe", injectionLikeName, "safe"}
	before := append([]string(nil), names...)
	uniqueNames := []string{"safe", injectionLikeName}

	mock.ExpectQuery(knowledgeFolderNamesQueryPattern(len(uniqueNames))).
		WithArgs(knowledgeFolderNamesQueryArgs(7, "kb-1", parentID, uniqueNames)...).
		WillReturnRows(knowledgeFolderNamesRows().AddRow(
			knowledgeFolderTestID("safe"),
			uint64(7),
			"kb-1",
			parentID,
			"safe",
			knowledgeFolderTestPath("/parent/safe/"),
			2,
			0,
			nil,
		))

	folders, err := repo.ListByParentAndNames(
		context.Background(),
		7,
		"kb-1",
		parentID,
		names,
	)
	require.NoError(t, err)
	require.Len(t, folders, 1)
	assert.Equal(t, "safe", folders[0].Name)
	assert.Equal(t, before, names)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderRepositoryListByParentAndNamesChunksAndMerges(t *testing.T) {
	repo, mock := newKnowledgeFolderNamesSQLMock(t)
	parentID := knowledgeFolderTestID("parent")
	names := make([]string, knowledgeFolderNamesBatchSize+1)
	for index := range names {
		names[index] = fmt.Sprintf("name-%03d", index)
	}
	names = append(names, names[0])
	before := append([]string(nil), names...)

	firstBatch := names[:knowledgeFolderNamesBatchSize]
	secondBatch := names[knowledgeFolderNamesBatchSize : knowledgeFolderNamesBatchSize+1]
	mock.ExpectQuery(knowledgeFolderNamesQueryPattern(len(firstBatch))).
		WithArgs(knowledgeFolderNamesQueryArgs(9, "kb-9", parentID, firstBatch)...).
		WillReturnRows(knowledgeFolderNamesRows().AddRow(
			knowledgeFolderTestID("first"),
			uint64(9),
			"kb-9",
			parentID,
			firstBatch[0],
			knowledgeFolderTestPath("/parent/first/"),
			2,
			0,
			nil,
		))
	mock.ExpectQuery(knowledgeFolderNamesQueryPattern(len(secondBatch))).
		WithArgs(knowledgeFolderNamesQueryArgs(9, "kb-9", parentID, secondBatch)...).
		WillReturnRows(knowledgeFolderNamesRows().AddRow(
			knowledgeFolderTestID("last"),
			uint64(9),
			"kb-9",
			parentID,
			secondBatch[0],
			knowledgeFolderTestPath("/parent/last/"),
			2,
			0,
			nil,
		))

	folders, err := repo.ListByParentAndNames(
		context.Background(),
		9,
		"kb-9",
		parentID,
		names,
	)
	require.NoError(t, err)
	require.Len(t, folders, 2)
	assert.Equal(t, []string{firstBatch[0], secondBatch[0]}, []string{
		folders[0].Name,
		folders[1].Name,
	})
	assert.Equal(t, before, names)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderRepositoryListByParentAndNamesPropagatesErrors(t *testing.T) {
	t.Run("database error discards earlier batches", func(t *testing.T) {
		repo, mock := newKnowledgeFolderNamesSQLMock(t)
		parentID := knowledgeFolderTestID("parent")
		names := make([]string, knowledgeFolderNamesBatchSize+1)
		for index := range names {
			names[index] = fmt.Sprintf("name-%03d", index)
		}
		databaseErr := errors.New("database unavailable")

		mock.ExpectQuery(knowledgeFolderNamesQueryPattern(knowledgeFolderNamesBatchSize)).
			WithArgs(knowledgeFolderNamesQueryArgs(
				3,
				"kb-3",
				parentID,
				names[:knowledgeFolderNamesBatchSize],
			)...).
			WillReturnRows(knowledgeFolderNamesRows().AddRow(
				knowledgeFolderTestID("first"),
				uint64(3),
				"kb-3",
				parentID,
				names[0],
				knowledgeFolderTestPath("/parent/first/"),
				2,
				0,
				nil,
			))
		mock.ExpectQuery(knowledgeFolderNamesQueryPattern(1)).
			WithArgs(knowledgeFolderNamesQueryArgs(
				3,
				"kb-3",
				parentID,
				names[knowledgeFolderNamesBatchSize:],
			)...).
			WillReturnError(databaseErr)

		folders, err := repo.ListByParentAndNames(
			context.Background(),
			3,
			"kb-3",
			parentID,
			names,
		)
		require.ErrorIs(t, err, databaseErr)
		assert.Nil(t, folders)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("context cancellation", func(t *testing.T) {
		repo, mock := newKnowledgeFolderNamesSQLMock(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		folders, err := repo.ListByParentAndNames(ctx, 1, "kb-1", "", []string{"name"})
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, folders)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func newKnowledgeFolderNamesSQLMock(
	t *testing.T,
) (*knowledgeFolderReader, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	return newKnowledgeFolderReader(db), mock
}

func newKnowledgeFolderCreateIfAbsentSQLMock(
	t *testing.T,
) (*knowledgeFolderTreeRepository, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(
		postgres.New(postgres.Config{Conn: sqlDB, WithoutReturning: true}),
		&gorm.Config{SkipDefaultTransaction: true},
	)
	require.NoError(t, err)
	return newKnowledgeFolderTreeRepository(db), mock
}

func knowledgeFolderCreateIfAbsentInsertPattern() string {
	return regexp.QuoteMeta(`INSERT INTO "knowledge_folders"`) +
		`.*` +
		regexp.QuoteMeta(`ON CONFLICT DO NOTHING`)
}

func knowledgeFolderCreateIfAbsentFixture(label string, name string) *types.KnowledgeFolder {
	id := knowledgeFolderTestID(label)
	return &types.KnowledgeFolder{
		ID:              id,
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParentID:        types.KnowledgeFolderRootID,
		Name:            name,
		Path:            "/" + id + "/",
		Depth:           1,
	}
}

func knowledgeFolderNamesQueryPattern(nameCount int) string {
	placeholders := ""
	for index := 0; index < nameCount; index++ {
		if index > 0 {
			placeholders += ","
		}
		placeholders += fmt.Sprintf("$%d", index+4)
	}
	return regexp.QuoteMeta(
		`SELECT * FROM "knowledge_folders" WHERE ` +
			`(tenant_id = $1 AND knowledge_base_id = $2 AND parent_id = $3 AND name IN (` +
			placeholders +
			`)) AND "knowledge_folders"."deleted_at" IS NULL ` +
			`ORDER BY name ASC,id ASC`,
	)
}

func knowledgeFolderNamesQueryArgs(
	tenantID uint64,
	kbID string,
	parentID string,
	names []string,
) []driver.Value {
	args := make([]driver.Value, 0, len(names)+3)
	args = append(args, tenantID, kbID, parentID)
	for _, name := range names {
		args = append(args, name)
	}
	return args
}

func knowledgeFolderNamesRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id",
		"tenant_id",
		"knowledge_base_id",
		"parent_id",
		"name",
		"path",
		"depth",
		"sort_order",
		"deleted_at",
	})
}
