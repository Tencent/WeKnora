package repository

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const knowledgeFolderScopePostgresDSNEnvironment = "WEKNORA_F4A_PG_DSN"

func TestKnowledgeFolderScopeSnapshotPostgresRepeatableReadAndReadOnly(t *testing.T) {
	db := openKnowledgeFolderScopePostgresTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	fixture := newKnowledgeFolderScopePostgresFixture(t, db)

	var isolation string
	var readOnly string
	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		context.Background(),
		fixture.tenantID,
		fixture.knowledgeBaseID,
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			txReader, ok := reader.(*knowledgeFolderScopeReader)
			require.True(t, ok)
			if err := txReader.db.
				Raw("SHOW transaction_isolation").
				Row().
				Scan(&isolation); err != nil {
				return err
			}
			return txReader.db.
				Raw("SHOW transaction_read_only").
				Row().
				Scan(&readOnly)
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "repeatable read", isolation)
	assert.Equal(t, "on", readOnly)
}

func TestKnowledgeFolderScopeReaderPostgresIsolationMultiRootAndStableOrder(t *testing.T) {
	db := openKnowledgeFolderScopePostgresTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	fixture := newKnowledgeFolderScopePostgresFixture(t, db)

	var firstIDs []string
	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		context.Background(),
		fixture.tenantID,
		fixture.knowledgeBaseID,
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			selected, err := reader.ListScopeFoldersByIDs(
				[]string{
					fixture.wrongTenant.ID,
					fixture.wrongKB.ID,
					fixture.deleted.ID,
					fixture.rootA.ID,
				},
			)
			if err != nil {
				return err
			}
			assert.Equal(t, []string{fixture.rootA.ID}, knowledgeFolderIDs(selected))

			first, err := reader.ListScopeSubtreeCandidates(
				[]interfaces.KnowledgeFolderScopeRoot{
					{ID: fixture.rootB.ID, Path: fixture.rootB.Path},
					{ID: fixture.rootA.ID, Path: fixture.rootA.Path},
				},
				100,
			)
			if err != nil {
				return err
			}
			second, err := reader.ListScopeSubtreeCandidates(
				[]interfaces.KnowledgeFolderScopeRoot{
					{ID: fixture.rootA.ID, Path: fixture.rootA.Path},
					{ID: fixture.rootB.ID, Path: fixture.rootB.Path},
					{ID: fixture.rootA.ID, Path: fixture.rootA.Path},
				},
				100,
			)
			if err != nil {
				return err
			}
			firstIDs = knowledgeFolderIDs(first)
			assert.Equal(t, firstIDs, knowledgeFolderIDs(second))
			return nil
		},
	)
	require.NoError(t, err)

	expected := []string{
		fixture.rootA.ID,
		fixture.rootB.ID,
		fixture.childA.ID,
		fixture.childB.ID,
		fixture.grandchildA.ID,
	}
	sort.Slice(expected, func(i, j int) bool {
		left := fixture.byID[expected[i]]
		right := fixture.byID[expected[j]]
		if left.Depth != right.Depth {
			return left.Depth < right.Depth
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.ID < right.ID
	})
	assert.Equal(t, expected, firstIDs)
}

func TestKnowledgeFolderScopeReaderPostgresLiteralPathAndLimit(t *testing.T) {
	db := openKnowledgeFolderScopePostgresTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	fixture := newKnowledgeFolderScopePostgresFixture(t, db)

	var folderIDs []string
	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		context.Background(),
		fixture.tenantID,
		fixture.knowledgeBaseID,
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			folders, err := reader.ListScopeSubtreeCandidates(
				knowledgeFolderScopeRoots(fixture.literalRoot),
				2,
			)
			if err != nil {
				return err
			}
			folderIDs = knowledgeFolderIDs(folders)
			return nil
		},
	)
	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{fixture.literalRoot.ID, fixture.literalChild.ID},
		folderIDs,
	)
	assert.NotContains(t, folderIDs, fixture.literalLookalike.ID)
}

