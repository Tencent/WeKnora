package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeCacheChat struct {
	calls   int
	content string
}

func (f *fakeCacheChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	f.calls++
	return &types.ChatResponse{Content: f.content}, nil
}

func (f *fakeCacheChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse)
	close(ch)
	return ch, nil
}

func (f *fakeCacheChat) GetModelName() string {
	return "fake-chat"
}

func (f *fakeCacheChat) GetModelID() string {
	return "chat-a"
}

func TestSummaryCacheHelpersRoundTrip(t *testing.T) {
	ctx := context.Background()
	cache := newMemoryProcessingCache()
	s := &knowledgeService{cacheRepo: cache}
	key := summaryCacheKey("document content", "chat-a", "summary prompt", 256)

	if _, ok := s.getSummaryCache(ctx, 7, key); ok {
		t.Fatal("empty summary cache should miss")
	}

	s.putSummaryCache(ctx, 7, key, "cached summary", "chat-a")

	got, ok := s.getSummaryCache(ctx, 7, key)
	if !ok {
		t.Fatal("summary cache should hit after write")
	}
	if got != "cached summary" {
		t.Fatalf("summary cache = %q", got)
	}
	if _, ok := s.getSummaryCache(ctx, 7, summaryCacheKey("changed", "chat-a", "summary prompt", 256)); ok {
		t.Fatal("changed content should miss summary cache")
	}
}

func TestGenerateQuestionsWithContextUsesProcessingCache(t *testing.T) {
	ctx := context.Background()
	cache := newMemoryProcessingCache()
	model := &fakeCacheChat{content: "1. What is WeKnora?\n2. Why cache chunks?"}
	s := &knowledgeService{
		cacheRepo: cache,
		config: &config.Config{
			Conversation: &config.ConversationConfig{
				GenerateQuestionsPrompt: "Make {{question_count}} questions in {{language}}.\n{{context}}\n{{content}}\n{{doc_name}}",
			},
		},
	}

	first, err := s.generateQuestionsWithContext(ctx, model, "chat-a", 7, "WeKnora caches work.", "", "", "Doc", 2)
	if err != nil {
		t.Fatalf("generate questions first call: %v", err)
	}
	second, err := s.generateQuestionsWithContext(ctx, model, "chat-a", 7, "WeKnora caches work.", "", "", "Doc", 2)
	if err != nil {
		t.Fatalf("generate questions second call: %v", err)
	}
	if model.calls != 1 {
		t.Fatalf("expected one chat call with cache hit, got %d", model.calls)
	}
	if len(second) != len(first) || second[0] != first[0] || second[1] != first[1] {
		t.Fatalf("cached questions mismatch: first=%#v second=%#v", first, second)
	}
}

func TestParseArtifactCacheHelpersRoundTripAndClone(t *testing.T) {
	ctx := context.Background()
	cache := newMemoryProcessingCache()
	s := &knowledgeService{cacheRepo: cache}
	key := parseArtifactCacheKey([]byte("file bytes"), "doc.pdf", "pdf", "mineru", "Doc", map[string]string{"mode": "fast"})
	result := &types.ReadResult{
		MarkdownContent: "# Doc\n\n![x](images/p1.png)",
		ImageRefs: []types.ImageRef{
			{
				Filename:    "p1.png",
				OriginalRef: "images/p1.png",
				MimeType:    "image/png",
				ImageData:   []byte{1, 2, 3},
			},
		},
		Metadata: map[string]string{"pages": "1"},
	}

	s.putParseArtifactCache(ctx, 7, key, result, map[string]string{"parser_engine": "mineru"})

	got, ok := s.getParseArtifactCache(ctx, 7, key)
	if !ok {
		t.Fatal("parse artifact cache should hit after write")
	}
	if got.MarkdownContent != result.MarkdownContent {
		t.Fatalf("markdown = %q", got.MarkdownContent)
	}
	if got.Metadata["pages"] != "1" {
		t.Fatalf("metadata pages = %q", got.Metadata["pages"])
	}
	if len(got.ImageRefs) != 1 || string(got.ImageRefs[0].ImageData) != string([]byte{1, 2, 3}) {
		t.Fatalf("image refs did not round trip: %#v", got.ImageRefs)
	}

	got.ImageRefs[0].ImageData[0] = 9
	got.Metadata["pages"] = "changed"
	again, ok := s.getParseArtifactCache(ctx, 7, key)
	if !ok {
		t.Fatal("parse artifact cache should still hit")
	}
	if again.ImageRefs[0].ImageData[0] != 1 {
		t.Fatal("parse artifact cache should return cloned image bytes")
	}
	if again.Metadata["pages"] != "1" {
		t.Fatal("parse artifact cache should return cloned metadata")
	}
}
