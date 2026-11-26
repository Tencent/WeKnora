package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestReaderDownloadFileRequiresExactGrantAndDocument(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 7}
	caller := types.Caller{TenantID: 7, UserID: "reader", Role: types.TenantRoleViewer}
	ctx := types.WithCaller(context.Background(), caller)
	grant, err := access.ResolveKB(ctx, access.KBRequest{Caller: caller}, kb, types.OrgRoleViewer, nil, nil)
	require.NoError(t, err)
	doc := &types.Knowledge{
		ID: "doc", Type: types.KnowledgeTypeManual, Title: "Manual", KnowledgeBaseID: kb.ID, TenantID: kb.TenantID,
	}
	s := &knowledgeService{repo: &metadataUpdateKnowledgeRepo{knowledge: doc}}
	_, _, err = s.GetReaderDownloadFile(ctx, kb.ID, doc.ID)
	require.ErrorIs(t, err, access.ErrForbidden)
	ctx = grant.Context(ctx)
	file, _, err := s.GetReaderDownloadFile(ctx, kb.ID, doc.ID)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	_, _, err = s.GetReaderDownloadFile(ctx, "unlisted", doc.ID)
	require.ErrorIs(t, err, access.ErrForbidden)
	doc.ParseStatus = "draft"
	_, _, err = s.GetReaderDownloadFile(ctx, kb.ID, doc.ID)
	require.ErrorIs(t, err, repository.ErrKnowledgeNotFound)
	doc.ParseStatus = "completed"
	doc.KnowledgeBaseID = "moved"
	_, _, err = s.GetReaderDownloadFile(ctx, kb.ID, doc.ID)
	require.ErrorIs(t, err, repository.ErrKnowledgeNotFound)
}
