package retriever

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	cachedEmbeddingImageTag       = regexp.MustCompile(`(?i)<(?:image|img)(?:\s(?:[^"'<>]|"[^"]*"|'[^']*')*)?/?>`)
	cachedEmbeddingImageAttribute = regexp.MustCompile(
		`(\s+)([^\s"'<>/=]+)(\s*=\s*)("[^"]*"|'[^']*'|[^\s"'=` + "`" + `<>]+)`,
	)
)

func maskEmbeddingImageDestinations(content string) string {
	return cachedEmbeddingImageTag.ReplaceAllStringFunc(content, func(tag string) string {
		// Match whole attributes so src= inside a title is never treated as a destination.
		return cachedEmbeddingImageAttribute.ReplaceAllStringFunc(tag, func(attribute string) string {
			parts := cachedEmbeddingImageAttribute.FindStringSubmatchIndex(attribute)
			name := attribute[parts[4]:parts[5]]
			if !strings.EqualFold(name, "src") && !strings.EqualFold(name, "url") {
				return attribute
			}
			start, end := parts[8], parts[9]
			if attribute[start] == '"' || attribute[start] == '\'' {
				start++
				end--
			}
			return attribute[:start] + "image" + attribute[end:]
		})
	})
}

// Text embeddings consume image descriptions, not storage locations. Normalize
// the actual provider input as well as the key, retaining alt/caption/OCR text.
// Pixel embeddings remain on their separate precomputed path.
func (v *KeywordsVectorHybridRetrieveEngineService) embeddingText(ctx context.Context, content string) string {
	if v.processingCache.Enabled() {
		content = docparser.MaskMarkdownImageDestinations(content, "image")
		content = maskEmbeddingImageDestinations(content)
	}
	return sanitizeForEmbedding(ctx, content)
}

// SetProcessingCache attaches the optional runtime before the engine is used.
func (v *KeywordsVectorHybridRetrieveEngineService) SetProcessingCache(cache *processingcache.Runtime) {
	v.processingCache = cache
}

// embedTextWindow caches only model outputs. Row identity is always supplied by
// the current indexing operation, and windows retain the existing memory bound.
func (v *KeywordsVectorHybridRetrieveEngineService) embedTextWindow(
	ctx context.Context,
	model embedding.Embedder,
	rows []*types.IndexInfo,
	texts []string,
	single bool,
) ([][]float32, error) {
	compute := func(ctx context.Context, texts []string) ([][]float32, error) {
		if single {
			vector, err := model.Embed(ctx, texts[0])
			return [][]float32{vector}, err
		}
		return batchEmbedWithBackoff(ctx, model, texts)
	}
	tenant, ok := types.TenantIDFromContext(ctx)
	identityModel, hasIdentity := model.(interface{ ProcessingCacheIdentity() string })
	_, precomputed := model.(*precomputedEmbedder)
	if v.processingCache == nil || !ok || tenant == 0 || !hasIdentity || precomputed {
		return compute(ctx, texts)
	}
	identity := identityModel.ProcessingCacheIdentity()
	dimension := model.GetDimensions()
	if identity == "" || dimension <= 0 {
		return compute(ctx, texts)
	}
	validate := func(payload []byte) error { _, err := decodeCachedVector(payload, dimension); return err }
	// Use first occurrence order, preserving stable calls and restoring mixed
	// document windows to their original positions after scope-local deduplication.
	groups := map[string][]int{}
	scopes := []string{}
	for i, row := range rows {
		if _, exists := groups[row.KnowledgeID]; !exists {
			scopes = append(scopes, row.KnowledgeID)
		}
		groups[row.KnowledgeID] = append(groups[row.KnowledgeID], i)
	}
	out := make([][]float32, len(texts))
	for _, scope := range scopes {
		positions := groups[scope]
		inputs := make([]any, len(positions))
		scopedTexts := make([]string, len(positions))
		for i, pos := range positions {
			scopedTexts[i] = texts[pos]
			inputs[i] = struct {
				Model, Text string
				Dimension   int
			}{identity, texts[pos], dimension}
		}
		if scope == "" {
			vectors, err := compute(ctx, scopedTexts)
			if err != nil {
				return nil, err
			}
			if len(vectors) != len(positions) {
				return nil, fmt.Errorf(
					"embedding model returned %d embeddings for %d inputs",
					len(vectors),
					len(positions),
				)
			}
			for i, pos := range positions {
				out[pos] = vectors[i]
			}
			continue
		}
		payloads, stats, err := v.processingCache.BatchShared(
			ctx,
			tenant,
			scope,
			"text_embedding/v1",
			inputs,
			validate,
			func(ctx context.Context, missing []int) ([][]byte, error) {
				missingTexts := make([]string, len(missing))
				for i, pos := range missing {
					missingTexts[i] = scopedTexts[pos]
				}
				vectors, err := compute(ctx, missingTexts)
				if err != nil {
					return nil, err
				}
				if len(vectors) != len(missing) {
					return nil, fmt.Errorf(
						"embedding model returned %d embeddings for %d inputs",
						len(vectors),
						len(missing),
					)
				}
				payloads := make([][]byte, len(vectors))
				for i, vector := range vectors {
					if err := validateVector(vector, dimension); err != nil {
						return nil, err
					}
					payloads[i], err = json.Marshal(vector)
					if err != nil {
						return nil, err
					}
				}
				return payloads, nil
			},
		)
		if err != nil {
			return nil, err
		}
		logger.Infof(
			ctx,
			"text embedding cache: hits=%d misses=%d bypass=%d deduplicated=%d",
			stats.Hits,
			stats.Misses,
			stats.Bypass,
			stats.Deduplicated,
		)
		for i, pos := range positions {
			vector, err := decodeCachedVector(payloads[i], dimension)
			if err != nil {
				return nil, err
			}
			out[pos] = vector
		}
	}
	return out, nil
}

func validateVector(vector []float32, dimension int) error {
	if dimension <= 0 || len(vector) != dimension {
		return fmt.Errorf("embedding dimension %d, expected %d", len(vector), dimension)
	}
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fmt.Errorf("embedding contains non-finite value")
		}
	}
	return nil
}

func decodeCachedVector(payload []byte, dimension int) ([]float32, error) {
	var values []*float32
	if err := json.Unmarshal(payload, &values); err != nil {
		return nil, err
	}
	vector := make([]float32, len(values))
	for i, value := range values {
		if value == nil {
			return nil, fmt.Errorf("embedding contains null value")
		}
		vector[i] = *value
	}
	if err := validateVector(vector, dimension); err != nil {
		return nil, err
	}
	return vector, nil
}
