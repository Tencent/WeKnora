package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type callerScopeShares struct {
	interfaces.KBShareService
	callers []uint64
}

func (s *callerScopeShares) CheckTenantKBPermission(
	_ context.Context,
	kbID string,
	caller uint64,
	_ types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	s.callers = append(s.callers, caller)
	// Tenant 2 has access to a third tenant's KB; caller 1 does not.
	return types.OrgRoleViewer, kbID == "onward" && caller == 2, nil
}

func TestSharedExecutionFiltersDocumentsChunksAndSearchScope(t *testing.T) {
	shares := &callerScopeShares{}
	svc, db := newKnowledgeSharedAccessService(t, shares)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	kbs := []*types.KnowledgeBase{
		{ID: "own", TenantID: 1},
		{ID: "granted", TenantID: 2},
		{ID: "private", TenantID: 2},
		{ID: "onward", TenantID: 3},
	}
	var ids []string
	for _, kb := range kbs {
		ids = append(ids, kb.ID)
		seedKnowledge(t, db, &types.Knowledge{ID: kb.ID, TenantID: kb.TenantID, KnowledgeBaseID: kb.ID, Type: "file"})
		require.NoError(
			t,
			db.Create(
				&types.Chunk{
					ID:              kb.ID,
					TenantID:        kb.TenantID,
					KnowledgeBaseID: kb.ID,
					KnowledgeID:     kb.ID,
					Content:         kb.ID,
				},
			).Error,
		)
	}
	search := &knowledgeBaseService{
		kgRepo:         svc.repo,
		chunkRepo:      repository.NewChunkRepository(db),
		kbShareService: shares,
	}
	kbService := &suggestionKBService{kbs: make(map[string]*types.KnowledgeBase)}
	for _, kb := range kbs {
		kbService.kbs[kb.ID] = kb
	}
	session := &sessionService{knowledgeBaseService: kbService, knowledgeService: svc, kbShareService: shares}
	base := newSharedAccessContext()
	grant := &access.KBAccess{
		KnowledgeBase:     kbs[1],
		Caller:            types.CallerFromContext(base),
		EffectiveTenantID: 2,
		Permission:        types.OrgRoleViewer,
	}
	for _, execution := range []uint64{1, 2, 3} {
		ctx := logger.CloneContext(types.WithExecutionTenant(grant.Context(base), execution))
		rows, err := svc.GetKnowledgeBatchWithSharedAccess(ctx, execution, ids)
		require.NoError(t, err)
		got := make([]string, 0, len(rows))
		for _, row := range rows {
			got = append(got, row.ID)
		}
		require.ElementsMatch(t, []string{"own", "granted"}, got)
		knowledgeMap, err := search.fetchKnowledgeDataWithShared(ctx, execution, ids)
		require.NoError(t, err)
		require.Len(t, knowledgeMap, 2)
		require.NotNil(t, knowledgeMap["own"])
		require.NotNil(t, knowledgeMap["granted"])
		chunks, err := search.listChunksByIDWithShared(ctx, execution, ids)
		require.NoError(t, err)
		got = got[:0]
		for _, chunk := range chunks {
			got = append(got, chunk.ID)
		}
		require.ElementsMatch(t, []string{"own", "granted"}, got)
		require.NoError(t, search.authorizeKBAccess(ctx, kbs[:2]))
		require.Error(t, search.authorizeKBAccess(ctx, kbs[2:3]), "execution tenant is not ownership")
		require.Error(t, search.authorizeKBAccess(ctx, kbs[3:]), "source tenant shares are not inherited")
		targets, err := session.buildSearchTargets(
			ctx,
			execution,
			ids,
			nil,
			[]types.TagScope{{KnowledgeBaseID: "private", TagIDs: []string{"tag"}}},
		)
		require.NoError(t, err)
		require.Len(t, targets, 2, "denied KBs must not become search or tag targets")
		got = got[:0]
		for _, target := range targets {
			got = append(got, target.KnowledgeBaseID)
		}
		require.ElementsMatch(t, []string{"own", "granted"}, got)
		_, err = resolveKBReadTenant(ctx, kbs[1], shares)
		require.NoError(t, err, "FAQ/tag reads accept the exact upstream grant")
		_, err = resolveKBReadTenant(ctx, kbs[2], shares)
		require.Error(t, err, "FAQ/tag reads cannot access another KB in the execution tenant")
	}
	for _, caller := range shares.callers {
		require.Equal(t, uint64(1), caller)
	}
}

type callerScopeKBList struct {
	interfaces.KnowledgeBaseService
	tenant uint64
}

func (s *callerScopeKBList) ListKnowledgeBases(ctx context.Context) ([]*types.KnowledgeBase, error) {
	s.tenant = types.MustTenantIDFromContext(ctx)
	return []*types.KnowledgeBase{{ID: "own", TenantID: s.tenant, Type: types.KnowledgeBaseTypeDocument}}, nil
}

type callerScopeSearchRepo struct {
	interfaces.KnowledgeRepository
	scopes []types.KnowledgeSearchScope
}

func (r *callerScopeSearchRepo) SearchKnowledgeInScopes(
	_ context.Context,
	scopes []types.KnowledgeSearchScope,
	_ string,
	_, _ int,
	_ []string,
) ([]*types.Knowledge, bool, int64, error) {
	r.scopes = scopes
	return nil, false, 0, nil
}

func TestDocumentSearchListsCallerTenantAfterExecutionSwitch(t *testing.T) {
	kbs := &callerScopeKBList{}
	repo := &callerScopeSearchRepo{}
	svc := &knowledgeService{kbService: kbs, repo: repo}
	ctx := types.WithExecutionTenant(newSharedAccessContext(), 2)
	_, _, _, err := svc.SearchKnowledge(ctx, "query", 0, 10, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), kbs.tenant)
	require.Equal(t, []types.KnowledgeSearchScope{{TenantID: 1, KBID: "own"}}, repo.scopes)
	require.Equal(t, uint64(2), types.MustTenantIDFromContext(ctx), "search must not mutate the parent context")
}

func TestSharedAgentBatchUsesGrantInsteadOfSourceTenantOwnership(t *testing.T) {
	svc, db := newKnowledgeSharedAccessService(t, &callerScopeShares{})
	for _, kbID := range []string{"selected", "private"} {
		seedKnowledge(t, db, &types.Knowledge{ID: kbID, TenantID: 2, KnowledgeBaseID: kbID, Type: "file"})
	}
	agent := &types.CustomAgent{
		TenantID: 2,
		Config:   types.CustomAgentConfig{KBSelectionMode: "selected", KnowledgeBases: []string{"selected"}},
	}
	ctx := logger.CloneContext(access.WithSharedAgent(newSharedAccessContext(), agent))
	rows, err := svc.GetKnowledgeBatchWithSharedAccess(ctx, 2, []string{"selected", "private"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "selected", rows[0].ID)
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"private"}})
	search := &knowledgeBaseService{}
	app, ok := apperrors.IsAppError(
		search.authorizeKBAccess(ctx, []*types.KnowledgeBase{{ID: "selected", TenantID: 2}}),
	)
	require.True(t, ok)
	require.Equal(t, apperrors.ErrForbidden, app.Code, "API-key scope denial must not become a lookup failure")
	rows, err = svc.GetKnowledgeBatchWithSharedAccess(ctx, 2, []string{"selected", "private"})
	require.NoError(t, err)
	require.Empty(t, rows)
}
