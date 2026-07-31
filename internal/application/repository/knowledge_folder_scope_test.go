package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestKnowledgeFolderScopeReaderScopesTenantKnowledgeBaseAndActiveRows(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	ctx := context.Background()
	reader := newTestKnowledgeFolderScopeReader(db, ctx, 1, "kb-1")

	wanted := knowledgeFolderFixture("wanted", 1, "kb-1", "", "Wanted", "/wanted/", 1)
	wrongTenant := knowledgeFolderFixture(
		"wrong-tenant",
		2,
		"kb-1",
		"",
		"Wrong tenant",
		"/wrong-tenant/",
		1,
	)
	wrongKB := knowledgeFolderFixture(
		"wrong-kb",
		1,
		"kb-2",
		"",
		"Wrong KB",
		"/wrong-kb/",
		1,
	)
	deleted := knowledgeFolderFixture("deleted", 1, "kb-1", "", "Deleted", "/deleted/", 1)
	deleted.DeletedAt = gorm.DeletedAt{
		Time:  time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		Valid: true,
	}
	wantedChild := knowledgeFolderFixture(
		"wanted-child",
		1,
		"kb-1",
		wanted.ID,
		"Wanted child",
		"/wanted/wanted-child/",
		2,
	)
	wrongTenantChild := knowledgeFolderFixture(
		"wrong-tenant-child",
		2,
		"kb-1",
		wanted.ID,
		"Wrong tenant child",
		"/wanted/wrong-tenant-child/",
		2,
	)
	wrongKBChild := knowledgeFolderFixture(
		"wrong-kb-child",
		1,
		"kb-2",
		wanted.ID,
		"Wrong KB child",
		"/wanted/wrong-kb-child/",
		2,
	)
	deletedChild := knowledgeFolderFixture(
		"deleted-child",
		1,
		"kb-1",
		wanted.ID,
		"Deleted child",
		"/wanted/deleted-child/",
		2,
	)
	deletedChild.DeletedAt = deleted.DeletedAt
	require.NoError(t, db.Create([]*types.KnowledgeFolder{
		wanted,
		wrongTenant,
		wrongKB,
		deleted,
		wantedChild,
		wrongTenantChild,
		wrongKBChild,
		deletedChild,
	}).Error)

	folders, err := reader.ListScopeFoldersByIDs(
		[]string{deleted.ID, wrongKB.ID, wanted.ID, wrongTenant.ID},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{wanted.ID}, knowledgeFolderIDs(folders))

	subtree, err := reader.ListScopeSubtreeCandidates(
		knowledgeFolderScopeRoots(wanted),
		10,
	)
	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{wanted.ID, wantedChild.ID},
		knowledgeFolderIDs(subtree),
	)
}

func TestKnowledgeFolderScopeReaderUsesSnapshotBoundTenant(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	tenantA := uint64(1)
	tenantB := uint64(2)
	knowledgeBaseID := "kb-bound-tenant"
	folderA := knowledgeFolderFixture(
		"bound-tenant-a",
		tenantA,
		knowledgeBaseID,
		"",
		"Tenant A",
		"/bound-tenant-a/",
		1,
	)
	folderB := knowledgeFolderFixture(
		"bound-tenant-b",
		tenantB,
		knowledgeBaseID,
		"",
		"Tenant B",
		"/bound-tenant-b/",
		1,
	)
	require.NoError(t, db.Create([]*types.KnowledgeFolder{folderA, folderB}).Error)

	var selected []*types.KnowledgeFolder
	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		context.Background(),
		tenantA,
		knowledgeBaseID,
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			var err error
			selected, err = reader.ListScopeFoldersByIDs(
				[]string{folderA.ID, folderB.ID},
			)
			return err
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{folderA.ID}, knowledgeFolderIDs(selected))
}

func TestKnowledgeFolderScopeReaderUsesSnapshotBoundKnowledgeBase(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	tenantID := uint64(1)
	knowledgeBaseA := "kb-bound-a"
	knowledgeBaseB := "kb-bound-b"
	folderA := knowledgeFolderFixture(
		"bound-kb-a",
		tenantID,
		knowledgeBaseA,
		"",
		"KB A",
		"/bound-kb-a/",
		1,
	)
	folderB := knowledgeFolderFixture(
		"bound-kb-b",
		tenantID,
		knowledgeBaseB,
		"",
		"KB B",
		"/bound-kb-b/",
		1,
	)
	require.NoError(t, db.Create([]*types.KnowledgeFolder{folderA, folderB}).Error)

	var selected []*types.KnowledgeFolder
	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		context.Background(),
		tenantID,
		knowledgeBaseA,
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			var err error
			selected, err = reader.ListScopeFoldersByIDs(
				[]string{folderA.ID, folderB.ID},
			)
			return err
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{folderA.ID}, knowledgeFolderIDs(selected))
}

