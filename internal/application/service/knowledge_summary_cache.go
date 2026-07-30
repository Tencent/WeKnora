package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/inferencecache"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

const documentSummaryCacheVersion = "v1"

var errEmptyDocumentSummary = errors.New("summary model returned empty content")

// documentSummaryCacheInput contains every non-model input that can change a
// generated document summary. Content is the final, image-enriched and sampled
// text sent to the provider, so parser, chunking, OCR/caption and sampling
// changes invalidate this layer only when they affect the actual request.
type documentSummaryCacheInput struct {
	Version             string  `json:"version"`
	Language            string  `json:"language"`
	Prompt              string  `json:"prompt"`
	Content             string  `json:"content"`
	MaxInputChars       int     `json:"max_input_chars"`
	MaxCompletionTokens int     `json:"max_completion_tokens"`
	Temperature         float64 `json:"temperature"`
	Thinking            bool    `json:"thinking"`
}

func documentSummaryCacheKey(
	ctx context.Context,
	summaryModel chat.Chat,
	input documentSummaryCacheInput,
) string {
	tenantID, _ := types.TenantIDFromContext(ctx)
	input.Version = documentSummaryCacheVersion
	input.Content = searchutil.CanonicalizeImageURLsForModel(input.Content)
	encoded, _ := json.Marshal(input)
	return inferencecache.Key(
		"document.summary",
		tenantID,
		chat.FingerprintOf(summaryModel),
		[]byte(documentSummaryCacheVersion),
		encoded,
	)
}

// resolveDocumentSummaryValue caches only validated, non-empty provider
// results. Errors and fallback summaries are deliberately excluded. An empty
// or corrupt cached entry is evicted and recomputed once.
func resolveDocumentSummaryValue(
	ctx context.Context,
	cache inferencecache.Cache,
	key string,
	loader func(context.Context) (string, error),
) (string, inferencecache.Stats, error) {
	validatedLoader := func(loadCtx context.Context) ([]byte, error) {
		value, err := loader(loadCtx)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(value) == "" {
			return nil, errEmptyDocumentSummary
		}
		return []byte(value), nil
	}

	if cache == nil {
		raw, err := validatedLoader(ctx)
		return string(raw), inferencecache.Stats{}, err
	}

	raw, stats, err := cache.Resolve(ctx, key, validatedLoader)
	if err != nil {
		return "", stats, err
	}
	if strings.TrimSpace(string(raw)) != "" {
		return string(raw), stats, nil
	}

	// Validated loaders never write an empty value, so this can only be a
	// corrupt or legacy cache entry.
	_ = cache.Invalidate(ctx, key)
	raw, retryStats, err := cache.Resolve(ctx, key, validatedLoader)
	stats.Hit = false
	stats.Coalesced = retryStats.Coalesced
	stats.WriteError = retryStats.WriteError
	if stats.ReadError == nil {
		stats.ReadError = errEmptyDocumentSummary
	}
	if err != nil {
		return "", stats, err
	}
	return string(raw), stats, nil
}
