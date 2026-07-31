package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var knowledgeDeleteFixtureUpdatedAt = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

func TestKnowledgeDeleteCoordinatorDoesNotCallGenericDeleteMethods(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(
		fileSet,
		filepath.Join(filepath.Dir(testFile), "knowledge_delete.go"),
		nil,
		0,
	)
	require.NoError(t, err)

	targets := map[string]bool{
		"BeginKnowledgeDelete":    false,
		"FinalizeKnowledgeDelete": false,
	}
	forbidden := map[string]struct{}{
		"DeleteKnowledge":     {},
		"DeleteKnowledgeList": {},
	}

	for _, declaration := range file.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if !ok || method.Recv == nil || method.Body == nil {
			continue
		}
		if _, ok := targets[method.Name.Name]; !ok {
			continue
		}
		targets[method.Name.Name] = true

		ast.Inspect(method.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			var calledName string
			switch called := call.Fun.(type) {
			case *ast.Ident:
				calledName = called.Name
			case *ast.SelectorExpr:
				calledName = called.Sel.Name
			}
			if _, ok := forbidden[calledName]; ok {
				t.Errorf("%s must not call generic delete method %s", method.Name.Name, calledName)
			}
			return true
		})
	}

	for method, found := range targets {
		assert.Truef(t, found, "expected method %s in knowledge_delete.go", method)
	}
}

func setupKnowledgeDeleteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return setupKnowledgeFolderMoveTestDB(t)
}

func insertKnowledgeDeleteKnowledgeBase(
	t *testing.T,
	db *gorm.DB,
	tenantID uint64,
	kbID string,
	deleted bool,
) {
	t.Helper()
	var deletedAt interface{}
	if deleted {
		deletedAt = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	}
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id, deleted_at) VALUES (?, ?, ?)`,
		kbID,
		tenantID,
		deletedAt,
	).Error)
}

func insertKnowledgeDeleteKnowledge(
	t *testing.T,
	db *gorm.DB,
	tenantID uint64,
	kbID string,
	knowledgeID string,
	parseStatus string,
	deleted bool,
) {
	t.Helper()
	var deletedAt interface{}
	if deleted {
		deletedAt = time.Date(2026, 7, 25, 12, 30, 0, 0, time.UTC)
	}
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (
			id,
			tenant_id,
			knowledge_base_id,
			folder_id,
			folder_version,
			folder_indexed_version,
			parse_status,
			updated_at,
			deleted_at
		) VALUES (?, ?, ?, '', 1, 0, ?, ?, ?)
	`,
		knowledgeID,
		tenantID,
		kbID,
		parseStatus,
		knowledgeDeleteFixtureUpdatedAt,
		deletedAt,
	).Error)
}

