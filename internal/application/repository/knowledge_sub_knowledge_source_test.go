package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func subKnowledgeTestRepo(t *testing.T) *knowledgeRepository {
	t.Helper()
	db, e := gorm.Open(
		sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared&_foreign_keys=on"),
		&gorm.Config{},
	)
	require.NoError(t, e)
	sqlDB, e := db.DB()
	require.NoError(t, e)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	ddl, e := os.ReadFile("../../../migrations/sqlite/000053_knowledge_sub_knowledge.up.sql")
	require.NoError(t, e)
	require.NoError(t, db.Exec(string(ddl)).Error)
	require.NoError(
		t,
		db.Exec(
			`CREATE TABLE chunks(knowledge_id TEXT, tenant_id BIGINT,
			 image_info TEXT, deleted_at DATETIME, is_enabled BOOLEAN DEFAULT 1)`,
		).Error,
	)
	return &knowledgeRepository{db: db}
}

func subKnowledgeTestKnowledge(
	t *testing.T,
	tenant uint64,
	kb, kind, id string,
	order int64,
) (*types.Knowledge, *types.SubKnowledgeMapping) {
	t.Helper()
	k := &types.Knowledge{
		TenantID:        tenant,
		KnowledgeBaseID: kb,
		Type:            types.KnowledgeTypeManual,
		Title:           id,
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
	}
	require.NoError(t, k.SetManualMetadata(types.NewManualKnowledgeMetadata("正文 "+id, "publish", 1)))
	f := &types.SubKnowledgeMapping{
		SubKnowledgeInput: types.SubKnowledgeInput{
			SourceNamespace: "taptap",
			RootID:          "P100",
			NodeType:        kind,
			SourceID:        id,
			SortOrder:       order,
		},
	}
	f.SourceHash = f.ContentHash(k.Title, "正文 "+id, "publish")
	return k, f
}

func TestSubKnowledgeDuplicateIsAtomicAndScoped(t *testing.T) {
	r := subKnowledgeTestRepo(t)
	ctx := context.Background()
	k, f := subKnowledgeTestKnowledge(t, 1, "a", "root", "P100", 0)
	first, isNew, e := r.CreateSubKnowledge(ctx, k, f)
	require.NoError(t, e)
	require.True(t, isNew)
	k2, f2 := subKnowledgeTestKnowledge(t, 1, "a", "root", "P100", 0)
	again, isNew, e := r.CreateSubKnowledge(ctx, k2, f2)
	require.NoError(t, e)
	require.False(t, isNew)
	require.Equal(t, first.ID, again.ID)
	k3, f3 := subKnowledgeTestKnowledge(t, 1, "a", "root", "P100", 0)
	f3.SourceHash = "different"
	_, _, e = r.CreateSubKnowledge(ctx, k3, f3)
	require.ErrorIs(t, e, ErrSubKnowledgeConflict)
	var n int64
	require.NoError(t, r.db.Model(&types.Knowledge{}).Count(&n).Error)
	require.EqualValues(t, 1, n, "failed duplicate must roll back its Knowledge")
	for _, scope := range []struct {
		tenant uint64
		kb     string
	}{{1, "b"}, {2, "a"}} {
		other, source := subKnowledgeTestKnowledge(t, scope.tenant, scope.kb, "root", "P100", 0)
		_, isNew, e = r.CreateSubKnowledge(ctx, other, source)
		require.NoError(t, e)
		require.True(t, isNew)
	}
	page, e := r.ListSubKnowledge(ctx, 1, "a", "taptap", "P100", "", 10)
	require.NoError(t, e)
	require.EqualValues(t, 1, page.Total)
	_, e = r.GetSubKnowledgeMapping(ctx, 2, first.ID)
	require.ErrorIs(t, e, gorm.ErrRecordNotFound)
}

