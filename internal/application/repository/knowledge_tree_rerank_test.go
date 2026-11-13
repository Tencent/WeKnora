package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func treePassage(t *testing.T, r *knowledgeRepository, k *types.Knowledge, index int) *types.Chunk {
	t.Helper()
	c := &types.Chunk{
		ID: uuid.NewString(), TenantID: k.TenantID, KnowledgeBaseID: k.KnowledgeBaseID,
		KnowledgeID: k.ID, Content: fmt.Sprintf("%s passage %d", k.Title, index),
		ChunkType: types.ChunkTypeText, IsEnabled: true, IndexStatus: "ready", ChunkIndex: index,
		SeqID: int64(index + 1),
	}
	// SeqID is global; these tests concern native chunk identity, not FAQ numbers.
	require.NoError(t, r.db.Omit("SeqID").Create(c).Error)
	return c
}

func TestNativeKnowledgeRerankParentSiblingsDepthAndLimit(t *testing.T) {
	r := nativeTreeRepo(t)
	require.NoError(t, r.db.AutoMigrate(&types.Chunk{}))
	p := nativeNode(t, r, "a", "", "parent")
	c := nativeNode(t, r, "a", p.ID, "hit")
	s := nativeNode(t, r, "a", p.ID, "sibling")
	g := nativeNode(t, r, "a", s.ID, "grandchild")
	for _, k := range []*types.Knowledge{p, c, s, g} {
		treePassage(t, r, k, 0)
	}
	second := treePassage(t, r, s, 1)
	roots := []types.KnowledgeTreeRoot{{TenantID: 1, KBID: "a", KnowledgeID: c.ID}}
	for _, tc := range []struct{ depth, limit, wantKnowledge, wantPassages int }{
		{0, 20, 0, 0}, {1, 20, 2, 3}, {2, 20, 3, 4}, {-1, 20, 3, 4}, {2, 1, 1, 1},
	} {
		rows, diag, err := r.KnowledgeRerankPassages(context.Background(), roots,
			types.KnowledgeChildrenOptions{Depth: tc.depth, Limit: tc.limit})
		require.NoError(t, err)
		require.Len(t, rows, tc.wantPassages)
		require.Equal(t, tc.wantKnowledge, diag.AddedKnowledge)
		seen := map[string]bool{}
		for _, row := range rows {
			require.NotEqual(t, c.ID, row.Knowledge.ID, "original knowledge is not duplicated")
			require.Equal(t, p.ID, row.AnchorKnowledgeID)
			seen[row.Chunk.ID] = true
		}
		if tc.limit > 1 && tc.depth != 0 {
			require.True(t, seen[second.ID], "later passages must also reach rerank")
		}
		if tc.limit == 1 {
			require.True(t, diag.Truncated)
			require.Equal(t, p.ID, rows[0].Knowledge.ID)
		}
	}
	// Two hits under the same parent add its passages only once.
	roots = append(roots, types.KnowledgeTreeRoot{TenantID: 1, KBID: "a", KnowledgeID: s.ID})
	rows, diag, err := r.KnowledgeRerankPassages(context.Background(), roots,
		types.KnowledgeChildrenOptions{Depth: 1, Limit: 20})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, p.ID, rows[0].Knowledge.ID)
	require.Equal(t, 1, diag.AddedKnowledge)
	// The old detail contract stays downward-only and strict on excess size.
	_, err = r.KnowledgeChildren(context.Background(), roots[:1],
		types.KnowledgeChildrenOptions{Depth: -1, Limit: 1})
	require.NoError(t, err, "hit itself has no descendants")
	_, err = r.KnowledgeChildren(context.Background(),
		[]types.KnowledgeTreeRoot{{TenantID: 1, KBID: "a", KnowledgeID: p.ID}},
		types.KnowledgeChildrenOptions{Depth: -1, Limit: 1})
	require.ErrorContains(t, err, "children_limit_exceeded")
}