func TestKnowledgeFolderScopeReaderUsesSnapshotBoundContext(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	ctx, cancel := context.WithCancel(context.Background())

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		ctx,
		1,
		"kb-bound-context",
		func(reader interfaces.KnowledgeFolderScopeReader) error {
			cancel()
			_, err := reader.ListScopeFoldersByIDs(nil)
			return err
		},
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestKnowledgeFolderScopeReaderRejectsInvalidBoundDatabase(t *testing.T) {
	reader := &knowledgeFolderScopeReader{
		db:              &gorm.DB{},
		ctx:             context.Background(),
		sourceTenantID:  1,
		knowledgeBaseID: "kb-invalid-database",
	}

	_, err := reader.ListScopeFoldersByIDs([]string{"folder-id"})

	require.ErrorIs(t, err, ErrKnowledgeFolderInvalid)
}

func TestKnowledgeFolderScopeReaderCannotOverrideSnapshotScope(t *testing.T) {
	readerType := reflect.TypeOf(
		(*interfaces.KnowledgeFolderScopeReader)(nil),
	).Elem()

	foldersMethod, found := readerType.MethodByName("ListScopeFoldersByIDs")
	require.True(t, found)
	assert.Equal(t, 1, foldersMethod.Type.NumIn())
	assert.Equal(t, reflect.TypeOf([]string{}), foldersMethod.Type.In(0))

	subtreeMethod, found := readerType.MethodByName("ListScopeSubtreeCandidates")
	require.True(t, found)
	assert.Equal(t, 2, subtreeMethod.Type.NumIn())
	assert.Equal(
		t,
		reflect.TypeOf([]interfaces.KnowledgeFolderScopeRoot{}),
		subtreeMethod.Type.In(0),
	)
	assert.Equal(t, reflect.TypeOf(0), subtreeMethod.Type.In(1))
}

func TestKnowledgeFolderScopeReaderCannotOverrideSnapshotContext(t *testing.T) {
	readerType := reflect.TypeOf(
		(*interfaces.KnowledgeFolderScopeReader)(nil),
	).Elem()
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()

	for _, methodName := range []string{
		"ListScopeFoldersByIDs",
		"ListScopeSubtreeCandidates",
	} {
		method, found := readerType.MethodByName(methodName)
		require.True(t, found)
		for index := 0; index < method.Type.NumIn(); index++ {
			assert.NotEqual(t, contextType, method.Type.In(index))
		}
	}
}

func TestKnowledgeFolderScopeRootDoesNotMarshalRuntimeFields(t *testing.T) {
	root := interfaces.KnowledgeFolderScopeRoot{
		ID:   "secret-folder",
		Path: "/secret-parent/secret-folder/",
	}

	encoded, err := json.Marshal(root)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "secret-folder")
	assert.NotContains(t, string(encoded), "secret-parent")
	assert.NotContains(t, string(encoded), `"ID"`)
	assert.NotContains(t, string(encoded), `"Path"`)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	assert.Empty(t, fields)
}

func TestKnowledgeFolderScopeRootFormattingDoesNotExposeRuntimeFields(t *testing.T) {
	root := interfaces.KnowledgeFolderScopeRoot{
		ID:   "secret-folder",
		Path: "/secret-parent/secret-folder/",
	}

	for _, format := range []string{"%v", "%+v", "%#v"} {
		output := fmt.Sprintf(format, root)
		assert.NotContains(t, output, "secret-folder", format)
		assert.NotContains(t, output, "secret-parent", format)
		assert.NotContains(t, output, "/secret-parent/secret-folder/", format)
		assert.Contains(t, output, "KnowledgeFolderScopeRoot", format)
		assert.Contains(t, output, "id_set=true", format)
		assert.Contains(t, output, "path_set=true", format)
	}
}

