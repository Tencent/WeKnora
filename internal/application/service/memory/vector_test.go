package memory

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The reason for adding semantic recall at all: a memory the user has since
// re-worded shares no tokens with the question that should find it. Every
// comparable system embeds; this one was the only one matching on characters.

func newVectorHarness(t *testing.T) (*Service, *stubTenantRepo, *stubModelService) {
	t.Helper()
	svc, _, tenantRepo := newMemoryHarness(t)
	models := &stubModelService{
		workspaceModels: []*types.Model{
			{ID: "embed-1", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive},
		},
		embedder: &stubEmbedder{vectors: map[string][]float32{
			// Two ways of saying the same thing, no shared characters.
			"直接给结论": {1, 0, 0},
			"别铺垫":   {0.98, 0.2, 0},
			// A different subject entirely.
			"连接池": {0, 1, 0},
		}},
	}
	svc.modelService = models
	return svc, tenantRepo, models
}

func TestARewordedMemoryIsStillFound(t *testing.T) {
	svc, tenantRepo, _ := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "回答风格", Content: "回答直接给结论",
	})
	require.NoError(t, err)
	_, err = svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "数据库", Content: "生产库的连接池配置",
	})
	require.NoError(t, err)

	// The question shares no characters with the stored wording.
	recall := svc.Recall(ctx, "别铺垫那么多")
	require.NotEmpty(t, recall.Items,
		"lexical matching cannot find this; that is the entire point of embedding")
	require.Equal(t, "回答直接给结论", recall.Items[0].Content)
}

// An interest is promoted from a subject label, so its topic and content hold
// the same string. Joining them sent "WeKnora混合检索：WeKnora混合检索" to the
// embedder, which is not a sentence any question resembles.
func TestEmbeddedTextDoesNotRepeatTheSubject(t *testing.T) {
	require.Equal(t, "WeKnora混合检索", embeddableText(&types.MemoryItem{
		Kind: types.MemoryKindInterest, Topic: "WeKnora混合检索", Content: "WeKnora混合检索",
	}, nil))
	require.Equal(t, "数据库：生产库用 PostgreSQL 17", embeddableText(&types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "数据库", Content: "生产库用 PostgreSQL 17",
	}, nil), "a topic that adds something still has to be kept")
}

// A one-word interest is a weak vector. The other wordings this person used
// for the same subject widen what a question can match, and they cost nothing
// in the prompt because they never leave the vector.
func TestInterestEmbedsTheOtherWordingsOfItsSubject(t *testing.T) {
	text := embeddableText(&types.MemoryItem{
		Kind: types.MemoryKindInterest, Topic: "WeKnora混合检索", Content: "WeKnora混合检索",
	}, []string{"混合检索调优", "WeKnora混合检索", "", "召回率优化"})

	require.Equal(t, "WeKnora混合检索；混合检索调优；召回率优化", text,
		"aliases are appended once each, and the subject is not repeated")
}

// The aliases have to reach the embedder from the topic tracker, not just from
// a caller that already happens to hold them.
func TestPromotedInterestIsEmbeddedWithItsOtherWordings(t *testing.T) {
	svc, tenantRepo, models := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, InterestThreshold: 2,
		EmbeddingModelID: "embed-1",
	})

	require.Empty(t, svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"}))
	require.Equal(t, []string{"门店排班管理"},
		svc.ObserveQuestionTopics(ctx, []string{"连锁门店排班管理"}),
		"the second wording is the same subject, so it promotes")

	var embedded string
	for _, text := range models.embedder.texts {
		if strings.Contains(text, "门店排班管理") {
			embedded = text
		}
	}
	require.Contains(t, embedded, "连锁门店排班管理",
		"the wording the user also used has to be part of what the interest matches")
}

// A vector is written once, at promotion. A wording that arrives later would
// otherwise never make it in, which would leave this feature working only for
// subjects whose every wording appeared before they were promoted.
func TestALaterWordingRebuildsTheInterestVector(t *testing.T) {
	svc, tenantRepo, _ := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, InterestThreshold: 2,
		EmbeddingModelID: "embed-1",
	})
	scope := scopeFor(t, ctx)

	svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"})
	require.NotEmpty(t, svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"}))

	items, err := svc.repo.ListActiveByKinds(ctx, scope, []string{types.MemoryKindInterest}, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	vectors, err := svc.repo.ItemEmbeddings(ctx, scope, []string{items[0].ID}, "embed-1")
	require.NoError(t, err)
	require.NotEmpty(t, vectors, "promotion embeds the interest")

	svc.ObserveQuestionTopics(ctx, []string{"连锁门店排班管理"})

	vectors, err = svc.repo.ItemEmbeddings(ctx, scope, []string{items[0].ID}, "embed-1")
	require.NoError(t, err)
	require.Empty(t, vectors,
		"the stale vector is dropped so the maintenance backfill rebuilds it")

	cfg := &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, EmbeddingModelID: "embed-1",
	}
	require.Equal(t, 1, svc.backfillEmbeddings(ctx, scope, cfg))
	vectors, err = svc.repo.ItemEmbeddings(ctx, scope, []string{items[0].ID}, "embed-1")
	require.NoError(t, err)
	require.NotEmpty(t, vectors, "and it comes back")
}

