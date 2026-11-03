package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// processingCacheFingerprint conservatively includes the complete configuration.
// Only this digest leaves the model wrapper; credentials and routing headers are
// never persisted in cache inputs.
func processingCacheFingerprint(config Config) string {
	return processingCacheDigest(config)
}

func processingCacheDigest(input any) string {
	material, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(material)
	return hex.EncodeToString(sum[:])
}

func effectiveProcessingCacheIdentity(config Config, model Embedder) string {
	if local, ok := model.(*OllamaEmbedder); ok {
		endpoint := local.ollamaService.BaseURL()
		if endpoint == "" {
			return ""
		}
		return processingCacheDigest(struct {
			Config          Config
			Endpoint, Model string
			Truncate        int
		}{config, endpoint, local.modelName, local.truncatePromptTokens})
	}
	if identified, ok := model.(interface{ ProcessingCacheIdentity() string }); ok {
		return identified.ProcessingCacheIdentity()
	}
	return ""
}

type processingCacheEmbedder struct {
	Embedder
	identity string
}

func (e *processingCacheEmbedder) ProcessingCacheIdentity() string { return e.identity }
func (e *processingCacheEmbedder) AcceptsImages() bool {
	_, ok := AsImageEmbedder(e.Embedder)
	return ok
}
func (e *processingCacheEmbedder) ImageLimits() ImageLimits { return imageLimitsOf(e.Embedder) }
func (e *processingCacheEmbedder) BatchEmbedImages(ctx context.Context, images []Image) ([][]float32, error) {
	inner, err := imageSide(e.Embedder)
	if err != nil {
		return nil, err
	}
	return inner.BatchEmbedImages(ctx, images)
}

func (e *protocolEmbedder) ProcessingCacheIdentity() string { return e.cacheIdentity }
