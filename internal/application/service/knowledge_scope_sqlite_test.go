package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openKnowledgeScopeSQLiteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:knowledge_scope_%s?mode=memory&cache=shared&_busy_timeout=5000",
		uuid.NewString(),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeFolder{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func openKnowledgeScopeSQLiteFileTestDB(
	t *testing.T,
) (*gorm.DB, *gorm.DB) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "knowledge_scope.db")
	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_busy_timeout=5000",
		filepath.ToSlash(databasePath),
	)
	readDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	writeDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, writeDB.AutoMigrate(&types.KnowledgeFolder{}))
	readSQLDB, err := readDB.DB()
	require.NoError(t, err)
	writeSQLDB, err := writeDB.DB()
	require.NoError(t, err)
	readSQLDB.SetMaxOpenConns(2)
	writeSQLDB.SetMaxOpenConns(2)
	t.Cleanup(func() {
		require.NoError(t, readSQLDB.Close())
		require.NoError(t, writeSQLDB.Close())
	})
	return readDB, writeDB
}

func newKnowledgeScopeSQLiteResolver(
	t *testing.T,
	db *gorm.DB,
	limits KnowledgeScopeLimits,
) interfaces.KnowledgeScopeResolver {
	t.Helper()
	return newKnowledgeScopeTestResolver(
		t,
		repository.NewKnowledgeFolderScopeRepository(db),
		limits,
	)
}

func createKnowledgeScopeSQLiteFolders(
	t *testing.T,
	db *gorm.DB,
	folders ...*types.KnowledgeFolder,
) {
	t.Helper()
	for _, folder := range folders {
		require.NoError(t, db.Create(folder).Error)
	}
}

func TestKnowledgeScopeResolverSQLiteDirectRecursiveMultipleRootsStable(
	t *testing.T,
) {
	db := openKnowledgeScopeSQLiteTestDB(t)
	tree := knowledgeScopeTestTree()
	createKnowledgeScopeSQLiteFolders(
		t,
		db,
		tree[knowledgeScopeTestFolderA],
		tree[knowledgeScopeTestFolderB],
		tree[knowledgeScopeTestFolderC],
		tree[knowledgeScopeTestFolderD],
	)
	resolver := newKnowledgeScopeSQLiteResolver(t, db, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 10000,
	})

	t.Run("direct", func(t *testing.T) {
		folderScopes := []types.FolderScopeRequest{{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderB},
			IncludeDescendants: knowledgeScopeTestBool(false),
		}}
		scope, err := resolver.Resolve(
			context.Background(),
			knowledgeScopeTestInput(&folderScopes),
		)
		require.NoError(t, err)
		assert.Equal(
			t,
			[]string{knowledgeScopeTestFolderB},
			knowledgeScopeTestFilter(t, scope).FolderIDs(),
		)
	})

	t.Run("recursive", func(t *testing.T) {
		folderScopes := []types.FolderScopeRequest{{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{knowledgeScopeTestFolderA},
		}}
		scope, err := resolver.Resolve(
			context.Background(),
			knowledgeScopeTestInput(&folderScopes),
		)
		require.NoError(t, err)
		assert.Equal(
			t,
			[]string{
				knowledgeScopeTestFolderA,
				knowledgeScopeTestFolderB,
				knowledgeScopeTestFolderC,
			},
			knowledgeScopeTestFilter(t, scope).FolderIDs(),
		)
	})

	t.Run("multiple roots and stable output", func(t *testing.T) {
		folderScopes := []types.FolderScopeRequest{{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs: []string{
				knowledgeScopeTestFolderD,
				knowledgeScopeTestFolderB,
			},
		}}
		first, err := resolver.Resolve(
			context.Background(),
			knowledgeScopeTestInput(&folderScopes),
		)
		require.NoError(t, err)
		second, err := resolver.Resolve(
			context.Background(),
			knowledgeScopeTestInput(&folderScopes),
		)
		require.NoError(t, err)
		expected := []string{
			knowledgeScopeTestFolderB,
			knowledgeScopeTestFolderC,
			knowledgeScopeTestFolderD,
		}
		assert.Equal(t, expected, knowledgeScopeTestFilter(t, first).FolderIDs())
		assert.Equal(t, expected, knowledgeScopeTestFilter(t, second).FolderIDs())
	})
}

