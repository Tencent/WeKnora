package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hit is a hit in knowledge base "kb".
func hit(id string, score float64, source types.SourceType) *types.IndexWithScore {
	return kbHit("kb", id, score, source)
}

func kbHit(kbID, id string, score float64, source types.SourceType) *types.IndexWithScore {
	return &types.IndexWithScore{
		ChunkID: id, SourceID: id, Score: score, SourceType: source, KnowledgeBaseID: kbID,
	}
}

// recalls is the ImageKBIDs of a group whose KBs recall images.
func recalls(ids ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

func ids(hits []*types.IndexWithScore) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ChunkID
	}
	return out
}

func TestParamsWithTopKWidensOnlyTheDocumentVectorSearchForImages(t *testing.T) {
	g := &storeGroup{
		TopK: 100, ImageKBIDs: recalls("kb"), VectorThreshold: 0.3,
		BaseParams: []types.RetrieveParams{
			{RetrieverType: types.VectorRetrieverType, Threshold: 0.3},
			{RetrieverType: types.VectorRetrieverType, Threshold: 0.3, KnowledgeType: types.KnowledgeTypeFAQ},
			{RetrieverType: types.KeywordsRetrieverType, Threshold: 0.5},
		},
	}
	got := paramsWithTopK(g)
	assert.Equal(t, 150, got[0].TopK)
	assert.Equal(t, imageVectorThreshold, got[0].Threshold)
	assert.Equal(t, 100, got[1].TopK, "FAQ indexes hold no images")
	assert.Equal(t, 0.3, got[1].Threshold)
	assert.Equal(t, 100, got[2].TopK, "keyword search is left alone")
	assert.Equal(t, 0.5, got[2].Threshold)
	assert.Equal(t, 0.3, g.BaseParams[0].Threshold, "base params stay immutable")

	// A text threshold already under the image one is not raised, and the
	// pool never grows past the global cap.
	g = &storeGroup{TopK: maxRetrievalPoolSize, ImageKBIDs: recalls("kb"), BaseParams: []types.RetrieveParams{
		{RetrieverType: types.VectorRetrieverType, Threshold: 0.05},
	}}
	got = paramsWithTopK(g)
	assert.Equal(t, 0.05, got[0].Threshold)
	assert.Equal(t, maxRetrievalPoolSize, got[0].TopK)

	g.ImageKBIDs, g.TopK = nil, 100
	got = paramsWithTopK(g)
	assert.Equal(t, 0.05, got[0].Threshold)
	assert.Equal(t, 100, got[0].TopK, "without images the group TopK is used as is")
}

func TestFilterImageHitsHoldsEachKindToItsOwnThreshold(t *testing.T) {
	g := &storeGroup{ImageKBIDs: recalls("kb"), VectorThreshold: 0.3}
	vector := &types.RetrieveResult{RetrieverType: types.VectorRetrieverType, Results: []*types.IndexWithScore{
		hit("text-strong", 0.6, types.ChunkSourceType),
		hit("image-strong", 0.25, types.ImageSourceType),
		hit("text-weak", 0.2, types.ChunkSourceType), // only reached because the query threshold was lowered
		hit("image-weak", 0.05, types.ImageSourceType),
	}}
	keyword := &types.RetrieveResult{RetrieverType: types.KeywordsRetrieverType, Results: []*types.IndexWithScore{
		hit("text-kw", 0.9, types.ChunkSourceType),
		hit("image-kw", 0.9, types.ImageSourceType),
	}}
	filterImageHits([]*types.RetrieveResult{vector, keyword}, g)
	assert.Equal(t, []string{"text-strong", "image-strong"}, ids(vector.Results))
	assert.Equal(t, []string{"text-kw"}, ids(keyword.Results),
		"an image row's Content is the caption, already indexed as text")
}

func TestFilterImageHitsLeavesTextOnlySearchesAlone(t *testing.T) {
	// Without image recall the query ran at the text threshold, so every
	// text hit already passed it. Image rows a KB indexed while it was
	// opted in are dropped once it opts out, however well they score.
	g := &storeGroup{VectorThreshold: 0.3}
	vector := &types.RetrieveResult{RetrieverType: types.VectorRetrieverType, Results: []*types.IndexWithScore{
		hit("a", 0.31, types.ChunkSourceType), hit("b", 0.9, types.ImageSourceType),
	}}
	keyword := &types.RetrieveResult{RetrieverType: types.KeywordsRetrieverType, Results: []*types.IndexWithScore{
		hit("c", 0.9, types.ImageSourceType),
	}}
	filterImageHits([]*types.RetrieveResult{vector, nil, keyword}, g)
	assert.Equal(t, []string{"a"}, ids(vector.Results))
	assert.Empty(t, keyword.Results)
}

