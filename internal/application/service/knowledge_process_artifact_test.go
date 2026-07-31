package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeArtifactChat struct {
	mu    sync.Mutex
	calls int
	out   []string
}

func (c *fakeArtifactChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	content := fmt.Sprintf("answer-%d", c.calls)
	if len(c.out) >= c.calls {
		content = c.out[c.calls-1]
	}
	return &types.ChatResponse{Content: content}, nil
}

func (c *fakeArtifactChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse)
	close(ch)
	return ch, nil
}

func (c *fakeArtifactChat) GetModelName() string { return "fake-chat" }
func (c *fakeArtifactChat) GetModelID() string   { return "fake-chat-id" }

type fakeArtifactChatWithID struct {
	fakeArtifactChat
	modelID string
}

func (c *fakeArtifactChatWithID) GetModelID() string {
	return c.modelID
}

func TestChatContentWithArtifactReusesResponse(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	svc := &knowledgeService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config:          &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"summary": true}}},
	}
	model := &fakeArtifactChat{out: []string{"first summary", "second summary"}}
	messages := []chat.Message{{Role: "user", Content: "summarize"}}
	opts := &chat.ChatOptions{Temperature: 0.3, MaxTokens: 128}

	got, meta, err := svc.chatContentWithArtifact(ctx, model, 7, "summary", messages, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got != "first summary" || meta.Outcome != artifact.OutcomeComputed {
		t.Fatalf("first call got text=%q outcome=%s", got, meta.Outcome)
	}
	got, meta, err = svc.chatContentWithArtifact(ctx, model, 7, "summary", messages, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got != "first summary" || meta.Outcome != artifact.OutcomeHit || model.calls != 1 {
		t.Fatalf("second call got text=%q outcome=%s calls=%d", got, meta.Outcome, model.calls)
	}
}

func TestChatContentWithArtifactInvalidatesOnOptionsAndModel(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	svc := &knowledgeService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config:          &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"summary": true}}},
	}
	modelOne := &fakeArtifactChatWithID{
		fakeArtifactChat: fakeArtifactChat{out: []string{"summary one", "summary with new options"}},
		modelID:          "chat-1",
	}
	modelTwo := &fakeArtifactChatWithID{
		fakeArtifactChat: fakeArtifactChat{out: []string{"summary with new model"}},
		modelID:          "chat-2",
	}
	messages := []chat.Message{{Role: "user", Content: "summarize"}}

	if _, _, err := svc.chatContentWithArtifact(ctx, modelOne, 7, "summary", messages, &chat.ChatOptions{Temperature: 0.1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.chatContentWithArtifact(ctx, modelOne, 7, "summary", messages, &chat.ChatOptions{Temperature: 0.2}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.chatContentWithArtifact(ctx, modelTwo, 7, "summary", messages, &chat.ChatOptions{Temperature: 0.1}); err != nil {
		t.Fatal(err)
	}

	if modelOne.calls != 2 {
		t.Fatalf("model one provider calls=%d, want separate calls for changed options", modelOne.calls)
	}
	if modelTwo.calls != 1 {
		t.Fatalf("model two provider calls=%d, want miss for changed model", modelTwo.calls)
	}
}

func TestChatContentWithArtifactDoesNotCacheEmptyResponse(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	svc := &knowledgeService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config:          &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"question": true}}},
	}
	model := &fakeArtifactChat{out: []string{"", "question one"}}
	messages := []chat.Message{{Role: "user", Content: "questions"}}

	got, _, err := svc.chatContentWithArtifact(ctx, model, 7, "question", messages, &chat.ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "" || store.puts != 0 {
		t.Fatalf("empty response got %q puts=%d, want no cached payload", got, store.puts)
	}
	got, meta, err := svc.chatContentWithArtifact(ctx, model, 7, "question", messages, &chat.ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "question one" || meta.Outcome != artifact.OutcomeComputed || store.puts != 1 {
		t.Fatalf("second response got text=%q outcome=%s puts=%d", got, meta.Outcome, store.puts)
	}
}

func TestGenerateQuestionsWithArtifactInvalidatesOnlyChangedChunkNeighborhood(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	store := newMultimodalArtifactStore()
	svc := &knowledgeService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config: &config.Config{
			ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"question": true}},
			Conversation: &config.ConversationConfig{
				GenerateQuestionsPrompt: "{{content}}\n{{context}}\n{{doc_name}}\n{{question_count}}\n{{language}}",
			},
		},
	}
	model := &fakeArtifactChat{out: []string{
		"what stable question one?",
		"what stable question two?",
		"what stable question three?",
		"what stable question four?",
		"what stable question five?",
		"what changed question two?",
		"what changed question three?",
		"what changed question four?",
	}}
	contents := []string{"chunk one", "chunk two", "chunk three", "chunk four", "chunk five"}

	runWindow := func(contents []string) {
		t.Helper()
		for i, content := range contents {
			prev := ""
			if i > 0 {
				prev = contents[i-1]
			}
			next := ""
			if i+1 < len(contents) {
				next = contents[i+1]
			}
			if _, err := svc.generateQuestionsWithContext(ctx, model, content, prev, next, "doc.md", 1, ""); err != nil {
				t.Fatalf("generate questions for chunk %d: %v", i, err)
			}
		}
	}

	runWindow(contents)
	changed := append([]string(nil), contents...)
	changed[2] = "chunk three changed"
	runWindow(changed)

	if model.calls != 8 {
		t.Fatalf("question provider calls = %d, want 5 initial + 3 changed-neighborhood calls", model.calls)
	}
}