func TestKnowledgeScopeResolverSQLiteRejectsDeletedAndWrongScope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.KnowledgeFolder)
	}{
		{
			name: "deleted",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.DeletedAt = gorm.DeletedAt{
					Time:  time.Now(),
					Valid: true,
				}
			},
		},
		{
			name: "wrong tenant",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.TenantID++
			},
		},
		{
			name: "wrong knowledge base",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.KnowledgeBaseID = knowledgeScopeTestOtherKB
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openKnowledgeScopeSQLiteTestDB(t)
			folder := knowledgeScopeTestFolder(
				knowledgeScopeTestFolderA,
				types.KnowledgeFolderRootID,
				"/"+knowledgeScopeTestFolderA+"/",
				1,
			)
			test.mutate(folder)
			createKnowledgeScopeSQLiteFolders(t, db, folder)
			resolver := newKnowledgeScopeSQLiteResolver(
				t,
				db,
				KnowledgeScopeLimits{
					MaxSelectors:         10,
					MaxResolvedFolderIDs: 10000,
				},
			)
			folderScopes := []types.FolderScopeRequest{{
				KnowledgeBaseID:    knowledgeScopeTestKB,
				FolderIDs:          []string{knowledgeScopeTestFolderA},
				IncludeDescendants: knowledgeScopeTestBool(false),
			}}

			scope, err := resolver.Resolve(
				context.Background(),
				knowledgeScopeTestInput(&folderScopes),
			)

			assert.Nil(t, scope)
			require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
		})
	}
}

func TestKnowledgeScopeResolverSQLiteRejectsDirtyStructure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.KnowledgeFolder)
	}{
		{
			name: "path",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.Path = knowledgeScopeTestFolderA
			},
		},
		{
			name: "parent",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.ParentID = knowledgeScopeTestFolderD
			},
		},
		{
			name: "depth",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.Depth = 2
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openKnowledgeScopeSQLiteTestDB(t)
			folder := knowledgeScopeTestFolder(
				knowledgeScopeTestFolderA,
				types.KnowledgeFolderRootID,
				"/"+knowledgeScopeTestFolderA+"/",
				1,
			)
			test.mutate(folder)
			createKnowledgeScopeSQLiteFolders(t, db, folder)
			resolver := newKnowledgeScopeSQLiteResolver(
				t,
				db,
				KnowledgeScopeLimits{
					MaxSelectors:         10,
					MaxResolvedFolderIDs: 10000,
				},
			)
			folderScopes := []types.FolderScopeRequest{{
				KnowledgeBaseID:    knowledgeScopeTestKB,
				FolderIDs:          []string{knowledgeScopeTestFolderA},
				IncludeDescendants: knowledgeScopeTestBool(false),
			}}

			scope, err := resolver.Resolve(
				context.Background(),
				knowledgeScopeTestInput(&folderScopes),
			)

			assert.Nil(t, scope)
			require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		})
	}
}

func TestKnowledgeScopeResolverSQLiteRejectsDeletedBridge(t *testing.T) {
	db := openKnowledgeScopeSQLiteTestDB(t)
	tree := knowledgeScopeTestTree()
	tree[knowledgeScopeTestFolderB].DeletedAt = gorm.DeletedAt{
		Time:  time.Now(),
		Valid: true,
	}
	createKnowledgeScopeSQLiteFolders(
		t,
		db,
		tree[knowledgeScopeTestFolderA],
		tree[knowledgeScopeTestFolderB],
		tree[knowledgeScopeTestFolderC],
	)
	resolver := newKnowledgeScopeSQLiteResolver(t, db, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 10000,
	})
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeScopeResolverSQLiteLimitPlusOne(t *testing.T) {
	db := openKnowledgeScopeSQLiteTestDB(t)
	tree := knowledgeScopeTestTree()
	createKnowledgeScopeSQLiteFolders(
		t,
		db,
		tree[knowledgeScopeTestFolderA],
		tree[knowledgeScopeTestFolderB],
		tree[knowledgeScopeTestFolderC],
	)
	resolver := newKnowledgeScopeSQLiteResolver(t, db, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 2,
	})
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
}

