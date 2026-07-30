package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/inferencecache"
	"github.com/Tencent/WeKnora/internal/types"
)

type summaryCacheTestChat struct {
	id   string
	name string
}

func (*summaryCacheTestChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return nil, nil
}

func (*summaryCacheTestChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *summaryCacheTestChat) GetModelName() string { return m.name }
func (m *summaryCacheTestChat) GetModelID() string   { return m.id }

func TestDocumentSummaryCacheKeyLayeredInvalidation(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	model := &summaryCacheTestChat{id: "model-a", name: "chat-a"}
	base := documentSummaryCacheInput{
		Language:            "English",
		Prompt:              "Summarize in English",
		Content:             "stable frozen document content",
		MaxInputChars:       24 * 1024,
		MaxCompletionTokens: 2048,
		Temperature:         0.3,
		Thinking:            false,
	}
	baseKey := documentSummaryCacheKey(ctx, model, base)
	if got := documentSummaryCacheKey(ctx, model, base); got != baseKey {
		t.Fatalf("identical inputs produced different keys: %q != %q", got, baseKey)
	}

	tests := []struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentSummaryCacheInput
	}{
		{
			name:  "tenant",
			ctx:   context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001)),
			model: model,
			input: base,
		},
		{
			name:  "model",
			ctx:   ctx,
			model: &summaryCacheTestChat{id: "model-b", name: "chat-b"},
			input: base,
		},
	}

	changed := base
	changed.Content = "changed document content"
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentSummaryCacheInput
	}{"content", ctx, model, changed})
	changed = base
	changed.Prompt = "A revised summary prompt"
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentSummaryCacheInput
	}{"prompt", ctx, model, changed})
	changed = base
	changed.Language = "Chinese"
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentSummaryCacheInput
	}{"language", ctx, model, changed})
	changed = base
	changed.MaxInputChars++
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentSummaryCacheInput
	}{"max input", ctx, model, changed})
	changed = base
	changed.MaxCompletionTokens++
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentSummaryCacheInput
	}{"max completion", ctx, model, changed})
	changed = base
	changed.Temperature = 0.4
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentSummaryCacheInput
	}{"temperature", ctx, model, changed})
	changed = base
	changed.Thinking = true
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentSummaryCacheInput
	}{"thinking", ctx, model, changed})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := documentSummaryCacheKey(tt.ctx, tt.model, tt.input); got == baseKey {
				t.Fatalf("%s change did not invalidate summary cache key", tt.name)
			}
		})
	}

	firstImage := base
	firstImage.Content = "diagram ![architecture](local://exports/random-a.png)\n<image_caption>system layout</image_caption>"
	secondImage := firstImage
	secondImage.Content = "diagram ![architecture](local://exports/random-b.png)\n<image_caption>system layout</image_caption>"
	if firstKey, secondKey := documentSummaryCacheKey(ctx, model, firstImage), documentSummaryCacheKey(ctx, model, secondImage); firstKey != secondKey {
		t.Fatalf("random image URL changed summary cache key: %q != %q", firstKey, secondKey)
	}
	secondImage.Content = "diagram ![architecture](local://exports/random-b.png)\n<image_caption>changed layout</image_caption>"
	if documentSummaryCacheKey(ctx, model, firstImage) == documentSummaryCacheKey(ctx, model, secondImage) {
		t.Fatal("semantic image caption change did not invalidate summary cache key")
	}
}

func TestResolveDocumentSummaryValueCachesOnlySuccessfulNonEmptyResults(t *testing.T) {
	t.Setenv("WEKNORA_INFERENCE_CACHE_ENABLED", "true")
	cache := inferencecache.New(nil)
	ctx := context.Background()

	successCalls := 0
	loader := func(context.Context) (string, error) {
		successCalls++
		return "cached summary", nil
	}
	first, firstStats, err := resolveDocumentSummaryValue(ctx, cache, "summary-success", loader)
	if err != nil {
		t.Fatalf("first resolve failed: %v", err)
	}
	if first != "cached summary" || firstStats.Hit {
		t.Fatalf("unexpected first resolve: value=%q hit=%v", first, firstStats.Hit)
	}
	second, secondStats, err := resolveDocumentSummaryValue(ctx, cache, "summary-success", loader)
	if err != nil {
		t.Fatalf("second resolve failed: %v", err)
	}
	if second != first || !secondStats.Hit || successCalls != 1 {
		t.Fatalf("summary was not reused: value=%q hit=%v calls=%d", second, secondStats.Hit, successCalls)
	}

	providerErr := errors.New("provider unavailable")
	errorCalls := 0
	errorLoader := func(context.Context) (string, error) {
		errorCalls++
		return "", providerErr
	}
	for i := 0; i < 2; i++ {
		if _, _, err := resolveDocumentSummaryValue(ctx, cache, "summary-error", errorLoader); !errors.Is(err, providerErr) {
			t.Fatalf("attempt %d: expected provider error, got %v", i+1, err)
		}
	}
	if errorCalls != 2 {
		t.Fatalf("provider errors were cached: calls=%d", errorCalls)
	}

	emptyCalls := 0
	emptyLoader := func(context.Context) (string, error) {
		emptyCalls++
		return "   ", nil
	}
	for i := 0; i < 2; i++ {
		if _, _, err := resolveDocumentSummaryValue(ctx, cache, "summary-empty", emptyLoader); !errors.Is(err, errEmptyDocumentSummary) {
			t.Fatalf("attempt %d: expected empty-summary error, got %v", i+1, err)
		}
	}
	if emptyCalls != 2 {
		t.Fatalf("empty summaries were cached: calls=%d", emptyCalls)
	}
}
