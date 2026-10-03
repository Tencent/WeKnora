package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type libraryMessageRepo struct {
	interfaces.MessageRepository
	query *types.ArtifactLibraryQuery
	items []*types.ArtifactLibraryItem
}

func (r *libraryMessageRepo) ListArtifactLibrary(
	_ context.Context, q *types.ArtifactLibraryQuery,
) ([]*types.ArtifactLibraryItem, int64, error) {
	r.query = q
	return r.items, int64(len(r.items)), nil
}

func TestListArtifactLibraryScopesToCallerAndNormalizesQuery(t *testing.T) {
	handle := "resource://dHZ_fFslfs0GgJGaJZGjGA"
	repo := &libraryMessageRepo{items: []*types.ArtifactLibraryItem{
		{FileName: "deck.pptx", URL: handle},
		{FileName: "raw.csv", URL: "local://tenant/raw.csv"},
	}}
	s := &messageService{messageRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")

	result, err := s.ListArtifactLibrary(ctx, &types.ArtifactLibraryQuery{
		TenantID:  99,
		UserID:    "mallory",
		FileTypes: []string{"PDF", " .pptx ", "pdf", ""},
		PageSize:  5000,
	})
	require.NoError(t, err)

	require.Equal(t, uint64(7), repo.query.TenantID, "tenant comes from the context, not the request")
	require.Equal(t, "alice", repo.query.UserID, "owner comes from the context, not the request")
	require.Equal(t, []string{".pdf", ".pptx"}, repo.query.FileTypes)
	require.Equal(t, 1, repo.query.Page)
	require.Equal(t, artifactLibraryMaxPageSize, repo.query.PageSize)
	require.Equal(t, artifactLibraryMaxPageSize, result.PageSize)

	items := result.Data.([]*types.ArtifactLibraryItem)
	require.Equal(t, handle, items[0].Handle)
	require.Empty(t, items[1].Handle, "non-resource URLs never leak as handles")
}
