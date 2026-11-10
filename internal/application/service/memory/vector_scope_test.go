package memory

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// Semantic recall used to score only the memories a prior `ORDER BY
// importance` had already listed. Importance says nothing about whether a
// memory answers the question, so the ranking could re-order that window but
// never look outside it — a well-matching memory below the cut was
// unreachable, and the only knob available was to make the window bigger.
//
// These tests pin the inverted order: the store is asked which memories are
// closest to the question, over everything it holds.

func TestVectorSearchRanksTheWholeSubjectNotAListedWindow(t *testing.T) {
	svc, tenantRepo, _ := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)

	// The memory that answers the question is the least important one, so any
	// importance-ordered window of size one excludes it.
	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "回答风格", Content: "回答直接给结论", Importance: 1,
	})
	require.NoError(t, err)
	_, err = svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "数据库", Content: "生产库的连接池配置", Importance: 5,
	})
	require.NoError(t, err)

	window, err := svc.repo.ListActiveByKinds(ctx, scope, []string{types.MemoryKindFact}, 1)
	require.NoError(t, err)
	require.Len(t, window, 1)
	require.Equal(t, "生产库的连接池配置", window[0].Content,
		"the window the old implementation scored holds the wrong memory")

	hits, err := svc.repo.SearchItemsByVector(ctx, scope, interfaces.MemoryVectorQuery{
		ModelID:  "embed-1",
		Vector:   []float32{0.98, 0.2, 0},
		Kinds:    []string{types.MemoryKindFact},
		MinScore: minCosine,
		Limit:    1,
	})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "回答直接给结论", hits[0].Item.Content,
		"the search has to see every stored vector, not the ones importance listed")
	require.Greater(t, hits[0].Score, minCosine)
}

// A search that reached past the candidate list would also reach past the
// filters that list applied, which is how a superseded or expired memory gets
// back into an answer. The filters move into the search instead.
func TestVectorSearchHonoursKindAndLifecycleFilters(t *testing.T) {
	svc, db, tenantRepo, _ := newVectorHarnessWithDB(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindPreference, Topic: "回答风格", Content: "回答直接给结论",
	})
	require.NoError(t, err)
	expired, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindTask, Topic: "待办", Content: "本周要回答直接给结论的评审",
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec("UPDATE memory_items SET expires_at = ? WHERE id = ?",
		time.Now().Add(-time.Hour), expired.ID).Error)

	query := interfaces.MemoryVectorQuery{
		ModelID:  "embed-1",
		Vector:   []float32{0.98, 0.2, 0},
		MinScore: minCosine,
		Limit:    10,
	}

	all, err := svc.repo.SearchItemsByVector(ctx, scope, query)
	require.NoError(t, err)
	for _, hit := range all {
		require.NotEqual(t, expired.ID, hit.Item.ID, "an expired memory is not a match")
	}
	require.Len(t, all, 1)

	query.Kinds = []string{types.MemoryKindFact}
	none, err := svc.repo.SearchItemsByVector(ctx, scope, query)
	require.NoError(t, err)
	require.Empty(t, none, "a kind the caller excluded must not come back")
}

// Fusion combines two rankings over one slice, so a match only the vector side
// found has to be added to that slice. Dropping it instead would make the
// wider search pointless.
func TestMergeVectorHitsGrowsThePoolWithWhatItDidNotHold(t *testing.T) {
	inPool := &types.MemoryItem{ID: "a", Content: "已经在候选池里"}
	outsidePool := &types.MemoryItem{ID: "b", Content: "候选池没有的记忆"}
	excluded := &types.MemoryItem{ID: "c", Content: "常驻块已经写过"}

	// Room to spare, so an append that forgot to copy would overwrite the
	// caller's slice rather than allocate.
	candidates := make([]*types.MemoryItem, 1, 8)
	candidates[0] = inPool

	ranking, pool, added := mergeVectorHits(candidates,
		[]interfaces.MemoryVectorHit{
			{Item: outsidePool, Score: 0.9},
			{Item: excluded, Score: 0.8},
			{Item: inPool, Score: 0.7},
		},
		map[string]struct{}{"c": {}},
	)

	require.Equal(t, 1, added)
	require.Equal(t, []int{1, 0}, ranking, "best first, addressing the returned pool")
	require.Equal(t, []*types.MemoryItem{inPool, outsidePool}, pool)
	require.Equal(t, []*types.MemoryItem{inPool}, candidates,
		"the caller's slice is not written through")
}