func insertKnowledgeDeletePending(
	t *testing.T,
	db *gorm.DB,
	tenantID uint64,
	kbID string,
	knowledgeID string,
) {
	t.Helper()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_folder_index_pending (
			id,
			tenant_id,
			knowledge_base_id,
			knowledge_id,
			target_folder_id,
			requested_version
		) VALUES (?, ?, ?, ?, '', 1)
	`, uuid.NewString(), tenantID, kbID, knowledgeID).Error)
}

func readKnowledgeDeleteKnowledge(
	t *testing.T,
	db *gorm.DB,
	knowledgeID string,
) *types.Knowledge {
	t.Helper()
	var knowledge types.Knowledge
	require.NoError(t, db.Unscoped().Where("id = ?", knowledgeID).Take(&knowledge).Error)
	return &knowledge
}

func countKnowledgeDeletePending(
	t *testing.T,
	db *gorm.DB,
	tenantID uint64,
	kbID string,
	knowledgeID string,
) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.
		Model(&types.KnowledgeFolderIndexPending{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ?",
			tenantID,
			kbID,
			knowledgeID,
		).
		Count(&count).Error)
	return count
}

func TestLockActiveKnowledgeForDeleteUsesPostgresForUpdate(t *testing.T) {
	tests := []struct {
		name           string
		requiredStatus string
		queryArgs      []driver.Value
	}{
		{
			name:      "begin",
			queryArgs: []driver.Value{uint64(7), "kb-1", "knowledge-1", 1},
		},
		{
			name:           "finalize",
			requiredStatus: types.ParseStatusDeleting,
			queryArgs: []driver.Value{
				uint64(7),
				"kb-1",
				"knowledge-1",
				types.ParseStatusDeleting,
				1,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			db, err := gorm.Open(
				postgres.New(postgres.Config{Conn: sqlDB}),
				&gorm.Config{},
			)
			require.NoError(t, err)

			mock.ExpectQuery(
				`SELECT .* FROM "knowledges".*"deleted_at" IS NULL.*FOR UPDATE`,
			).
				WithArgs(test.queryArgs...).
				WillReturnRows(sqlmock.NewRows([]string{
					"id",
					"tenant_id",
					"knowledge_base_id",
					"parse_status",
					"deleted_at",
				}).AddRow(
					"knowledge-1",
					uint64(7),
					"kb-1",
					types.ParseStatusDeleting,
					nil,
				))

			knowledge, err := lockActiveKnowledgeForDelete(
				context.Background(),
				db,
				7,
				"kb-1",
				"knowledge-1",
				test.requiredStatus,
			)

			require.NoError(t, err)
			require.NotNil(t, knowledge)
			assert.Equal(t, "knowledge-1", knowledge.ID)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestBeginKnowledgeDeleteTransitionsActiveStatusesAndReturnsPreTransitionSnapshot(t *testing.T) {
	statuses := []string{
		types.ParseStatusPending,
		types.ParseStatusProcessing,
		types.ParseStatusFinalizing,
		types.ParseStatusCompleted,
		types.ParseStatusFailed,
		types.ParseStatusCancelled,
	}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			db := setupKnowledgeDeleteTestDB(t)
			repo := NewKnowledgeRepository(db)
			insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
			insertKnowledgeDeleteKnowledge(t, db, 7, "kb-1", "knowledge-1", status, false)
			insertKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1")

			snapshot, err := repo.BeginKnowledgeDelete(
				context.Background(),
				7,
				"kb-1",
				"knowledge-1",
			)

			require.NoError(t, err)
			require.NotNil(t, snapshot)
			assert.Equal(t, "knowledge-1", snapshot.ID)
			assert.Equal(t, status, snapshot.ParseStatus)
			assert.True(t, snapshot.UpdatedAt.Equal(knowledgeDeleteFixtureUpdatedAt))
			assert.False(t, snapshot.DeletedAt.Valid)

			stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
			assert.Equal(t, types.ParseStatusDeleting, stored.ParseStatus)
			assert.True(t, stored.UpdatedAt.After(snapshot.UpdatedAt))
			assert.False(t, stored.DeletedAt.Valid, "Begin must not call the generic delete path")
			assert.Zero(t, countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1"))
		})
	}
}

func TestBeginKnowledgeDeleteIsIdempotentForDeletingKnowledge(t *testing.T) {
	db := setupKnowledgeDeleteTestDB(t)
	repo := NewKnowledgeRepository(db)
	insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
	insertKnowledgeDeleteKnowledge(
		t,
		db,
		7,
		"kb-1",
		"knowledge-1",
		types.ParseStatusDeleting,
		false,
	)
	insertKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1")
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_duplicate_deleting_update
		BEFORE UPDATE OF parse_status ON knowledges
		BEGIN
			SELECT RAISE(ABORT, 'duplicate deleting update');
		END
	`).Error)

	snapshot, err := repo.BeginKnowledgeDelete(
		context.Background(),
		7,
		"kb-1",
		"knowledge-1",
	)

	require.NoError(t, err)
	require.NotNil(t, snapshot)
	assert.Equal(t, types.ParseStatusDeleting, snapshot.ParseStatus)
	assert.True(t, snapshot.UpdatedAt.Equal(knowledgeDeleteFixtureUpdatedAt))
	assert.Zero(t, countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1"))
	assert.True(
		t,
		readKnowledgeDeleteKnowledge(t, db, "knowledge-1").UpdatedAt.
			Equal(knowledgeDeleteFixtureUpdatedAt),
	)
}

func TestBeginKnowledgeDeleteAcceptsSoftDeletedKnowledgeBase(t *testing.T) {
	db := setupKnowledgeDeleteTestDB(t)
	repo := NewKnowledgeRepository(db)
	insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", true)
	insertKnowledgeDeleteKnowledge(
		t,
		db,
		7,
		"kb-1",
		"knowledge-1",
		types.ParseStatusCompleted,
		false,
	)

	snapshot, err := repo.BeginKnowledgeDelete(
		context.Background(),
		7,
		"kb-1",
		"knowledge-1",
	)

	require.NoError(t, err)
	require.NotNil(t, snapshot)
	assert.Equal(t, types.ParseStatusCompleted, snapshot.ParseStatus)
	assert.Equal(
		t,
		types.ParseStatusDeleting,
		readKnowledgeDeleteKnowledge(t, db, "knowledge-1").ParseStatus,
	)
}