func TestFilterImageHitsRecallsImagesOnlyOfOptedInKBs(t *testing.T) {
	// One store group can hold KBs on both sides of the switch.
	g := &storeGroup{ImageKBIDs: recalls("kb-on"), VectorThreshold: 0.3}
	vector := &types.RetrieveResult{RetrieverType: types.VectorRetrieverType, Results: []*types.IndexWithScore{
		kbHit("kb-on", "image-on", 0.25, types.ImageSourceType),
		kbHit("kb-off", "image-off", 0.9, types.ImageSourceType),
		kbHit("kb-off", "text-off", 0.6, types.ChunkSourceType),
	}}
	filterImageHits([]*types.RetrieveResult{vector}, g)
	assert.Equal(t, []string{"image-on", "text-off"}, ids(vector.Results))
}

// imageRecallModels hands out one embedding model and records each lookup.
type imageRecallModels struct {
	interfaces.ModelService
	model     embedding.Embedder
	requested []string
}

func (m *imageRecallModels) GetEmbeddingModel(_ context.Context, id string) (embedding.Embedder, error) {
	m.requested = append(m.requested, id)
	return m.model, nil
}

func TestApplyImageRecallNeedsTheKBSwitchAndAnImageModel(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	vectorKB := func(id string, imageVectors bool) *types.KnowledgeBase {
		return &types.KnowledgeBase{
			ID: id, TenantID: 1, EmbeddingModelID: "embed-1",
			IndexingStrategy:      types.IndexingStrategy{VectorEnabled: true},
			ImageProcessingConfig: types.ImageProcessingConfig{ImageVectorEnabled: imageVectors},
		}
	}
	params := types.SearchParams{VectorThreshold: 0.3}

	cases := []struct {
		name          string
		kbs           []*types.KnowledgeBase
		model         embedding.Embedder
		params        types.SearchParams
		want          map[string]struct{}
		modelResolved bool
	}{
		{
			// Every KB from before the switch: the image-capable model is
			// not even looked up.
			name:  "switch off, image model",
			kbs:   []*types.KnowledgeBase{vectorKB("kb-a", false)},
			model: &imageModel{dims: 3}, params: params,
		},
		{
			name:  "switch on, text-only model",
			kbs:   []*types.KnowledgeBase{vectorKB("kb-a", true)},
			model: &textModel{}, params: params, modelResolved: true,
		},
		{
			name:  "switch on, vector match disabled",
			kbs:   []*types.KnowledgeBase{vectorKB("kb-a", true)},
			model: &imageModel{dims: 3}, params: types.SearchParams{DisableVectorMatch: true},
		},
		{
			name:  "switch on, image model",
			kbs:   []*types.KnowledgeBase{vectorKB("kb-a", true), vectorKB("kb-b", false)},
			model: &imageModel{dims: 3}, params: params,
			want: recalls("kb-a"), modelResolved: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			models := &imageRecallModels{model: tc.model}
			s := &knowledgeBaseService{modelService: models}
			g := &storeGroup{KBIDs: []string{"kb-a", "kb-b"}, TopK: 100, BaseParams: []types.RetrieveParams{
				{RetrieverType: types.VectorRetrieverType, Threshold: 0.3},
			}}
			s.applyImageRecall(ctx, tc.kbs, []*storeGroup{g}, tc.params)

			assert.Equal(t, tc.modelResolved, len(models.requested) > 0)
			if tc.want == nil {
				assert.False(t, g.imageRecall())
				got := paramsWithTopK(g)
				require.Len(t, got, 1)
				assert.Equal(t, 100, got[0].TopK, "the pool is not widened")
				assert.Equal(t, 0.3, got[0].Threshold, "the threshold is not lowered")
				return
			}
			assert.Equal(t, tc.want, g.ImageKBIDs)
			assert.Equal(t, 0.3, g.VectorThreshold)
			got := paramsWithTopK(g)
			assert.Equal(t, 150, got[0].TopK)
			assert.Equal(t, imageVectorThreshold, got[0].Threshold)
		})
	}
}

