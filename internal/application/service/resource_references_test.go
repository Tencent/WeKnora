package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestKBFileBindingRequiresLiveKnowledgeInExactKB(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}, &types.Knowledge{}, &types.Chunk{}, &types.WikiPage{}))
	ctx := context.Background()
	ref, err := catalog.Register(ctx, 7, "local://7/exports/image.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: "kb", TenantID: 7}).Error)
	knowledge := &types.Knowledge{ID: "doc", TenantID: 7, KnowledgeBaseID: "kb", Type: "file"}
	require.NoError(t, db.Create(knowledge).Error)
	require.NoError(
		t,
		catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, "doc", types.ResourceRelationExtractedImage),
	)
	lookup := catalog.(interfaces.KBResourceLookup)
	for _, reference := range []string{ref, "local://7/exports/image.png"} {
		ok, err := lookup.IsReferencedByKnowledgeBase(ctx, 7, "kb", reference)
		require.NoError(t, err)
		require.True(t, ok)
		ok, err = lookup.IsReferencedByKnowledgeBase(ctx, 7, "other-kb", reference)
		require.NoError(t, err)
		require.False(t, ok)
	}
	require.NoError(t, db.Delete(knowledge).Error)
	ok, err := lookup.IsReferencedByKnowledgeBase(ctx, 7, "kb", ref)
	require.NoError(t, err)
	require.False(t, ok, "a stale binding cannot authorize a deleted document")
}

func TestKBFileLegacyTextReferencesDoNotAuthorizeFiles(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}, &types.Knowledge{}, &types.Chunk{}, &types.WikiPage{}))
	lookup := catalog.(interfaces.KBResourceLookup)
	const reference = "local://7/exports/image.png"
	chunk := &types.Chunk{
		ID:              "chunk",
		TenantID:        7,
		KnowledgeBaseID: "kb",
		Content:         "![image](" + reference + ".private)",
	}
	require.NoError(t, db.Create(chunk).Error)
	ok, err := lookup.IsReferencedByKnowledgeBase(context.Background(), 7, "kb", reference)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, db.Model(chunk).Update("image_info", `[{"url":"`+reference+`"}]`).Error)
	ok, err = lookup.IsReferencedByKnowledgeBase(context.Background(), 7, "kb", reference)
	require.NoError(t, err)
	require.False(t, ok, "text is not an ownership binding")
	require.NoError(t, db.Delete(chunk).Error)
	ok, err = lookup.IsReferencedByKnowledgeBase(context.Background(), 7, "kb", reference)
	require.NoError(t, err)
	require.False(t, ok)
	page := &types.WikiPage{ID: "page", TenantID: 7, KnowledgeBaseID: "kb", Content: "![image](" + reference + ")"}
	require.NoError(t, db.Create(page).Error)
	ok, err = lookup.IsReferencedByKnowledgeBase(context.Background(), 7, "kb", reference)
	require.NoError(t, err)
	require.False(t, ok, "text is not an ownership binding")
	ok, err = lookup.IsReferencedByKnowledgeBase(context.Background(), 8, "kb", reference)
	require.NoError(t, err)
	require.False(t, ok)
}
