package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeImageEmbedder records whether the multimodal path was used at all.
type fakeImageEmbedder struct {
	embedding.Embedder
	supportsImage bool
	inputs        []embedding.Input
}

func (e *fakeImageEmbedder) BatchEmbedMultimodal(
	_ context.Context, inputs []embedding.Input,
) ([][]float32, error) {
	e.inputs = append(e.inputs, inputs...)
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{2}
	}
	return out, nil
}

func (e *fakeImageEmbedder) Capabilities() embedding.Capabilities {
	return embedding.CapabilitiesFor(e.supportsImage)
}

func (e *fakeImageEmbedder) GetModelName() string { return "fake" }

// fakeImageVectorIndexer records the IndexInfo that would reach the vector store.
type fakeImageVectorIndexer struct {
	got []*types.IndexInfo
}

func (i *fakeImageVectorIndexer) BatchIndex(
	_ context.Context, _ embedding.Embedder, list []*types.IndexInfo,
) error {
	i.got = append(i.got, list...)
	return nil
}

type recordingChunkService struct {
	interfaces.ChunkService
	created []*types.Chunk
	status  map[string]int
}

func (s *recordingChunkService) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	s.created = append(s.created, chunks...)
	return nil
}

func (s *recordingChunkService) GetChunkByIDOnly(_ context.Context, id string) (*types.Chunk, error) {
	return &types.Chunk{ID: id}, nil
}

func (s *recordingChunkService) UpdateChunk(_ context.Context, chunk *types.Chunk) error {
	if s.status == nil {
		s.status = map[string]int{}
	}
	s.status[chunk.ID] = chunk.Status
	return nil
}

func imageVectorPayload() types.ImageMultimodalPayload {
	return types.ImageMultimodalPayload{
		TenantID:        7,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		ChunkID:         "text-1",
		ImageURL:        "resource://img-1",
	}
}

func TestIndexImageVectorSkippedWhenDisabled(t *testing.T) {
	t.Parallel()
	chunks := &recordingChunkService{}
	indexer := &fakeImageVectorIndexer{}
	svc := &ImageMultimodalService{chunkService: chunks}

	// Default strategy: image vectors off. This is the case that matters most —
	// every existing knowledge base is in this state and must not change.
	svc.indexImageVector(context.Background(), imageVectorPayload(),
		types.ImageInfo{URL: "resource://img-1"}, []byte{0x89},
		&types.KnowledgeBase{ID: "kb-1", IndexingStrategy: types.DefaultIndexingStrategy()},
		&fakeImageEmbedder{supportsImage: true}, indexer)

	if len(chunks.created) != 0 || len(indexer.got) != 0 {
		t.Fatalf("disabled KB should not create or index anything: created=%d indexed=%d",
			len(chunks.created), len(indexer.got))
	}
}

func TestIndexImageVectorSkippedForTextOnlyModel(t *testing.T) {
	t.Parallel()
	chunks := &recordingChunkService{}
	indexer := &fakeImageVectorIndexer{}
	svc := &ImageMultimodalService{chunkService: chunks}

	strategy := types.DefaultIndexingStrategy()
	strategy.ImageVectorEnabled = true

	svc.indexImageVector(context.Background(), imageVectorPayload(),
		types.ImageInfo{URL: "resource://img-1"}, []byte{0x89},
		&types.KnowledgeBase{ID: "kb-1", IndexingStrategy: strategy},
		&fakeImageEmbedder{supportsImage: false}, indexer)

	// The flag being on must not break uploads for a text-only model: the
	// operator gets a warning, not a failed document.
	if len(chunks.created) != 0 || len(indexer.got) != 0 {
		t.Fatalf("text-only model should skip image vectors: created=%d indexed=%d",
			len(chunks.created), len(indexer.got))
	}
}

