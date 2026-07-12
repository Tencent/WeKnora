package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type countingEmbedder struct{ calls int }

func (e *countingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	e.calls++
	return []float32{1, 2}, nil
}
func (e *countingEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls++
	result := make([][]float32, len(texts))
	for i := range result {
		result[i] = []float32{float32(i + 1), 2}
	}
	return result, nil
}
func (e *countingEmbedder) BatchEmbedWithPool(ctx context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}
func (*countingEmbedder) GetModelName() string { return "model" }
func (*countingEmbedder) GetModelID() string   { return "model-id" }
func (*countingEmbedder) GetDimensions() int   { return 2 }

type countingVLM struct{ calls int }

func (v *countingVLM) Predict(_ context.Context, _ [][]byte, _ string) (string, error) {
	v.calls++
	return "frozen result", nil
}
func (*countingVLM) GetModelName() string { return "vision" }
func (*countingVLM) GetModelID() string   { return "vision-id" }

type countingChat struct{ calls int }

func (c *countingChat) Chat(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	c.calls++
	return &types.ChatResponse{Content: "mapped artifact"}, nil
}
func (*countingChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return make(chan types.StreamResponse), nil
}
func (*countingChat) GetModelName() string { return "chat" }
func (*countingChat) GetModelID() string   { return "chat-id" }

type countingReader struct{ calls int }

func (r *countingReader) Read(_ context.Context, _ *types.ReadRequest) (*types.ReadResult, error) {
	r.calls++
	return &types.ReadResult{MarkdownContent: "parsed markdown"}, nil
}

func testArtifactRedis(t *testing.T) *redis.Client {
	t.Helper()
	server, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestEmbeddingCacheHitsAndInvalidatesByContent(t *testing.T) {
	inner := &countingEmbedder{}
	cached := cacheEmbeddingModel(testArtifactRedis(t), inner)
	ctx := context.Background()
	first, err := cached.BatchEmbed(ctx, []string{"same content"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := cached.BatchEmbed(ctx, []string{" same\ncontent "})
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 || first[0][0] != second[0][0] {
		t.Fatalf("expected normalized cache hit, calls=%d", inner.calls)
	}
	_, _ = cached.Embed(ctx, "changed")
	if inner.calls != 2 {
		t.Fatalf("changed content must miss cache, calls=%d", inner.calls)
	}
}

func TestVLMCacheFreezesImageResult(t *testing.T) {
	inner := &countingVLM{}
	cached := cacheVLM(testArtifactRedis(t), inner)
	ctx := context.Background()
	for range 2 {
		got, err := cached.Predict(ctx, [][]byte{[]byte("image")}, "prompt-v1")
		if err != nil || got != "frozen result" {
			t.Fatalf("unexpected result %q: %v", got, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("unchanged image should hit cache, calls=%d", inner.calls)
	}
	_, _ = cached.Predict(ctx, [][]byte{[]byte("image")}, "prompt-v2")
	_, _ = cached.Predict(ctx, [][]byte{[]byte("changed")}, "prompt-v1")
	if inner.calls != 3 {
		t.Fatalf("prompt and image changes must invalidate independently, calls=%d", inner.calls)
	}
}

func TestArtifactChatCacheIsNamespaced(t *testing.T) {
	inner := &countingChat{}
	client := testArtifactRedis(t)
	ctx := context.Background()
	messages := []chat.Message{{Role: "user", Content: "document content"}}
	for range 2 {
		response, err := cacheArtifactChat(client, inner, "wiki-map-v1").Chat(ctx, messages, nil)
		if err != nil || response.Content != "mapped artifact" {
			t.Fatalf("unexpected cached response: %#v, %v", response, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("wiki map should hit content cache, calls=%d", inner.calls)
	}
	_, _ = cacheArtifactChat(client, inner, "graph-extract-v1").Chat(ctx, messages, nil)
	if inner.calls != 2 {
		t.Fatalf("artifact stages must not share results, calls=%d", inner.calls)
	}
}

func TestParseArtifactCacheUsesFileBytesAndConfiguration(t *testing.T) {
	reader := &countingReader{}
	svc := &knowledgeService{redisClient: testArtifactRedis(t)}
	ctx := context.Background()
	req := &types.ReadRequest{FileContent: []byte("file"), FileType: "pdf", ParserEngine: "reader"}
	for range 2 {
		result, err := svc.readDocumentCached(ctx, reader, req)
		if err != nil || result.MarkdownContent != "parsed markdown" {
			t.Fatalf("unexpected parse result: %#v, %v", result, err)
		}
	}
	if reader.calls != 1 {
		t.Fatalf("unchanged file should reuse parse artifact, calls=%d", reader.calls)
	}
	req.ParserEngineOverrides = map[string]string{"layout": "changed"}
	_, _ = svc.readDocumentCached(ctx, reader, req)
	if reader.calls != 2 {
		t.Fatalf("parser configuration must invalidate artifact, calls=%d", reader.calls)
	}
}
