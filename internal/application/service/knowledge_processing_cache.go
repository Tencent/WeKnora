package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
)

type processingDocumentKey struct{}
type processingBypassKey struct{}

var errProcessingCacheAdmission = errors.New("successful legacy output is incomplete and cannot be cached")

func (s *DataTableSummaryService) tableCachedResponse(ctx context.Context, model chat.Chat, stage string, messages []chat.Message, options *chat.ChatOptions) (*types.ChatResponse, error) {
	tenant, _ := types.TenantIDFromContext(ctx)
	text, err := cachedProcessingModelValue(ctx, s.processingCache, tenant, processingDocument(ctx), stage, model, messages, options, nil, func(v string) error {
		if strings.TrimSpace(v) == "" {
			return errEmptySummaryOutput
		}
		return nil
	}, func(ctx context.Context, messages []chat.Message) (string, error) {
		response, err := model.Chat(ctx, messages, options)
		if err != nil {
			return "", err
		}
		_, err = validateSummaryOutput(response)
		if err != nil {
			return "", err
		}
		if err = completeProcessingResponse(response); err != nil {
			return response.Content, errProcessingCacheAdmission
		}
		return response.Content, nil
	})
	if err != nil {
		return nil, err
	}
	return &types.ChatResponse{Content: text}, nil
}

func withProcessingDocument(ctx context.Context, document string) context.Context {
	return context.WithValue(ctx, processingDocumentKey{}, document)
}

func validateCachedGraph(graph *types.GraphData) error {
	if graph == nil {
		return fmt.Errorf("null extracted graph")
	}
	for _, node := range graph.Node {
		if node == nil || strings.TrimSpace(node.Name) == "" || len(node.Chunks) != 0 {
			return fmt.Errorf("invalid or bound extracted node")
		}
	}
	for _, relation := range graph.Relation {
		if relation == nil || strings.TrimSpace(relation.Node1) == "" || strings.TrimSpace(relation.Node2) == "" {
			return fmt.Errorf("invalid extracted relationship")
		}
	}
	return nil
}

func (s *ChunkExtractService) extractGraphCached(ctx context.Context, model chat.Chat, template *types.PromptTemplateStructured, tenant uint64, document, content string) (*types.GraphData, error) {
	extractor := chatpipeline.NewExtractor(model, template)
	messages, options := extractor.ProcessingRequest(ctx, content)
	ctx = types.WithLLMCallMetadata(ctx, "entity_extraction", "")
	return cachedProcessingModelValue(ctx, s.processingCache, tenant, document, "document.graph.v1", model, messages, options, nil, validateCachedGraph, func(ctx context.Context, messages []chat.Message) (*types.GraphData, error) {
		response, err := model.Chat(ctx, messages, options)
		if err != nil {
			return nil, err
		}
		if err = completeProcessingResponse(response); err != nil {
			return nil, err
		}
		graph, err := chatpipeline.NewFormater().ParseGraph(ctx, response.Content)
		if err != nil {
			if errors.Is(err, chatpipeline.ErrModelDeclined) && strings.TrimSpace(response.FinishReason) == "" {
				return nil, fmt.Errorf("graph response stopped without finish reason: %v", err)
			}
			return nil, err
		}
		if err = validateCachedGraph(graph); err != nil {
			return nil, err
		}
		return graph, nil
	})
}
func withProcessingCacheBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, processingBypassKey{}, true)
}
func processingDocument(ctx context.Context) string {
	v, _ := ctx.Value(processingDocumentKey{}).(string)
	return v
}
func processingCacheBypassed(ctx context.Context) bool {
	v, _ := ctx.Value(processingBypassKey{}).(bool)
	return v
}

func completeProcessingResponse(response *types.ChatResponse) error {
	if response == nil {
		return fmt.Errorf("nil processing response")
	}
	if isLengthStopFinishReason(response.FinishReason) || strings.EqualFold(strings.TrimSpace(response.FinishReason), types.FinishReasonIncomplete) {
		return fmt.Errorf("processing response truncated (finish_reason=%q)", response.FinishReason)
	}
	return nil
}

// These stages consume text, not pixels: image URLs are transport references.
// Use the same deterministic placeholders in the actual request and cache,
// preserving alt/caption/OCR text, then restore the current run's references.
func cachedProcessingModelValue[T any](ctx context.Context, runtime *processingcache.Runtime, tenant uint64, document, stage string, model chat.Chat, messages []chat.Message, options *chat.ChatOptions, behavior any, validate func(T) error, compute func(context.Context, []chat.Message) (T, error)) (T, error) {
	identity := processingModelIdentity(model)
	if !runtime.Enabled() || identity == "" || document == "" || tenant == 0 || processingCacheBypassed(ctx) {
		v, err := compute(ctx, messages)
		if ctx.Err() != nil {
			var zero T
			return zero, ctx.Err()
		}
		if errors.Is(err, errProcessingCacheAdmission) {
			return v, nil
		}
		return v, err
	}
	masked := append([]chat.Message(nil), messages...)
	urlToToken, tokenToURL := map[string]string{}, map[string]string{}
	for i := range masked {
		masked[i].Content = maskImageURLsWithState(masked[i].Content, urlToToken, tokenToURL)
	}
	var original T
	var originalErr error
	value, err := processingcache.Value(ctx, runtime, tenant, document, stage, struct {
		Model    string
		Messages []chat.Message
		Options  *chat.ChatOptions
		Behavior any
	}{identity, masked, options, behavior}, validate, func(ctx context.Context) (T, error) {
		original, originalErr = compute(ctx, masked)
		return original, originalErr
	})
	if ctx.Err() != nil {
		var zero T
		return zero, ctx.Err()
	}
	if errors.Is(err, errProcessingCacheAdmission) && ctx.Err() == nil {
		if !errors.Is(originalErr, errProcessingCacheAdmission) {
			original, originalErr = compute(ctx, masked)
		}
		if ctx.Err() != nil {
			var zero T
			return zero, ctx.Err()
		}
		value, err = original, originalErr
		if errors.Is(err, errProcessingCacheAdmission) {
			err = nil
		}
	}
	if err != nil || len(tokenToURL) == 0 {
		return value, err
	}
	// Walk JSON string values rather than replacing in serialized JSON, so URL
	// escaping can never corrupt a typed result containing quotes or backslashes.
	raw, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	var tree any
	if err = json.Unmarshal(raw, &tree); err != nil {
		return value, err
	}
	var restore func(any) any
	restore = func(v any) any {
		switch v := v.(type) {
		case string:
			return unmaskImageURLs(v, tokenToURL)
		case []any:
			for i := range v {
				v[i] = restore(v[i])
			}
			return v
		case map[string]any:
			for k, x := range v {
				v[k] = restore(x)
			}
			return v
		default:
			return v
		}
	}
	raw, err = json.Marshal(restore(tree))
	if err != nil {
		return value, err
	}
	err = json.Unmarshal(raw, &value)
	return value, err
}