func TestSubKnowledgeTagFailureRollsBackBothRows(t *testing.T) {
	r := subKnowledgeTestRepo(t)
	k, f := subKnowledgeTestKnowledge(t, 1, "a", "root", "P100", 0)
	// This fixture intentionally has no tag relation table: the failure must
	// not leave a pending Knowledge that future identical retries just reuse.
	_, _, e := r.CreateSubKnowledge(context.Background(), k, f, "tag")
	require.Error(t, e)
	for _, model := range []any{&types.Knowledge{}, &types.SubKnowledgeMapping{}} {
		var count int64
		require.NoError(t, r.db.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestSubKnowledgeChildBeforeRootAndCursorSnapshot(t *testing.T) {
	r := subKnowledgeTestRepo(t)
	ctx := context.Background()
	child, f := subKnowledgeTestKnowledge(t, 1, "a", "child", "C2", 2)
	f.ReferenceID = "C1"
	_, _, e := r.CreateSubKnowledge(ctx, child, f)
	require.NoError(t, e)
	page, e := r.ListSubKnowledge(ctx, 1, "a", "taptap", "P100", "", 1)
	require.NoError(t, e)
	require.False(t, page.HasRoot)
	for _, item := range []struct {
		kind, id string
		order    int64
	}{{"root", "P100", 0}, {"child", "C1", 1}} {
		k, src := subKnowledgeTestKnowledge(t, 1, "a", item.kind, item.id, item.order)
		_, _, e = r.CreateSubKnowledge(ctx, k, src)
		require.NoError(t, e)
	}
	page, e = r.ListSubKnowledge(ctx, 1, "a", "taptap", "P100", "", 1)
	require.NoError(t, e)
	require.True(t, page.HasRoot)
	require.True(t, page.HasMore)
	require.Equal(t, "P100", page.Items[0].SourceID)
	// A late backfill with an earlier sort order is excluded by the cursor's
	// import cutoff, rather than pushing an old item into the next page twice.
	later, src := subKnowledgeTestKnowledge(t, 1, "a", "child", "C0", 0)
	src.CreatedAt = page.SnapshotAt.Add(time.Second)
	_, _, e = r.CreateSubKnowledge(ctx, later, src)
	require.NoError(t, e)
	next, e := r.ListSubKnowledge(ctx, 1, "a", "taptap", "P100", page.NextCursor, 1)
	require.NoError(t, e)
	require.EqualValues(t, 3, next.Total)
	require.Equal(t, "C1", next.Items[0].SourceID)
	last, e := r.ListSubKnowledge(ctx, 1, "a", "taptap", "P100", next.NextCursor, 1)
	require.NoError(t, e)
	require.Equal(t, "C2", last.Items[0].SourceID)
	require.False(t, last.HasMore)
	require.Equal(t, "C1", last.Items[0].ReferenceID)
	_, e = r.ListSubKnowledge(ctx, 1, "b", "taptap", "P100", page.NextCursor, 1)
	require.ErrorIs(t, e, ErrInvalidSubKnowledgeCursor)
	_, e = r.ListSubKnowledge(ctx, 1, "a", "taptap", "P100", "not a cursor", 1)
	require.ErrorIs(t, e, ErrInvalidSubKnowledgeCursor)
}

func TestSubKnowledgeUpdateMoveDeleteAndClone(t *testing.T) {
	r := subKnowledgeTestRepo(t)
	ctx := context.Background()
	k, f := subKnowledgeTestKnowledge(t, 1, "a", "root", "P100", 0)
	_, _, e := r.CreateSubKnowledge(ctx, k, f)
	require.NoError(t, e)
	meta := types.NewManualKnowledgeMetadata("修改后的完整正文", "publish", 2)
	require.NoError(t, k.SetManualMetadata(meta))
	require.NoError(t, r.UpdateSubKnowledge(ctx, k, nil, "new-hash"))
	saved, e := r.GetSubKnowledgeMapping(ctx, 1, k.ID)
	require.NoError(t, e)
	require.Equal(t, "new-hash", saved.SourceHash)
	bad := f.SubKnowledgeInput
	bad.RootID = "P200"
	bad.SourceID = "P200"
	require.ErrorIs(t, r.UpdateSubKnowledge(ctx, k, &bad, "bad"), ErrSubKnowledgeIdentityChange)
	clone := *k
	clone.ID = ""
	require.NoError(t, r.CreateKnowledge(ctx, &clone))
	_, e = r.GetSubKnowledgeMapping(ctx, 1, clone.ID)
	require.ErrorIs(t, e, gorm.ErrRecordNotFound)
	// The migration trigger follows transfers, even through generic writes.
	require.NoError(t, r.db.Model(&types.Knowledge{}).Where("id = ?", k.ID).Update("knowledge_base_id", "b").Error)
	saved, e = r.GetSubKnowledgeMapping(ctx, 1, k.ID)
	require.NoError(t, e)
	require.Equal(t, "b", saved.KnowledgeBaseID)
	old, e := r.ListSubKnowledge(ctx, 1, "a", "taptap", "P100", "", 10)
	require.NoError(t, e)
	require.Empty(t, old.Items)
	require.NoError(t, r.DeleteKnowledge(ctx, 1, k.ID))
	_, e = r.GetSubKnowledgeMapping(ctx, 1, k.ID)
	require.ErrorIs(t, e, gorm.ErrRecordNotFound)
	replacement, source := subKnowledgeTestKnowledge(t, 1, "b", "root", "P100", 0)
	_, _, e = r.CreateSubKnowledge(ctx, replacement, source)
	require.NoError(t, e)
	require.NoError(t, r.HardDeleteKnowledge(ctx, 1, replacement.ID))
	var n int64
	require.NoError(
		t,
		r.db.Unscoped().Model(&types.SubKnowledgeMapping{}).Where("knowledge_id = ?", replacement.ID).Count(&n).Error,
	)
	require.Zero(t, n)
}

func TestSubKnowledgeReadFiltersDraftDeletingAndDeletedImages(t *testing.T) {
	r := subKnowledgeTestRepo(t)
	ctx := context.Background()
	var published *types.Knowledge
	for i, status := range []string{"completed", "draft", "deleting"} {
		k, f := subKnowledgeTestKnowledge(t, 1, "a", "child", string(rune('a'+i)), int64(i))
		k.ParseStatus = status
		_, _, e := r.CreateSubKnowledge(ctx, k, f)
		require.NoError(t, e)
		if i == 0 {
			published = k
		}
	}
	require.NoError(
		t,
		r.db.Exec(
			`INSERT INTO chunks(knowledge_id,tenant_id,image_info,deleted_at) VALUES
			 (?,1,'[{"url":"local://tenant/image.jpg"}]',NULL),
			 (?,1,'[{"url":"local://tenant/image.jpg"}]',NULL),
			 (?,1,'[{"url":"https://deleted.invalid/image.jpg"}]',CURRENT_TIMESTAMP)`,
			published.ID,
			published.ID,
			published.ID,
		).Error,
	)
	require.NoError(
		t,
		r.db.Exec(
			`INSERT INTO chunks(knowledge_id,tenant_id,image_info,is_enabled)
			 VALUES (?,1,'[{"url":"https://disabled.invalid/image.jpg"}]',0)`,
			published.ID,
		).Error,
	)
	page, e := r.ListSubKnowledge(ctx, 1, "a", "taptap", "P100", "", 10)
	require.NoError(t, e)
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, "正文 a", page.Items[0].Content)
	require.Len(t, page.Items[0].Images, 1)
	require.Equal(t, 1, page.Items[0].ContentVersion)
	// EnableStatus controls retrieval, not the KB read grant. The original
	// content remains readable through the same policy as normal preview.
	require.NoError(t, r.db.Model(published).Update("enable_status", "disabled").Error)
	page, e = r.ListSubKnowledge(ctx, 1, "a", "taptap", "P100", "", 10)
	require.NoError(t, e)
	require.Equal(t, "正文 a", page.Items[0].Content)
	require.Equal(t, "disabled", page.Items[0].EnableStatus)
}

func TestSubKnowledgeCorruptMetadataDoesNotDropHealthyItems(t *testing.T) {
	r := subKnowledgeTestRepo(t)
	ctx := context.Background()
	k, f := subKnowledgeTestKnowledge(t, 1, "a", "root", "P100", 0)
	_, _, err := r.CreateSubKnowledge(ctx, k, f)
	require.NoError(t, err)
	broken := &types.Knowledge{
		TenantID:        1,
		KnowledgeBaseID: "a",
		Type:            types.KnowledgeTypeManual,
		Title:           "bad stored data",
		ParseStatus:     types.ParseStatusCompleted,
		Metadata:        types.JSON(`{"content":123}`),
	}
	require.NoError(t, r.CreateKnowledge(ctx, broken))
	input := types.SubKnowledgeInput{SourceNamespace: "taptap", RootID: "P100", SourceID: "bad", NodeType: "child"}
	require.NoError(
		t,
		r.SetSubKnowledge(ctx, 1, broken.ID, "a", &input),
		"association itself does not depend on valid text metadata",
	)
	// Invalid raw JSON can occur in legacy SQLite TEXT storage. Read it per
	// item so its sql.Scanner cannot abort the entire page.
	require.NoError(t, r.db.Model(broken).Update("metadata", types.JSON("not-json")).Error)
	require.NoError(
		t,
		r.db.Exec(
			`INSERT INTO chunks(knowledge_id,tenant_id,image_info)
			 VALUES (?,1,'not-json'),(?,1,'[{"url":"https://valid.invalid/a.jpg"}]')`,
			k.ID,
			k.ID,
		).Error,
	)
	page, err := r.ListSubKnowledge(ctx, 1, "a", "taptap", "P100", "", 10)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.Equal(t, "正文 P100", page.Items[0].Content)
	require.True(t, page.Items[0].ContentAvailable)
	require.Len(t, page.Items[0].Images, 1)
	require.Equal(t, "PARTIAL_METADATA_UNAVAILABLE", page.Items[0].ImagesStatus)
	require.False(t, page.Items[1].ContentAvailable)
	require.Equal(t, "unavailable", page.Items[1].ContentSource)
}

func TestSubKnowledgeGenericAssociationAndConflictRecovery(t *testing.T) {
	r := subKnowledgeTestRepo(t)
	ctx := context.Background()
	root, src := subKnowledgeTestKnowledge(t, 1, "a", "root", "doc-root", 0)
	src.RootID, src.ReferenceID = "collection-42", "upstream-object"
	_, _, err := r.CreateSubKnowledge(ctx, root, src)
	require.NoError(t, err, "a group ID need not equal its root source ID")

	// One external source ID cannot create a second Knowledge by changing
	// node_type; a second root with another source ID also conflicts.
	for _, tc := range []struct{ kind, id string }{{"child", "doc-root"}, {"root", "another-root"}} {
		k, f := subKnowledgeTestKnowledge(t, 1, "a", tc.kind, tc.id, 0)
		f.RootID = "collection-42"
		_, _, err = r.CreateSubKnowledge(ctx, k, f)
		var conflict *SubKnowledgeConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, root.ID, conflict.KnowledgeID)
	}
	for _, kind := range []string{"file", "url"} {
		k := &types.Knowledge{
			TenantID:        1,
			KnowledgeBaseID: "a",
			Type:            kind,
			Title:           kind,
			ParseStatus:     types.ParseStatusCompleted,
		}
		require.NoError(t, r.CreateKnowledge(ctx, k))
		input := types.SubKnowledgeInput{
			SourceNamespace: "taptap",
			RootID:          "collection-42",
			NodeType:        "child",
			SourceID:        kind,
			ParentID:        "doc-root",
			ReferenceID:     "not-yet-imported",
		}
		require.NoError(t, r.SetSubKnowledge(ctx, 1, k.ID, "a", &input))
		input.SortOrder = 5
		input.ParentID, input.ReferenceID = "parent-later", "reference-later"
		require.NoError(t, r.SetSubKnowledge(ctx, 1, k.ID, "a", &input), "relation-only update")
		saved, e := r.GetSubKnowledgeMapping(ctx, 1, k.ID)
		require.NoError(t, e)
		require.Equal(t, input.ParentID, saved.ParentID)
		require.Equal(t, input.ReferenceID, saved.ReferenceID)
		changed := input
		changed.RootID = "another-collection"
		require.ErrorIs(t, r.SetSubKnowledge(ctx, 1, k.ID, "a", &changed), ErrSubKnowledgeIdentityChange)
		changed = input
		changed.NodeType, changed.ParentID = "root", ""
		require.ErrorIs(t, r.SetSubKnowledge(ctx, 1, k.ID, "a", &changed), ErrSubKnowledgeIdentityChange)
		require.ErrorIs(t, r.SetSubKnowledge(ctx, 1, k.ID, "other", &input), gorm.ErrRecordNotFound)
		input.SourceID = "different-source"
		require.ErrorIs(t, r.SetSubKnowledge(ctx, 1, k.ID, "a", &input), ErrSubKnowledgeIdentityChange)
	}
	page, err := r.ListSubKnowledge(ctx, 1, "a", "taptap", "collection-42", "", 10)
	require.NoError(t, err)
	require.EqualValues(t, 3, page.Total)
	require.True(t, page.HasRoot)
	require.Equal(t, "doc-root", page.Items[0].SourceID)
	for _, item := range page.Items[1:] {
		require.False(t, item.ContentAvailable, "a file link is not parsed full text")
		require.Equal(t, "original_resource_link", item.ContentSource)
		require.Equal(t, "reference-later", item.ReferenceID)
	}

	// Attaching an identity already in use must produce the same typed 409 as
	// create, with no content mutation or partially saved source mapping.
	unmapped, _ := subKnowledgeTestKnowledge(t, 1, "a", "child", "unmapped", 0)
	require.NoError(t, r.CreateKnowledge(ctx, unmapped))
	input := types.SubKnowledgeInput{
		SourceNamespace: "taptap",
		RootID:          "collection-42",
		SourceID:        "file",
		NodeType:        "child",
	}
	err = r.SetSubKnowledge(ctx, 1, unmapped.ID, "a", &input)
	require.ErrorIs(t, err, ErrSubKnowledgeConflict)
	_, err = r.GetSubKnowledgeMapping(ctx, 1, unmapped.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	err = r.UpdateSubKnowledge(ctx, unmapped, &input, "input-hash")
	require.ErrorIs(t, err, ErrSubKnowledgeConflict)
	input.SourceID, input.NodeType = "second-root", "root"
	err = r.SetSubKnowledge(ctx, 1, unmapped.ID, "a", &input)
	require.ErrorIs(t, err, ErrSubKnowledgeConflict)
}