// Recall used to make no model call at all. Adding one has to be free to fail:
// an embedding endpoint being slow or down must cost a slightly worse memory
// selection, never a slow or broken answer.
func TestRecallDegradesToLexicalWhenEmbeddingFails(t *testing.T) {
	svc, tenantRepo, models := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "数据库", Content: "生产库用 PostgreSQL 17",
	})
	require.NoError(t, err)

	models.embedder.fail = true

	recall := svc.Recall(ctx, "生产库怎么配")
	require.NotEmpty(t, recall.Items,
		"with the embedder down, lexical matching still has to work")
	require.Equal(t, "生产库用 PostgreSQL 17", recall.Items[0].Content)
}

func TestRecallDoesNotWaitForeverOnTheEmbedder(t *testing.T) {
	svc, tenantRepo, models := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "数据库", Content: "生产库用 PostgreSQL 17",
	})
	require.NoError(t, err)

	models.embedder.delay = embedTimeout * 3

	started := time.Now()
	recall := svc.Recall(ctx, "生产库怎么配")
	elapsed := time.Since(started)

	require.Less(t, elapsed, embedTimeout*2,
		"a wedged embedding endpoint must not hold up the answer")
	require.NotEmpty(t, recall.Items, "and the lexical result still has to come back")
}

func TestVectorRecallCanBeTurnedOff(t *testing.T) {
	svc, tenantRepo, models := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	off := false
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, VectorRecall: &off,
		EmbeddingModelID: "embed-1",
	})

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "回答风格", Content: "回答直接给结论",
	})
	require.NoError(t, err)

	callsAfterWrite := models.embedder.calls
	require.Empty(t, svc.Recall(ctx, "别铺垫那么多").Items,
		"with vector recall off this falls back to lexical, which cannot match this")
	require.Equal(t, callsAfterWrite, models.embedder.calls,
		"and no embedding call is made at all")
}

// Fusion, not replacement: lexical still wins on the exact tokens that models
// embed poorly — version numbers, error codes, product names.
func TestFusionKeepsWhatEachSignalIsGoodAt(t *testing.T) {
	lexical := []int{3, 1}
	vector := []int{1, 2}
	fused := fuseRankings(lexical, vector)

	require.Equal(t, 1, fused[0],
		"the candidate both signals rank highly comes first")
	require.ElementsMatch(t, []int{1, 2, 3}, fused,
		"and neither signal's candidates are dropped")
}

func TestFusionIsStableWhenOneSignalIsMissing(t *testing.T) {
	require.Equal(t, []int{5, 2}, fuseRankings([]int{5, 2}, nil))
	require.Equal(t, []int{5, 2}, fuseRankings(nil, []int{5, 2}))
	require.Empty(t, fuseRankings(nil, nil))
}

func TestEmbeddingRoundTripsAndScoresItself(t *testing.T) {
	vector := []float32{0.5, -0.25, 0.125}
	decoded := types.DecodeEmbedding(types.EncodeEmbedding(vector))
	require.Equal(t, vector, decoded)
	require.InDelta(t, 1.0, types.CosineSimilarity(vector, decoded), 1e-6)
}

// Vectors from different models are not comparable. Scoring them anyway would
// produce confident nonsense, which is worse than declining to score.
func TestMismatchedVectorsScoreZero(t *testing.T) {
	require.Equal(t, 0.0, types.CosineSimilarity([]float32{1, 0}, []float32{1, 0, 0}))
	require.Equal(t, 0.0, types.CosineSimilarity(nil, []float32{1, 0, 0}))
	require.Equal(t, 0.0, types.CosineSimilarity([]float32{0, 0}, []float32{0, 0}))
}