func TestArtifactCachingChatReusesResponse(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	model := &fakeArtifactChat{out: []string{"graph json", "different graph"}}
	cached := newArtifactCachingChat(
		model,
		artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		7,
		"graph_extract",
	)
	messages := []chat.Message{{Role: "user", Content: "extract graph"}}

	first, err := cached.Chat(ctx, messages, &chat.ChatOptions{Temperature: 0.3})
	if err != nil {
		t.Fatal(err)
	}
	second, err := cached.Chat(ctx, messages, &chat.ChatOptions{Temperature: 0.3})
	if err != nil {
		t.Fatal(err)
	}
	if first.Content != "graph json" || second.Content != "graph json" || model.calls != 1 {
		t.Fatalf("contents=%q/%q calls=%d, want cached graph response", first.Content, second.Content, model.calls)
	}
}

type fakeArtifactDocReader struct {
	mu      sync.Mutex
	calls   int
	reqs    []*types.ReadRequest
	results []*types.ReadResult
}

func (r *fakeArtifactDocReader) Read(_ context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if len(r.reqs) < r.calls {
		reqCopy := *req
		reqCopy.FileContent = append([]byte(nil), req.FileContent...)
		r.reqs = append(r.reqs, &reqCopy)
	}
	if len(r.results) >= r.calls {
		return r.results[r.calls-1], nil
	}
	return &types.ReadResult{MarkdownContent: fmt.Sprintf("doc-%d", r.calls)}, nil
}

func TestCallDocReaderWithArtifactReusesSuccessfulParse(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	svc := &knowledgeService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config:          &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"parse": true}}},
	}
	reader := &fakeArtifactDocReader{
		results: []*types.ReadResult{
			{MarkdownContent: "first parse"},
			{MarkdownContent: "second parse"},
		},
	}
	req := &types.ReadRequest{FileContent: []byte("source"), FileName: "a.md", FileType: "md", ParserEngine: "builtin"}

	first, err := svc.callDocReaderWithArtifact(ctx, reader, req, 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.callDocReaderWithArtifact(ctx, reader, req, 7)
	if err != nil {
		t.Fatal(err)
	}
	if first.MarkdownContent != "first parse" || second.MarkdownContent != "first parse" || reader.calls != 1 {
		t.Fatalf("contents=%q/%q calls=%d, want cached parse", first.MarkdownContent, second.MarkdownContent, reader.calls)
	}
}

func TestCallDocReaderWithArtifactInvalidatesOnParserEngineAndOverrides(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	svc := &knowledgeService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config:          &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"parse": true}}},
	}
	reader := &fakeArtifactDocReader{
		results: []*types.ReadResult{
			{MarkdownContent: "builtin parse"},
			{MarkdownContent: "mineru parse"},
			{MarkdownContent: "mineru override parse"},
		},
	}
	base := &types.ReadRequest{
		FileContent:  []byte("same source"),
		FileName:     "a.pdf",
		FileType:     "pdf",
		ParserEngine: "builtin",
	}
	engineChanged := *base
	engineChanged.ParserEngine = "mineru"
	overrideChanged := engineChanged
	overrideChanged.ParserEngineOverrides = map[string]string{"mineru_base_url": "https://parser.example"}

	if _, err := svc.callDocReaderWithArtifact(ctx, reader, base, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.callDocReaderWithArtifact(ctx, reader, &engineChanged, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.callDocReaderWithArtifact(ctx, reader, &overrideChanged, 7); err != nil {
		t.Fatal(err)
	}

	if reader.calls != 3 {
		t.Fatalf("docreader calls = %d, want separate parse calls for engine and override changes", reader.calls)
	}
}

func TestCallDocReaderWithArtifactDoesNotCacheParserError(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	svc := &knowledgeService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config:          &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"parse": true}}},
	}
	reader := &fakeArtifactDocReader{
		results: []*types.ReadResult{
			{Error: "bad file"},
			{MarkdownContent: "parsed"},
		},
	}
	req := &types.ReadRequest{FileContent: []byte("source"), FileName: "a.md", FileType: "md", ParserEngine: "builtin"}

	first, err := svc.callDocReaderWithArtifact(ctx, reader, req, 7)
	if err != nil {
		t.Fatal(err)
	}
	if first.Error != "bad file" || store.puts != 0 {
		t.Fatalf("first parse error=%q puts=%d, want parser error without cache write", first.Error, store.puts)
	}
	second, err := svc.callDocReaderWithArtifact(ctx, reader, req, 7)
	if err != nil {
		t.Fatal(err)
	}
	if second.MarkdownContent != "parsed" || store.puts != 1 {
		t.Fatalf("second parse content=%q puts=%d, want successful retry cached", second.MarkdownContent, store.puts)
	}
}