func TestKnowledgeScopeResolverSQLiteNoFolderUsesZeroSQL(t *testing.T) {
	db := openKnowledgeScopeSQLiteTestDB(t)
	var queryCount atomic.Int64
	require.NoError(
		t,
		db.Callback().Query().Before("gorm:query").
			Register("knowledge_scope_query_count", func(*gorm.DB) {
				queryCount.Add(1)
			}),
	)
	require.NoError(
		t,
		db.Callback().Raw().Before("gorm:raw").
			Register("knowledge_scope_raw_count", func(*gorm.DB) {
				queryCount.Add(1)
			}),
	)
	resolver := newKnowledgeScopeSQLiteResolver(t, db, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 10000,
	})

	scope, err := resolver.Resolve(context.Background(), types.KnowledgeScopeResolveInput{
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTarget(),
		},
	})

	require.NoError(t, err)
	assert.False(t, knowledgeScopeTestFilter(t, scope).Enabled())
	assert.Zero(t, queryCount.Load())
}

func TestKnowledgeScopeResolverSQLiteContextCancellation(t *testing.T) {
	db := openKnowledgeScopeSQLiteTestDB(t)
	resolver := newKnowledgeScopeSQLiteResolver(t, db, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 10000,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(ctx, knowledgeScopeTestInput(&folderScopes))

	assert.Nil(t, scope)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestKnowledgeScopeResolverSQLiteConcurrentMoveUsesOneSnapshot(
	t *testing.T,
) {
	readDB, writeDB := openKnowledgeScopeSQLiteFileTestDB(t)
	tree := knowledgeScopeTestTree()
	createKnowledgeScopeSQLiteFolders(
		t,
		writeDB,
		tree[knowledgeScopeTestFolderA],
		tree[knowledgeScopeTestFolderB],
		tree[knowledgeScopeTestFolderC],
	)

	selectedRead := make(chan struct{})
	moveComplete := make(chan struct{})
	var blockOnce sync.Once
	require.NoError(
		t,
		readDB.Callback().Query().After("gorm:query").
			Register("knowledge_scope_snapshot_barrier", func(tx *gorm.DB) {
				if tx.Statement == nil ||
					tx.Statement.Table != "knowledge_folders" {
					return
				}
				blockOnce.Do(func() {
					close(selectedRead)
					<-moveComplete
				})
			}),
	)
	resolver := newKnowledgeScopeSQLiteResolver(t, readDB, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 10000,
	})
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}
	type resolveResult struct {
		scope *types.KnowledgeScope
		err   error
	}
	result := make(chan resolveResult, 1)
	go func() {
		scope, err := resolver.Resolve(
			context.Background(),
			knowledgeScopeTestInput(&folderScopes),
		)
		result <- resolveResult{scope: scope, err: err}
	}()

	select {
	case <-selectedRead:
	case <-time.After(5 * time.Second):
		t.Fatal("resolver did not establish its SQLite snapshot")
	}
	moveErr := writeDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&types.KnowledgeFolder{}).
			Where(
				"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
				knowledgeScopeTestTenant,
				knowledgeScopeTestKB,
				knowledgeScopeTestFolderB,
			).
			Updates(map[string]interface{}{
				"parent_id": types.KnowledgeFolderRootID,
				"path":      "/" + knowledgeScopeTestFolderB + "/",
				"depth":     1,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&types.KnowledgeFolder{}).
			Where(
				"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
				knowledgeScopeTestTenant,
				knowledgeScopeTestKB,
				knowledgeScopeTestFolderC,
			).
			Updates(map[string]interface{}{
				"path": "/" + knowledgeScopeTestFolderB + "/" +
					knowledgeScopeTestFolderC + "/",
				"depth": 2,
			}).Error
	})
	close(moveComplete)
	require.NoError(t, moveErr)

	var resolved resolveResult
	select {
	case resolved = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("resolver did not complete after the concurrent move")
	}
	require.NoError(t, resolved.err)
	folderIDs := knowledgeScopeTestFilter(t, resolved.scope).FolderIDs()
	oldTree := []string{
		knowledgeScopeTestFolderA,
		knowledgeScopeTestFolderB,
		knowledgeScopeTestFolderC,
	}
	assert.Equal(t, oldTree, folderIDs)
}
