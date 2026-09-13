package memory

import (
	"context"
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

// seedRewordedEpisodes files two accounts whose subjects are far apart, one of
// which a differently-worded question is about.
func seedRewordedEpisodes(t *testing.T, svc *Service, ctx context.Context) {
	t.Helper()
	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-style", Slug: "answer-style", Title: "回答直接给结论",
		Summary:  "用户要求回答直接给结论。",
		Keywords: types.MemoryEpisodeTokens{"回答风格"},
		ToAt:     time.Now().Add(-time.Hour),
	})
	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-pool", Slug: "db-pool", Title: "生产库的连接池配置",
		Summary:  "用户在调生产库的连接池。",
		Keywords: types.MemoryEpisodeTokens{"数据库"},
		ToAt:     time.Now().Add(-2 * time.Hour),
	})
}

func TestARewordedMemoryIsStillFound(t *testing.T) {
	svc, tenantRepo, _ := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedRewordedEpisodes(t, svc, ctx)

	// The question shares no characters with the stored wording.
	recall := svc.Recall(ctx, "别铺垫那么多")
	require.NotEmpty(t, recall.Episodes,
		"lexical matching cannot find this; that is the entire point of embedding")
	require.Equal(t, "回答直接给结论", recall.Episodes[0].Title)
}

// Recall makes an embedding call in front of every answer, so that call has to
// be free to fail: an embedding endpoint being slow or down must cost a
// slightly worse memory selection, never a slow or broken answer.
func TestRecallDegradesToLexicalWhenEmbeddingFails(t *testing.T) {
	svc, tenantRepo, models := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedRewordedEpisodes(t, svc, ctx)

	models.embedder.fail = true

	// Shares characters with the stored title, which is all lexical matching
	// has to work with.
	recall := svc.Recall(ctx, "生产库怎么配连接池")
	require.NotEmpty(t, recall.Episodes,
		"with the embedder down, lexical matching still has to work")
	require.Equal(t, "生产库的连接池配置", recall.Episodes[0].Title)
}

func TestRecallDoesNotWaitForeverOnTheEmbedder(t *testing.T) {
	svc, tenantRepo, models := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedRewordedEpisodes(t, svc, ctx)

	models.embedder.delay = embedTimeout * 3

	started := time.Now()
	recall := svc.Recall(ctx, "生产库怎么配连接池")
	elapsed := time.Since(started)

	require.Less(t, elapsed, embedTimeout*2,
		"a wedged embedding endpoint must not hold up the answer")
	require.NotEmpty(t, recall.Episodes, "and the lexical result still has to come back")
}

func TestVectorRecallCanBeTurnedOff(t *testing.T) {
	svc, tenantRepo, models := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	off := false
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, VectorRecall: &off,
		EmbeddingModelID: "embed-1",
	})
	seedRewordedEpisodes(t, svc, ctx)

	callsAfterWrite := models.embedder.calls
	require.Empty(t, svc.Recall(ctx, "别铺垫那么多").Prompt,
		"with vector recall off this falls back to lexical, which cannot match this")
	require.Equal(t, callsAfterWrite, models.embedder.calls,
		"and no embedding call is made at all")
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

// Without a similarity floor, every account that has a vector enters the
// ranking — including the ones scoring zero — and the turn then carries all of
// them. The feature would go straight from "cannot find a re-worded memory" to
// "recalls everything", which is worse.
func TestUnrelatedMemoriesAreNotPulledInByVectorRecall(t *testing.T) {
	svc, tenantRepo, _ := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedRewordedEpisodes(t, svc, ctx)

	recall := svc.Recall(ctx, "别铺垫那么多")
	require.Len(t, recall.Episodes, 1,
		"only the conversation this question is about belongs in the prompt")
	require.Equal(t, "回答直接给结论", recall.Episodes[0].Title)
}

// Semantic recall has to be pinned to one model. Grabbing "the first embedding
// model in the workspace" would mix knowledge-base models into memory and
// change space whenever that list shuffled.
func TestBlankEmbeddingModelDoesNotGrabTheFirstListedModel(t *testing.T) {
	svc, tenantRepo, models := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{Enabled: true, WriteMode: types.MemoryWriteAuto})
	seedRewordedEpisodes(t, svc, ctx)

	require.Equal(t, 0, models.embedder.calls,
		"a workspace that has not pinned a model must not embed at all")

	require.Empty(t, svc.Recall(ctx, "别铺垫那么多").Prompt,
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
	seedRewordedEpisodes(t, svc, ctx)

	require.Equal(t, "embed-1", models.requestedEmbedID,
		"the workspace pin, not whichever embedding model ListModels returned first")

	recall := svc.Recall(ctx, "别铺垫那么多")
	require.NotEmpty(t, recall.Episodes)
	require.Equal(t, "embed-1", models.requestedEmbedID)
}

func TestRecallIgnoresVectorsFromAnotherModel(t *testing.T) {
	svc, tenantRepo, _ := newVectorHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)

	stored := seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-style", Slug: "answer-style", Title: "回答直接给结论",
		Summary:  "用户要求回答直接给结论。",
		Keywords: types.MemoryEpisodeTokens{"回答风格"},
		ToAt:     time.Now().Add(-time.Hour),
	})

	require.NoError(t, svc.repo.UpsertEpisodeEmbedding(ctx, scope, &types.MemoryEpisodeEmbedding{
		EpisodeID: stored.ID,
		ModelID:   "other-embed",
		Dims:      3,
		Vector:    types.EncodeEmbedding([]float32{1, 0, 0}),
	}))

	require.Empty(t, svc.Recall(ctx, "别铺垫那么多").Prompt,
		"a vector from a different model must not be scored against this query")
}
