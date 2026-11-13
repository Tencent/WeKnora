package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func nativeTreeRepo(t *testing.T) *knowledgeRepository {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared&_foreign_keys=on"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	require.NoError(t, db.Migrator().DropColumn(&types.Knowledge{}, "ParentKnowledgeID"))
	ddl, err := os.ReadFile("../../../migrations/sqlite/000053_native_knowledge_parent.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(ddl)).Error)
	require.NoError(
		t,
		db.Exec(
			`CREATE TABLE knowledge_bases(id TEXT PRIMARY KEY,tenant_id BIGINT,deleted_at DATETIME);
			 INSERT INTO knowledge_bases VALUES ('a',1,NULL),('b',1,NULL),('c',2,NULL)`,
		).Error,
	)
	return &knowledgeRepository{db: db}
}

func nativeNode(t *testing.T, r *knowledgeRepository, kb, parent, title string) *types.Knowledge {
	t.Helper()
	k := &types.Knowledge{
		TenantID:          1,
		KnowledgeBaseID:   kb,
		Title:             title,
		Type:              types.KnowledgeTypeManual,
		ParentKnowledgeID: parent,
		ParseStatus:       types.ParseStatusCompleted,
		EnableStatus:      "enabled",
	}
	require.NoError(t, k.SetManualMetadata(types.NewManualKnowledgeMetadata("body "+title, "publish", 1)))
	require.NoError(t, r.CreateKnowledge(context.Background(), k))
	return k
}

func TestNativeKnowledgeParentCreateDepthAndIsolation(t *testing.T) {
	r := nativeTreeRepo(t)
	ctx := context.Background()
	root := nativeNode(t, r, "a", "", "root")
	child := nativeNode(t, r, "a", root.ID, "child")
	grand := nativeNode(t, r, "a", child.ID, "grand")
	roots := []types.KnowledgeTreeRoot{{TenantID: 1, KBID: "a", KnowledgeID: root.ID}}
	for _, tc := range []struct{ depth, want int }{{0, 0}, {1, 1}, {2, 2}, {-1, 2}} {
		rows, err := r.KnowledgeChildren(ctx, roots, types.KnowledgeChildrenOptions{Depth: tc.depth, Limit: 20})
		require.NoError(t, err)
		require.Len(t, rows, tc.want)
		if tc.want > 0 {
			require.Equal(t, child.ID, rows[0].KnowledgeID)
			require.Equal(t, root.ID, rows[0].ParentKnowledgeID)
			require.Equal(t, 1, rows[0].Depth)
			require.True(t, rows[0].ContentAvailable)
		}
		if tc.want > 1 {
			require.Equal(t, grand.ID, rows[1].KnowledgeID)
			require.Equal(t, 2, rows[1].Depth)
		}
	}
	_, err := r.KnowledgeChildren(ctx, roots, types.KnowledgeChildrenOptions{Depth: -1, Limit: 1})
	require.ErrorContains(t, err, "children_limit_exceeded")
	for _, k := range []*types.Knowledge{
		{TenantID: 1, KnowledgeBaseID: "b", ParentKnowledgeID: root.ID},
		{TenantID: 2, KnowledgeBaseID: "c", ParentKnowledgeID: root.ID},
		{TenantID: 1, KnowledgeBaseID: "a", ParentKnowledgeID: "missing"},
	} {
		require.Error(t, r.CreateKnowledge(ctx, k))
		require.Empty(t, k.ID, "failed parent validation must happen before insert")
	}
	// A stale background save must not clear the authoritative relation.
	child.ParentKnowledgeID = ""
	child.Description = "updated"
	require.NoError(t, r.UpdateKnowledge(ctx, child))
	saved, err := r.GetKnowledgeByID(ctx, 1, child.ID)
	require.NoError(t, err)
	require.Equal(t, root.ID, saved.ParentKnowledgeID)
	require.Error(t, r.CheckKnowledgeTreeMove(ctx, 1, root.ID, "b"))
	require.Error(t, r.db.Model(root).Update("knowledge_base_id", "b").Error)
	// DB guard also rejects reparenting into a descendant, independently of APIs.
	require.Error(t, r.db.Model(root).Update("parent_knowledge_id", grand.ID).Error)
}

func TestNativeKnowledgeDeleteDetachAndCascade(t *testing.T) {
	r := nativeTreeRepo(t)
	ctx := context.Background()
	root := nativeNode(t, r, "a", "", "root")
	child := nativeNode(t, r, "a", root.ID, "child")
	grand := nativeNode(t, r, "a", child.ID, "grand")
	// Non-cascade delete only detaches direct children. Their own subtree stays.
	require.NoError(t, r.DeleteKnowledge(ctx, 1, root.ID))
	saved, err := r.GetKnowledgeByID(ctx, 1, child.ID)
	require.NoError(t, err)
	require.Empty(t, saved.ParentKnowledgeID)
	saved, err = r.GetKnowledgeByID(ctx, 1, grand.ID)
	require.NoError(t, err)
	require.Equal(t, child.ID, saved.ParentKnowledgeID)
	ids, err := r.PrepareKnowledgeCascade(ctx, 1, "a", []string{child.ID}, false)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{child.ID, grand.ID}, ids)
	other := nativeNode(t, r, "b", "", "other scope")
	_, err = r.PrepareKnowledgeCascade(ctx, 1, "a", []string{child.ID, other.ID}, true)
	require.Error(t, err)
	saved, err = r.GetKnowledgeByID(ctx, 1, child.ID)
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusCompleted, saved.ParseStatus, "invalid cascade must not mark a partial tree")
	late := nativeNode(t, r, "a", grand.ID, "late")
	ids, err = r.PrepareKnowledgeCascade(ctx, 1, "a", ids, true)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{child.ID, grand.ID, late.ID}, ids)
	for _, id := range ids {
		k, e := r.GetKnowledgeByID(ctx, 1, id)
		require.NoError(t, e)
		require.Equal(t, types.ParseStatusDeleting, k.ParseStatus)
	}
	require.Error(
		t,
		r.db.Model(&types.Knowledge{}).
			Where("id = ?", late.ID).
			Update("parse_status", types.ParseStatusCompleted).
			Error,
		"stale parser cannot reopen a deleting parent",
	)
	require.Error(
		t,
		r.CreateKnowledge(ctx, &types.Knowledge{TenantID: 1, KnowledgeBaseID: "a", ParentKnowledgeID: late.ID}),
	)
	require.NoError(t, r.DeleteKnowledgeList(ctx, 1, ids))
	for _, id := range ids {
		_, e := r.GetKnowledgeByID(ctx, 1, id)
		require.Error(t, e)
	}
	ids, err = r.PrepareKnowledgeCascade(ctx, 1, "a", ids, true)
	require.NoError(t, err)
	require.Empty(t, ids, "worker retry after successful deletion")
}