func TestKnowledgeFolderScopeReaderEmptyIDsUseZeroQueries(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(
		postgres.New(postgres.Config{Conn: sqlDB}),
		&gorm.Config{},
	)
	require.NoError(t, err)
	reader := newTestKnowledgeFolderScopeReader(
		db,
		context.Background(),
		1,
		"kb-1",
	)

	selected, err := reader.ListScopeFoldersByIDs(
		nil,
	)
	require.NoError(t, err)
	assert.Empty(t, selected)

	subtree, err := reader.ListScopeSubtreeCandidates(
		nil,
		1,
	)
	require.NoError(t, err)
	assert.Empty(t, subtree)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderScopeReaderStableOrder(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	reader := newTestKnowledgeFolderScopeReader(
		db,
		context.Background(),
		1,
		"kb-1",
	)
	firstID := knowledgeFolderTestID("first")
	secondID := knowledgeFolderTestID("second")
	deepID := knowledgeFolderTestID("deep")
	first := rawKnowledgeFolderFixture(firstID, 1, "kb-1", "", "First", "/a/", 1)
	second := rawKnowledgeFolderFixture(secondID, 1, "kb-1", "", "Second", "/b/", 1)
	deep := rawKnowledgeFolderFixture(deepID, 1, "kb-1", first.ID, "Deep", "/a/deep/", 2)
	require.NoError(t, db.Create([]*types.KnowledgeFolder{deep, second, first}).Error)

	input := []string{deep.ID, second.ID, first.ID, second.ID}
	originalInput := append([]string(nil), input...)
	folders, err := reader.ListScopeFoldersByIDs(
		input,
	)
	require.NoError(t, err)
	assert.Equal(t, originalInput, input)
	assert.Equal(
		t,
		[]string{first.ID, second.ID, deep.ID},
		knowledgeFolderIDs(folders),
	)
}

func TestKnowledgeFolderScopeReaderReturnsReachableAndPathCandidates(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	reader := newTestKnowledgeFolderScopeReader(
		db,
		context.Background(),
		1,
		"kb-1",
	)

	root := knowledgeFolderFixture("scope-root", 1, "kb-1", "", "Root", "/scope-root/", 1)
	reachableOnly := knowledgeFolderFixture(
		"reachable-only",
		1,
		"kb-1",
		root.ID,
		"Reachable",
		"/dirty/reachable-only/",
		2,
	)
	pathOnly := knowledgeFolderFixture(
		"path-only",
		1,
		"kb-1",
		"",
		"Path",
		"/scope-root/path-only/",
		2,
	)
	unrelated := knowledgeFolderFixture(
		"unrelated",
		1,
		"kb-1",
		"",
		"Unrelated",
		"/unrelated/",
		1,
	)
	require.NoError(t, db.Create([]*types.KnowledgeFolder{
		unrelated,
		pathOnly,
		reachableOnly,
		root,
	}).Error)

	folders, err := reader.ListScopeSubtreeCandidates(
		knowledgeFolderScopeRoots(root),
		10,
	)
	require.NoError(t, err)
	assert.ElementsMatch(
		t,
		[]string{root.ID, reachableOnly.ID, pathOnly.ID},
		knowledgeFolderIDs(folders),
	)
}

func TestKnowledgeFolderScopeReaderLiteralPathPrefix(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	reader := newTestKnowledgeFolderScopeReader(
		db,
		context.Background(),
		1,
		"kb-1",
	)
	rootID := "literal%!_"
	root := rawKnowledgeFolderFixture(
		rootID,
		1,
		"kb-1",
		"",
		"Root",
		"/literal%!_/",
		1,
	)
	literal := rawKnowledgeFolderFixture(
		knowledgeFolderTestID("literal-child"),
		1,
		"kb-1",
		"",
		"Literal",
		"/literal%!_/child/",
		2,
	)
	wildcardLookalike := rawKnowledgeFolderFixture(
		knowledgeFolderTestID("wildcard-lookalike"),
		1,
		"kb-1",
		"",
		"Lookalike",
		"/literalXX_/child/",
		2,
	)
	require.NoError(t, db.Create([]*types.KnowledgeFolder{
		root,
		literal,
		wildcardLookalike,
	}).Error)

	folders, err := reader.ListScopeSubtreeCandidates(
		knowledgeFolderScopeRoots(root),
		10,
	)
	require.NoError(t, err)
	assert.Equal(t, []string{root.ID, literal.ID}, knowledgeFolderIDs(folders))
}

func TestKnowledgeFolderScopeReaderLimitPlusOne(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	reader := newTestKnowledgeFolderScopeReader(
		db,
		context.Background(),
		1,
		"kb-1",
	)
	root := knowledgeFolderFixture("limit-root", 1, "kb-1", "", "Root", "/limit-root/", 1)
	child := knowledgeFolderFixture(
		"limit-child",
		1,
		"kb-1",
		root.ID,
		"Child",
		"/limit-root/limit-child/",
		2,
	)
	grandchild := knowledgeFolderFixture(
		"limit-grandchild",
		1,
		"kb-1",
		child.ID,
		"Grandchild",
		"/limit-root/limit-child/limit-grandchild/",
		3,
	)
	require.NoError(t, db.Create([]*types.KnowledgeFolder{root, child, grandchild}).Error)

	folders, err := reader.ListScopeSubtreeCandidates(
		knowledgeFolderScopeRoots(root),
		2,
	)
	require.NoError(t, err)
	assert.Equal(t, []string{root.ID, child.ID}, knowledgeFolderIDs(folders))
}

func TestKnowledgeFolderScopeReaderUsesProvidedValidatedRoots(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	reader := newTestKnowledgeFolderScopeReader(
		db,
		context.Background(),
		1,
		"kb-1",
	)
	rootID := knowledgeFolderTestID("provided-root")
	storedParentID := knowledgeFolderTestID("stored-parent")
	providedParentID := knowledgeFolderTestID("provided-parent")
	providedCandidateID := knowledgeFolderTestID("provided-candidate")
	storedCandidateID := knowledgeFolderTestID("stored-candidate")
	root := rawKnowledgeFolderFixture(
		rootID,
		1,
		"kb-1",
		storedParentID,
		"Root",
		"/"+storedParentID+"/"+rootID+"/",
		2,
	)
	providedCandidate := rawKnowledgeFolderFixture(
		providedCandidateID,
		1,
		"kb-1",
		"",
		"Provided candidate",
		"/"+providedParentID+"/"+rootID+"/"+providedCandidateID+"/",
		3,
	)
	storedCandidate := rawKnowledgeFolderFixture(
		storedCandidateID,
		1,
		"kb-1",
		"",
		"Stored candidate",
		"/"+storedParentID+"/"+rootID+"/"+storedCandidateID+"/",
		3,
	)
	require.NoError(t, db.Create([]*types.KnowledgeFolder{
		root,
		providedCandidate,
		storedCandidate,
	}).Error)

	folders, err := reader.ListScopeSubtreeCandidates(
		[]interfaces.KnowledgeFolderScopeRoot{{
			ID:   root.ID,
			Path: "/" + providedParentID + "/" + root.ID + "/",
		}},
		10,
	)
	require.NoError(t, err)
	assert.ElementsMatch(
		t,
		[]string{root.ID, providedCandidate.ID},
		knowledgeFolderIDs(folders),
	)
	assert.NotContains(t, knowledgeFolderIDs(folders), storedCandidate.ID)
}

func TestKnowledgeFolderScopeReaderDoesNotReloadRootRows(t *testing.T) {
	reader, mock := newKnowledgeFolderScopeSQLMockReader(t)
	root := interfaces.KnowledgeFolderScopeRoot{
		ID:   "root-a",
		Path: "/root-a/",
	}
	mock.ExpectQuery(knowledgeFolderScopeSubtreeQueryPattern).
		WithArgs(
			root.ID,
			root.Path,
			uint64(1),
			"kb-1",
			uint64(1),
			"kb-1",
			"/root-a/%",
			uint64(1),
			"kb-1",
			10,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	folders, err := reader.ListScopeSubtreeCandidates(
		[]interfaces.KnowledgeFolderScopeRoot{root},
		10,
	)
	require.NoError(t, err)
	assert.Empty(t, folders)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderScopeReaderUsesBoundRootValues(t *testing.T) {
	reader, mock := newKnowledgeFolderScopeSQLMockReader(t)
	root := interfaces.KnowledgeFolderScopeRoot{
		ID:   "root') SELECT pg_sleep(10); --",
		Path: "/root') SELECT pg_sleep(10); --/",
	}
	mock.ExpectQuery(knowledgeFolderScopeSubtreeQueryPattern).
		WithArgs(
			root.ID,
			root.Path,
			uint64(1),
			"kb-1",
			uint64(1),
			"kb-1",
			"/root') SELECT pg!_sleep(10); --/%",
			uint64(1),
			"kb-1",
			10,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, err := reader.ListScopeSubtreeCandidates(
		[]interfaces.KnowledgeFolderScopeRoot{root},
		10,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderScopeReaderStableRootOrder(t *testing.T) {
	reader, mock := newKnowledgeFolderScopeSQLMockReader(t)
	rootA := interfaces.KnowledgeFolderScopeRoot{ID: "root-a", Path: "/root-a/"}
	rootB := interfaces.KnowledgeFolderScopeRoot{ID: "root-b", Path: "/root-b/"}
	expectedArgs := []driver.Value{
		rootA.ID,
		rootA.Path,
		rootB.ID,
		rootB.Path,
		uint64(1),
		"kb-1",
		uint64(1),
		"kb-1",
		"/root-a/%",
		"/root-b/%",
		uint64(1),
		"kb-1",
		10,
	}
	for range 2 {
		mock.ExpectQuery(knowledgeFolderScopeSubtreeQueryPattern).
			WithArgs(expectedArgs...).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))
	}

	reversed := []interfaces.KnowledgeFolderScopeRoot{rootB, rootA}
	original := append([]interfaces.KnowledgeFolderScopeRoot(nil), reversed...)
	_, err := reader.ListScopeSubtreeCandidates(
		reversed,
		10,
	)
	require.NoError(t, err)
	assert.Equal(t, original, reversed)

	_, err = reader.ListScopeSubtreeCandidates(
		[]interfaces.KnowledgeFolderScopeRoot{rootA, rootB},
		10,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderScopeReaderRejectsEmptyRootID(t *testing.T) {
	reader, mock := newKnowledgeFolderScopeSQLMockReader(t)

	_, err := reader.ListScopeSubtreeCandidates(
		[]interfaces.KnowledgeFolderScopeRoot{{Path: "/root-a/"}},
		10,
	)
	require.ErrorIs(t, err, ErrKnowledgeFolderInvalid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderScopeReaderRejectsInvalidRootPath(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "empty", path: ""},
		{name: "missing leading slash", path: "root-a/"},
		{name: "missing trailing slash", path: "/root-a"},
		{name: "empty segment", path: "/parent//root-a/"},
		{name: "does not end with root ID", path: "/other/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, mock := newKnowledgeFolderScopeSQLMockReader(t)
			_, err := reader.ListScopeSubtreeCandidates(
				[]interfaces.KnowledgeFolderScopeRoot{{
					ID:   "root-a",
					Path: tt.path,
				}},
				10,
			)
			require.ErrorIs(t, err, ErrKnowledgeFolderInvalid)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKnowledgeFolderScopeReaderRejectsDuplicateRootWithDifferentPath(t *testing.T) {
	reader, mock := newKnowledgeFolderScopeSQLMockReader(t)

	_, err := reader.ListScopeSubtreeCandidates(
		[]interfaces.KnowledgeFolderScopeRoot{
			{ID: "root-a", Path: "/parent-a/root-a/"},
			{ID: "root-a", Path: "/parent-b/root-a/"},
		},
		10,
	)
	require.ErrorIs(t, err, ErrKnowledgeFolderInvalid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeFolderScopeSnapshotRejectsUnsupportedDialect(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	db.Config.Dialector = knowledgeFolderScopeUnsupportedDialector{
		Dialector: db.Config.Dialector,
	}
	repository := NewKnowledgeFolderScopeRepository(db)
	callbackCalls := 0

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		context.Background(),
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderScopeReader) error {
			callbackCalls++
			return nil
		},
	)
	require.ErrorIs(t, err, ErrKnowledgeFolderUnsupportedDialect)
	assert.Zero(t, callbackCalls)
}

func TestKnowledgeFolderScopeSnapshotPropagatesContextCancellation(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	callbackCalls := 0

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		ctx,
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderScopeReader) error {
			callbackCalls++
			return nil
		},
	)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, callbackCalls)
}

func TestKnowledgeFolderScopeSnapshotPreservesWrappedCancellationWhenContextIsCanceled(
	t *testing.T,
) {
	db := setupKnowledgeFolderTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := fmt.Errorf("测试包装上下文: %w", context.Canceled)

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		ctx,
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderScopeReader) error {
			cancel()
			return wrapped
		},
	)

	assert.Same(t, wrapped, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, strings.Contains(err.Error(), "测试包装上下文"))
}

func TestKnowledgeFolderScopeSnapshotPreservesWrappedDeadlineWhenContextDeadlineExceeded(
	t *testing.T,
) {
	db := setupKnowledgeFolderTestDB(t)
	repository := NewKnowledgeFolderScopeRepository(db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	wrapped := fmt.Errorf("测试包装上下文: %w", context.DeadlineExceeded)

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		ctx,
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderScopeReader) error {
			<-ctx.Done()
			return wrapped
		},
	)

	assert.Same(t, wrapped, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, strings.Contains(err.Error(), "测试包装上下文"))
}

func TestKnowledgeFolderScopeSnapshotDoesNotRetryBusinessErrors(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repository := &knowledgeFolderScopeRepository{
		db: db,
		sqliteRetryWait: func(context.Context, int) error {
			return errors.New("unexpected retry")
		},
	}
	businessErr := errors.New("business validation failed")
	callbackCalls := 0

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		context.Background(),
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderScopeReader) error {
			callbackCalls++
			return businessErr
		},
	)
	require.ErrorIs(t, err, businessErr)
	assert.Equal(t, 1, callbackCalls)
}

func TestKnowledgeFolderScopeSnapshotRetriesSQLiteBusyLocked(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	waitAttempts := make([]int, 0, 2)
	repository := &knowledgeFolderScopeRepository{
		db: db,
		sqliteRetryWait: func(_ context.Context, attempt int) error {
			waitAttempts = append(waitAttempts, attempt)
			return nil
		},
	}
	callbackCalls := 0

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		context.Background(),
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderScopeReader) error {
			callbackCalls++
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
	assert.Equal(t, []int{0, 1}, waitAttempts)
}

func TestKnowledgeFolderScopeSnapshotClearsAttemptLocalResults(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repository := &knowledgeFolderScopeRepository{
		db: db,
		sqliteRetryWait: func(context.Context, int) error {
			return nil
		},
	}
	callbackCalls := 0
	var attemptLocal []string

	err := repository.RunKnowledgeFolderScopeReadSnapshot(
		context.Background(),
		1,
		"kb-1",
		func(interfaces.KnowledgeFolderScopeReader) error {
			attemptLocal = nil
			callbackCalls++
			attemptLocal = append(
				attemptLocal,
				fmt.Sprintf("attempt-%d", callbackCalls),
			)
			if callbackCalls == 1 {
				return sqlite3.Error{Code: sqlite3.ErrBusy}
			}
			return nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 2, callbackCalls)
	assert.Equal(t, []string{"attempt-2"}, attemptLocal)
}

func TestListActiveKnowledgeIDsByFolderIDsRootDirect(t *testing.T) {
	db, repository := setupActiveKnowledgeIDTestRepository(t)
	rootID := activeKnowledgeIDTestID(1)
	nestedID := activeKnowledgeIDTestID(2)
	insertActiveKnowledgeIDTestRow(
		t, db, rootID, 1, "kb-1", "", "enabled", nil,
	)
	insertActiveKnowledgeIDTestRow(
		t,
		db,
		nestedID,
		1,
		"kb-1",
		knowledgeFolderTestID("nested"),
		"enabled",
		nil,
	)

	ids, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{""},
		nil,
		"",
		10,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{rootID}, ids)
	assert.False(t, hasMore)
}

func TestListActiveKnowledgeIDsByFolderIDsScopesTenantAndKB(t *testing.T) {
	db, repository := setupActiveKnowledgeIDTestRepository(t)
	folderID := knowledgeFolderTestID("shared-folder")
	wantedID := activeKnowledgeIDTestID(1)
	insertActiveKnowledgeIDTestRow(
		t, db, wantedID, 1, "kb-1", folderID, "enabled", nil,
	)
	insertActiveKnowledgeIDTestRow(
		t, db, activeKnowledgeIDTestID(2), 2, "kb-1", folderID, "enabled", nil,
	)
	insertActiveKnowledgeIDTestRow(
		t, db, activeKnowledgeIDTestID(3), 1, "kb-2", folderID, "enabled", nil,
	)

	ids, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{folderID},
		nil,
		"",
		10,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{wantedID}, ids)
	assert.False(t, hasMore)
}