func TestNativeKnowledgeRerankUnavailableParentAndScope(t *testing.T) {
	r := nativeTreeRepo(t)
	require.NoError(t, r.db.AutoMigrate(&types.Chunk{}))
	p := nativeNode(t, r, "a", "", "parent")
	c := nativeNode(t, r, "a", p.ID, "hit")
	s := nativeNode(t, r, "a", p.ID, "sibling")
	g := nativeNode(t, r, "a", c.ID, "own child")
	for _, k := range []*types.Knowledge{p, c, s, g} {
		treePassage(t, r, k, 0)
	}
	root := types.KnowledgeTreeRoot{TenantID: 1, KBID: "a", KnowledgeID: c.ID}
	// Disabled parent cannot expose its other children; fall back to the hit.
	require.NoError(t, r.db.Model(p).Update("enable_status", "disabled").Error)
	rows, _, err := r.KnowledgeRerankPassages(context.Background(), []types.KnowledgeTreeRoot{root},
		types.KnowledgeChildrenOptions{Depth: 1, Limit: 20})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, g.ID, rows[0].Knowledge.ID)
	for _, bad := range []types.KnowledgeTreeRoot{
		{TenantID: 1, KBID: "b", KnowledgeID: c.ID}, {TenantID: 2, KBID: "a", KnowledgeID: c.ID},
	} {
		rows, _, err = r.KnowledgeRerankPassages(context.Background(), []types.KnowledgeTreeRoot{bad},
			types.KnowledgeChildrenOptions{Depth: 2, Limit: 20})
		require.NoError(t, err)
		require.Empty(t, rows)
	}
	// Non-ready or disabled chunks are never supplied to the model.
	require.NoError(t, r.db.Model(&types.Chunk{}).Where("knowledge_id = ?", g.ID).
		Update("index_status", "processing").Error)
	rows, _, err = r.KnowledgeRerankPassages(context.Background(), []types.KnowledgeTreeRoot{root},
		types.KnowledgeChildrenOptions{Depth: 1, Limit: 20})
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestNativeKnowledgeRerankPassageBudgetIsFairAndBounded(t *testing.T) {
	r := nativeTreeRepo(t)
	require.NoError(t, r.db.AutoMigrate(&types.Chunk{}))
	p := nativeNode(t, r, "a", "", "long parent")
	c := nativeNode(t, r, "a", p.ID, "hit")
	s := nativeNode(t, r, "a", p.ID, "short sibling")
	treePassage(t, r, c, 0)
	siblingChunk := treePassage(t, r, s, 0)
	chunks := make([]*types.Chunk, 0, types.MaxKnowledgeTreePassages+1)
	for i := 0; i <= types.MaxKnowledgeTreePassages; i++ {
		chunks = append(chunks, &types.Chunk{
			ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "a", KnowledgeID: p.ID,
			ChunkIndex: i, ChunkType: types.ChunkTypeText, Content: fmt.Sprintf("parent passage %d", i),
			IsEnabled: true, IndexStatus: "ready",
		})
	}
	require.NoError(t, r.db.Omit("SeqID").CreateInBatches(chunks, 50).Error)
	rows, diag, err := r.KnowledgeRerankPassages(context.Background(),
		[]types.KnowledgeTreeRoot{{TenantID: 1, KBID: "a", KnowledgeID: c.ID}},
		types.KnowledgeChildrenOptions{Depth: 1, Limit: 20})
	require.NoError(t, err)
	require.Len(t, rows, types.MaxKnowledgeTreePassages)
	require.True(t, diag.Truncated)
	require.Equal(t, 2, diag.AddedKnowledge)
	seen := false
	for _, row := range rows {
		seen = seen || row.Chunk.ID == siblingChunk.ID
	}
	require.True(t, seen, "a long parent must not consume every sibling passage slot")
}

func TestNativeKnowledgeRerankRoundsDoNotDependOnAbsoluteChunkIndex(t *testing.T) {
	r := nativeTreeRepo(t)
	require.NoError(t, r.db.AutoMigrate(&types.Chunk{}))
	p := nativeNode(t, r, "a", "", "parent")
	c := nativeNode(t, r, "a", p.ID, "hit")
	s := nativeNode(t, r, "a", p.ID, "sibling")
	first := treePassage(t, r, p, 0)
	second := treePassage(t, r, p, 1)
	treePassage(t, r, c, 0)
	sibling := treePassage(t, r, s, 1000)
	rows, _, err := r.KnowledgeRerankPassages(context.Background(),
		[]types.KnowledgeTreeRoot{{TenantID: 1, KBID: "a", KnowledgeID: c.ID}},
		types.KnowledgeChildrenOptions{Depth: 1, Limit: 20})
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, first.ID, rows[0].Chunk.ID)
	require.Equal(t, sibling.ID, rows[1].Chunk.ID, "each knowledge gets its first passage before any second passage")
	require.Equal(t, second.ID, rows[2].Chunk.ID)
}
