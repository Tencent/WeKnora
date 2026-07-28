package embedding

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/url"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	embeddingArtifactStage        = "embedding"
	embeddingArtifactKeyVersion   = uint16(1)
	embeddingArtifactSchemaPrefix = "embedding.float32"
)

type ArtifactCacheConfig struct {
	TenantID             uint64
	Processor            artifact.ProcessorIdentity
	Dimensions           int
	TruncatePromptTokens int
}

type artifactCachedEmbedder struct {
	inner   Embedder
	runtime *artifact.Runtime
	config  ArtifactCacheConfig
}

// ArtifactCacheConfigFromModel builds an effective identity without credentials.
// Unknown extra configuration and custom headers disable caching because their
// effect and secrecy cannot be classified safely.
func ArtifactCacheConfigFromModel(model *types.Model, tenantID uint64) (ArtifactCacheConfig, bool) {
	if model == nil || tenantID == 0 {
		return ArtifactCacheConfig{}, false
	}
	if len(model.Parameters.CustomHeaders) > 0 {
		return ArtifactCacheConfig{}, false
	}
	endpoint := model.Parameters.BaseURL
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return ArtifactCacheConfig{}, false
		}
	}
	extra := make(map[string]any)
	for key, value := range model.Parameters.ExtraConfig {
		switch key {
		case "api_version", "remote_model_name":
			extra[key] = value
		default:
			return ArtifactCacheConfig{}, false
		}
	}
	parameters := map[string]any{
		"dimensions":                  model.Parameters.EmbeddingParameters.Dimension,
		"truncate_prompt_tokens":      model.Parameters.EmbeddingParameters.TruncatePromptTokens,
		"supports_dimension_override": model.Parameters.EmbeddingParameters.SupportsDimensionOverride,
	}
	if len(extra) > 0 {
		parameters["extra_config"] = extra
	}
	return ArtifactCacheConfig{
		TenantID: tenantID,
		Processor: artifact.ProcessorIdentity{
			ModelID:          model.ID,
			ModelName:        model.Name,
			Source:           string(model.Source),
			Provider:         model.Parameters.Provider,
			EndpointIdentity: endpoint,
			Parameters:       parameters,
		},
		Dimensions:           model.Parameters.EmbeddingParameters.Dimension,
		TruncatePromptTokens: model.Parameters.EmbeddingParameters.TruncatePromptTokens,
	}, model.Parameters.EmbeddingParameters.Dimension > 0
}

func NewArtifactCachedEmbedder(
	inner Embedder,
	runtime *artifact.Runtime,
	config ArtifactCacheConfig,
) Embedder {
	if inner == nil || runtime == nil || config.TenantID == 0 || config.Dimensions <= 0 {
		return inner
	}
	return &artifactCachedEmbedder{inner: inner, runtime: runtime, config: config}
}

func (e *artifactCachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if !isDocumentEmbedding(ctx) {
		return e.inner.Embed(ctx, text)
	}
	expected, err := e.expected(text)
	if err != nil {
		return nil, err
	}
	value, err := e.runtime.LoadOrCompute(ctx, expected, func(ctx context.Context) ([]byte, error) {
		vector, err := e.inner.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		return encodeEmbeddingArtifact(vector, e.config.Dimensions)
	})
	if err != nil {
		return nil, err
	}
	return decodeEmbeddingArtifact(value.Payload, e.config.Dimensions)
}

func (e *artifactCachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.batchEmbed(ctx, texts, e.inner.BatchEmbed)
}

func (e *artifactCachedEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ Embedder,
	texts []string,
) ([][]float32, error) {
	return e.batchEmbed(ctx, texts, func(ctx context.Context, missing []string) ([][]float32, error) {
		return e.inner.BatchEmbedWithPool(ctx, e.inner, missing)
	})
}

func (e *artifactCachedEmbedder) batchEmbed(
	ctx context.Context,
	texts []string,
	provider func(context.Context, []string) ([][]float32, error),
) ([][]float32, error) {
	if !isDocumentEmbedding(ctx) {
		return provider(ctx, texts)
	}
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	keys := make([]types.ProcessingArtifactLookup, len(texts))
	unique := make([]artifact.Expected, 0, len(texts))
	uniqueIndex := make(map[types.ProcessingArtifactLookup]int, len(texts))
	uniqueTexts := make([]string, 0, len(texts))
	for index, text := range texts {
		expected, err := e.expected(text)
		if err != nil {
			return nil, err
		}
		key := expected.Key.Lookup
		keys[index] = key
		if _, found := uniqueIndex[key]; found {
			continue
		}
		uniqueIndex[key] = len(unique)
		unique = append(unique, expected)
		uniqueTexts = append(uniqueTexts, text)
	}

	cached := e.runtime.BatchLoad(ctx, unique)
	uniqueVectors := make([][]float32, len(unique))
	missingIndexes := make([]int, 0, len(unique))
	for index, expected := range unique {
		value, found := cached[expected.Key.Lookup]
		if !found {
			missingIndexes = append(missingIndexes, index)
			continue
		}
		vector, err := decodeEmbeddingArtifact(value.Payload, e.config.Dimensions)
		if err != nil {
			missingIndexes = append(missingIndexes, index)
			continue
		}
		uniqueVectors[index] = vector
	}

	if len(missingIndexes) > 0 {
		missingTexts := make([]string, len(missingIndexes))
		for index, uniquePosition := range missingIndexes {
			missingTexts[index] = uniqueTexts[uniquePosition]
		}
		vectors, err := provider(ctx, missingTexts)
		if err != nil {
			return nil, err
		}
		if err := ValidateEmbeddingBatch(vectors, len(missingTexts), e.config.Dimensions); err != nil {
			return nil, err
		}

		candidates := make([]artifact.Candidate, 0, len(missingIndexes))
		for index, uniquePosition := range missingIndexes {
			payload, err := encodeEmbeddingArtifact(vectors[index], e.config.Dimensions)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, artifact.Candidate{
				Expected: unique[uniquePosition],
				Payload:  payload,
			})
		}
		frozen := e.runtime.BatchFreeze(ctx, candidates)
		for index, uniquePosition := range missingIndexes {
			value, found := frozen[unique[uniquePosition].Key.Lookup]
			if !found {
				uniqueVectors[uniquePosition] = append([]float32(nil), vectors[index]...)
				continue
			}
			vector, err := decodeEmbeddingArtifact(value.Payload, e.config.Dimensions)
			if err != nil {
				uniqueVectors[uniquePosition] = append([]float32(nil), vectors[index]...)
				continue
			}
			uniqueVectors[uniquePosition] = vector
		}
	}

	result := make([][]float32, len(texts))
	for index, key := range keys {
		result[index] = append([]float32(nil), uniqueVectors[uniqueIndex[key]]...)
	}
	return result, nil
}