// rankedEngine answers each search with the first TopK rows of its ranking,
// cut as an engine cuts its own, and records the TopK each search asked for.
type rankedEngine struct {
	fakeRetrieveEngineService
	ranked map[types.RetrieverType][]*types.IndexWithScore
	mu     sync.Mutex
	topKs  map[types.RetrieverType][]int
}

func (e *rankedEngine) Retrieve(_ context.Context, p types.RetrieveParams) ([]*types.RetrieveResult, error) {
	e.mu.Lock()
	if e.topKs == nil {
		e.topKs = make(map[types.RetrieverType][]int)
	}
	e.topKs[p.RetrieverType] = append(e.topKs[p.RetrieverType], p.TopK)
	e.mu.Unlock()
	rows := e.ranked[p.RetrieverType]
	return []*types.RetrieveResult{{
		Results:             slices.Clone(rows[:min(p.TopK, len(rows))]),
		RetrieverEngineType: types.PostgresRetrieverEngineType,
		RetrieverType:       p.RetrieverType,
	}}, nil
}

// rankedHits is n hits of one kind in knowledge base kbID, best first.
func rankedHits(kbID, prefix string, n int, from float64, source types.SourceType) []*types.IndexWithScore {
	out := make([]*types.IndexWithScore, n)
	for i := range out {
		out[i] = kbHit(kbID, fmt.Sprintf("%s%d", prefix, i), from-float64(i)*1e-4, source)
	}
	return out
}

func TestRetrieveFromStoresRefillsPastStaleImageVectors(t *testing.T) {
	cases := []struct {
		name       string
		imageKBs   map[string]struct{}
		stale      int // image rows of the KB, ranked above all its text
		text       int
		wantText   int
		wantImages int
		wantTopKs  []int
	}{
		{
			// MatchCount 5 over-retrieves a pool of 50, all of it image rows
			// of a KB that has since turned image vectors off.
			name: "the whole pool is stale", stale: 50, text: 10,
			wantText: 10, wantTopKs: []int{50, 100},
		},
		{
			name: "stale rows push text out of the pool", stale: 120, text: 200,
			wantText: 50, wantTopKs: []int{50, 100, 200},
		},
		{
			// Past the cap the search stays short: the stated bound.
			name: "more stale rows than the largest pool", stale: 600, text: 10,
			wantTopKs: []int{50, 100, 200, 400, maxRetrievalPoolSize},
		},
		{
			name: "a pool that is not full is not searched again", stale: 5, text: 10,
			wantText: 10, wantTopKs: []int{50},
		},
		{
			name: "images of a KB that recalls them are not stale", imageKBs: recalls("kb"),
			stale: 50, text: 10, wantText: 10, wantImages: 50, wantTopKs: []int{75},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := &rankedEngine{
				fakeRetrieveEngineService: fakeRetrieveEngineService{
					engineType: types.PostgresRetrieverEngineType,
					support:    []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType},
				},
				ranked: map[types.RetrieverType][]*types.IndexWithScore{
					types.VectorRetrieverType: append(
						rankedHits("kb", "image", tc.stale, 0.9, types.ImageSourceType),
						rankedHits("kb", "text", tc.text, 0.5, types.ChunkSourceType)...),
					types.KeywordsRetrieverType: rankedHits("kb", "kw", 3, 0.9, types.ChunkSourceType),
				},
			}
			g := &storeGroup{
				KBIDs: []string{"kb"}, Engine: buildBoundComposite(t, engine), TopK: 50,
				ImageKBIDs: tc.imageKBs,
				BaseParams: []types.RetrieveParams{
					{RetrieverType: types.VectorRetrieverType},
					{RetrieverType: types.KeywordsRetrieverType},
				},
			}
			res, err := (&knowledgeBaseService{}).retrieveFromStores(
				context.Background(), []*storeGroup{g}, nil)
			require.NoError(t, err)

			var text, images, keyword int
			for _, rr := range res {
				for _, h := range rr.Results {
					switch {
					case rr.RetrieverType == types.KeywordsRetrieverType:
						keyword++
					case h.SourceType == types.ImageSourceType:
						images++
					default:
						text++
					}
				}
			}
			assert.Equal(t, tc.wantText, text)
			assert.Equal(t, tc.wantImages, images)
			assert.Equal(t, 3, keyword, "the keyword search is left as it was")
			assert.Equal(t, tc.wantTopKs, engine.topKs[types.VectorRetrieverType])
			assert.Equal(t, []int{50}, engine.topKs[types.KeywordsRetrieverType],
				"only the document vector search runs again")
		})
	}
}