func TestKnowledgeDeleteCoordinatorMapsMissingOrWrongTenantKnowledgeBase(t *testing.T) {
	tests := []struct {
		name       string
		seedKB     bool
		kbTenantID uint64
	}{
		{name: "physically missing", seedKB: false},
		{name: "wrong tenant", seedKB: true, kbTenantID: 8},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, operation := range []string{"begin", "finalize"} {
				t.Run(operation, func(t *testing.T) {
					db := setupKnowledgeDeleteTestDB(t)
					repo := NewKnowledgeRepository(db)
					if test.seedKB {
						insertKnowledgeDeleteKnowledgeBase(
							t,
							db,
							test.kbTenantID,
							"kb-1",
							false,
						)
					}
					insertKnowledgeDeleteKnowledge(
						t,
						db,
						7,
						"kb-1",
						"knowledge-1",
						types.ParseStatusDeleting,
						false,
					)

					if operation == "begin" {
						snapshot, err := repo.BeginKnowledgeDelete(
							context.Background(),
							7,
							"kb-1",
							"knowledge-1",
						)
						require.ErrorIs(t, err, ErrKnowledgeBaseNotFound)
						assert.Nil(t, snapshot)
					} else {
						err := repo.FinalizeKnowledgeDelete(
							context.Background(),
							7,
							"kb-1",
							"knowledge-1",
						)
						require.ErrorIs(t, err, ErrKnowledgeBaseNotFound)
					}

					stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
					assert.False(t, stored.DeletedAt.Valid)
				})
			}
		})
	}
}

func TestBeginKnowledgeDeleteRejectsMissingOrOutOfScopeKnowledge(t *testing.T) {
	tests := []struct {
		name      string
		seed      bool
		tenantID  uint64
		kbID      string
		deleted   bool
		requestID string
	}{
		{
			name:      "missing",
			requestID: "knowledge-missing",
		},
		{
			name:      "wrong tenant",
			seed:      true,
			tenantID:  8,
			kbID:      "kb-1",
			requestID: "knowledge-1",
		},
		{
			name:      "wrong knowledge base",
			seed:      true,
			tenantID:  7,
			kbID:      "kb-2",
			requestID: "knowledge-1",
		},
		{
			name:      "soft deleted",
			seed:      true,
			tenantID:  7,
			kbID:      "kb-1",
			deleted:   true,
			requestID: "knowledge-1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeDeleteTestDB(t)
			repo := NewKnowledgeRepository(db)
			insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
			if test.seed {
				insertKnowledgeDeleteKnowledge(
					t,
					db,
					test.tenantID,
					test.kbID,
					test.requestID,
					types.ParseStatusCompleted,
					test.deleted,
				)
			}
			insertKnowledgeDeletePending(t, db, 7, "kb-1", test.requestID)

			snapshot, err := repo.BeginKnowledgeDelete(
				context.Background(),
				7,
				"kb-1",
				test.requestID,
			)

			require.ErrorIs(t, err, ErrKnowledgeNotFound)
			assert.Nil(t, snapshot)
			assert.Equal(
				t,
				int64(1),
				countKnowledgeDeletePending(t, db, 7, "kb-1", test.requestID),
			)
		})
	}
}

func TestKnowledgeDeleteCoordinatorScopesPendingHardDelete(t *testing.T) {
	tests := []struct {
		name        string
		parseStatus string
	}{
		{name: "begin", parseStatus: types.ParseStatusCompleted},
		{name: "finalize", parseStatus: types.ParseStatusDeleting},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeDeleteTestDB(t)
			repo := NewKnowledgeRepository(db)
			insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
			insertKnowledgeDeleteKnowledge(
				t,
				db,
				7,
				"kb-1",
				"knowledge-1",
				test.parseStatus,
				false,
			)

			pendingScopes := []struct {
				tenantID    uint64
				kbID        string
				knowledgeID string
			}{
				{tenantID: 7, kbID: "kb-1", knowledgeID: "knowledge-1"},
				{tenantID: 7, kbID: "kb-1", knowledgeID: "knowledge-2"},
				{tenantID: 7, kbID: "kb-2", knowledgeID: "knowledge-1"},
				{tenantID: 8, kbID: "kb-1", knowledgeID: "knowledge-1"},
			}
			for _, scope := range pendingScopes {
				insertKnowledgeDeletePending(
					t,
					db,
					scope.tenantID,
					scope.kbID,
					scope.knowledgeID,
				)
			}

			if test.name == "begin" {
				_, err := repo.BeginKnowledgeDelete(
					context.Background(),
					7,
					"kb-1",
					"knowledge-1",
				)
				require.NoError(t, err)
			} else {
				require.NoError(t, repo.FinalizeKnowledgeDelete(
					context.Background(),
					7,
					"kb-1",
					"knowledge-1",
				))
			}

			assert.Zero(
				t,
				countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1"),
			)
			for _, scope := range pendingScopes[1:] {
				assert.Equal(
					t,
					int64(1),
					countKnowledgeDeletePending(
						t,
						db,
						scope.tenantID,
						scope.kbID,
						scope.knowledgeID,
					),
				)
			}
		})
	}
}