func TestListActiveKnowledgeIDsByFolderIDsExcludesDisabledAndSoftDeleted(
	t *testing.T,
) {
	db, repository := setupActiveKnowledgeIDTestRepository(t)
	folderID := knowledgeFolderTestID("active-only")
	wantedID := activeKnowledgeIDTestID(1)
	insertActiveKnowledgeIDTestRow(
		t, db, wantedID, 1, "kb-1", folderID, "enabled", nil,
	)
	insertActiveKnowledgeIDTestRow(
		t,
		db,
		activeKnowledgeIDTestID(2),
		1,
		"kb-1",
		folderID,
		"disabled",
		nil,
	)
	deletedAt := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	insertActiveKnowledgeIDTestRow(
		t,
		db,
		activeKnowledgeIDTestID(3),
		1,
		"kb-1",
		folderID,
		"enabled",
		deletedAt,
	)

	ids, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{folderID},
		nil,
		"",
		10,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{wantedID}, ids)
	assert.False(t, hasMore)
}

func TestListActiveKnowledgeIDsByFolderIDsIntersectsExistingIDs(t *testing.T) {
	db, repository := setupActiveKnowledgeIDTestRepository(t)
	folderID := knowledgeFolderTestID("intersection")
	otherFolderID := knowledgeFolderTestID("outside-intersection")
	firstID := activeKnowledgeIDTestID(1)
	secondID := activeKnowledgeIDTestID(2)
	outsideID := activeKnowledgeIDTestID(3)
	insertActiveKnowledgeIDTestRow(
		t, db, firstID, 1, "kb-1", folderID, "enabled", nil,
	)
	insertActiveKnowledgeIDTestRow(
		t, db, secondID, 1, "kb-1", folderID, "enabled", nil,
	)
	insertActiveKnowledgeIDTestRow(
		t, db, outsideID, 1, "kb-1", otherFolderID, "enabled", nil,
	)

	ids, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{folderID},
		[]string{secondID, outsideID},
		"",
		10,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{secondID}, ids)
	assert.False(t, hasMore)

	unrestrictedIDs, hasMore, err :=
		repository.ListActiveKnowledgeIDsByFolderIDs(
			context.Background(),
			1,
			"kb-1",
			[]string{folderID},
			[]string{},
			"",
			10,
		)
	require.NoError(t, err)
	assert.Equal(t, []string{firstID, secondID}, unrestrictedIDs)
	assert.False(t, hasMore)
}