// interleave alternates the hits of a and b, a first, then appends the rest.
func interleave(a, b []*types.IndexWithScore) []*types.IndexWithScore {
	out := make([]*types.IndexWithScore, 0, len(a)+len(b))
	for i := 0; i < max(len(a), len(b)); i++ {
		if i < len(a) {
			out = append(out, a[i])
		}
		if i < len(b) {
			out = append(out, b[i])
		}
	}
	return out
}

func TestRetrieveFromStoresRefillsPastKeywordImageRows(t *testing.T) {
	cases := []struct {
		name      string
		imageKBs  map[string]struct{}
		ranked    []*types.IndexWithScore
		wantText  int
		wantTopKs []int
	}{
		{
			// An engine that ranks every image row above the text, as a
			// constant keyword score can, fills the whole pool with them.
			name: "the whole pool is image rows",
			ranked: append(rankedHits("kb", "image", 50, 0.9, types.ImageSourceType),
				rankedHits("kb", "text", 10, 0.5, types.ChunkSourceType)...),
			wantText: 10, wantTopKs: []int{50, 100},
		},
		{
			// An image row's Content is its caption, so it ranks beside the
			// caption's own chunk and takes half the pool.
			name: "image rows beside their captions", imageKBs: recalls("kb"),
			ranked: interleave(rankedHits("kb", "image", 100, 0.9, types.ImageSourceType),
				rankedHits("kb", "text", 200, 0.9, types.ChunkSourceType)),
			wantText: 50, wantTopKs: []int{50, 100},
		},
		{
			name: "a pool that is not full is not searched again",
			ranked: append(rankedHits("kb", "image", 5, 0.9, types.ImageSourceType),
				rankedHits("kb", "text", 10, 0.5, types.ChunkSourceType)...),
			wantText: 10, wantTopKs: []int{50},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := &rankedEngine{
				fakeRetrieveEngineService: fakeRetrieveEngineService{
					engineType: types.PostgresRetrieverEngineType,
					support:    []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType},
				},
				ranked: map[types.RetrieverType][]*types.IndexWithScore{
					types.VectorRetrieverType:   rankedHits("kb", "vec", 3, 0.9, types.ChunkSourceType),
					types.KeywordsRetrieverType: tc.ranked,
				},
			}
			g := &storeGroup{
				KBIDs: []string{"kb"}, Engine: buildBoundComposite(t, engine), TopK: 50,
				ImageKBIDs: tc.imageKBs,
				BaseParams: []types.RetrieveParams{
					{RetrieverType: types.VectorRetrieverType},
					{RetrieverType: types.KeywordsRetrieverType},
				},
			}
			res, err := (&knowledgeBaseService{}).retrieveFromStores(
				context.Background(), []*storeGroup{g}, nil)
			require.NoError(t, err)

			var keyword []*types.IndexWithScore
			for _, rr := range res {
				if rr.RetrieverType == types.KeywordsRetrieverType {
					keyword = append(keyword, rr.Results...)
				}
			}
			assert.Len(t, keyword, tc.wantText)
			for _, h := range keyword {
				assert.Equal(t, types.ChunkSourceType, h.SourceType)
			}
			assert.Equal(t, tc.wantTopKs, engine.topKs[types.KeywordsRetrieverType])
			assert.Len(t, engine.topKs[types.VectorRetrieverType], 1,
				"a vector pool without dropped rows is not searched again")
		})
	}
}

// flakyRefillEngine answers the first search of each retriever type in full
// and every later one, a refill, as refill says.
type flakyRefillEngine struct {
	rankedEngine
	refill func(rows []*types.IndexWithScore) ([]*types.RetrieveResult, error)
}

func (e *flakyRefillEngine) Retrieve(ctx context.Context, p types.RetrieveParams) ([]*types.RetrieveResult, error) {
	res, err := e.rankedEngine.Retrieve(ctx, p)
	e.mu.Lock()
	calls := len(e.topKs[p.RetrieverType])
	e.mu.Unlock()
	if calls == 1 || err != nil {
		return res, err
	}
	return e.refill(res[0].Results)
}

