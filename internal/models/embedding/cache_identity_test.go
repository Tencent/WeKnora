package embedding

import (
	"context"
	"errors"
	"testing"

	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestProcessingCacheFingerprintIncludesSecretsAndHeaders(t *testing.T) {
	a := Config{ModelName: "m", APIKey: "a", CustomHeaders: map[string]string{"x": "a"}}
	first := processingCacheFingerprint(a)
	if len(first) != 64 {
		t.Fatalf("invalid fingerprint %q", first)
	}
	a.APIKey = "b"
	if first == processingCacheFingerprint(a) {
		t.Fatal("credential did not invalidate")
	}
	a.APIKey = "a"
	a.CustomHeaders["x"] = "b"
	if first == processingCacheFingerprint(a) {
		t.Fatal("headers did not invalidate")
	}
}

func TestProcessingCacheFactoryProvidesFullIdentity(t *testing.T) {
	allowLoopback(t)
	config := Config{
		Source:    types.ModelSourceRemote,
		Provider:  "openai",
		ModelName: "text-embedding-3-small",
		APIKey:    "secret",
	}
	first, err := NewEmbedder(config, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	identified, ok := first.(interface{ ProcessingCacheIdentity() string })
	if !ok || len(identified.ProcessingCacheIdentity()) != 64 {
		t.Fatal("outer factory wrapper did not expose complete identity")
	}
	config.BaseURL = "http://127.0.0.1:11434/v1"
	second, err := NewEmbedder(config, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondIdentity := second.(interface{ ProcessingCacheIdentity() string }).ProcessingCacheIdentity()
	if identified.ProcessingCacheIdentity() == secondIdentity {
		t.Fatal("endpoint change did not invalidate")
	}
}

type cacheImageEmbedder struct {
	Embedder
	accepts bool
	calls   int
}

func (e *cacheImageEmbedder) AcceptsImages() bool      { return e.accepts }
func (e *cacheImageEmbedder) ImageLimits() ImageLimits { return ImageLimits{MaxBytes: 123} }
func (e *cacheImageEmbedder) GetModelName() string     { return "image" }
func (e *cacheImageEmbedder) BatchEmbedImages(_ context.Context, _ []Image) ([][]float32, error) {
	e.calls++
	return [][]float32{{42}}, nil
}

func TestProcessingCacheIdentityWrapperPreservesImageSide(t *testing.T) {
	inner := &cacheImageEmbedder{accepts: true}
	outer := &processingCacheEmbedder{Embedder: inner, identity: "x"}
	image, ok := AsImageEmbedder(outer)
	if !ok || image.ImageLimits().MaxBytes != 123 {
		t.Fatal("image capabilities lost")
	}
	vectors, err := image.BatchEmbedImages(context.Background(), []Image{{}})
	if err != nil || inner.calls != 1 || vectors[0][0] != 42 {
		t.Fatal("image call not forwarded")
	}
	inner.accepts = false
	if _, ok := AsImageEmbedder(outer); ok {
		t.Fatal("text model reports image support")
	}
	if _, err := outer.BatchEmbedImages(context.Background(), nil); !errors.Is(err, ErrImagesUnsupported) {
		t.Fatalf("got %v", err)
	}
}

func TestProcessingCacheLocalUsesInjectedEndpoint(t *testing.T) {
	config := Config{
		Source:     types.ModelSourceLocal,
		BaseURL:    "http://ignored:11434",
		ModelName:  "nomic-embed-text",
		Dimensions: 1,
	}
	t.Setenv("OLLAMA_BASE_URL", "http://ollama-a:11434")
	firstService, err := ollama.GetOllamaService()
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewEmbedder(config, nil, firstService)
	if err != nil {
		t.Fatal(err)
	}
	identity := func(e Embedder) string {
		return e.(interface{ ProcessingCacheIdentity() string }).ProcessingCacheIdentity()
	}
	t.Setenv("OLLAMA_BASE_URL", "http://ollama-b:11434")
	secondService, err := ollama.GetOllamaService()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewEmbedder(config, nil, secondService)
	if err != nil {
		t.Fatal(err)
	}
	if identity(first) == identity(second) {
		t.Fatal("injected Ollama endpoint change reused identity")
	}
	oldServiceNewModel, err := NewEmbedder(config, nil, firstService)
	if err != nil {
		t.Fatal(err)
	}
	if identity(first) != identity(oldServiceNewModel) {
		t.Fatal("identity used mutable environment instead of injected service")
	}
	for _, service := range []*ollama.OllamaService{nil, {}} {
		model, err := NewEmbedder(config, nil, service)
		if err != nil {
			t.Fatal(err)
		}
		if identity(model) != "" {
			t.Fatal("missing effective endpoint did not bypass cache")
		}
	}
}

func TestProcessingCacheRemoteEffectiveOverlayInvalidates(t *testing.T) {
	allowLoopback(t)
	snapshot := modelruntime.SnapshotCurrent()
	t.Cleanup(func() { modelruntime.RestoreSnapshot(snapshot) })
	config := Config{
		Source:     types.ModelSourceRemote,
		Provider:   "openai",
		ModelName:  "text-embedding-3-small",
		Dimensions: 1,
	}
	identity := func(e Embedder) string {
		return e.(interface{ ProcessingCacheIdentity() string }).ProcessingCacheIdentity()
	}
	build := func() Embedder {
		t.Helper()
		model, err := NewEmbedder(config, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return model
	}
	first := build()
	firstIdentity := identity(first)
	for _, overlay := range []string{
		`{"providers":{"openai":{"base_urls":{"embedding":"http://127.0.0.1:11434/v1"}}}}`,
		`{"providers":{"openai":{"api_key":"deployment-secret"}}}`,
		`{"providers":{"openai":{"headers":{"X-Route":"deployment-route"}}}}`,
	} {
		modelruntime.RestoreSnapshot(snapshot)
		if err := modelruntime.ApplyOverlay([]byte(overlay), ""); err != nil {
			t.Fatal(err)
		}
		if identity(build()) == firstIdentity {
			t.Fatalf("effective configuration omitted: %s", overlay)
		}
		if identity(first) != firstIdentity {
			t.Fatal("existing model identity changed after overlay")
		}
	}
}
