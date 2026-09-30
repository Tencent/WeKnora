package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// recordingBatchIndexer captures what the write path handed to the engine.
type recordingBatchIndexer struct {
	got []*types.IndexInfo
	err error
}

func (r *recordingBatchIndexer) BatchIndex(
	_ context.Context, _ embedding.Embedder, indexInfoList []*types.IndexInfo,
) error {
	r.got = indexInfoList
	return r.err
}

func envelopeTestInfos() []*types.IndexInfo {
	return []*types.IndexInfo{
		{ID: "text-1", Content: "一段说明文字", SourceID: "text-1"},
		{ID: "text-2", Content: "另一段说明文字", SourceID: "text-2"},
		{
			ID: "image-1", SourceID: "image-1", Content: "![image](https://example.test/red.png)",
			Modality: types.EmbeddingModalityImage, ImageBytes: []byte("png"),
		},
	}
}

func kbWithIndexing(strategy types.IndexingStrategy) *types.KnowledgeBase {
	return &types.KnowledgeBase{IndexingStrategy: strategy}
}

// Direct guard for the envelope mismatch: a KB that stores image vectors must
// encode its TEXT chunks through the same envelope as its images and queries.
// Before this was centralised each call site decided, and text used `input`.
func TestIndexChunksWithEnvelopeStampsEveryEntry(t *testing.T) {
	kb := kbWithIndexing(types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true})
	engine := &recordingBatchIndexer{}
	model := &capabilityEmbedder{supportsImage: true}

	require.NoError(t, indexChunksWithEnvelope(context.Background(), engine, kb, model, envelopeTestInfos()))

	require.Len(t, engine.got, 3, "every entry must reach the engine")
	for _, info := range engine.got {
		require.True(t, info.MultimodalEnvelope,
			"entry %s is missing the multimodal envelope stamp; its vector would land in a "+
				"space no query can reach", info.ID)
	}
}

func TestIndexChunksWithEnvelopeLeavesEntriesAloneWhenImageVectorOff(t *testing.T) {
	cases := map[string]*types.KnowledgeBase{
		// The feature is off, so nothing changes from the pre-existing behavior.
		"image vector disabled": kbWithIndexing(types.IndexingStrategy{VectorEnabled: true}),
		// Guards NeedsImageVector's VectorEnabled precondition: a KB with no
		// vector index at all has no collection to keep consistent.
		"vector disabled":    kbWithIndexing(types.IndexingStrategy{ImageVectorEnabled: true}),
		"nil knowledge base": nil,
	}

	for name, kb := range cases {
		t.Run(name, func(t *testing.T) {
			engine := &recordingBatchIndexer{}
			model := &capabilityEmbedder{supportsImage: true}

			require.NoError(t, indexChunksWithEnvelope(context.Background(), engine, kb, model, envelopeTestInfos()))

			require.Len(t, engine.got, 3)
			for _, info := range engine.got {
				require.False(t, info.MultimodalEnvelope,
					"entry %s must not be stamped when the KB has no image vectors", info.ID)
			}
		})
	}
}

// Covers the drift the predicate exists to prevent: image vectors enabled but
// the model cannot encode images, so the query side stays on the plain envelope
// and indexing must stay there with it. Stamping — i.e. only checking
// NeedsImageVector — would split text from queries; the KB degrades to "no
// image vectors" instead, which is coherent.
func TestIndexChunksWithEnvelopeFallsBackWithATextOnlyModel(t *testing.T) {
	kb := kbWithIndexing(types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true})
	engine := &recordingBatchIndexer{}
	model := &capabilityEmbedder{supportsImage: false}

	require.NoError(t, indexChunksWithEnvelope(context.Background(), engine, kb, model, envelopeTestInfos()))

	require.Len(t, engine.got, 3)
	for _, info := range engine.got {
		require.False(t, info.MultimodalEnvelope,
			"entry %s must use the plain envelope while the model has no image support", info.ID)
	}
}

func TestIndexChunksWithEnvelopePropagatesEngineError(t *testing.T) {
	kb := kbWithIndexing(types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true})
	boom := errors.New("vector store unavailable")
	engine := &recordingBatchIndexer{err: boom}

	model := &capabilityEmbedder{supportsImage: true}

	require.ErrorIs(t, indexChunksWithEnvelope(context.Background(), engine, kb, model, envelopeTestInfos()), boom)
}

// newEnvelopeKnowledgeService builds the minimum knowledgeService needed to
// compare two knowledge bases' envelopes.
func newEnvelopeKnowledgeService(embedder embedding.Embedder, resolveErr error) *knowledgeService {
	return &knowledgeService{
		modelService: &capabilityModelService{embedder: embedder, err: resolveErr},
	}
}

func envelopeKB(id, modelID string, strategy types.IndexingStrategy) *types.KnowledgeBase {
	return &types.KnowledgeBase{ID: id, EmbeddingModelID: modelID, IndexingStrategy: strategy}
}

var (
	envelopeOn  = types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true}
	envelopeOff = types.IndexingStrategy{VectorEnabled: true}
)

// Guards the knowledge move: "reuse vectors" copies vectors verbatim, so
// moving from a multimodal KB into a text-only one leaves them in a space the
// target cannot query.
func TestEnvelopesCompatibleReusesVectorsOnlyWhenBothSidesAgree(t *testing.T) {
	ctx := context.Background()
	model := &capabilityEmbedder{supportsImage: true}

	t.Run("identical strategy and model short-circuit without a lookup", func(t *testing.T) {
		// A failing model service proves the fast path skipped it.
		svc := newEnvelopeKnowledgeService(nil, errors.New("must not be called"))
		a := envelopeKB("kb-a", "model-1", envelopeOn)
		b := envelopeKB("kb-b", "model-1", envelopeOn)

		ok, err := svc.envelopesCompatible(ctx, a, b)
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("image vector on vs off", func(t *testing.T) {
		svc := newEnvelopeKnowledgeService(model, nil)
		a := envelopeKB("kb-a", "model-1", envelopeOn)
		b := envelopeKB("kb-b", "model-2", envelopeOff)

		ok, err := svc.envelopesCompatible(ctx, a, b)
		require.NoError(t, err)
		require.False(t, ok, "moving out of a multimodal KB must not reuse its vectors verbatim")
	})

	t.Run("text-only model cancels the image vector flag on both sides", func(t *testing.T) {
		// Both KBs ask for image vectors, but the model cannot encode images,
		// so both fall back to the plain envelope and stay compatible.
		svc := newEnvelopeKnowledgeService(&capabilityEmbedder{supportsImage: false}, nil)
		a := envelopeKB("kb-a", "model-1", envelopeOn)
		b := envelopeKB("kb-b", "model-2", envelopeOff)

		ok, err := svc.envelopesCompatible(ctx, a, b)
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("nil knowledge base", func(t *testing.T) {
		svc := newEnvelopeKnowledgeService(model, nil)
		ok, err := svc.envelopesCompatible(ctx, nil, envelopeKB("kb-b", "model-1", envelopeOn))
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("model resolution failure propagates", func(t *testing.T) {
		svc := newEnvelopeKnowledgeService(nil, errors.New("model service down"))
		a := envelopeKB("kb-a", "model-1", envelopeOn)
		b := envelopeKB("kb-b", "model-2", envelopeOff)

		_, err := svc.envelopesCompatible(ctx, a, b)
		require.Error(t, err)
	})
}