func TestBeginKnowledgeDeleteRollsBackStatusAndPendingOnFailure(t *testing.T) {
	tests := []struct {
		name        string
		triggerName string
		triggerSQL  string
		expectedErr error
	}{
		{
			name:        "status update error",
			triggerName: "reject_begin_status_update",
			triggerSQL: `
				CREATE TRIGGER reject_begin_status_update
				BEFORE UPDATE OF parse_status ON knowledges
				WHEN NEW.parse_status = 'deleting'
				BEGIN
					SELECT RAISE(ABORT, 'forced status update failure');
				END
			`,
		},
		{
			name:        "status compare and swap affects zero rows",
			triggerName: "ignore_begin_status_update",
			triggerSQL: `
				CREATE TRIGGER ignore_begin_status_update
				BEFORE UPDATE OF parse_status ON knowledges
				WHEN NEW.parse_status = 'deleting'
				BEGIN
					SELECT RAISE(IGNORE);
				END
			`,
			expectedErr: ErrKnowledgeNotFound,
		},
		{
			name:        "pending delete error",
			triggerName: "reject_begin_pending_delete",
			triggerSQL: `
				CREATE TRIGGER reject_begin_pending_delete
				BEFORE DELETE ON knowledge_folder_index_pending
				BEGIN
					SELECT RAISE(ABORT, 'forced pending delete failure');
				END
			`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeDeleteTestDB(t)
			repo := NewKnowledgeRepository(db)
			insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
			insertKnowledgeDeleteKnowledge(
				t,
				db,
				7,
				"kb-1",
				"knowledge-1",
				types.ParseStatusCompleted,
				false,
			)
			insertKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1")
			require.NotEmpty(t, test.triggerName)
			require.NoError(t, db.Exec(test.triggerSQL).Error)

			snapshot, err := repo.BeginKnowledgeDelete(
				context.Background(),
				7,
				"kb-1",
				"knowledge-1",
			)

			require.Error(t, err)
			if test.expectedErr != nil {
				require.ErrorIs(t, err, test.expectedErr)
			}
			assert.Nil(t, snapshot)
			stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
			assert.Equal(t, types.ParseStatusCompleted, stored.ParseStatus)
			assert.True(t, stored.UpdatedAt.Equal(knowledgeDeleteFixtureUpdatedAt))
			assert.False(t, stored.DeletedAt.Valid)
			assert.Equal(
				t,
				int64(1),
				countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1"),
			)
		})
	}
}

func TestBeginKnowledgeDeleteReusesUpdatedAtAcrossSQLiteRetry(t *testing.T) {
	db := setupKnowledgeDeleteTestDB(t)
	repo := NewKnowledgeRepository(db)
	insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
	insertKnowledgeDeleteKnowledge(
		t,
		db,
		7,
		"kb-1",
		"knowledge-1",
		types.ParseStatusCompleted,
		false,
	)

	var attemptUpdatedAt []time.Time
	require.NoError(t, db.Callback().Update().
		Before("gorm:update").
		Register("f23a:retry_fixed_updated_at", func(tx *gorm.DB) {
			if tx.Statement.Table != "knowledges" {
				return
			}
			updates, ok := tx.Statement.Dest.(map[string]interface{})
			if !ok {
				return
			}
			updatedAt, ok := updates["updated_at"].(time.Time)
			if !ok {
				return
			}
			attemptUpdatedAt = append(attemptUpdatedAt, updatedAt)
			if len(attemptUpdatedAt) == 1 {
				_ = tx.AddError(sqlite3.Error{Code: sqlite3.ErrBusy})
			}
		}))

	snapshot, err := repo.BeginKnowledgeDelete(
		context.Background(),
		7,
		"kb-1",
		"knowledge-1",
	)

	require.NoError(t, err)
	require.NotNil(t, snapshot)
	assert.Equal(t, types.ParseStatusCompleted, snapshot.ParseStatus)
	require.Len(t, attemptUpdatedAt, 2)
	assert.True(t, attemptUpdatedAt[0].Equal(attemptUpdatedAt[1]))
	stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
	assert.Equal(t, types.ParseStatusDeleting, stored.ParseStatus)
	assert.True(t, stored.UpdatedAt.Equal(attemptUpdatedAt[0]))
}