// The lexical pool was a fixed 400 while the capacity cap goes to 2000, which
// made everything past the 400th most important memory unrecallable. Tying the
// pool to the cap is what removes that second, invisible limit: the cap is
// also what write-time capacity enforcement trims to, so the pool now covers
// every live memory by construction.
func TestLexicalPoolCoversEveryLiveMemory(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	cfg := &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, MaxItems: 3,
	}
	tenantRepo.set(1, cfg)
	scope := scopeFor(t, ctx)

	for _, content := range []string{"第一条记忆", "第二条记忆", "第三条记忆", "第四条记忆", "第五条记忆"} {
		_, err := svc.Remember(ctx, types.MemoryItem{
			Kind: types.MemoryKindFact, Topic: content, Content: content,
		})
		require.NoError(t, err)
	}

	active, err := svc.repo.CountActive(ctx, scope)
	require.NoError(t, err)
	require.GreaterOrEqual(t, int64(lexicalPoolSize(cfg)), active,
		"the pool must not be a smaller limit than the one that decides what stays active")
}

// Extraction decides whether a sentence updates something already stored, so
// the memories it is shown have to be the ones the sentence might collide
// with. Selecting them by importance first — which is what listing a pool did
// — hides exactly the collision that matters: a memory nobody marked important
// gets a second, contradicting copy written next to it, and both stay active.
func TestExtractionSeesTheMemoryItWouldDuplicateEvenWhenUnimportant(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
		ExtractModelID: "model-1", ExtractDelaySeconds: 1,
		EmbeddingModelID: "embed-1",
	})
	models.workspaceModels = []*types.Model{
		{ID: "embed-1", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive},
	}
	models.embedder = &stubEmbedder{vectors: map[string][]float32{
		"连接数": {1, 0, 0},
		"生产库": {1, 0, 0},
	}}
	models.response = `{"memories":[]}`

	// Enough important memories to fill the window on their own.
	for i := 0; i <= extractRelevantCandidates; i++ {
		_, err := svc.Remember(ctx, types.MemoryItem{
			Kind: types.MemoryKindFact, Importance: 5,
			Topic:   fmt.Sprintf("话题%d", i),
			Content: fmt.Sprintf("与本次提问无关的第 %d 条记忆", i),
		})
		require.NoError(t, err)
	}
	// The one the next turn contradicts is the least important of the lot, so
	// every importance-ordered window excludes it.
	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Importance: 1,
		Topic: "生产库", Content: "生产库连接数上限是 200",
	})
	require.NoError(t, err)

	messages.set("session-1", []*types.Message{
		userMessage("session-1", "生产库连接数上限昨天调成 500 了", time.Now().Add(-time.Hour)),
	})
	svc.ScheduleExtraction(ctx, "session-1", "message-1", "model-1")
	drainExtractions(t, svc, enqueuer)

	notes := existingNotesBlock(models.lastPromptContaining("What the user said:"))
	require.Contains(t, notes, "生产库连接数上限是 200",
		"the model cannot supersede a memory it was never shown")
	require.LessOrEqual(t, strings.Count(notes, "\n["), extractRelevantCandidates,
		"and it still must not be shown the whole store")
}

// The fan-out feeds fusion. Asking for exactly as many hits as may be returned
// would leave nothing for the lexical ranking to be weighed against, and the
// rune budget can skip several long items before finding ones that fit.
func TestVectorFanoutLeavesRoomForFusion(t *testing.T) {
	require.Equal(t, 20, vectorFanout(types.MemoryRecallMaxItems),
		"a five-item recall still needs a usable ranking")
	require.Equal(t, 80, vectorFanout(types.MemorySearchMaxItems))
}