func TestKnowledgeFolderScopeSnapshotPostgresPropagatesContextCancellation(t *testing.T) {
	db := openKnowledgeFolderScopePostgresTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	callbackCalls := 0

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		ctx,
		1,
		"kb-canceled",
		func(interfaces.KnowledgeFolderScopeReader) error {
			callbackCalls++
			return nil
		},
	)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, callbackCalls)
}

func TestKnowledgeFolderScopeSnapshotPostgresCancelsActiveQuery(t *testing.T) {
	db := openKnowledgeFolderScopePostgresTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	callbackEntered := false

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		ctx,
		1,
		"kb-canceled-query",
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			callbackEntered = true
			txReader, ok := reader.(*knowledgeFolderScopeReader)
			require.True(t, ok)
			return txReader.db.WithContext(ctx).Exec(
				"SELECT pg_sleep(10)",
			).Error
		},
	)

	require.True(t, callbackEntered)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestKnowledgeFolderScopeSnapshotPostgresSeesConsistentValidMove(t *testing.T) {
	db := openKnowledgeFolderScopePostgresTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	fixture := newKnowledgeFolderScopePostgresFixture(t, db)

	oldRootPath := fixture.rootA.Path
	oldChildPath := fixture.childA.Path
	oldGrandchildPath := fixture.grandchildA.Path
	newRootParentID := fixture.rootB.ID
	newRootDepth := fixture.rootB.Depth + 1
	newRootPath := fixture.rootB.Path + fixture.rootA.ID + "/"
	newChildDepth := newRootDepth + 1
	newChildPath := newRootPath + fixture.childA.ID + "/"
	newGrandchildDepth := newChildDepth + 1
	newGrandchildPath := newChildPath + fixture.grandchildA.ID + "/"
	moveStarted := make(chan struct{})
	moveFinished := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		ctx,
		fixture.tenantID,
		fixture.knowledgeBaseID,
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			before, err := reader.ListScopeFoldersByIDs(
				[]string{
					fixture.rootA.ID,
					fixture.childA.ID,
					fixture.grandchildA.ID,
				},
			)
			if err != nil {
				return err
			}
			for _, folder := range before {
				if _, err := types.ValidateKnowledgeFolderStructure(folder); err != nil {
					return err
				}
			}
			assert.Equal(
				t,
				[]string{oldRootPath, oldChildPath, oldGrandchildPath},
				scopeFolderPaths(before),
			)

			go func() {
				moveFinished <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					updates := []struct {
						id      string
						columns map[string]interface{}
					}{
						{
							id: fixture.rootA.ID,
							columns: map[string]interface{}{
								"parent_id": newRootParentID,
								"depth":     newRootDepth,
								"path":      newRootPath,
							},
						},
						{
							id: fixture.childA.ID,
							columns: map[string]interface{}{
								"parent_id": fixture.rootA.ID,
								"depth":     newChildDepth,
								"path":      newChildPath,
							},
						},
						{
							id: fixture.grandchildA.ID,
							columns: map[string]interface{}{
								"parent_id": fixture.childA.ID,
								"depth":     newGrandchildDepth,
								"path":      newGrandchildPath,
							},
						},
					}
					for _, update := range updates {
						result := tx.Model(&types.KnowledgeFolder{}).
							Where(
								`tenant_id = ? AND knowledge_base_id = ?
									AND id = ? AND deleted_at IS NULL`,
								fixture.tenantID,
								fixture.knowledgeBaseID,
								update.id,
							).
							UpdateColumns(update.columns)
						if result.Error != nil {
							return result.Error
						}
						if result.RowsAffected != 1 {
							return errors.New("valid move did not update exactly one folder")
						}
					}
					return nil
				})
			}()
			close(moveStarted)
			if moveErr := <-moveFinished; moveErr != nil {
				return moveErr
			}

			after, err := reader.ListScopeSubtreeCandidates(
				knowledgeFolderScopeRoots(fixture.rootA),
				100,
			)
			if err != nil {
				return err
			}
			for _, folder := range after {
				if _, err := types.ValidateKnowledgeFolderStructure(folder); err != nil {
					return err
				}
			}
			assert.Equal(
				t,
				[]string{oldRootPath, oldChildPath, oldGrandchildPath},
				scopeFolderPaths(after),
			)
			return nil
		},
	)
	require.NoError(t, err)
	select {
	case <-moveStarted:
	default:
		t.Fatal("concurrent move was not started")
	}

	var newSnapshotFolders []*types.KnowledgeFolder
	err = repository.RunKnowledgeFolderScopeReadSnapshot(
		ctx,
		fixture.tenantID,
		fixture.knowledgeBaseID,
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			var err error
			newSnapshotFolders, err = reader.ListScopeSubtreeCandidates(
				knowledgeFolderScopeRoots(fixture.rootB),
				100,
			)
			return err
		},
	)
	require.NoError(t, err)
	assert.ElementsMatch(
		t,
		[]string{
			fixture.rootB.ID,
			fixture.childB.ID,
			fixture.rootA.ID,
			fixture.childA.ID,
			fixture.grandchildA.ID,
		},
		knowledgeFolderIDs(newSnapshotFolders),
	)
	for _, folder := range newSnapshotFolders {
		_, err := types.ValidateKnowledgeFolderStructure(folder)
		require.NoError(t, err)
	}
	newSnapshotByID := make(
		map[string]*types.KnowledgeFolder,
		len(newSnapshotFolders),
	)
	for _, folder := range newSnapshotFolders {
		newSnapshotByID[folder.ID] = folder
	}
	assert.Equal(t, fixture.rootB.ID, newSnapshotByID[fixture.rootA.ID].ParentID)
	assert.Equal(t, newRootDepth, newSnapshotByID[fixture.rootA.ID].Depth)
	assert.Equal(t, newRootPath, newSnapshotByID[fixture.rootA.ID].Path)
	assert.Equal(t, fixture.rootA.ID, newSnapshotByID[fixture.childA.ID].ParentID)
	assert.Equal(t, newChildDepth, newSnapshotByID[fixture.childA.ID].Depth)
	assert.Equal(t, newChildPath, newSnapshotByID[fixture.childA.ID].Path)
	assert.Equal(
		t,
		fixture.childA.ID,
		newSnapshotByID[fixture.grandchildA.ID].ParentID,
	)
	assert.Equal(
		t,
		newGrandchildDepth,
		newSnapshotByID[fixture.grandchildA.ID].Depth,
	)
	assert.Equal(
		t,
		newGrandchildPath,
		newSnapshotByID[fixture.grandchildA.ID].Path,
	)
}