func TestListActiveKnowledgeIDsByFolderIDsSupportsResolvedMultiFolderScope(
	t *testing.T,
) {
	db, repository := setupActiveKnowledgeIDTestRepository(t)
	folderA := knowledgeFolderTestID("resolved-a")
	folderB := knowledgeFolderTestID("resolved-b")
	folderOutside := knowledgeFolderTestID("resolved-outside")
	firstFolderAID := activeKnowledgeIDTestID(1)
	folderBID := activeKnowledgeIDTestID(2)
	outsideID := activeKnowledgeIDTestID(3)
	secondFolderAID := activeKnowledgeIDTestID(4)

	insertActiveKnowledgeIDTestRow(
		t, db, secondFolderAID, 1, "kb-1", folderA, "enabled", nil,
	)
	insertActiveKnowledgeIDTestRow(
		t, db, outsideID, 1, "kb-1", folderOutside, "enabled", nil,
	)
	insertActiveKnowledgeIDTestRow(
		t, db, folderBID, 1, "kb-1", folderB, "enabled", nil,
	)
	insertActiveKnowledgeIDTestRow(
		t, db, firstFolderAID, 1, "kb-1", folderA, "enabled", nil,
	)

	ids, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{folderB, folderA},
		nil,
		"",
		10,
	)
	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{firstFolderAID, folderBID, secondFolderAID},
		ids,
	)
	assert.NotContains(t, ids, outsideID)
	assert.False(t, hasMore)

	intersectedIDs, hasMore, err :=
		repository.ListActiveKnowledgeIDsByFolderIDs(
			context.Background(),
			1,
			"kb-1",
			[]string{folderB, folderA},
			[]string{secondFolderAID, outsideID, folderBID},
			"",
			10,
		)
	require.NoError(t, err)
	assert.Equal(t, []string{folderBID, secondFolderAID}, intersectedIDs)
	assert.False(t, hasMore)
}