func TestRetrieveFromStoresKeepsThePoolWhenARefillIsIncomplete(t *testing.T) {
	cases := []struct {
		name   string
		refill func(rows []*types.IndexWithScore) ([]*types.RetrieveResult, error)
	}{
		{
			// The collection holding the text did not answer the larger
			// search: what came back is the image rows alone.
			name: "the refill answers in part",
			refill: func(rows []*types.IndexWithScore) ([]*types.RetrieveResult, error) {
				var images []*types.IndexWithScore
				for _, h := range rows {
					if h.SourceType == types.ImageSourceType {
						images = append(images, h)
					}
				}
				return []*types.RetrieveResult{{
					Results:             images,
					RetrieverEngineType: types.PostgresRetrieverEngineType,
					RetrieverType:       types.VectorRetrieverType,
					Error:               errors.New("collection text: unavailable"),
				}}, nil
			},
		},
		{
			name: "the refill fails",
			refill: func([]*types.IndexWithScore) ([]*types.RetrieveResult, error) {
				return nil, errors.New("store unavailable")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := &flakyRefillEngine{
				rankedEngine: rankedEngine{
					fakeRetrieveEngineService: fakeRetrieveEngineService{
						engineType: types.PostgresRetrieverEngineType,
						support:    []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType},
					},
					ranked: map[types.RetrieverType][]*types.IndexWithScore{
						// A full first pool: 40 stale image rows above 10 text.
						types.VectorRetrieverType: append(
							rankedHits("kb", "image", 40, 0.9, types.ImageSourceType),
							rankedHits("kb", "text", 10, 0.5, types.ChunkSourceType)...),
						types.KeywordsRetrieverType: rankedHits("kb", "kw", 3, 0.9, types.ChunkSourceType),
					},
				},
				refill: tc.refill,
			}
			g := &storeGroup{
				KBIDs: []string{"kb"}, Engine: buildBoundComposite(t, engine), TopK: 50,
				BaseParams: []types.RetrieveParams{
					{RetrieverType: types.VectorRetrieverType},
					{RetrieverType: types.KeywordsRetrieverType},
				},
			}
			res, err := (&knowledgeBaseService{}).retrieveFromStores(
				context.Background(), []*storeGroup{g}, nil)
			require.NoError(t, err)

			var vector []*types.IndexWithScore
			for _, rr := range res {
				if rr.RetrieverType == types.VectorRetrieverType {
					vector = append(vector, rr.Results...)
				}
			}
			assert.Equal(t, ids(rankedHits("kb", "text", 10, 0.5, types.ChunkSourceType)), ids(vector),
				"the text hits of the complete first pool survive an incomplete refill")
			assert.Equal(t, []int{50, 100}, engine.topKs[types.VectorRetrieverType])
		})
	}
}

// Fusion turns Score into a rank-based number; reranking still needs to know
// how close an image's vector actually was, so the engine's similarity is
// recorded before that happens and survives it.
func TestVectorScoreSurvivesFusion(t *testing.T) {
	g := &storeGroup{ImageKBIDs: recalls("kb"), VectorThreshold: 0.3}
	vector := &types.RetrieveResult{RetrieverType: types.VectorRetrieverType, Results: []*types.IndexWithScore{
		hit("text", 0.62, types.ChunkSourceType),
		hit("image", 0.41, types.ImageSourceType),
	}}
	keyword := &types.RetrieveResult{RetrieverType: types.KeywordsRetrieverType, Results: []*types.IndexWithScore{
		hit("text", 7.5, types.ChunkSourceType),
		hit("kw-only", 3.1, types.ChunkSourceType),
	}}
	filterImageHits([]*types.RetrieveResult{vector, keyword}, g)

	fused := fuseOrDeduplicate(context.Background(),
		[][]*types.IndexWithScore{vector.Results}, [][]*types.IndexWithScore{keyword.Results}, nil)
	byID := map[string]*types.IndexWithScore{}
	for _, h := range fused {
		byID[h.ChunkID] = h
	}
	assert.Equal(t, 0.62, byID["text"].VectorScore)
	assert.Equal(t, 0.41, byID["image"].VectorScore)
	assert.NotEqual(t, 0.41, byID["image"].Score, "fusion rewrote Score")
	assert.Zero(t, byID["kw-only"].VectorScore, "a keyword hit has no vector similarity")
}
