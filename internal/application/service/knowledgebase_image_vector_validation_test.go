package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// capabilityEmbedder is the minimum Embedder plus an optional multimodal
// capability. Whether it reports image support is the only thing that varies
// between the cases below, which is exactly what the validation is about.
type capabilityEmbedder struct {
	supportsImage bool
}

func (c *capabilityEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1}, nil
}

func (c *capabilityEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1}
	}
	return out, nil
}

func (c *capabilityEmbedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	return c.BatchEmbed(context.Background(), texts)
}

func (c *capabilityEmbedder) GetModelName() string { return "capability-test-embedder" }
func (c *capabilityEmbedder) GetDimensions() int   { return 4 }
func (c *capabilityEmbedder) GetModelID() string   { return "capability-test-embedder-id" }

func (c *capabilityEmbedder) BatchEmbedMultimodal(
	_ context.Context, inputs []embedding.Input,
) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{1}
	}
	return out, nil
}

func (c *capabilityEmbedder) Capabilities() embedding.Capabilities {
	modalities := []embedding.Modality{types.EmbeddingModalityText}
	if c.supportsImage {
		modalities = append(modalities, types.EmbeddingModalityImage)
	}
	return embedding.Capabilities{
		Modalities:   modalities,
		UnifiedSpace: c.supportsImage,
	}
}

type capabilityModelService struct {
	interfaces.ModelService
	embedder embedding.Embedder
	err      error
}

func (s *capabilityModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.embedder, nil
}

func newImageVectorValidationService(embedder embedding.Embedder, resolveErr error) *knowledgeBaseService {
	return &knowledgeBaseService{
		modelService: &capabilityModelService{embedder: embedder, err: resolveErr},
	}
}

func imageVectorKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:               "kb-image-vector",
		EmbeddingModelID: "model-1",
		IndexingStrategy: types.IndexingStrategy{
			VectorEnabled:      true,
			ImageVectorEnabled: true,
		},
	}
}

// Guard against the silent-failure mode: the KB saves, ingestion drops every
// image with only a log warning, and image search does nothing at all.
func TestImageVectorRejectedWhenModelCannotEmbedImages(t *testing.T) {
	svc := newImageVectorValidationService(&capabilityEmbedder{supportsImage: false}, nil)

	err := svc.validateImageVectorModel(context.Background(), imageVectorKB())
	if err == nil {
		t.Fatal("expected an error when the embedding model cannot embed images")
	}
	if !strings.Contains(err.Error(), "supports images") {
		t.Fatalf("error should explain the missing image support, got: %v", err)
	}
}

func TestImageVectorAcceptedWhenModelSupportsImages(t *testing.T) {
	svc := newImageVectorValidationService(&capabilityEmbedder{supportsImage: true}, nil)

	if err := svc.validateImageVectorModel(context.Background(), imageVectorKB()); err != nil {
		t.Fatalf("expected no error for a multimodal model, got: %v", err)
	}
}

// Keeps the gate from becoming a precondition for every existing KB: only
// those that opt in pay for a model lookup.
func TestImageVectorValidationSkippedWhenDisabled(t *testing.T) {
	// A nil embedder would panic if the code tried to inspect it.
	svc := newImageVectorValidationService(nil, errors.New("must not be called"))

	kb := imageVectorKB()
	kb.IndexingStrategy.ImageVectorEnabled = false
	if err := svc.validateImageVectorModel(context.Background(), kb); err != nil {
		t.Fatalf("disabled image vector must not be validated, got: %v", err)
	}
}

// Image vectors land in the vector collection, so without the vector pipeline
// there is nowhere to put them.
func TestImageVectorRequiresVectorPipeline(t *testing.T) {
	kb := imageVectorKB()
	kb.IndexingStrategy.VectorEnabled = false

	if err := validateIndexingStrategy(kb.IndexingStrategy); err == nil {
		t.Fatal("expected an error when image vector is enabled without vector indexing")
	}
}

// Vector indexing on and image vectors requested, but no model bound to the KB.
func TestImageVectorRequiresEmbeddingModel(t *testing.T) {
	svc := newImageVectorValidationService(&capabilityEmbedder{supportsImage: true}, nil)

	kb := imageVectorKB()
	kb.EmbeddingModelID = ""

	if err := svc.validateImageVectorModel(context.Background(), kb); err == nil {
		t.Fatal("expected an error when no embedding model is configured")
	}
}

// A broken model reference must be reported as such rather than treated as
// "no image support", which sends the operator looking in the wrong place.
func TestImageVectorValidationSurfacesModelResolutionErrors(t *testing.T) {
	svc := newImageVectorValidationService(nil, errors.New("model not found"))

	err := svc.validateImageVectorModel(context.Background(), imageVectorKB())
	if err == nil {
		t.Fatal("expected the model resolution error to be surfaced")
	}
	if !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("error should carry the underlying cause, got: %v", err)
	}
}
