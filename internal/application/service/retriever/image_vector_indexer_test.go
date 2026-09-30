package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// recordingEmbedder captures everything the indexer hands to the embedding
// model, and can be told to accept or reject image input.
type recordingEmbedder struct {
	embedding.Embedder

	texts  []string
	inputs []embedding.Input

	supportsImage bool
	imageErr      error
}

func (e *recordingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.texts = append(e.texts, text)
	return []float32{1}, nil
}

func (e *recordingEmbedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	e.texts = append(e.texts, texts...)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1}
	}
	return out, nil
}

func (e *recordingEmbedder) BatchEmbedMultimodal(
	_ context.Context, inputs []embedding.Input,
) ([][]float32, error) {
	e.inputs = append(e.inputs, inputs...)
	if !e.supportsImage {
		return nil, embedding.ErrMultimodalUnsupported
	}
	if e.imageErr != nil {
		return nil, e.imageErr
	}
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{2}
	}
	return out, nil
}

func (e *recordingEmbedder) Capabilities() embedding.Capabilities {
	return embedding.CapabilitiesFor(e.supportsImage)
}

func (e *recordingEmbedder) GetModelName() string { return "fake" }

// recordingRepository records what actually reached storage, which is the only
// way to tell "indexed" apart from "silently dropped".
type recordingRepository struct {
	interfaces.RetrieveEngineRepository

	savedIDs   []string
	batchSizes []int
	vectors    map[string][]float32
}

func (r *recordingRepository) init() {
	if r.vectors == nil {
		r.vectors = map[string][]float32{}
	}
}

func (r *recordingRepository) Save(_ context.Context, indexInfo *types.IndexInfo, params map[string]any) error {
	r.init()
	r.savedIDs = append(r.savedIDs, indexInfo.SourceID)
	r.capture(indexInfo, params)
	return nil
}

func (r *recordingRepository) BatchSave(
	_ context.Context, indexInfoList []*types.IndexInfo, params map[string]any,
) error {
	r.init()
	r.batchSizes = append(r.batchSizes, len(indexInfoList))
	for _, info := range indexInfoList {
		r.savedIDs = append(r.savedIDs, info.SourceID)
		r.capture(info, params)
	}
	return nil
}

func (r *recordingRepository) capture(info *types.IndexInfo, params map[string]any) {
	if m, ok := params["embedding"].(map[string][]float32); ok {
		if v, ok := m[info.SourceID]; ok {
			r.vectors[info.SourceID] = v
		}
	}
}

func textIndex(id, content string) *types.IndexInfo {
	return &types.IndexInfo{
		SourceID: id,
		ChunkID:  id,
		Content:  content,
	}
}

func imageIndex(id string) *types.IndexInfo {
	info := textIndex(id, "![image](resource://"+id+")")
	info.Modality = types.EmbeddingModalityImage
	info.ImageBytes = []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic
	return info
}

func TestBatchIndexRoutesImagesToMultimodalPath(t *testing.T) {
	embedder := &recordingEmbedder{supportsImage: true}
	repo := &recordingRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}

	err := service.BatchIndex(context.Background(), embedder,
		[]*types.IndexInfo{textIndex("text-1", "hello"), imageIndex("img-1")},
		[]types.RetrieverType{types.VectorRetrieverType})
	if err != nil {
		t.Fatalf("BatchIndex: %v", err)
	}

	// The text must NOT be sent through the multimodal envelope, and the image
	// must not be flattened into a text embed.
	if len(embedder.texts) != 1 || embedder.texts[0] != "hello" {
		t.Fatalf("text path got %v, want [hello]", embedder.texts)
	}
	if len(embedder.inputs) != 1 || len(embedder.inputs[0].Images) != 1 {
		t.Fatalf("image path got %+v, want one input with one image", embedder.inputs)
	}
	if embedder.inputs[0].Text != "" {
		t.Fatalf("image input should carry no text, got %q", embedder.inputs[0].Text)
	}

	if len(repo.savedIDs) != 2 {
		t.Fatalf("saved %v, want both entries", repo.savedIDs)
	}
	if got := repo.vectors["img-1"]; len(got) != 1 || got[0] != 2 {
		t.Fatalf("image vector = %v, want the multimodal marker [2]", got)
	}
	if got := repo.vectors["text-1"]; len(got) != 1 || got[0] != 1 {
		t.Fatalf("text vector = %v, want the text marker [1]", got)
	}
}