func TestBeginKnowledgeDeleteAttemptClearsSnapshotAcrossSQLiteRetry(t *testing.T) {
	db := setupKnowledgeDeleteTestDB(t)
	insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
	insertKnowledgeDeleteKnowledge(
		t,
		db,
		7,
		"kb-1",
		"knowledge-1",
		types.ParseStatusCompleted,
		false,
	)

	attempts := 0
	updatedAt := time.Date(2026, 7, 25, 13, 0, 0, 0, time.UTC)
	var snapshot *types.Knowledge
	err := runKnowledgeBaseScopedWriteTransaction(
		context.Background(),
		db,
		7,
		"kb-1",
		knowledgeBaseScopedWriteOptions{
			lockMode: knowledgeBaseLockIncludeSoftDeleted,
			sqliteRetryWait: func(context.Context, int) error {
				return nil
			},
		},
		func(tx *gorm.DB) error {
			attempts++
			knowledgeID := "knowledge-1"
			if attempts == 2 {
				knowledgeID = "knowledge-missing"
			}
			if err := beginKnowledgeDeleteAttempt(
				context.Background(),
				tx,
				7,
				"kb-1",
				knowledgeID,
				updatedAt,
				&snapshot,
			); err != nil {
				return err
			}
			if attempts == 1 {
				return sqlite3.Error{Code: sqlite3.ErrBusy}
			}
			return nil
		},
	)

	require.ErrorIs(t, err, ErrKnowledgeNotFound)
	assert.Equal(t, 2, attempts)
	assert.Nil(t, snapshot)
	stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
	assert.Equal(t, types.ParseStatusCompleted, stored.ParseStatus)
}

func TestFinalizeKnowledgeDeleteSoftDeletesDeletingKnowledgeAndPending(t *testing.T) {
	tests := []struct {
		name        string
		kbDeleted   bool
		seedPending bool
	}{
		{name: "active knowledge base", seedPending: true},
		{name: "soft deleted knowledge base", kbDeleted: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeDeleteTestDB(t)
			repo := NewKnowledgeRepository(db)
			insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", test.kbDeleted)
			insertKnowledgeDeleteKnowledge(
				t,
				db,
				7,
				"kb-1",
				"knowledge-1",
				types.ParseStatusDeleting,
				false,
			)
			if test.seedPending {
				insertKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1")
			}

			err := repo.FinalizeKnowledgeDelete(
				context.Background(),
				7,
				"kb-1",
				"knowledge-1",
			)

			require.NoError(t, err)
			stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
			assert.Equal(t, types.ParseStatusDeleting, stored.ParseStatus)
			assert.True(t, stored.DeletedAt.Valid)
			assert.Zero(t, countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1"))
		})
	}
}

func TestFinalizeKnowledgeDeleteRejectsMissingOrOutOfScopeKnowledge(t *testing.T) {
	tests := []struct {
		name      string
		seed      bool
		tenantID  uint64
		kbID      string
		deleted   bool
		requestID string
	}{
		{
			name:      "missing",
			requestID: "knowledge-missing",
		},
		{
			name:      "wrong tenant",
			seed:      true,
			tenantID:  8,
			kbID:      "kb-1",
			requestID: "knowledge-1",
		},
		{
			name:      "wrong knowledge base",
			seed:      true,
			tenantID:  7,
			kbID:      "kb-2",
			requestID: "knowledge-1",
		},
		{
			name:      "soft deleted",
			seed:      true,
			tenantID:  7,
			kbID:      "kb-1",
			deleted:   true,
			requestID: "knowledge-1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeDeleteTestDB(t)
			repo := NewKnowledgeRepository(db)
			insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
			if test.seed {
				insertKnowledgeDeleteKnowledge(
					t,
					db,
					test.tenantID,
					test.kbID,
					test.requestID,
					types.ParseStatusDeleting,
					test.deleted,
				)
			}
			insertKnowledgeDeletePending(t, db, 7, "kb-1", test.requestID)

			err := repo.FinalizeKnowledgeDelete(
				context.Background(),
				7,
				"kb-1",
				test.requestID,
			)

			require.ErrorIs(t, err, ErrKnowledgeNotFound)
			assert.Equal(
				t,
				int64(1),
				countKnowledgeDeletePending(t, db, 7, "kb-1", test.requestID),
			)
			if test.seed {
				stored := readKnowledgeDeleteKnowledge(t, db, test.requestID)
				assert.Equal(t, types.ParseStatusDeleting, stored.ParseStatus)
				assert.Equal(t, test.deleted, stored.DeletedAt.Valid)
			}
		})
	}
}