func TestListActiveKnowledgeIDsByFolderIDsStableLimitAndHasMore(t *testing.T) {
	db, repository := setupActiveKnowledgeIDTestRepository(t)
	folderID := knowledgeFolderTestID("stable-limit")
	orderedIDs := []string{
		activeKnowledgeIDTestID(1),
		activeKnowledgeIDTestID(2),
		activeKnowledgeIDTestID(3),
		activeKnowledgeIDTestID(4),
	}
	for _, index := range []int{3, 1, 4, 2} {
		insertActiveKnowledgeIDTestRow(
			t,
			db,
			activeKnowledgeIDTestID(index),
			1,
			"kb-1",
			folderID,
			"enabled",
			nil,
		)
	}

	firstPage, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{folderID},
		nil,
		"",
		2,
	)
	require.NoError(t, err)
	assert.Equal(t, orderedIDs[:2], firstPage)
	assert.True(t, hasMore)

	allIDs, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{folderID},
		nil,
		"",
		len(orderedIDs),
	)
	require.NoError(t, err)
	assert.Equal(t, orderedIDs, allIDs)
	assert.False(t, hasMore)
}

func TestListActiveKnowledgeIDsByFolderIDsUsesStableAfterIDCursor(t *testing.T) {
	db, repository := setupActiveKnowledgeIDTestRepository(t)
	folderID := knowledgeFolderTestID("stable-cursor")
	orderedIDs := []string{
		activeKnowledgeIDTestID(1),
		activeKnowledgeIDTestID(2),
		activeKnowledgeIDTestID(3),
		activeKnowledgeIDTestID(4),
	}
	for _, id := range orderedIDs {
		insertActiveKnowledgeIDTestRow(
			t, db, id, 1, "kb-1", folderID, "enabled", nil,
		)
	}

	firstPage, firstHasMore, err :=
		repository.ListActiveKnowledgeIDsByFolderIDs(
			context.Background(),
			1,
			"kb-1",
			[]string{folderID},
			nil,
			"",
			2,
		)
	require.NoError(t, err)
	require.Equal(t, orderedIDs[:2], firstPage)
	require.True(t, firstHasMore)

	secondPage, secondHasMore, err :=
		repository.ListActiveKnowledgeIDsByFolderIDs(
			context.Background(),
			1,
			"kb-1",
			[]string{folderID},
			nil,
			firstPage[len(firstPage)-1],
			2,
		)
	require.NoError(t, err)
	assert.Equal(t, orderedIDs[2:], secondPage)
	assert.False(t, secondHasMore)
}