func TestKnowledgeFolderScopeSnapshotPostgresTakesNoAdvisoryLock(t *testing.T) {
	db := openKnowledgeFolderScopePostgresTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	fixture := newKnowledgeFolderScopePostgresFixture(t, db)
	var advisoryLocks int64

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		context.Background(),
		fixture.tenantID,
		fixture.knowledgeBaseID,
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			txReader, ok := reader.(*knowledgeFolderScopeReader)
			require.True(t, ok)
			return txReader.db.Raw(`
				SELECT COUNT(*)
				FROM pg_locks
				WHERE pid = pg_backend_pid()
					AND locktype = 'advisory'
					AND granted
			`).Scan(&advisoryLocks).Error
		},
	)
	require.NoError(t, err)
	assert.Zero(t, advisoryLocks)
}

func TestListActiveKnowledgeIDsByFolderIDsPostgresSemantics(t *testing.T) {
	db := openKnowledgeFolderScopePostgresTestDB(t)
	repository := NewKnowledgeRepository(db)
	seed := uuid.New()
	tenantID := binary.BigEndian.Uint64(seed[:8])%uint64(math.MaxInt32-2) + 1
	knowledgeBaseID := uuid.NewString()
	folderID := uuid.NewString()
	secondFolderID := uuid.NewString()
	outsideFolderID := uuid.NewString()
	knowledgeIDPrefix := uuid.NewString()[:24]
	activeIDs := []string{
		knowledgeIDPrefix + "000000000001",
		knowledgeIDPrefix + "000000000002",
		knowledgeIDPrefix + "000000000004",
	}
	secondFolderKnowledgeID := knowledgeIDPrefix + "000000000003"
	mergedActiveIDs := []string{
		activeIDs[0],
		activeIDs[1],
		secondFolderKnowledgeID,
		activeIDs[2],
	}
	rootID := uuid.NewString()
	wrongTenantID := uuid.NewString()
	wrongKnowledgeBaseID := uuid.NewString()
	disabledID := uuid.NewString()
	deletedID := uuid.NewString()
	outsideFolderKnowledgeID := uuid.NewString()
	allIDs := append([]string{}, mergedActiveIDs...)
	allIDs = append(
		allIDs,
		rootID,
		wrongTenantID,
		wrongKnowledgeBaseID,
		disabledID,
		deletedID,
		outsideFolderKnowledgeID,
	)
	t.Cleanup(func() {
		result := db.Exec("DELETE FROM knowledges WHERE id IN ?", allIDs)
		if result.Error != nil {
			t.Errorf("clean PostgreSQL active knowledge ID fixtures: %v", result.Error)
		}
	})

	for _, id := range activeIDs {
		insertActiveKnowledgeIDPostgresTestRow(
			t,
			db,
			id,
			tenantID,
			knowledgeBaseID,
			folderID,
			"enabled",
			nil,
		)
	}
	insertActiveKnowledgeIDPostgresTestRow(
		t,
		db,
		secondFolderKnowledgeID,
		tenantID,
		knowledgeBaseID,
		secondFolderID,
		"enabled",
		nil,
	)
	insertActiveKnowledgeIDPostgresTestRow(
		t, db, rootID, tenantID, knowledgeBaseID, "", "enabled", nil,
	)
	insertActiveKnowledgeIDPostgresTestRow(
		t,
		db,
		wrongTenantID,
		tenantID+1,
		knowledgeBaseID,
		folderID,
		"enabled",
		nil,
	)
	insertActiveKnowledgeIDPostgresTestRow(
		t,
		db,
		wrongKnowledgeBaseID,
		tenantID,
		uuid.NewString(),
		folderID,
		"enabled",
		nil,
	)
	insertActiveKnowledgeIDPostgresTestRow(
		t,
		db,
		disabledID,
		tenantID,
		knowledgeBaseID,
		folderID,
		"disabled",
		nil,
	)
	insertActiveKnowledgeIDPostgresTestRow(
		t,
		db,
		deletedID,
		tenantID,
		knowledgeBaseID,
		folderID,
		"enabled",
		time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
	)
	insertActiveKnowledgeIDPostgresTestRow(
		t,
		db,
		outsideFolderKnowledgeID,
		tenantID,
		knowledgeBaseID,
		outsideFolderID,
		"enabled",
		nil,
	)

	rootIDs, rootHasMore, err :=
		repository.ListActiveKnowledgeIDsByFolderIDs(
			context.Background(),
			tenantID,
			knowledgeBaseID,
			[]string{""},
			nil,
			"",
			10,
		)
	require.NoError(t, err)
	assert.Equal(t, []string{rootID}, rootIDs)
	assert.False(t, rootHasMore)

	scopedIDs, scopedHasMore, err :=
		repository.ListActiveKnowledgeIDsByFolderIDs(
			context.Background(),
			tenantID,
			knowledgeBaseID,
			[]string{secondFolderID, folderID},
			nil,
			"",
			10,
		)
	require.NoError(t, err)
	assert.Equal(t, mergedActiveIDs, scopedIDs)
	assert.NotContains(t, scopedIDs, outsideFolderKnowledgeID)
	assert.False(t, scopedHasMore)

	expectedIntersectionIDs := []string{
		activeIDs[1],
		secondFolderKnowledgeID,
	}
	sort.Strings(expectedIntersectionIDs)
	intersectionIDs, intersectionHasMore, err :=
		repository.ListActiveKnowledgeIDsByFolderIDs(
			context.Background(),
			tenantID,
			knowledgeBaseID,
			[]string{secondFolderID, folderID},
			[]string{
				activeIDs[1],
				outsideFolderKnowledgeID,
				secondFolderKnowledgeID,
			},
			"",
			10,
		)
	require.NoError(t, err)
	assert.Equal(t, expectedIntersectionIDs, intersectionIDs)
	assert.False(t, intersectionHasMore)

	firstPage, firstHasMore, err :=
		repository.ListActiveKnowledgeIDsByFolderIDs(
			context.Background(),
			tenantID,
			knowledgeBaseID,
			[]string{secondFolderID, folderID},
			nil,
			"",
			2,
		)
	require.NoError(t, err)
	require.Equal(t, mergedActiveIDs[:2], firstPage)
	require.True(t, firstHasMore)

	secondPage, secondHasMore, err :=
		repository.ListActiveKnowledgeIDsByFolderIDs(
			context.Background(),
			tenantID,
			knowledgeBaseID,
			[]string{secondFolderID, folderID},
			nil,
			firstPage[len(firstPage)-1],
			2,
		)
	require.NoError(t, err)
	assert.Equal(t, mergedActiveIDs[2:], secondPage)
	assert.False(t, secondHasMore)
}