func TestFinalizeKnowledgeDeleteRejectsWrongStatus(t *testing.T) {
	statuses := []string{
		types.ParseStatusPending,
		types.ParseStatusProcessing,
		types.ParseStatusFinalizing,
		types.ParseStatusCompleted,
		types.ParseStatusFailed,
		types.ParseStatusCancelled,
	}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			db := setupKnowledgeDeleteTestDB(t)
			repo := NewKnowledgeRepository(db)
			insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
			insertKnowledgeDeleteKnowledge(t, db, 7, "kb-1", "knowledge-1", status, false)
			insertKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1")

			err := repo.FinalizeKnowledgeDelete(
				context.Background(),
				7,
				"kb-1",
				"knowledge-1",
			)

			require.ErrorIs(t, err, ErrKnowledgeNotFound)
			stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
			assert.Equal(t, status, stored.ParseStatus)
			assert.False(t, stored.DeletedAt.Valid)
			assert.Equal(
				t,
				int64(1),
				countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1"),
			)
		})
	}
}

func TestFinalizeKnowledgeDeleteRollsBackWhenPendingDeleteFails(t *testing.T) {
	db := setupKnowledgeDeleteTestDB(t)
	repo := NewKnowledgeRepository(db)
	insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
	insertKnowledgeDeleteKnowledge(
		t,
		db,
		7,
		"kb-1",
		"knowledge-1",
		types.ParseStatusDeleting,
		false,
	)
	insertKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1")
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_finalize_pending_delete
		BEFORE DELETE ON knowledge_folder_index_pending
		BEGIN
			SELECT RAISE(ABORT, 'forced pending delete failure');
		END
	`).Error)

	err := repo.FinalizeKnowledgeDelete(
		context.Background(),
		7,
		"kb-1",
		"knowledge-1",
	)

	require.Error(t, err)
	stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
	assert.Equal(t, types.ParseStatusDeleting, stored.ParseStatus)
	assert.False(t, stored.DeletedAt.Valid)
	assert.Equal(
		t,
		int64(1),
		countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1"),
	)
}

func TestFinalizeKnowledgeDeleteRequiresExactlyOneSoftDeletedRow(t *testing.T) {
	db := setupKnowledgeDeleteTestDB(t)
	repo := NewKnowledgeRepository(db)
	insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
	insertKnowledgeDeleteKnowledge(
		t,
		db,
		7,
		"kb-1",
		"knowledge-1",
		types.ParseStatusDeleting,
		false,
	)
	insertKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1")
	require.NoError(t, db.Exec(`
		CREATE TRIGGER ignore_finalize_soft_delete
		BEFORE UPDATE OF deleted_at ON knowledges
		WHEN NEW.deleted_at IS NOT NULL
		BEGIN
			SELECT RAISE(IGNORE);
		END
	`).Error)

	err := repo.FinalizeKnowledgeDelete(
		context.Background(),
		7,
		"kb-1",
		"knowledge-1",
	)

	require.ErrorIs(t, err, ErrKnowledgeNotFound)
	stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
	assert.False(t, stored.DeletedAt.Valid)
	assert.Equal(
		t,
		int64(1),
		countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1"),
	)
}

func TestKnowledgeDeleteWrappersDoNotUseLegacyDeletePaths(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	file, err := parser.ParseFile(
		token.NewFileSet(),
		filepath.Join(filepath.Dir(testFile), "knowledge.go"),
		nil,
		0,
	)
	require.NoError(t, err)

	targets := map[string]bool{
		"DeleteKnowledge":     false,
		"DeleteKnowledgeList": false,
	}
	forbidden := map[string]struct{}{
		"Delete":              {},
		"DeleteKnowledge":     {},
		"DeleteKnowledgeList": {},
	}

	for _, declaration := range file.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if !ok || method.Recv == nil || method.Body == nil {
			continue
		}
		if _, ok := targets[method.Name.Name]; !ok {
			continue
		}
		targets[method.Name.Name] = true

		ast.Inspect(method.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			var calledName string
			switch called := call.Fun.(type) {
			case *ast.Ident:
				calledName = called.Name
			case *ast.SelectorExpr:
				calledName = called.Sel.Name
			}
			if _, ok := forbidden[calledName]; ok {
				t.Errorf("%s must not call legacy delete path %s", method.Name.Name, calledName)
			}
			return true
		})
	}

	for method, found := range targets {
		assert.Truef(t, found, "expected method %s in knowledge.go", method)
	}
}

func TestDeleteKnowledgeCoordinatesBeginAndFinalize(t *testing.T) {
	tests := []struct {
		name      string
		kbDeleted bool
	}{
		{name: "active knowledge base"},
		{name: "soft deleted knowledge base", kbDeleted: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeDeleteTestDB(t)
			repo := NewKnowledgeRepository(db)
			insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", test.kbDeleted)
			insertKnowledgeDeleteKnowledge(
				t,
				db,
				7,
				"kb-1",
				"knowledge-1",
				types.ParseStatusCompleted,
				false,
			)
			insertKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1")

			err := repo.DeleteKnowledge(context.Background(), 7, "knowledge-1")

			require.NoError(t, err)
			stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
			assert.Equal(t, types.ParseStatusDeleting, stored.ParseStatus)
			assert.True(t, stored.DeletedAt.Valid)
			assert.Zero(t, countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-1"))
		})
	}
}

func TestDeleteKnowledgeRejectsMissingWrongTenantOrSoftDeletedKnowledge(t *testing.T) {
	tests := []struct {
		name         string
		seed         bool
		seedTenantID uint64
		softDeleted  bool
	}{
		{name: "missing"},
		{name: "wrong tenant", seed: true, seedTenantID: 8},
		{name: "soft deleted", seed: true, seedTenantID: 7, softDeleted: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupKnowledgeDeleteTestDB(t)
			repo := NewKnowledgeRepository(db)
			insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
			if test.seed {
				insertKnowledgeDeleteKnowledge(
					t,
					db,
					test.seedTenantID,
					"kb-1",
					"knowledge-1",
					types.ParseStatusDeleting,
					test.softDeleted,
				)
			}

			err := repo.DeleteKnowledge(context.Background(), 7, "knowledge-1")

			require.ErrorIs(t, err, ErrKnowledgeNotFound)
			if test.seed && !test.softDeleted {
				stored := readKnowledgeDeleteKnowledge(t, db, "knowledge-1")
				assert.False(t, stored.DeletedAt.Valid)
			}
		})
	}
}

func TestDeleteKnowledgeListUsesStableActiveSubsetAcrossKnowledgeBases(t *testing.T) {
	db := setupKnowledgeDeleteTestDB(t)
	repo := NewKnowledgeRepository(db)
	insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-a", false)
	insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-b", true)

	fixtures := []struct {
		id      string
		kbID    string
		deleted bool
	}{
		{id: "knowledge-c", kbID: "kb-a"},
		{id: "knowledge-a", kbID: "kb-a"},
		{id: "knowledge-b", kbID: "kb-b"},
		{id: "knowledge-soft-deleted", kbID: "kb-a", deleted: true},
	}
	for _, fixture := range fixtures {
		insertKnowledgeDeleteKnowledge(
			t,
			db,
			7,
			fixture.kbID,
			fixture.id,
			types.ParseStatusCompleted,
			fixture.deleted,
		)
		insertKnowledgeDeletePending(t, db, 7, fixture.kbID, fixture.id)
	}

	var beginOrder []string
	captureBeginOrder := true
	require.NoError(t, db.Callback().Query().
		After("gorm:query").
		Register("f23b:capture_delete_list_begin_order", func(tx *gorm.DB) {
			if !captureBeginOrder {
				return
			}
			knowledge, ok := tx.Statement.Dest.(*types.Knowledge)
			if !ok || knowledge.ID == "" ||
				knowledge.ParseStatus != types.ParseStatusCompleted {
				return
			}
			beginOrder = append(beginOrder, knowledge.ID)
		}))

	err := repo.DeleteKnowledgeList(
		context.Background(),
		7,
		[]string{
			"knowledge-b",
			"knowledge-c",
			"knowledge-soft-deleted",
			"knowledge-missing",
			"knowledge-a",
		},
	)
	captureBeginOrder = false

	require.NoError(t, err)
	assert.Equal(t, []string{"knowledge-a", "knowledge-c", "knowledge-b"}, beginOrder)
	for _, id := range []string{"knowledge-a", "knowledge-c", "knowledge-b"} {
		stored := readKnowledgeDeleteKnowledge(t, db, id)
		assert.True(t, stored.DeletedAt.Valid)
		assert.Equal(t, types.ParseStatusDeleting, stored.ParseStatus)
		assert.Zero(t, countKnowledgeDeletePending(t, db, 7, stored.KnowledgeBaseID, id))
	}
	softDeleted := readKnowledgeDeleteKnowledge(t, db, "knowledge-soft-deleted")
	assert.True(t, softDeleted.DeletedAt.Valid)
	assert.Equal(
		t,
		int64(1),
		countKnowledgeDeletePending(t, db, 7, "kb-a", "knowledge-soft-deleted"),
	)
}

func TestDeleteKnowledgeListReturnsNilForEmptyActiveSubset(t *testing.T) {
	db := setupKnowledgeDeleteTestDB(t)
	repo := NewKnowledgeRepository(db)
	insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
	insertKnowledgeDeleteKnowledge(
		t,
		db,
		7,
		"kb-1",
		"knowledge-soft-deleted",
		types.ParseStatusDeleting,
		true,
	)
	insertKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-soft-deleted")

	require.NoError(t, repo.DeleteKnowledgeList(context.Background(), 7, nil))
	require.NoError(t, repo.DeleteKnowledgeList(
		context.Background(),
		7,
		[]string{"knowledge-missing", "knowledge-soft-deleted"},
	))
	assert.Equal(
		t,
		int64(1),
		countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-soft-deleted"),
	)
}

func TestDeleteKnowledgeListJoinsOrderedBeginAndFinalizeErrorsAndContinues(t *testing.T) {
	db := setupKnowledgeDeleteTestDB(t)
	repo := NewKnowledgeRepository(db)
	insertKnowledgeDeleteKnowledgeBase(t, db, 7, "kb-1", false)
	for _, id := range []string{"knowledge-c", "knowledge-b", "knowledge-a"} {
		insertKnowledgeDeleteKnowledge(
			t,
			db,
			7,
			"kb-1",
			id,
			types.ParseStatusCompleted,
			false,
		)
		insertKnowledgeDeletePending(t, db, 7, "kb-1", id)
	}
	require.NoError(t, db.Exec(`
		CREATE TRIGGER ignore_begin_knowledge_a
		BEFORE UPDATE OF parse_status ON knowledges
		WHEN OLD.id = 'knowledge-a' AND NEW.parse_status = 'deleting'
		BEGIN
			SELECT RAISE(IGNORE);
		END
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_finalize_knowledge_b
		BEFORE UPDATE OF deleted_at ON knowledges
		WHEN OLD.id = 'knowledge-b' AND NEW.deleted_at IS NOT NULL
		BEGIN
			SELECT RAISE(ABORT, 'finalize-knowledge-b');
		END
	`).Error)

	err := repo.DeleteKnowledgeList(
		context.Background(),
		7,
		[]string{"knowledge-c", "knowledge-a", "knowledge-b"},
	)

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrKnowledgeNotFound))
	require.Contains(t, err.Error(), ErrKnowledgeNotFound.Error())
	require.Contains(t, err.Error(), "finalize-knowledge-b")
	assert.Less(
		t,
		strings.Index(err.Error(), ErrKnowledgeNotFound.Error()),
		strings.Index(err.Error(), "finalize-knowledge-b"),
	)

	knowledgeA := readKnowledgeDeleteKnowledge(t, db, "knowledge-a")
	assert.Equal(t, types.ParseStatusCompleted, knowledgeA.ParseStatus)
	assert.False(t, knowledgeA.DeletedAt.Valid)
	assert.Equal(t, int64(1), countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-a"))

	knowledgeB := readKnowledgeDeleteKnowledge(t, db, "knowledge-b")
	assert.Equal(t, types.ParseStatusDeleting, knowledgeB.ParseStatus)
	assert.False(t, knowledgeB.DeletedAt.Valid)
	assert.Zero(t, countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-b"))

	knowledgeC := readKnowledgeDeleteKnowledge(t, db, "knowledge-c")
	assert.Equal(t, types.ParseStatusDeleting, knowledgeC.ParseStatus)
	assert.True(t, knowledgeC.DeletedAt.Valid)
	assert.Zero(t, countKnowledgeDeletePending(t, db, 7, "kb-1", "knowledge-c"))
}