func TestNativeKnowledgeMaximumDepth(t *testing.T) {
	r := nativeTreeRepo(t)
	node := nativeNode(t, r, "a", "", "root")
	for i := 0; i < types.MaxKnowledgeDepth; i++ {
		node = nativeNode(t, r, "a", node.ID, "child")
	}
	require.Error(
		t,
		r.CreateKnowledge(
			context.Background(),
			&types.Knowledge{TenantID: 1, KnowledgeBaseID: "a", ParentKnowledgeID: node.ID},
		),
	)
}

func TestNativeKnowledgeParentAndTagsRollbackTogether(t *testing.T) {
	r := nativeTreeRepo(t)
	root := nativeNode(t, r, "a", "", "root")
	child := &types.Knowledge{TenantID: 1, KnowledgeBaseID: "a", ParentKnowledgeID: root.ID, Title: "child"}
	err := r.CreateKnowledgeWithParent(context.Background(), child, "missing-tag")
	require.Error(t, err, "fixture has no tag relation table")
	var count int64
	require.NoError(t, r.db.Model(&types.Knowledge{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestNativeKnowledgeVisibilityBudgetAndDeletedKB(t *testing.T) {
	r := nativeTreeRepo(t)
	ctx := context.Background()
	root := nativeNode(t, r, "a", "", "root")
	draft := nativeNode(t, r, "a", root.ID, "draft")
	require.NoError(t, r.db.Model(draft).Update("parse_status", types.ManualKnowledgeStatusDraft).Error)
	_ = nativeNode(t, r, "a", draft.ID, "hidden behind draft")
	large := nativeNode(t, r, "a", root.ID, "large")
	require.NoError(
		t,
		large.SetManualMetadata(types.NewManualKnowledgeMetadata(strings.Repeat("中文", 100000), "publish", 1)),
	)
	require.NoError(t, r.UpdateKnowledge(ctx, large))
	rows, err := r.KnowledgeChildren(
		ctx,
		[]types.KnowledgeTreeRoot{{TenantID: 1, KBID: "a", KnowledgeID: root.ID}},
		types.KnowledgeChildrenOptions{Depth: -1, Limit: 20},
	)
	require.NoError(t, err)
	require.Len(t, rows, 1, "draft branches must not expose descendants")
	require.Equal(t, large.ID, rows[0].KnowledgeID)
	require.True(t, rows[0].ContentTruncated)
	require.True(t, utf8.ValidString(rows[0].Content))
	require.LessOrEqual(t, len(rows[0].Content), 512*1024)
	require.Empty(t, rows[0].Metadata)
	require.NoError(t, r.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='a'").Error)
	_, err = r.PrepareKnowledgeCascade(ctx, 1, "a", []string{root.ID}, true)
	require.Error(t, err)
	require.Error(
		t,
		r.CreateKnowledge(ctx, &types.Knowledge{TenantID: 1, KnowledgeBaseID: "a", ParentKnowledgeID: root.ID}),
	)
}

func TestNativeKnowledgeUnpublishedAnchorDoesNotExpand(t *testing.T) {
	for _, status := range []string{types.ManualKnowledgeStatusDraft, types.ParseStatusDeleting} {
		t.Run(status, func(t *testing.T) {
			r := nativeTreeRepo(t)
			ctx := context.Background()
			root := nativeNode(t, r, "a", "", "root")
			child := nativeNode(t, r, "a", root.ID, "published child")
			require.NoError(t, r.db.Model(root).Update("parse_status", status).Error)
			rows, err := r.KnowledgeChildren(
				ctx,
				[]types.KnowledgeTreeRoot{{TenantID: 1, KBID: "a", KnowledgeID: root.ID}},
				types.KnowledgeChildrenOptions{Depth: 1, Limit: 200},
			)
			require.Error(t, err)
			require.Empty(t, rows)
			ids, err := r.PrepareKnowledgeCascade(ctx, 1, "a", []string{root.ID}, false)
			require.NoError(t, err)
			require.ElementsMatch(t, []string{root.ID, child.ID}, ids)
		})
	}
}

func TestNativeKnowledgeFailedDeleteCanRecoverWithoutReopening(t *testing.T) {
	r := nativeTreeRepo(t)
	ctx := context.Background()
	root := nativeNode(t, r, "a", "", "root")
	ids, err := r.PrepareKnowledgeCascade(ctx, 1, "a", []string{root.ID}, true)
	require.NoError(t, err)
	// Same transition used by the existing housekeeping/dead-letter recovery.
	require.NoError(
		t,
		r.db.Model(root).
			Updates(map[string]any{
				"parse_status":  types.ParseStatusFailed,
				"error_message": "delete task stranded, recovered by housekeeping",
			}).
			Error,
	)
	before, err := r.GetKnowledgeByID(ctx, 1, root.ID)
	require.NoError(t, err)
	replacement := *before
	replacement.ID = uuid.NewString()
	require.Error(t, r.ReplaceKnowledge(ctx, before, &replacement, nil), "replacement cannot bypass a pending delete")
	after := *before
	after.ParseStatus = types.ParseStatusCompleted
	err = r.UpdateKnowledgeForTransfer(ctx, before, &after)
	var appErr *apperrors.AppError
	require.True(t, errors.As(err, &appErr))
	require.Equal(t, 409, appErr.HTTPCode)
	require.Error(
		t,
		r.CreateKnowledge(ctx, &types.Knowledge{TenantID: 1, KnowledgeBaseID: "a", ParentKnowledgeID: root.ID}),
	)
	_, err = r.PrepareKnowledgeCascade(ctx, 1, "a", ids, true)
	require.NoError(t, err, "failed deletion can be retried")
	require.NoError(t, r.DeleteKnowledgeList(ctx, 1, ids))
}

func TestNativeKnowledgeReplacementPreservesTreeOrRollsBack(t *testing.T) {
	for _, failTags := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[failTags], func(t *testing.T) {
			r := nativeTreeRepo(t)
			ctx := context.Background()
			root := nativeNode(t, r, "a", "", "root")
			before := nativeNode(t, r, "a", root.ID, "source")
			child := nativeNode(t, r, "a", before.ID, "child")
			grand := nativeNode(t, r, "a", child.ID, "grand")
			after := *before
			after.ID = uuid.NewString()
			after.ParentKnowledgeID = ""
			var tags []string
			if failTags {
				tags = []string{"missing-tag"}
			}
			err := r.ReplaceKnowledge(ctx, before, &after, tags)
			if failTags {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, root.ID, after.ParentKnowledgeID)
			}
			saved, err := r.GetKnowledgeByID(ctx, 1, child.ID)
			require.NoError(t, err)
			if failTags {
				require.Equal(t, before.ID, saved.ParentKnowledgeID)
				_, err = r.GetKnowledgeByID(ctx, 1, before.ID)
				require.NoError(t, err)
				_, err = r.GetKnowledgeByID(ctx, 1, after.ID)
				require.Error(t, err)
			} else {
				require.Equal(t, after.ID, saved.ParentKnowledgeID)
				_, err = r.GetKnowledgeByID(ctx, 1, before.ID)
				require.Error(t, err)
			}
			saved, err = r.GetKnowledgeByID(ctx, 1, grand.ID)
			require.NoError(t, err)
			require.Equal(t, child.ID, saved.ParentKnowledgeID)
		})
	}
}

func TestNativeKnowledgeMoveClaimSerializesChildCreation(t *testing.T) {
	for _, lateChild := range []bool{true, false} {
		t.Run(map[bool]string{true: "child wins", false: "move wins"}[lateChild], func(t *testing.T) {
			r := nativeTreeRepo(t)
			ctx := context.Background()
			root := nativeNode(t, r, "a", "", "root")
			before, err := r.GetKnowledgeByID(ctx, 1, root.ID)
			require.NoError(t, err)
			if lateChild {
				_ = nativeNode(t, r, "a", root.ID, "late child")
			}
			after := *before
			after.ParseStatus = types.ParseStatusProcessing
			after.Metadata = types.JSON(`{"_knowledge_transfer":{"operation":"move","phase":"moving"}}`)
			err = r.UpdateKnowledgeForTransfer(ctx, before, &after)
			if lateChild {
				require.Error(t, err)
				saved, e := r.GetKnowledgeByID(ctx, 1, root.ID)
				require.NoError(t, e)
				require.Equal(t, types.ParseStatusCompleted, saved.ParseStatus)
			} else {
				require.NoError(t, err)
				require.Error(t, r.CreateKnowledge(ctx, &types.Knowledge{
					TenantID: 1, KnowledgeBaseID: "a", ParentKnowledgeID: root.ID,
				}))
			}
		})
	}
}

func TestNativeKnowledgeMovingReadIsRejected(t *testing.T) {
	for _, movingChild := range []bool{false, true} {
		t.Run(map[bool]string{false: "root", true: "child"}[movingChild], func(t *testing.T) {
			r := nativeTreeRepo(t)
			root := nativeNode(t, r, "a", "", "root")
			child := nativeNode(t, r, "a", root.ID, "child")
			id := root.ID
			if movingChild {
				id = child.ID
			}
			// Simulate a legacy/in-flight move predating native tree admission.
			require.NoError(
				t,
				r.db.Model(&types.Knowledge{}).
					Where("id = ?", id).
					Update("metadata", types.JSON(`{"_knowledge_transfer":{"operation":"move","phase":"moving"}}`)).
					Error,
			)
			rows, err := r.KnowledgeChildren(
				context.Background(),
				[]types.KnowledgeTreeRoot{{TenantID: 1, KBID: "a", KnowledgeID: root.ID}},
				types.KnowledgeChildrenOptions{Depth: 2, Limit: 200},
			)
			require.ErrorContains(t, err, "unfinished move")
			require.Empty(t, rows)
		})
	}
}

func TestNativeKnowledgeMigrationDown(t *testing.T) {
	r := nativeTreeRepo(t)
	ddl, err := os.ReadFile("../../../migrations/sqlite/000053_native_knowledge_parent.down.sql")
	require.NoError(t, err)
	require.NoError(t, r.db.Exec(string(ddl)).Error)
	require.False(t, r.db.Migrator().HasColumn("knowledges", "parent_knowledge_id"))
	require.False(t, r.db.Migrator().HasColumn("knowledges", "deletion_pending"))
}
