package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	embeddingArtifactStage                       = "embedding.vector"
	embeddingArtifactKeyVersion           uint16 = 1
	embeddingArtifactCodecVersion         byte   = 1
	embeddingArtifactNormalizationVersion        = "embedding-text-v1"
	embeddingArtifactHeaderSize                  = 5
)

type embeddingArtifactKeyRequest struct {
	tenantID             uint64
	modelID              string
	modelName            string
	modelRevision        string
	dimensions           int
	normalizationVersion string
	text                 string
}

type embeddingArtifactEmbedder struct {
	inner         embedding.Embedder
	store         interfaces.ProcessingArtifactStore
	tenantID      uint64
	modelRevision string
}

func newEmbeddingArtifactEmbedder(
	inner embedding.Embedder,
	store interfaces.ProcessingArtifactStore,
	tenantID uint64,
	modelRevision string,
) embedding.Embedder {
	if inner == nil {
		return nil
	}
	return &embeddingArtifactEmbedder{
		inner:         inner,
		store:         store,
		tenantID:      tenantID,
		modelRevision: modelRevision,
	}
}

func normalizeEmbeddingArtifactText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimSpace(text)
}

func newEmbeddingArtifactKey(
	request embeddingArtifactKeyRequest,
) (types.ProcessingArtifactKey, string, error) {
	if request.dimensions <= 0 {
		return types.ProcessingArtifactKey{}, "", errors.New("embedding artifact dimensions must be positive")
	}
	if strings.TrimSpace(request.normalizationVersion) == "" {
		return types.ProcessingArtifactKey{}, "", errors.New("embedding artifact normalization version must not be empty")
	}
	if strings.TrimSpace(request.modelRevision) == "" {
		return types.ProcessingArtifactKey{}, "", errors.New("embedding artifact model revision must not be empty")
	}

	normalized := normalizeEmbeddingArtifactText(request.text)
	key, err := types.NewProcessingArtifactKey(
		request.tenantID,
		embeddingArtifactStage,
		embeddingArtifactKeyVersion,
		[]byte(request.modelID),
		[]byte(request.modelName),
		[]byte(request.modelRevision),
		[]byte(strconv.Itoa(request.dimensions)),
		[]byte(request.normalizationVersion),
		[]byte(normalized),
	)
	return key, normalized, err
}