func TestListActiveKnowledgeIDsByFolderIDsRejectsEmptyFolderScope(t *testing.T) {
	_, repository := setupActiveKnowledgeIDTestRepository(t)

	for _, testCase := range []struct {
		name      string
		folderIDs []string
	}{
		{name: "nil", folderIDs: nil},
		{name: "empty", folderIDs: []string{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ids, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
				context.Background(),
				1,
				"kb-1",
				testCase.folderIDs,
				nil,
				"",
				10,
			)

			require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
			assert.Empty(t, ids)
			assert.False(t, hasMore)
		})
	}
}

func TestListActiveKnowledgeIDsByFolderIDsPreservesEmptyStringRoot(t *testing.T) {
	db, repository := setupActiveKnowledgeIDTestRepository(t)
	rootID := activeKnowledgeIDTestID(1)
	insertActiveKnowledgeIDTestRow(
		t, db, rootID, 1, "kb-1", "", "enabled", nil,
	)

	ids, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{""},
		nil,
		"",
		1,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{rootID}, ids)
	assert.False(t, hasMore)
}

func TestListActiveKnowledgeIDsByFolderIDsRejectsInvalidRequest(t *testing.T) {
	_, repository := setupActiveKnowledgeIDTestRepository(t)
	validFolderIDs := []string{knowledgeFolderTestID("valid")}

	for _, testCase := range []struct {
		name      string
		tenantID  uint64
		kbID      string
		folderIDs []string
		limit     int
	}{
		{
			name:      "tenant",
			tenantID:  0,
			kbID:      "kb-1",
			folderIDs: validFolderIDs,
			limit:     10,
		},
		{
			name:      "knowledge base",
			tenantID:  1,
			kbID:      "",
			folderIDs: validFolderIDs,
			limit:     10,
		},
		{
			name:      "zero limit",
			tenantID:  1,
			kbID:      "kb-1",
			folderIDs: validFolderIDs,
			limit:     0,
		},
		{
			name:      "negative limit",
			tenantID:  1,
			kbID:      "kb-1",
			folderIDs: validFolderIDs,
			limit:     -1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ids, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
				context.Background(),
				testCase.tenantID,
				testCase.kbID,
				testCase.folderIDs,
				nil,
				"",
				testCase.limit,
			)

			require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
			assert.Empty(t, ids)
			assert.False(t, hasMore)
		})
	}
}