func insertActiveKnowledgeIDPostgresTestRow(
	t *testing.T,
	db *gorm.DB,
	id string,
	tenantID uint64,
	knowledgeBaseID string,
	folderID string,
	enableStatus string,
	deletedAt interface{},
) {
	t.Helper()
	require.NoError(
		t,
		db.Exec(`
			INSERT INTO knowledges (
				id,
				tenant_id,
				knowledge_base_id,
				type,
				title,
				source,
				enable_status,
				folder_id,
				deleted_at
			)
			VALUES (?, ?, ?, 'manual', ?, 'phase5a2-test', ?, ?, ?)
		`,
			id,
			tenantID,
			knowledgeBaseID,
			id,
			enableStatus,
			folderID,
			deletedAt,
		).Error,
	)
}

type knowledgeFolderScopePostgresFixture struct {
	tenantID         uint64
	knowledgeBaseID  string
	rootA            *types.KnowledgeFolder
	rootB            *types.KnowledgeFolder
	childA           *types.KnowledgeFolder
	childB           *types.KnowledgeFolder
	grandchildA      *types.KnowledgeFolder
	wrongTenant      *types.KnowledgeFolder
	wrongKB          *types.KnowledgeFolder
	deleted          *types.KnowledgeFolder
	literalRoot      *types.KnowledgeFolder
	literalChild     *types.KnowledgeFolder
	literalLookalike *types.KnowledgeFolder
	byID             map[string]*types.KnowledgeFolder
}

