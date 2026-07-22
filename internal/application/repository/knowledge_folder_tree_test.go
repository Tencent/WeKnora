package repository

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeFolderRepository_ListByIDsAndSubtree(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderRepository(db)
	ctx := context.Background()

	for _, folder := range []*types.KnowledgeFolder{
		knowledgeFolderFixture("root-a", 1, "kb-1", "", "A", "/root-a/", 1),
		knowledgeFolderFixture("child-a", 1, "kb-1", "root-a", "Child", "/root-a/child-a/", 2),
		knowledgeFolderFixture("grandchild-a", 1, "kb-1", "child-a", "Grandchild", "/root-a/child-a/grandchild-a/", 3),
		knowledgeFolderFixture("root-b", 1, "kb-1", "", "B", "/root-b/", 1),
		knowledgeFolderFixture("other-tenant", 2, "kb-1", "root-a", "Other", "/root-a/other-tenant/", 2),
		knowledgeFolderFixture("other-kb", 1, "kb-2", "root-a", "Other KB", "/root-a/other-kb/", 2),
	} {
		require.NoError(t, db.Create(folder).Error)
	}
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("id = ?", knowledgeFolderTestID("grandchild-a")).
		Update("deleted_at", time.Now().UTC()).Error)

	byIDs, err := repo.ListByIDs(
		ctx,
		1,
		"kb-1",
		[]string{
			knowledgeFolderTestID("child-a"),
			knowledgeFolderTestID("root-a"),
			knowledgeFolderTestID("other-tenant"),
			knowledgeFolderTestID("other-kb"),
			knowledgeFolderTestID("grandchild-a"),
		},
	)
	require.NoError(t, err)
	require.Len(t, byIDs, 2)
	assert.ElementsMatch(t, []string{
		knowledgeFolderTestID("root-a"),
		knowledgeFolderTestID("child-a"),
	}, knowledgeFolderIDs(byIDs))

	empty, err := repo.ListByIDs(ctx, 1, "kb-1", nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	subtree, err := repo.ListSubtreeFolders(
		ctx,
		1,
		"kb-1",
		knowledgeFolderTestID("root-a"),
		knowledgeFolderTestPath("/root-a/"),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		knowledgeFolderTestID("root-a"),
		knowledgeFolderTestID("child-a"),
	}, knowledgeFolderIDs(subtree))

	all, err := repo.ListSubtreeFolders(ctx, 1, "kb-1", "", "")
	require.NoError(t, err)
	rootIDs := []string{knowledgeFolderTestID("root-a"), knowledgeFolderTestID("root-b")}
	sort.Strings(rootIDs)
	assert.Equal(t, append(rootIDs, knowledgeFolderTestID("child-a")), knowledgeFolderIDs(all))
}

func TestKnowledgeFolderRepository_PathPrefixTreatsLikeCharactersLiterally(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderRepository(db)
	ctx := context.Background()

	for _, folder := range []*types.KnowledgeFolder{
		rawKnowledgeFolderFixture("percent%", 1, "kb-1", "", "Percent", "/percent%/", 1),
		rawKnowledgeFolderFixture(
			"percent-child",
			1,
			"kb-1",
			"percent%",
			"Percent child",
			"/percent%/percent-child/",
			2,
		),
		rawKnowledgeFolderFixture("percent-sibling", 1, "kb-1", "", "Percent sibling", "/percentX/", 8),
		rawKnowledgeFolderFixture("under_score", 1, "kb-1", "", "Underscore", "/under_score/", 1),
		rawKnowledgeFolderFixture(
			"underscore-child",
			1,
			"kb-1",
			"under_score",
			"Underscore child",
			"/under_score/underscore-child/",
			2,
		),
		rawKnowledgeFolderFixture("underscore-sibling", 1, "kb-1", "", "Underscore sibling", "/underXscore/", 9),
	} {
		require.NoError(t, db.Create(folder).Error)
	}

	percentSubtree, err := repo.ListSubtreeFolders(ctx, 1, "kb-1", "percent%", "/percent%/")
	require.NoError(t, err)
	assert.Equal(t, []string{"percent%", "percent-child"}, knowledgeFolderIDs(percentSubtree))

	underscoreSubtree, err := repo.ListSubtreeFolders(
		ctx,
		1,
		"kb-1",
		"under_score",
		"/under_score/",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"under_score", "underscore-child"}, knowledgeFolderIDs(underscoreSubtree))

	assert.Equal(t, `a!!b!\c!%d!_e`, escapeKnowledgeFolderLikeLiteral(`a!b\c%d_e`))
}