func TestListActiveKnowledgeIDsByFolderIDsReturnsEmptyWithoutBroadening(
	t *testing.T,
) {
	db, repository := setupActiveKnowledgeIDTestRepository(t)
	insertActiveKnowledgeIDTestRow(
		t,
		db,
		activeKnowledgeIDTestID(1),
		1,
		"kb-1",
		knowledgeFolderTestID("outside-empty"),
		"enabled",
		nil,
	)

	ids, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{knowledgeFolderTestID("no-matches")},
		nil,
		"",
		10,
	)

	require.NoError(t, err)
	assert.Empty(t, ids)
	assert.False(t, hasMore)
}

func TestListActiveKnowledgeIDsByFolderIDsPreservesContextCancellation(
	t *testing.T,
) {
	db, repository := setupActiveKnowledgeIDTestRepository(t)
	folderID := knowledgeFolderTestID("canceled-query")
	insertActiveKnowledgeIDTestRow(
		t,
		db,
		activeKnowledgeIDTestID(1),
		1,
		"kb-1",
		folderID,
		"enabled",
		nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ids, hasMore, err := repository.ListActiveKnowledgeIDsByFolderIDs(
		ctx,
		1,
		"kb-1",
		[]string{folderID},
		nil,
		"",
		10,
	)

	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.Empty(t, ids)
	assert.False(t, hasMore)
}

func setupActiveKnowledgeIDTestRepository(
	t *testing.T,
) (*gorm.DB, interfaces.KnowledgeRepository) {
	t.Helper()
	db := setupKnowledgeFolderTestDB(t)
	require.NoError(
		t,
		db.Exec(`
			ALTER TABLE knowledges
			ADD COLUMN enable_status VARCHAR(50) NOT NULL DEFAULT 'enabled'
		`).Error,
	)
	return db, NewKnowledgeRepository(db)
}

func insertActiveKnowledgeIDTestRow(
	t *testing.T,
	db *gorm.DB,
	id string,
	tenantID uint64,
	kbID string,
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
				folder_id,
				enable_status,
				deleted_at
			)
			VALUES (?, ?, ?, ?, ?, ?)
		`, id, tenantID, kbID, folderID, enableStatus, deletedAt).Error,
	)
}

func activeKnowledgeIDTestID(sequence int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", sequence)
}

const knowledgeFolderScopeSubtreeQueryPattern = `(?s)WITH RECURSIVE\s+selected_roots\(id, path\) AS`

func newKnowledgeFolderScopeSQLMockReader(
	t *testing.T,
) (*knowledgeFolderScopeReader, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(
		postgres.New(postgres.Config{Conn: sqlDB}),
		&gorm.Config{},
	)
	require.NoError(t, err)
	return newTestKnowledgeFolderScopeReader(
		db,
		context.Background(),
		1,
		"kb-1",
	), mock
}

func newTestKnowledgeFolderScopeReader(
	db *gorm.DB,
	ctx context.Context,
	sourceTenantID uint64,
	knowledgeBaseID string,
) *knowledgeFolderScopeReader {
	return &knowledgeFolderScopeReader{
		db:              db,
		ctx:             ctx,
		sourceTenantID:  sourceTenantID,
		knowledgeBaseID: knowledgeBaseID,
	}
}

func knowledgeFolderScopeRoots(
	folders ...*types.KnowledgeFolder,
) []interfaces.KnowledgeFolderScopeRoot {
	roots := make([]interfaces.KnowledgeFolderScopeRoot, len(folders))
	for index, folder := range folders {
		roots[index] = interfaces.KnowledgeFolderScopeRoot{
			ID:   folder.ID,
			Path: folder.Path,
		}
	}
	return roots
}

type knowledgeFolderScopeUnsupportedDialector struct {
	gorm.Dialector
}

func (knowledgeFolderScopeUnsupportedDialector) Name() string {
	return "unsupported"
}