// Showing every stored memory to the extraction model does not survive a store
// of any size: the model has to hold dozens of unrelated notes in mind to judge
// one sentence, and unrelated notes invite spurious update and delete
// decisions. mem0 shows 10 by similarity, Graphiti at most 15 per entity.
func TestExtractionOnlySeesRelevantMemories(t *testing.T) {
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
		"数据库": {1, 0, 0},
		"生产库": {1, 0, 0},
	}}
	models.response = `{"memories":[]}`

	// More stored memories than the model is allowed to see.
	for i := 0; i < extractRelevantCandidates*2; i++ {
		_, err := svc.Remember(ctx, types.MemoryItem{
			Kind:    types.MemoryKindFact,
			Topic:   fmt.Sprintf("话题%d", i),
			Content: fmt.Sprintf("与本次提问无关的第 %d 条记忆", i),
		})
		require.NoError(t, err)
	}

	messages.set("session-1", []*types.Message{
		userMessage("session-1", "生产库的连接数上限是多少", time.Now().Add(-time.Hour)),
	})
	svc.ScheduleExtraction(ctx, "session-1", "message-1", "model-1")
	drainExtractions(t, svc, enqueuer)

	notes := existingNotesBlock(models.lastPromptContaining("What the user said:"))
	shown := strings.Count(notes, "\n[")
	require.LessOrEqual(t, shown, extractRelevantCandidates,
		"the model must not be shown the whole store; it saw %d notes", shown)
	require.Greater(t, shown, 0, "but it still has to see something to update against")
}

// existingNotesBlock returns just the "Existing notes:" section of the user
// prompt. The last occurrence, because the system prompt's few-shot examples
// contain the same heading and would otherwise be what gets measured.
func existingNotesBlock(prompt string) string {
	start := strings.LastIndex(prompt, "Existing notes:")
	if start < 0 {
		return ""
	}
	rest := prompt[start:]
	if end := strings.Index(rest, "\n\n"); end > 0 {
		return rest[:end]
	}
	return rest
}

// Without a similarity floor, every memory that has a vector enters the
// ranking — including the ones scoring zero — and fusion then pulls them into
// the prompt. The feature would go straight from "cannot find a re-worded
// memory" to "recalls everything", which is worse.
func TestUnrelatedMemoriesAreNotPulledInByVectorRecall(t *testing.T) {
	svc, tenantRepo, _ := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "回答风格", Content: "回答直接给结论",
	})
	require.NoError(t, err)
	_, err = svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "数据库", Content: "生产库的连接池配置",
	})
	require.NoError(t, err)

	recall := svc.Recall(ctx, "别铺垫那么多")
	require.Len(t, recall.Items, 1,
		"only the memory this question is about belongs in the prompt")
	require.Equal(t, "回答直接给结论", recall.Items[0].Content)
}

// Semantic recall has to be pinned to one model. Grabbing "the first embedding
// model in the workspace" would mix knowledge-base models into memory and
// change space whenever that list shuffled.
func TestBlankEmbeddingModelDoesNotGrabTheFirstListedModel(t *testing.T) {
	svc, tenantRepo, models := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{Enabled: true, WriteMode: types.MemoryWriteAuto})

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "回答风格", Content: "回答直接给结论",
	})
	require.NoError(t, err)
	require.Equal(t, 0, models.embedder.calls,
		"a workspace that has not pinned a model must not embed at all")

	require.Empty(t, svc.Recall(ctx, "别铺垫那么多").Items,
		"without a pinned model, semantic recall must not silently pick one")
	require.Equal(t, 0, models.embedder.calls)
	require.Empty(t, models.requestedEmbedID)
}

func TestRecallUsesThePinnedModelNotTheFirstListed(t *testing.T) {
	svc, tenantRepo, models := newVectorHarness(t)
	models.workspaceModels = []*types.Model{
		{ID: "embed-2", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive},
		{ID: "embed-1", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive},
	}
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "回答风格", Content: "回答直接给结论",
	})
	require.NoError(t, err)
	require.Equal(t, "embed-1", models.requestedEmbedID,
		"the workspace pin, not whichever embedding model ListModels returned first")

	recall := svc.Recall(ctx, "别铺垫那么多")
	require.NotEmpty(t, recall.Items)
	require.Equal(t, "embed-1", models.requestedEmbedID)
}

func TestRecallIgnoresVectorsFromAnotherModel(t *testing.T) {
	svc, tenantRepo, _ := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)

	stored, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "回答风格", Content: "回答直接给结论",
	})
	require.NoError(t, err)

	require.NoError(t, svc.repo.UpsertItemEmbedding(ctx, scope, &types.MemoryItemEmbedding{
		ItemID:  stored.ID,
		ModelID: "other-embed",
		Dims:    3,
		Vector:  types.EncodeEmbedding([]float32{1, 0, 0}),
	}))

	require.Empty(t, svc.Recall(ctx, "别铺垫那么多").Items,
		"a vector from a different model must not be scored against this query")
}