func TestKnowledgeFolderRepository_ListSubtreeFoldersIncludesBothBoundaryMismatchDirections(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderRepository(db)
	ctx := context.Background()

	rootID := knowledgeFolderTestID("root")
	reachableID := knowledgeFolderTestID("reachable-outside-path")
	impostorID := knowledgeFolderTestID("path-impostor")
	outsideParentID := knowledgeFolderTestID("outside-parent")
	for _, folder := range []*types.KnowledgeFolder{
		knowledgeFolderFixture("root", 1, "kb-1", "", "Root", "/root/", 1),
		knowledgeFolderFixture("outside-parent", 1, "kb-1", "", "Outside", "/outside-parent/", 1),
		rawKnowledgeFolderFixture(
			reachableID,
			1,
			"kb-1",
			rootID,
			"Reachable",
			knowledgeFolderTestPath("/outside-parent/reachable-outside-path/"),
			2,
		),
		rawKnowledgeFolderFixture(
			impostorID,
			1,
			"kb-1",
			outsideParentID,
			"Impostor",
			knowledgeFolderTestPath("/root/path-impostor/"),
			2,
		),
	} {
		require.NoError(t, db.Create(folder).Error)
	}

	folders, err := repo.ListSubtreeFolders(
		ctx,
		1,
		"kb-1",
		rootID,
		knowledgeFolderTestPath("/root/"),
	)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{rootID, reachableID, impostorID}, knowledgeFolderIDs(folders))
}