func (e *artifactCachedEmbedder) expected(text string) (artifact.Expected, error) {
	key, err := artifact.BuildKey(e.config.TenantID, artifact.KeyMaterial{
		KeyVersion: embeddingArtifactKeyVersion,
		Stage:      embeddingArtifactStage,
		DirectInputs: []artifact.DirectInput{{
			Role:   "provider_input",
			Digest: artifact.SHA256Hex([]byte(text)),
		}},
		Processor: e.config.Processor,
		RenderedRequest: map[string]any{
			"input": text,
		},
		Options: map[string]any{
			"dimensions":             e.config.Dimensions,
			"truncate_prompt_tokens": e.config.TruncatePromptTokens,
		},
		CanonicalizerVersion: artifact.CanonicalJSONVersion,
		OutputSchemaVersion: fmt.Sprintf(
			"%s.%d.v1",
			embeddingArtifactSchemaPrefix,
			e.config.Dimensions,
		),
	})
	if err != nil {
		return artifact.Expected{}, err
	}
	return artifact.Expected{
		Key:   key,
		Codec: artifact.CodecFloat32BEV1,
		Validate: func(payload []byte) error {
			_, err := decodeEmbeddingArtifact(payload, e.config.Dimensions)
			return err
		},
	}, nil
}

func (e *artifactCachedEmbedder) GetModelName() string {
	return e.inner.GetModelName()
}

func (e *artifactCachedEmbedder) GetDimensions() int {
	return e.inner.GetDimensions()
}

func (e *artifactCachedEmbedder) GetModelID() string {
	return e.inner.GetModelID()
}

func isDocumentEmbedding(ctx context.Context) bool {
	document, _ := ctx.Value(types.EmbedDocumentContextKey).(bool)
	query, _ := ctx.Value(types.EmbedQueryContextKey).(bool)
	return document && !query
}

func encodeEmbeddingArtifact(vector []float32, expectedDimensions int) ([]byte, error) {
	if err := ValidateEmbeddingBatch([][]float32{vector}, 1, expectedDimensions); err != nil {
		return nil, err
	}
	payload := make([]byte, 4+len(vector)*4)
	binary.BigEndian.PutUint32(payload[:4], uint32(len(vector)))
	for index, value := range vector {
		binary.BigEndian.PutUint32(payload[4+index*4:], math.Float32bits(value))
	}
	return payload, nil
}

func decodeEmbeddingArtifact(payload []byte, expectedDimensions int) ([]float32, error) {
	if len(payload) < 4 || (len(payload)-4)%4 != 0 {
		return nil, errors.New("invalid embedding artifact payload length")
	}
	count := int(binary.BigEndian.Uint32(payload[:4]))
	if count != expectedDimensions || len(payload) != 4+count*4 {
		return nil, fmt.Errorf(
			"embedding artifact has %d dimensions, expected %d",
			count,
			expectedDimensions,
		)
	}
	vector := make([]float32, count)
	for index := range vector {
		vector[index] = math.Float32frombits(binary.BigEndian.Uint32(payload[4+index*4:]))
		if math.IsNaN(float64(vector[index])) || math.IsInf(float64(vector[index]), 0) {
			return nil, fmt.Errorf("embedding artifact dimension %d is not finite", index)
		}
	}
	return vector, nil
}

// ValidateEmbeddingBatch prevents short responses, wrong dimensions and
// non-finite values from reaching vector indexing.
func ValidateEmbeddingBatch(vectors [][]float32, expectedCount, expectedDimensions int) error {
	if len(vectors) != expectedCount {
		return fmt.Errorf(
			"embedding provider returned %d vectors for %d inputs",
			len(vectors),
			expectedCount,
		)
	}
	for vectorIndex, vector := range vectors {
		if expectedDimensions > 0 && len(vector) != expectedDimensions {
			return fmt.Errorf(
				"embedding provider vector %d has %d dimensions, expected %d",
				vectorIndex,
				len(vector),
				expectedDimensions,
			)
		}
		for dimension, value := range vector {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return fmt.Errorf(
					"embedding provider vector %d dimension %d is not finite",
					vectorIndex,
					dimension,
				)
			}
		}
	}
	return nil
}