func TestBatchIndexDropsImagesOnTextOnlyModel(t *testing.T) {
	embedder := &recordingEmbedder{supportsImage: false}
	repo := &recordingRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}

	// The whole point of this case: a text-only model must not take the
	// document down with it. Before image support existed this batch indexed
	// two entries; it must still index the text one.
	err := service.BatchIndex(context.Background(), embedder,
		[]*types.IndexInfo{textIndex("text-1", "hello"), imageIndex("img-1")},
		[]types.RetrieverType{types.VectorRetrieverType})
	if err != nil {
		t.Fatalf("BatchIndex: %v", err)
	}
	if len(repo.savedIDs) != 1 || repo.savedIDs[0] != "text-1" {
		t.Fatalf("saved %v, want [text-1]", repo.savedIDs)
	}
}

func TestBatchIndexFailsWhenEntireBatchIsUnembeddableImages(t *testing.T) {
	embedder := &recordingEmbedder{supportsImage: true, imageErr: errors.New("vision endpoint 503")}
	repo := &recordingRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}

	err := service.BatchIndex(context.Background(), embedder,
		[]*types.IndexInfo{imageIndex("img-1")},
		[]types.RetrieverType{types.VectorRetrieverType})
	if err == nil {
		t.Fatal("expected an error when every entry failed, got nil " +
			"(would leave the chunk marked indexed with no vector)")
	}
	if len(repo.savedIDs) != 0 {
		t.Fatalf("nothing should have been saved, got %v", repo.savedIDs)
	}
}

func TestBatchIndexSkipsImagesWithoutVectorRetriever(t *testing.T) {
	embedder := &recordingEmbedder{supportsImage: true}
	repo := &recordingRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}

	err := service.BatchIndex(context.Background(), embedder,
		[]*types.IndexInfo{textIndex("text-1", "hello"), imageIndex("img-1")},
		[]types.RetrieverType{types.KeywordsRetrieverType})
	if err != nil {
		t.Fatalf("BatchIndex: %v", err)
	}
	// An image has nothing to keyword-index. Storing one would inject a row
	// whose only text is a markdown URL into BM25 results.
	if len(repo.savedIDs) != 1 || repo.savedIDs[0] != "text-1" {
		t.Fatalf("saved %v, want [text-1]", repo.savedIDs)
	}
	if len(embedder.texts) != 0 || len(embedder.inputs) != 0 {
		t.Fatalf("no embedding should have been requested: texts=%v inputs=%v", embedder.texts, embedder.inputs)
	}
}

func TestIndexRoutesSingleImageToMultimodalPath(t *testing.T) {
	embedder := &recordingEmbedder{supportsImage: true}
	repo := &recordingRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}

	if err := service.Index(context.Background(), embedder, imageIndex("img-1"),
		[]types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(embedder.inputs) != 1 {
		t.Fatalf("image not routed to multimodal path: %+v", embedder.inputs)
	}
	if got := repo.vectors["img-1"]; len(got) != 1 || got[0] != 2 {
		t.Fatalf("image vector = %v, want [2]", got)
	}
}

func TestIndexSkipsImageWithoutVectorRetriever(t *testing.T) {
	embedder := &recordingEmbedder{supportsImage: true}
	repo := &recordingRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}

	if err := service.Index(context.Background(), embedder, imageIndex("img-1"),
		[]types.RetrieverType{types.KeywordsRetrieverType}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(repo.savedIDs) != 0 {
		t.Fatalf("image should not be stored without a vector retriever, saved %v", repo.savedIDs)
	}
}