func TestIndexImageVectorCreatesImageModalityChunk(t *testing.T) {
	t.Parallel()
	chunks := &recordingChunkService{}
	indexer := &fakeImageVectorIndexer{}
	svc := &ImageMultimodalService{chunkService: chunks}

	strategy := types.DefaultIndexingStrategy()
	strategy.ImageVectorEnabled = true
	imgBytes := []byte{0x89, 0x50, 0x4E, 0x47}

	svc.indexImageVector(context.Background(), imageVectorPayload(),
		types.ImageInfo{URL: "resource://img-1", OriginalURL: "resource://img-1"}, imgBytes,
		&types.KnowledgeBase{ID: "kb-1", IndexingStrategy: strategy},
		&fakeImageEmbedder{supportsImage: true}, indexer)

	if len(chunks.created) != 1 {
		t.Fatalf("expected one chunk, got %d", len(chunks.created))
	}
	chunk := chunks.created[0]
	if chunk.ChunkType != types.ChunkTypeImageVector {
		t.Fatalf("chunk type = %q, want %q", chunk.ChunkType, types.ChunkTypeImageVector)
	}
	if chunk.ParentChunkID != "text-1" {
		t.Fatalf("parent chunk = %q, want text-1", chunk.ParentChunkID)
	}
	if !chunk.IsEnabled {
		t.Fatal("image vector chunk must be enabled or retrieval never sees it")
	}
	if chunk.ImageInfo == "" {
		t.Fatal("image vector chunk must carry ImageInfo so results can render the image")
	}

	if len(indexer.got) != 1 {
		t.Fatalf("expected one index entry, got %d", len(indexer.got))
	}
	info := indexer.got[0]
	if !info.IsImage() {
		t.Fatalf("IndexInfo.Modality = %q, want image", info.Modality)
	}
	if string(info.ImageBytes) != string(imgBytes) {
		t.Fatalf("IndexInfo.ImageBytes = %v, want the original image bytes", info.ImageBytes)
	}
	if info.ChunkID != chunk.ID {
		t.Fatalf("index entry chunk id = %q, want %q", info.ChunkID, chunk.ID)
	}
	// Engines store this verbatim and retrieval filters on is_enabled = true,
	// so a zero value here hides the vector even though its chunk is enabled.
	if !info.IsEnabled {
		t.Fatal("index entry must be enabled or retrieval filters the image vector out")
	}
	if chunks.status[chunk.ID] != int(types.ChunkStatusIndexed) {
		t.Fatalf("chunk status = %d, want indexed", chunks.status[chunk.ID])
	}
}

func TestIndexImageVectorSkipsEmptyImage(t *testing.T) {
	t.Parallel()
	chunks := &recordingChunkService{}
	indexer := &fakeImageVectorIndexer{}
	svc := &ImageMultimodalService{chunkService: chunks}

	strategy := types.DefaultIndexingStrategy()
	strategy.ImageVectorEnabled = true

	svc.indexImageVector(context.Background(), imageVectorPayload(),
		types.ImageInfo{URL: "resource://img-1"}, nil,
		&types.KnowledgeBase{ID: "kb-1", IndexingStrategy: strategy},
		&fakeImageEmbedder{supportsImage: true}, indexer)

	if len(chunks.created) != 0 {
		t.Fatalf("no bytes should mean no chunk, got %d", len(chunks.created))
	}
}

// The query side is the other half of the contract: text queries must be
// encoded by the same multimodal path that created the image vectors.
func TestImageVectorQueryUsesMultimodalEnvelope(t *testing.T) {
	t.Parallel()
	embedder := &fakeImageEmbedder{supportsImage: true}

	vector, err := embedding.EmbedMultimodal(context.Background(), embedder,
		embedding.Input{Text: "find the wiring diagram"})
	if err != nil {
		t.Fatalf("EmbedMultimodal: %v", err)
	}
	if len(vector) == 0 {
		t.Fatal("expected a query vector")
	}
	if len(embedder.inputs) != 1 || embedder.inputs[0].Text != "find the wiring diagram" {
		t.Fatalf("query not sent through the multimodal envelope: %+v", embedder.inputs)
	}
	if embedder.inputs[0].HasImage() {
		t.Fatal("a text-only query must not carry an image part")
	}
}

func TestImageVectorStrategyFlags(t *testing.T) {
	t.Parallel()
	base := types.DefaultIndexingStrategy()
	if base.NeedsImageVector() {
		t.Fatal("default strategy must not enable image vectors")
	}
	if base.IsZero() {
		t.Fatal("default strategy is not the zero value")
	}

	on := base
	on.ImageVectorEnabled = true
	if !on.NeedsEmbedding() {
		t.Fatal("image vectors need an embedding model")
	}
	if !on.NeedsChunks() {
		t.Fatal("image vectors are stored as chunks")
	}

	// Image vectors alone are NOT a pipeline: with the vector pipeline off
	// there is nowhere to store them. They must therefore neither satisfy the
	// "at least one strategy" check nor rescue a strategy from being treated
	// as unconfigured — otherwise EnsureDefaults would silently rewrite an
	// invalid submission into a valid-looking default one.
	only := types.IndexingStrategy{ImageVectorEnabled: true}
	if only.HasAnyIndexing() {
		t.Fatal("image vectors alone must not count as an enabled indexing pipeline")
	}
	if !only.IsZero() {
		t.Fatal("image vectors alone must not rescue a strategy from the zero value")
	}
	if only.NeedsImageVector() {
		t.Fatal("image vectors must not be indexed without the vector pipeline")
	}

	// With the vector pipeline on, the flag becomes live.
	withVector := types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true}
	if !withVector.NeedsImageVector() {
		t.Fatal("image vectors should be indexed when the vector pipeline is on")
	}
}
