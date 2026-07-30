package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/inferencecache"
	"github.com/Tencent/WeKnora/internal/types"
)

type questionCacheTestChat struct {
	id   string
	name string
}

func (*questionCacheTestChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return nil, nil
}

func (*questionCacheTestChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *questionCacheTestChat) GetModelName() string { return m.name }
func (m *questionCacheTestChat) GetModelID() string   { return m.id }

func TestDocumentQuestionCacheKeyLayeredInvalidation(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	model := &questionCacheTestChat{id: "model-a", name: "chat-a"}
	base := documentQuestionCacheInput{
		Prompt:        "Generate 3 questions for ![diagram](local://exports/random-a.png)",
		QuestionCount: 3,
		Temperature:   0.7,
		MaxTokens:     512,
		Thinking:      false,
	}
	baseKey := documentQuestionCacheKey(ctx, model, base)

	sameContentNewURL := base
	sameContentNewURL.Prompt = "Generate 3 questions for ![diagram](local://exports/random-b.png)"
	if got := documentQuestionCacheKey(ctx, model, sameContentNewURL); got != baseKey {
		t.Fatalf("random image URL changed question cache key: %q != %q", got, baseKey)
	}

	tests := []struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentQuestionCacheInput
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
			model: &questionCacheTestChat{id: "model-b", name: "chat-b"},
			input: base,
		},
	}

	changed := base
	changed.Prompt = "Generate questions using a revised prompt"
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentQuestionCacheInput
	}{"prompt", ctx, model, changed})
	changed = base
	changed.QuestionCount++
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentQuestionCacheInput
	}{"question count", ctx, model, changed})
	changed = base
	changed.Temperature = 0.8
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentQuestionCacheInput
	}{"temperature", ctx, model, changed})
	changed = base
	changed.MaxTokens++
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentQuestionCacheInput
	}{"max tokens", ctx, model, changed})
	changed = base
	changed.Thinking = true
	tests = append(tests, struct {
		name  string
		ctx   context.Context
		model chat.Chat
		input documentQuestionCacheInput
	}{"thinking", ctx, model, changed})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := documentQuestionCacheKey(tt.ctx, tt.model, tt.input); got == baseKey {
				t.Fatalf("%s change did not invalidate question cache key", tt.name)
			}
		})
	}
}

func TestResolveDocumentQuestionsValueCachesOnlySuccessfulNonEmptyResults(t *testing.T) {
	t.Setenv("WEKNORA_INFERENCE_CACHE_ENABLED", "true")
	cache := inferencecache.New(nil)
	ctx := context.Background()

	successCalls := 0
	loader := func(context.Context) ([]string, error) {
		successCalls++
		return []string{"  First question?  ", "Second question?"}, nil
	}
	first, firstStats, err := resolveDocumentQuestionsValue(ctx, cache, "questions-success", loader)
	if err != nil {
		t.Fatalf("first resolve failed: %v", err)
	}
	if firstStats.Hit || len(first) != 2 || first[0] != "First question?" {
		t.Fatalf("unexpected first resolve: value=%v stats=%+v", first, firstStats)
	}
	second, secondStats, err := resolveDocumentQuestionsValue(ctx, cache, "questions-success", loader)
	if err != nil {
		t.Fatalf("second resolve failed: %v", err)
	}
	if !secondStats.Hit || len(second) != 2 || successCalls != 1 {
		t.Fatalf("questions were not reused: value=%v stats=%+v calls=%d", second, secondStats, successCalls)
	}

	providerErr := errors.New("provider unavailable")
	errorCalls := 0
	errorLoader := func(context.Context) ([]string, error) {
		errorCalls++
		return nil, providerErr
	}
	for i := 0; i < 2; i++ {
		if _, _, err := resolveDocumentQuestionsValue(ctx, cache, "questions-error", errorLoader); !errors.Is(err, providerErr) {
			t.Fatalf("attempt %d: expected provider error, got %v", i+1, err)
		}
	}
	if errorCalls != 2 {
		t.Fatalf("provider errors were cached: calls=%d", errorCalls)
	}

	emptyCalls := 0
	emptyLoader := func(context.Context) ([]string, error) {
		emptyCalls++
		return []string{"", "   "}, nil
	}
	for i := 0; i < 2; i++ {
		if _, _, err := resolveDocumentQuestionsValue(ctx, cache, "questions-empty", emptyLoader); !errors.Is(err, errEmptyDocumentQuestions) {
			t.Fatalf("attempt %d: expected empty-question error, got %v", i+1, err)
		}
	}
	if emptyCalls != 2 {
		t.Fatalf("empty question lists were cached: calls=%d", emptyCalls)
	}
}

func TestResolveDocumentQuestionsValueRepairsSemanticallyEmptyEntry(t *testing.T) {
	t.Setenv("WEKNORA_INFERENCE_CACHE_ENABLED", "true")
	cache := inferencecache.New(nil)
	ctx := context.Background()
	const key = "questions-corrupt-empty"

	if _, _, err := cache.Resolve(ctx, key, func(context.Context) ([]byte, error) {
		return []byte("[]"), nil
	}); err != nil {
		t.Fatalf("seed empty cache entry: %v", err)
	}

	loaderCalls := 0
	questions, stats, err := resolveDocumentQuestionsValue(ctx, cache, key, func(context.Context) ([]string, error) {
		loaderCalls++
		return []string{"Recovered question?"}, nil
	})
	if err != nil {
		t.Fatalf("repair resolve failed: %v", err)
	}
	if stats.Hit || loaderCalls != 1 || len(questions) != 1 || questions[0] != "Recovered question?" {
		t.Fatalf("unexpected repaired result: value=%v stats=%+v calls=%d", questions, stats, loaderCalls)
	}
}

func TestParseGeneratedQuestions(t *testing.T) {
	got := parseGeneratedQuestions("1. What is WeKnora?\n2) How does caching work?\n- Why use RAG?\n", 2)
	if len(got) != 2 || got[0] != "What is WeKnora?" || got[1] != "How does caching work?" {
		t.Fatalf("parsed questions = %v", got)
	}
}