func TestKnowledgeFolderRepository_MoveSubtreeTreatsLikeCharactersLiterally(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderRepository(db)
	ctx := context.Background()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES (?, ?)`,
		"kb-1",
		1,
	).Error)

	for _, folder := range []*types.KnowledgeFolder{
		rawKnowledgeFolderFixture("literal%_", 1, "kb-1", "", "Literal", "/literal%_/", 1),
		rawKnowledgeFolderFixture(
			"literal-child",
			1,
			"kb-1",
			"literal%_",
			"Child",
			"/literal%_/literal-child/",
			2,
		),
		rawKnowledgeFolderFixture("percent-match", 1, "kb-1", "", "Percent match", "/literalX_/", 7),
		rawKnowledgeFolderFixture("underscore-match", 1, "kb-1", "", "Underscore match", "/literal%X/", 8),
		rawKnowledgeFolderFixture("target", 1, "kb-1", "", "Target", "/target/", 1),
	} {
		require.NoError(t, db.Create(folder).Error)
	}

	err := repo.RunTreeWriteTransaction(ctx, 1, "kb-1", func(
		txRepo interfaces.KnowledgeFolderTreeRepository,
	) error {
		return txRepo.MoveSubtree(
			ctx,
			1,
			"kb-1",
			interfaces.KnowledgeFolderMoveSubtreeParams{
				FolderID:            "literal%_",
				ExpectedParentID:    "",
				ExpectedPath:        "/literal%_/",
				ExpectedDepth:       1,
				ExpectedFolderCount: 2,
				NewParentID:         "target",
				NewPath:             "/target/literal%_/",
				NewName:             "Moved",
				DepthDelta:          1,
			},
		)
	})
	require.NoError(t, err)

	moved, err := repo.GetByID(ctx, 1, "kb-1", "literal%_")
	require.NoError(t, err)
	assert.Equal(t, "/target/literal%_/", moved.Path)
	assert.Equal(t, 2, moved.Depth)
	movedChild, err := repo.GetByID(ctx, 1, "kb-1", "literal-child")
	require.NoError(t, err)
	assert.Equal(t, "/target/literal%_/literal-child/", movedChild.Path)
	assert.Equal(t, 3, movedChild.Depth)

	percentMatch, err := repo.GetByID(ctx, 1, "kb-1", "percent-match")
	require.NoError(t, err)
	assert.Equal(t, "/literalX_/", percentMatch.Path)
	assert.Equal(t, 7, percentMatch.Depth)
	underscoreMatch, err := repo.GetByID(ctx, 1, "kb-1", "underscore-match")
	require.NoError(t, err)
	assert.Equal(t, "/literal%X/", underscoreMatch.Path)
	assert.Equal(t, 8, underscoreMatch.Depth)
}

func TestKnowledgeFolderRepository_UpdateAttributesDoesNotChangeTreeFields(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderTreeRepository(db)
	ctx := context.Background()

	folder := knowledgeFolderFixture("folder", 1, "kb-1", "", "Before", "/folder/", 1)
	require.NoError(t, db.Create(folder).Error)
	name := "After"
	sortOrder := 17
	require.NoError(t, repo.UpdateFolderAttributes(
		ctx,
		1,
		"kb-1",
		folder.ID,
		&name,
		&sortOrder,
	))

	got, err := repo.GetByID(ctx, 1, "kb-1", folder.ID)
	require.NoError(t, err)
	assert.Equal(t, "After", got.Name)
	assert.Equal(t, 17, got.SortOrder)
	assert.Equal(t, "", got.ParentID)
	assert.Equal(t, knowledgeFolderTestPath("/folder/"), got.Path)
	assert.Equal(t, 1, got.Depth)

	err = repo.UpdateFolderAttributes(ctx, 2, "kb-1", folder.ID, &name, nil)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	err = repo.UpdateFolderAttributes(ctx, 1, "kb-2", folder.ID, &name, nil)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeFolderRepository_MoveSubtreeUpdatesActiveRowsAtomically(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := newKnowledgeFolderRepository(db)
	ctx := context.Background()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES (?, ?)`,
		"kb-1",
		1,
	).Error)

	for _, folder := range []*types.KnowledgeFolder{
		knowledgeFolderFixture("source", 1, "kb-1", "", "Source", "/source/", 1),
		knowledgeFolderFixture("child", 1, "kb-1", "source", "Child", "/source/child/", 2),
		knowledgeFolderFixture("grandchild", 1, "kb-1", "child", "Grandchild", "/source/child/grandchild/", 3),
		knowledgeFolderFixture("deleted-child", 1, "kb-1", "source", "Deleted", "/source/deleted-child/", 2),
		knowledgeFolderFixture("target", 1, "kb-1", "", "Target", "/target/", 1),
		knowledgeFolderFixture("other-tenant", 2, "kb-1", "source", "Other", "/source/other-tenant/", 2),
	} {
		require.NoError(t, db.Create(folder).Error)
	}
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("id = ?", knowledgeFolderTestID("deleted-child")).
		Update("deleted_at", time.Now().UTC()).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id)
		VALUES ('knowledge-child', 1, 'kb-1', ?)
	`, knowledgeFolderTestID("child")).Error)

	err := repo.RunTreeWriteTransaction(ctx, 1, "kb-1", func(
		txRepo interfaces.KnowledgeFolderTreeRepository,
	) error {
		return txRepo.MoveSubtree(
			ctx,
			1,
			"kb-1",
			interfaces.KnowledgeFolderMoveSubtreeParams{
				FolderID:            knowledgeFolderTestID("source"),
				ExpectedParentID:    "",
				ExpectedPath:        knowledgeFolderTestPath("/source/"),
				ExpectedDepth:       1,
				ExpectedFolderCount: 3,
				NewParentID:         knowledgeFolderTestID("target"),
				NewPath:             knowledgeFolderTestPath("/target/source/"),
				NewName:             "Renamed",
				NewSortOrder:        9,
				DepthDelta:          1,
			},
		)
	})
	require.NoError(t, err)

	expected := map[string]struct {
		parentID string
		name     string
		path     string
		depth    int
	}{
		knowledgeFolderTestID("source"): {
			parentID: knowledgeFolderTestID("target"),
			name:     "Renamed",
			path:     knowledgeFolderTestPath("/target/source/"),
			depth:    2,
		},
		knowledgeFolderTestID("child"): {
			parentID: knowledgeFolderTestID("source"),
			name:     "Child",
			path:     knowledgeFolderTestPath("/target/source/child/"),
			depth:    3,
		},
		knowledgeFolderTestID("grandchild"): {
			parentID: knowledgeFolderTestID("child"),
			name:     "Grandchild",
			path:     knowledgeFolderTestPath("/target/source/child/grandchild/"),
			depth:    4,
		},
	}
	for id, want := range expected {
		got, getErr := repo.GetByID(ctx, 1, "kb-1", id)
		require.NoError(t, getErr)
		assert.Equal(t, want.parentID, got.ParentID, id)
		assert.Equal(t, want.name, got.Name, id)
		assert.Equal(t, want.path, got.Path, id)
		assert.Equal(t, want.depth, got.Depth, id)
	}

	var deleted types.KnowledgeFolder
	require.NoError(t, db.Unscoped().
		Where("id = ?", knowledgeFolderTestID("deleted-child")).
		First(&deleted).Error)
	assert.Equal(t, knowledgeFolderTestPath("/source/deleted-child/"), deleted.Path)
	assert.Equal(t, 2, deleted.Depth)

	var knowledgeFolderID string
	require.NoError(t, db.Raw(
		`SELECT folder_id FROM knowledges WHERE id = ?`,
		"knowledge-child",
	).Scan(&knowledgeFolderID).Error)
	assert.Equal(t, knowledgeFolderTestID("child"), knowledgeFolderID)

	otherTenant, err := repo.GetByID(ctx, 2, "kb-1", knowledgeFolderTestID("other-tenant"))
	require.NoError(t, err)
	assert.Equal(t, knowledgeFolderTestPath("/source/other-tenant/"), otherTenant.Path)
}

func TestKnowledgeFolderRepository_MoveSubtreeExpectedStateMismatchRollsBack(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*interfaces.KnowledgeFolderMoveSubtreeParams)
	}{
		{
			name: "stale parent",
			mutate: func(params *interfaces.KnowledgeFolderMoveSubtreeParams) {
				params.ExpectedParentID = knowledgeFolderTestID("stale-parent")
			},
		},
		{
			name: "stale path",
			mutate: func(params *interfaces.KnowledgeFolderMoveSubtreeParams) {
				params.ExpectedPath = knowledgeFolderTestPath("/stale/")
			},
		},
		{
			name: "stale depth",
			mutate: func(params *interfaces.KnowledgeFolderMoveSubtreeParams) {
				params.ExpectedDepth = 2
			},
		},
		{
			name: "subtree count mismatch",
			mutate: func(params *interfaces.KnowledgeFolderMoveSubtreeParams) {
				params.ExpectedFolderCount = 3
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupKnowledgeFolderTestDB(t)
			repo := newKnowledgeFolderRepository(db)
			ctx := context.Background()
			require.NoError(t, db.Exec(
				`INSERT INTO knowledge_bases (id, tenant_id) VALUES (?, ?)`,
				"kb-1",
				1,
			).Error)
			for _, folder := range []*types.KnowledgeFolder{
				knowledgeFolderFixture("source", 1, "kb-1", "", "Source", "/source/", 1),
				knowledgeFolderFixture("child", 1, "kb-1", "source", "Child", "/source/child/", 2),
				knowledgeFolderFixture("target", 1, "kb-1", "", "Target", "/target/", 1),
			} {
				require.NoError(t, db.Create(folder).Error)
			}

			params := interfaces.KnowledgeFolderMoveSubtreeParams{
				FolderID:            knowledgeFolderTestID("source"),
				ExpectedParentID:    "",
				ExpectedPath:        knowledgeFolderTestPath("/source/"),
				ExpectedDepth:       1,
				ExpectedFolderCount: 2,
				NewParentID:         knowledgeFolderTestID("target"),
				NewPath:             knowledgeFolderTestPath("/target/source/"),
				NewName:             "Moved",
				NewSortOrder:        9,
				DepthDelta:          1,
			}
			tt.mutate(&params)
			err := repo.RunTreeWriteTransaction(ctx, 1, "kb-1", func(
				txRepo interfaces.KnowledgeFolderTreeRepository,
			) error {
				return txRepo.MoveSubtree(ctx, 1, "kb-1", params)
			})
			require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)

			source, getErr := repo.GetByID(
				ctx,
				1,
				"kb-1",
				knowledgeFolderTestID("source"),
			)
			require.NoError(t, getErr)
			assert.Equal(t, "", source.ParentID)
			assert.Equal(t, "Source", source.Name)
			assert.Equal(t, knowledgeFolderTestPath("/source/"), source.Path)
			assert.Equal(t, 1, source.Depth)
			assert.Zero(t, source.SortOrder)

			child, getErr := repo.GetByID(
				ctx,
				1,
				"kb-1",
				knowledgeFolderTestID("child"),
			)
			require.NoError(t, getErr)
			assert.Equal(t, knowledgeFolderTestPath("/source/child/"), child.Path)
			assert.Equal(t, 2, child.Depth)
		})
	}
}
