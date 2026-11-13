package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type scopedRerankTreeRepo struct {
	interfaces.KnowledgeRepository
	rows  []*types.Knowledge
	roots []types.KnowledgeTreeRoot
}

type rerankTreeKBRepo struct {
	interfaces.KnowledgeBaseRepository
	rows []*types.KnowledgeBase
}

func (r *rerankTreeKBRepo) GetKnowledgeBaseByIDs(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return r.rows, nil
}

func (r *scopedRerankTreeRepo) GetKnowledgeBatch(
	_ context.Context, tenant uint64, _ []string,
) ([]*types.Knowledge, error) {
	var rows []*types.Knowledge
	for _, k := range r.rows {
		if k.TenantID == tenant {
			rows = append(rows, k)
		}
	}
	return rows, nil
}

func (r *scopedRerankTreeRepo) GetKnowledgeBatchByIDOnly(context.Context, []string) ([]*types.Knowledge, error) {
	return r.rows, nil
}

func (r *scopedRerankTreeRepo) KnowledgeRerankPassages(
	_ context.Context, roots []types.KnowledgeTreeRoot, _ types.KnowledgeChildrenOptions,
) ([]types.KnowledgeTreePassage, *types.KnowledgeTreeDiagnostics, error) {
	r.roots = roots
	return nil, &types.KnowledgeTreeDiagnostics{}, nil
}

func TestNativeKnowledgeSearchExpansionUsesVerifiedTargets(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		repo := &scopedRerankTreeRepo{rows: []*types.Knowledge{
			{ID: "primary", TenantID: 1, KnowledgeBaseID: "kb-a"},
			{ID: "secondary", TenantID: 8, KnowledgeBaseID: "kb-b"},
			{ID: "outside", TenantID: 1, KnowledgeBaseID: "not-requested"},
		}}
		svc := &knowledgeBaseService{
			kgRepo: repo, kbShareService: &fakeKBShareService{allowedKBs: map[string]bool{"kb-b": true}},
		}
		ctx := searchKnowledgeCtx()
		if restricted {
			ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"kb-a"}})
		}
		hits := []*types.SearchResult{
			{ID: "first", KnowledgeID: "primary", Score: .93},
			{ID: "second", KnowledgeID: "secondary", Score: .8},
			{ID: "bad", KnowledgeID: "outside", Score: .5},
		}
		targets := types.SearchTargets{
			&types.SearchTarget{TenantID: 1, KnowledgeBaseID: "kb-a"},
			&types.SearchTarget{TenantID: 8, KnowledgeBaseID: "kb-b"},
		}
		got, _, err := svc.ExpandKnowledgeRerankCandidates(
			ctx, targets, hits, types.KnowledgeChildrenOptions{Depth: 2, Limit: 20},
		)
		require.NoError(t, err)
		want := []types.KnowledgeTreeRoot{{TenantID: 1, KBID: "kb-a", KnowledgeID: "primary"}}
		if !restricted {
			want = append(want, types.KnowledgeTreeRoot{TenantID: 8, KBID: "kb-b", KnowledgeID: "secondary"})
		}
		require.Equal(t, want, repo.roots)
		require.Equal(t, hits, got)
		require.Equal(t, .93, hits[0].Score)
	}
}

func TestNativeKnowledgeHybridTreeUsesAuthorizedMultiKBOwners(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		repo := &scopedRerankTreeRepo{rows: []*types.Knowledge{
			{ID: "primary", TenantID: 1, KnowledgeBaseID: "kb-a"},
			{ID: "shared", TenantID: 8, KnowledgeBaseID: "kb-b"},
		}}
		svc := &knowledgeBaseService{
			repo: &rerankTreeKBRepo{rows: []*types.KnowledgeBase{
				{ID: "kb-a", TenantID: 1}, {ID: "kb-b", TenantID: 8},
			}},
			kgRepo: repo, kbShareService: &fakeKBShareService{allowedKBs: map[string]bool{"kb-b": true}},
		}
		ctx := searchKnowledgeCtx()
		if restricted {
			ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"kb-a"}})
		}
		_, _, err := svc.hybridRerankTree(ctx, "kb-a", types.SearchParams{
			ChildrenDepth: 1, KnowledgeBaseIDs: []string{"kb-a", "kb-b"},
		}, []*types.SearchResult{{KnowledgeID: "primary"}, {KnowledgeID: "shared"}})
		if restricted {
			require.Error(t, err)
			require.Empty(t, repo.roots)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, []types.KnowledgeTreeRoot{
			{TenantID: 1, KBID: "kb-a", KnowledgeID: "primary"},
			{TenantID: 8, KBID: "kb-b", KnowledgeID: "shared"},
		}, repo.roots)
	}
}