func newKnowledgeFolderScopePostgresFixture(
	t *testing.T,
	db *gorm.DB,
) *knowledgeFolderScopePostgresFixture {
	t.Helper()
	seed := uuid.New()
	tenantID := binary.BigEndian.Uint64(seed[:8])%uint64(math.MaxInt64-1) + 1
	kbID := uuid.NewString()
	rootAID := uuid.NewString()
	rootBID := uuid.NewString()
	childAID := uuid.NewString()
	childBID := uuid.NewString()
	grandchildAID := uuid.NewString()
	literalRootID := "literal%!_"

	fixture := &knowledgeFolderScopePostgresFixture{
		tenantID:        tenantID,
		knowledgeBaseID: kbID,
		rootA: rawKnowledgeFolderFixture(
			rootAID,
			tenantID,
			kbID,
			"",
			"Root A",
			"/"+rootAID+"/",
			1,
		),
		rootB: rawKnowledgeFolderFixture(
			rootBID,
			tenantID,
			kbID,
			"",
			"Root B",
			"/"+rootBID+"/",
			1,
		),
		childA: rawKnowledgeFolderFixture(
			childAID,
			tenantID,
			kbID,
			rootAID,
			"Child A",
			"/"+rootAID+"/"+childAID+"/",
			2,
		),
		childB: rawKnowledgeFolderFixture(
			childBID,
			tenantID,
			kbID,
			rootBID,
			"Child B",
			"/"+rootBID+"/"+childBID+"/",
			2,
		),
		grandchildA: rawKnowledgeFolderFixture(
			grandchildAID,
			tenantID,
			kbID,
			childAID,
			"Grandchild A",
			"/"+rootAID+"/"+childAID+"/"+grandchildAID+"/",
			3,
		),
		wrongTenant: rawKnowledgeFolderFixture(
			uuid.NewString(),
			tenantID+1,
			kbID,
			"",
			"Wrong tenant",
			"/"+uuid.NewString()+"/",
			1,
		),
		wrongKB: rawKnowledgeFolderFixture(
			uuid.NewString(),
			tenantID,
			uuid.NewString(),
			"",
			"Wrong KB",
			"/"+uuid.NewString()+"/",
			1,
		),
		deleted: rawKnowledgeFolderFixture(
			uuid.NewString(),
			tenantID,
			kbID,
			"",
			"Deleted",
			"/"+uuid.NewString()+"/",
			1,
		),
		literalRoot: rawKnowledgeFolderFixture(
			literalRootID,
			tenantID,
			kbID,
			"",
			"Literal Root",
			"/literal%!_/",
			1,
		),
		literalChild: rawKnowledgeFolderFixture(
			uuid.NewString(),
			tenantID,
			kbID,
			"",
			"Literal Child",
			"/literal%!_/child/",
			2,
		),
		literalLookalike: rawKnowledgeFolderFixture(
			uuid.NewString(),
			tenantID,
			kbID,
			"",
			"Literal Lookalike",
			"/literalXX_/child/",
			2,
		),
	}
	fixture.deleted.DeletedAt = gorm.DeletedAt{
		Time:  time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		Valid: true,
	}
	folders := []*types.KnowledgeFolder{
		fixture.rootA,
		fixture.rootB,
		fixture.childA,
		fixture.childB,
		fixture.grandchildA,
		fixture.wrongTenant,
		fixture.wrongKB,
		fixture.deleted,
		fixture.literalRoot,
		fixture.literalChild,
		fixture.literalLookalike,
	}
	require.NoError(t, db.Create(folders).Error)
	fixture.byID = make(map[string]*types.KnowledgeFolder, len(folders))
	for _, folder := range folders {
		fixture.byID[folder.ID] = folder
	}
	t.Cleanup(func() {
		result := db.Exec(
			"DELETE FROM knowledge_folders WHERE tenant_id IN ?",
			[]uint64{tenantID, tenantID + 1},
		)
		if result.Error != nil {
			t.Errorf("clean PostgreSQL knowledge folder fixtures: %v", result.Error)
		}
	})
	return fixture
}

func openKnowledgeFolderScopePostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv(knowledgeFolderScopePostgresDSNEnvironment)
	if dsn == "" {
		t.Skip("WEKNORA_F4A_PG_DSN is not set; PostgreSQL scope tests were not run")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.PingContext(context.Background()))
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("close PostgreSQL test database: %v", err)
		}
	})
	return db
}

func scopeFolderPaths(folders []*types.KnowledgeFolder) []string {
	paths := make([]string, len(folders))
	for index, folder := range folders {
		paths[index] = folder.Path
	}
	return paths
}