func embeddingModelRevision(model *types.Model) (string, error) {
	if model == nil {
		return "", errors.New("embedding artifact model must not be nil")
	}
	endpoint, err := url.Parse(model.Parameters.BaseURL)
	if err != nil {
		return "", errors.New("invalid embedding artifact endpoint URL")
	}
	endpoint.User = nil
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	endpoint.Scheme = strings.ToLower(endpoint.Scheme)
	endpoint.Host = strings.ToLower(endpoint.Host)
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")

	params := model.Parameters.EmbeddingParameters
	extra := model.Parameters.ExtraConfig
	descriptor := strings.Join([]string{
		model.ID,
		model.Name,
		strings.ToLower(string(model.Source)),
		strings.ToLower(strings.TrimSpace(model.Parameters.Provider)),
		endpoint.String(),
		strconv.Itoa(params.Dimension),
		strconv.Itoa(params.TruncatePromptTokens),
		strconv.FormatBool(params.SupportsDimensionOverride),
		extra["api_version"],
		strings.TrimSpace(extra["remote_model_name"]),
		model.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	digest := sha256.Sum256([]byte(descriptor))
	return hex.EncodeToString(digest[:]), nil
}

func encodeEmbeddingVector(vector []float32) ([]byte, error) {
	if uint64(len(vector)) > uint64(^uint32(0)) {
		return nil, errors.New("embedding artifact vector is too large")
	}
	payload := make([]byte, embeddingArtifactHeaderSize+len(vector)*4)
	payload[0] = embeddingArtifactCodecVersion
	binary.BigEndian.PutUint32(payload[1:embeddingArtifactHeaderSize], uint32(len(vector)))
	for i, value := range vector {
		binary.BigEndian.PutUint32(payload[embeddingArtifactHeaderSize+i*4:], math.Float32bits(value))
	}
	return payload, nil
}

func decodeEmbeddingVector(payload []byte, expectedDimensions int) ([]float32, error) {
	if len(payload) < embeddingArtifactHeaderSize {
		return nil, errors.New("invalid embedding artifact payload")
	}
	if payload[0] != embeddingArtifactCodecVersion {
		return nil, fmt.Errorf("unsupported embedding artifact codec version %d", payload[0])
	}
	count := uint64(binary.BigEndian.Uint32(payload[1:embeddingArtifactHeaderSize]))
	if count > uint64((int(^uint(0)>>1)-embeddingArtifactHeaderSize)/4) ||
		uint64(len(payload)) != uint64(embeddingArtifactHeaderSize)+count*4 {
		return nil, errors.New("invalid embedding artifact payload length")
	}
	if expectedDimensions > 0 && count != uint64(expectedDimensions) {
		return nil, fmt.Errorf("embedding artifact has %d dimensions, expected %d", count, expectedDimensions)
	}

	vector := make([]float32, int(count))
	for i := range vector {
		vector[i] = math.Float32frombits(binary.BigEndian.Uint32(payload[embeddingArtifactHeaderSize+i*4:]))
	}
	return vector, nil
}

func (e *embeddingArtifactEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if e.store == nil || e.inner.GetDimensions() <= 0 || !isDocumentEmbedding(ctx) {
		return e.inner.Embed(ctx, text)
	}

	key, normalized, err := e.key(text)
	if err != nil {
		return nil, err
	}
	if vector, hit, err := e.load(ctx, key); err != nil || hit {
		return vector, err
	}

	vector, err := e.inner.Embed(ctx, normalized)
	if err != nil {
		return nil, err
	}
	if err := e.validateVector(vector); err != nil {
		return nil, err
	}
	return e.freeze(ctx, key, vector)
}

func (e *embeddingArtifactEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.batchEmbed(ctx, texts, e.inner.BatchEmbed)
}

func (e *embeddingArtifactEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	provider := func(ctx context.Context, misses []string) ([][]float32, error) {
		return e.inner.BatchEmbedWithPool(ctx, e.inner, misses)
	}
	return e.batchEmbed(ctx, texts, provider)
}

func (e *embeddingArtifactEmbedder) batchEmbed(
	ctx context.Context,
	texts []string,
	provider func(context.Context, []string) ([][]float32, error),
) ([][]float32, error) {
	if e.store == nil || e.inner.GetDimensions() <= 0 || !isDocumentEmbedding(ctx) {
		return provider(ctx, texts)
	}
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	keys := make([]types.ProcessingArtifactKey, len(texts))
	uniqueIndexes := make(map[types.ProcessingArtifactKey]int, len(texts))
	uniqueKeys := make([]types.ProcessingArtifactKey, 0, len(texts))
	uniqueTexts := make([]string, 0, len(texts))

	for i, text := range texts {
		key, normalized, err := e.key(text)
		if err != nil {
			return nil, err
		}
		keys[i] = key
		if _, exists := uniqueIndexes[key]; exists {
			continue
		}

		uniqueIndex := len(uniqueKeys)
		uniqueIndexes[key] = uniqueIndex
		uniqueKeys = append(uniqueKeys, key)
		uniqueTexts = append(uniqueTexts, normalized)
	}

	cached, err := e.loadMany(ctx, uniqueKeys)
	if err != nil {
		return nil, err
	}
	uniqueVectors := make([][]float32, len(uniqueKeys))
	missIndexes := make([]int, 0, len(uniqueKeys))
	for i, key := range uniqueKeys {
		if vector, hit := cached[key]; hit {
			uniqueVectors[i] = vector
		} else {
			missIndexes = append(missIndexes, i)
		}
	}

	if len(missIndexes) > 0 {
		missTexts := make([]string, len(missIndexes))
		for i, uniqueIndex := range missIndexes {
			missTexts[i] = uniqueTexts[uniqueIndex]
		}
		missVectors, err := provider(ctx, missTexts)
		if err != nil {
			return nil, err
		}
		if len(missVectors) != len(missIndexes) {
			return nil, fmt.Errorf(
				"embedding provider returned %d embeddings for %d inputs",
				len(missVectors), len(missIndexes),
			)
		}
		for _, vector := range missVectors {
			if err := e.validateVector(vector); err != nil {
				return nil, err
			}
		}
		misses := make(map[types.ProcessingArtifactKey][]float32, len(missIndexes))
		for i, uniqueIndex := range missIndexes {
			misses[uniqueKeys[uniqueIndex]] = missVectors[i]
		}
		canonical, err := e.freezeMany(ctx, misses)
		if err != nil {
			return nil, err
		}
		for _, uniqueIndex := range missIndexes {
			uniqueVectors[uniqueIndex] = canonical[uniqueKeys[uniqueIndex]]
		}
	}

	result := make([][]float32, len(texts))
	for i, key := range keys {
		result[i] = append([]float32(nil), uniqueVectors[uniqueIndexes[key]]...)
	}
	return result, nil
}

func isDocumentEmbedding(ctx context.Context) bool {
	document, _ := ctx.Value(types.EmbedDocumentContextKey).(bool)
	query, _ := ctx.Value(types.EmbedQueryContextKey).(bool)
	return document && !query
}

func (e *embeddingArtifactEmbedder) key(text string) (types.ProcessingArtifactKey, string, error) {
	return newEmbeddingArtifactKey(embeddingArtifactKeyRequest{
		tenantID:             e.tenantID,
		modelID:              e.inner.GetModelID(),
		modelName:            e.inner.GetModelName(),
		modelRevision:        e.modelRevision,
		dimensions:           e.inner.GetDimensions(),
		normalizationVersion: embeddingArtifactNormalizationVersion,
		text:                 text,
	})
}

func (e *embeddingArtifactEmbedder) load(
	ctx context.Context,
	key types.ProcessingArtifactKey,
) ([]float32, bool, error) {
	payload, hit, err := e.store.Get(ctx, key)
	if err != nil || !hit {
		return nil, hit, err
	}
	vector, err := decodeEmbeddingVector(payload, e.inner.GetDimensions())
	if err == nil {
		err = e.validateVector(vector)
	}
	return vector, true, err
}

func (e *embeddingArtifactEmbedder) loadMany(
	ctx context.Context,
	keys []types.ProcessingArtifactKey,
) (map[types.ProcessingArtifactKey][]float32, error) {
	result := make(map[types.ProcessingArtifactKey][]float32, len(keys))
	batchStore, ok := e.store.(interfaces.ProcessingArtifactBatchStore)
	if !ok {
		for _, key := range keys {
			vector, hit, err := e.load(ctx, key)
			if err != nil {
				return nil, err
			}
			if hit {
				result[key] = vector
			}
		}
		return result, nil
	}

	payloads, err := batchStore.GetMany(ctx, keys)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		payload, hit := payloads[key]
		if !hit {
			continue
		}
		vector, err := decodeEmbeddingVector(payload, e.inner.GetDimensions())
		if err == nil {
			err = e.validateVector(vector)
		}
		if err != nil {
			return nil, err
		}
		result[key] = vector
	}
	return result, nil
}

func (e *embeddingArtifactEmbedder) freeze(
	ctx context.Context,
	key types.ProcessingArtifactKey,
	vector []float32,
) ([]float32, error) {
	payload, err := encodeEmbeddingVector(vector)
	if err != nil {
		return nil, err
	}
	canonical, _, err := e.store.PutIfAbsent(ctx, key, payload)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeEmbeddingVector(canonical, e.inner.GetDimensions())
	if err != nil {
		return nil, err
	}
	if err := e.validateVector(decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func (e *embeddingArtifactEmbedder) freezeMany(
	ctx context.Context,
	vectors map[types.ProcessingArtifactKey][]float32,
) (map[types.ProcessingArtifactKey][]float32, error) {
	result := make(map[types.ProcessingArtifactKey][]float32, len(vectors))
	batchStore, ok := e.store.(interfaces.ProcessingArtifactBatchStore)
	if !ok {
		for key, vector := range vectors {
			canonical, err := e.freeze(ctx, key, vector)
			if err != nil {
				return nil, err
			}
			result[key] = canonical
		}
		return result, nil
	}

	payloads := make(map[types.ProcessingArtifactKey][]byte, len(vectors))
	for key, vector := range vectors {
		payload, err := encodeEmbeddingVector(vector)
		if err != nil {
			return nil, err
		}
		payloads[key] = payload
	}
	canonical, err := batchStore.PutManyIfAbsent(ctx, payloads)
	if err != nil {
		return nil, err
	}
	for key := range vectors {
		payload, ok := canonical[key]
		if !ok {
			return nil, errors.New("processing artifact batch store omitted a canonical embedding")
		}
		vector, err := decodeEmbeddingVector(payload, e.inner.GetDimensions())
		if err == nil {
			err = e.validateVector(vector)
		}
		if err != nil {
			return nil, err
		}
		result[key] = vector
	}
	return result, nil
}

func (e *embeddingArtifactEmbedder) validateVector(vector []float32) error {
	if dimensions := e.inner.GetDimensions(); dimensions > 0 && len(vector) != dimensions {
		return fmt.Errorf("embedding provider returned %d dimensions, expected %d", len(vector), dimensions)
	}
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return errors.New("embedding provider returned a non-finite vector value")
		}
	}
	return nil
}

func (e *embeddingArtifactEmbedder) GetModelName() string { return e.inner.GetModelName() }
func (e *embeddingArtifactEmbedder) GetDimensions() int   { return e.inner.GetDimensions() }
func (e *embeddingArtifactEmbedder) GetModelID() string   { return e.inner.GetModelID() }
