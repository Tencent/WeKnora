package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

type embeddedCacheChat interface{ chat.Chat }

func TestGraphProcessingCachePreservesTruncatedProseRetry(t *testing.T) {
	for _, reason := range []string{"length", "max_tokens", "incomplete", "", "stop"} {
		t.Run(reason, func(t *testing.T) {
			m := &processingCacheChat{
				identity: "model",
				response: &types.ChatResponse{
					Content:      "I cannot extract entities from this document.",
					FinishReason: reason,
				},
			}
			s := &ChunkExtractService{processingCache: newStageTestRuntime()}
			for i := 0; i < 2; i++ {
				_, err := s.extractGraphCached(
					context.Background(),
					m,
					&types.PromptTemplateStructured{Description: "extract"},
					1,
					"doc",
					"text",
				)
				if err == nil || errors.Is(err, chatpipeline.ErrModelDeclined) != (reason == "stop") {
					t.Fatalf("reason=%q err=%v", reason, err)
				}
			}
			if m.calls != 2 {
				t.Fatal("cached unsuccessful graph")
			}
		})
	}
}

type processingCacheChat struct {
	embeddedCacheChat
	identity string
	calls    int
	response *types.ChatResponse
	err      error
	requests [][]chat.Message
}

func (m *processingCacheChat) ProcessingCacheIdentity() string { return m.identity }

func (m *processingCacheChat) Chat(
	_ context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.calls++
	m.requests = append(m.requests, append([]chat.Message(nil), messages...))
	return m.response, m.err
}

func newStageTestRuntime() *processingcache.Runtime {
	return processingcache.New(
		&imageCacheStore{rows: map[string]*processingcache.Record{}},
		processingcache.Options{ReadEnabled: true, WriteEnabled: true},
	)
}

func TestQuestionProcessingCacheActualEntry(t *testing.T) {
	s := &knowledgeService{
		processingCache: newStageTestRuntime(),
		config: &config.Config{
			Conversation: &config.ConversationConfig{
				GenerateQuestionsPrompt: "{{content}} {{context}} {{doc_name}} {{language}} {{question_count}}",
			},
		},
	}
	m := &processingCacheChat{
		identity: "model",
		response: &types.ChatResponse{Content: "What is the document about?", FinishReason: "stop"},
	}
	ctx := withProcessingDocument(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), "doc")
	call := func(ctx context.Context, content, previous, name string, n int) ([]string, error) {
		return s.generateQuestionsWithContext(ctx, m, content, previous, "next", name, n, "")
	}
	for _, url := range []string{"https://old/image.png", "https://new/image.png"} {
		if _, err := call(ctx, "![diagram]("+url+") caption", "previous", "file", 1); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 1 {
		t.Fatalf("same text/new image URL calls=%d", m.calls)
	}
	if strings.Contains(m.requests[0][0].Content, "https://old") {
		t.Fatal("volatile URL in actual model input")
	}
	if _, err := call(ctx, "changed content", "previous", "file", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := call(ctx, "changed content", "changed context", "file", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := call(ctx, "changed content", "changed context", "renamed", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := call(ctx, "changed content", "changed context", "renamed", 2); err != nil {
		t.Fatal(err)
	}
	if m.calls != 5 {
		t.Fatalf("invalidation calls=%d", m.calls)
	}
	if _, err := call(withProcessingCacheBypass(ctx), "changed content", "changed context", "renamed", 2); err != nil {
		t.Fatal(err)
	}
	if m.calls != 6 {
		t.Fatal("explicit refresh cached")
	}
	m.response = &types.ChatResponse{Content: "usable question despite cut-off?", FinishReason: "length"}
	for i := 0; i < 2; i++ {
		if _, err := call(ctx, "truncated", "", "file", 1); err != nil {
			t.Fatal("changed legacy partial-output behavior", err)
		}
	}
	if m.calls != 8 {
		t.Fatal("cached truncated questions")
	}
	m.response = nil
	if _, err := call(ctx, "nil", "", "file", 1); err == nil {
		t.Fatal("accepted nil response")
	}
}

func TestQuestionProcessingCacheCompletionAdmission(t *testing.T) {
	for _, reason := range []string{
		"stop", "end_turn", " STOP ", "", "content_filter", "tool_calls",
		"length", "max_tokens", "incomplete", "unknown",
	} {
		t.Run(reason, func(t *testing.T) {
			s := &knowledgeService{
				processingCache: newStageTestRuntime(),
				config: &config.Config{Conversation: &config.ConversationConfig{
					GenerateQuestionsPrompt: "{{content}}",
				}},
			}
			m := &processingCacheChat{
				identity: "model",
				response: &types.ChatResponse{
					Content: "What is the document about?", FinishReason: reason,
				},
			}
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
			ctx = withProcessingDocument(ctx, "doc")
			call := func() {
				t.Helper()
				questions, err := s.generateQuestionsWithContext(ctx, m, "text", "", "", "file", 1, "")
				if err != nil || len(questions) != 1 || questions[0] != m.response.Content {
					t.Fatalf("legacy result changed: questions=%v err=%v", questions, err)
				}
			}
			call()
			call()
			complete := reason == "stop" || reason == "end_turn" || reason == " STOP "
			wantCalls := 2
			if complete {
				wantCalls = 1
			}
			if m.calls != wantCalls {
				t.Fatalf("finish_reason=%q: calls=%d want %d", reason, m.calls, wantCalls)
			}
			if !complete {
				// An inadmissible response must not prevent a later complete result from being cached.
				m.response = &types.ChatResponse{
					Content: "What does the complete document explain?", FinishReason: "stop",
				}
				call()
				call()
				if m.calls != 3 {
					t.Fatalf("complete retry was not cached: calls=%d", m.calls)
				}
			}
		})
	}
}

func TestGraphProcessingCacheParsedAndUnbound(t *testing.T) {
	m := &processingCacheChat{
		identity: "graph-model",
		response: &types.ChatResponse{
			Content:      `[{"entity":"Alpha","entity_attributes":["first"]}]`,
			FinishReason: "stop",
		},
	}
	s := &ChunkExtractService{processingCache: newStageTestRuntime()}
	template := &types.PromptTemplateStructured{Description: "extract entities"}
	a, err := s.extractGraphCached(context.Background(), m, template, 1, "doc", "Alpha is first")
	if err != nil {
		t.Fatal(err)
	}
	a.Node[0].Chunks = []string{"old chunk"}
	b, err := s.extractGraphCached(context.Background(), m, template, 1, "doc", "Alpha is first")
	if err != nil || len(b.Node[0].Chunks) != 0 || m.calls != 1 {
		t.Fatalf("bound cache or miss: %v calls=%d", err, m.calls)
	}
	m.response = &types.ChatResponse{Content: `[{"entity":"Beta"}]`, FinishReason: "length"}
	for i := 0; i < 2; i++ {
		if _, err := s.extractGraphCached(context.Background(), m, template, 1, "doc", "Beta"); err != nil {
			t.Fatal("changed legacy graph partial-output behavior", err)
		}
	}
	if m.calls != 3 {
		t.Fatal("cached truncated graph")
	}
	m.response = nil
	if _, err := s.extractGraphCached(context.Background(), m, template, 1, "doc", "nil"); err == nil {
		t.Fatal("accepted nil graph response")
	}
}

func TestTableProcessingCacheEntry(t *testing.T) {
	s := &DataTableSummaryService{processingCache: newStageTestRuntime()}
	m := &processingCacheChat{
		identity: "table-model",
		response: &types.ChatResponse{Content: "Descriptions of all columns", FinishReason: "stop"},
	}
	ctx := withProcessingDocument(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), "doc")
	for i := 0; i < 2; i++ {
		if _, err := s.generateTableDescription(ctx, m, "table", "schema", "sample", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.generateColumnDescriptions(ctx, m, "table", "schema", "sample", ""); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 2 {
		t.Fatalf("table/column calls=%d", m.calls)
	}
	m.response = &types.ChatResponse{Content: "legacy usable prefix", FinishReason: "length"}
	for i := 0; i < 2; i++ {
		if _, err := s.generateTableDescription(ctx, m, "table", "changed schema", "sample", ""); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 4 {
		t.Fatal("cached truncated table description")
	}
}

func TestSummaryProcessingCacheParsedCurrentURLs(t *testing.T) {
	s := &knowledgeService{
		processingCache: newStageTestRuntime(),
		config: &config.Config{
			Conversation: &config.ConversationConfig{GenerateSummaryPrompt: "Summarize in {{language}}"},
		},
		chunkRepo: summaryImageInfoChunkRepo{},
	}
	m := &processingCacheChat{
		identity: "summary-model",
		response: &types.ChatResponse{
			Content:      `{"summary":"The document describes a diagram ![diagram](wkimg:0001)","gist":"diagram"}`,
			FinishReason: "stop",
		},
	}
	knowledge := &types.Knowledge{ID: "doc", TenantID: 1}
	for _, url := range []string{"https://old/image.png", "https://current/image.png"} {
		content := "The document explains architecture, data movement, " +
			"caching and validation in sufficient detail. ![diagram](" + url + ")"
		got, err := s.getSummary(
			context.Background(),
			m,
			knowledge,
			[]*types.Chunk{{ID: url, Content: content, StartAt: 0, EndAt: len([]rune(content))}},
		)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got.Summary, url) {
			t.Fatalf("stale URL restored: %s", got.Summary)
		}
	}
	if m.calls != 1 {
		t.Fatalf("summary provider calls=%d", m.calls)
	}
	if _, err := s.getSummary(
		withProcessingCacheBypass(context.Background()),
		m,
		knowledge,
		[]*types.Chunk{
			{
				Content: "The document explains architecture, data movement, " +
					"caching and validation in sufficient detail.",
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	if m.calls != 2 {
		t.Fatal("explicit bypass did not call summary provider")
	}
	m.response = &types.ChatResponse{Content: "usable legacy prefix", FinishReason: "length"}
	for i := 0; i < 2; i++ {
		if _, err := s.getSummary(
			context.Background(),
			m,
			knowledge,
			[]*types.Chunk{
				{
					Content: "A different document with enough substantive text about " +
						"cache validation and current references.",
					EndAt: 95,
				},
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 4 {
		t.Fatal("cached truncated legacy summary")
	}
}

type graphCacheCaptureRepo struct {
	interfaces.RetrieveGraphRepository
	last *types.GraphData
}

func (g *graphCacheCaptureRepo) AddGraph(_ context.Context, _ types.NameSpace, graphs []*types.GraphData) error {
	g.last = graphs[0]
	return nil
}

func TestGraphProcessingCacheHandleRebindsCurrentUUID(t *testing.T) {
	m := &processingCacheChat{
		identity: "graph-model",
		response: &types.ChatResponse{Content: `[{"entity":"Alpha"}]`, FinishReason: "stop"},
	}
	chunk := &types.Chunk{
		ID:              "old",
		TenantID:        1,
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Alpha is a concept",
	}
	graphRepo := &graphCacheCaptureRepo{}
	s := &ChunkExtractService{
		processingCache: newStageTestRuntime(),
		template:        &types.PromptTemplateStructured{Description: "extract"},
		modelService:    &stubModelService{chatModel: m},
		chunkRepo:       graphChunkRepo{chunk: chunk},
		knowledgeBaseRepo: graphKBRepo{
			kb: &types.KnowledgeBase{ID: "kb", ExtractConfig: &types.ExtractConfig{Enabled: true}},
		},
		graphEngine: graphRepo,
	}
	for _, id := range []string{"old", "current"} {
		chunk.ID = id
		payload, _ := json.Marshal(types.ExtractChunkPayload{TenantID: 1, ChunkID: id, ModelID: "model"})
		if err := s.Handle(context.Background(), asynq.NewTask(types.TypeChunkExtract, payload)); err != nil {
			t.Fatal(err)
		}
		if graphRepo.last.Node[0].Chunks[0] != id {
			t.Fatal("stale chunk UUID")
		}
	}
	if m.calls != 1 {
		t.Fatalf("graph Handle provider calls=%d", m.calls)
	}
}

func TestQuestionProcessingCacheCountWithoutTemplatePlaceholder(t *testing.T) {
	s := &knowledgeService{
		processingCache: newStageTestRuntime(),
		config: &config.Config{
			Conversation: &config.ConversationConfig{GenerateQuestionsPrompt: "Ask questions about {{content}}"},
		},
	}
	m := &processingCacheChat{
		identity: "model",
		response: &types.ChatResponse{
			Content:      "What is the first topic?\nWhat is the second topic?",
			FinishReason: "stop",
		},
	}
	ctx := withProcessingDocument(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), "doc")
	a, e := s.generateQuestionsWithContext(ctx, m, "same", "", "", "file", 1, "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.generateQuestionsWithContext(ctx, m, "same", "", "", "file", 2, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(a) != 1 || len(b) != 2 || m.calls != 2 {
		t.Fatalf("count behavior absent from key: %v %v calls=%d", a, b, m.calls)
	}
}

func TestProcessingCacheAdmissionWaitersPreserveLegacyResult(t *testing.T) {
	runtime := newStageTestRuntime()
	m := &processingCacheChat{identity: "model"}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	compute := func(context.Context, []chat.Message) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return "legacy prefix", errProcessingCacheAdmission
	}
	run := func() error {
		v, e := cachedProcessingModelValue(
			context.Background(),
			runtime,
			1,
			"doc",
			"admission.test",
			m,
			[]chat.Message{{Content: "same"}},
			nil,
			nil,
			func(string) error { return nil },
			compute,
		)
		if e != nil {
			return e
		}
		if v != "legacy prefix" {
			return fmt.Errorf("lost legacy value %q", v)
		}
		return nil
	}
	results := make(chan error, 2)
	go func() { results <- run() }()
	<-started
	go func() { results <- run() }()
	time.Sleep(30 * time.Millisecond)
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("waiter should recompute uncacheable output, calls=%d", calls.Load())
	}
}

func TestExplicitSummaryRefreshEnqueuesCacheBypass(t *testing.T) {
	repo := &metadataUpdateKnowledgeRepo{
		knowledge: &types.Knowledge{
			ID:              "doc",
			TenantID:        7,
			KnowledgeBaseID: "kb-1",
			SummaryStatus:   types.SummaryStatusCompleted,
		},
	}
	queue := &metadataUpdateTaskEnqueuer{}
	s := &knowledgeService{repo: repo, kbService: metadataUpdateKBService{}, task: queue}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	if err := s.RequestKnowledgeSummaryRefresh(ctx, "doc"); err != nil {
		t.Fatal(err)
	}
	var payload types.SummaryGenerationPayload
	if err := json.Unmarshal(queue.tasks[0].Payload(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.BypassCache {
		t.Fatal("explicit summary refresh can hit old cached result")
	}
}

func TestProcessingCacheLegacyAdmissionHonorsCancellation(t *testing.T) {
	for _, runtime := range []*processingcache.Runtime{nil, newStageTestRuntime()} {
		ctx, cancel := context.WithCancel(context.Background())
		_, err := cachedProcessingModelValue(
			ctx,
			runtime,
			1,
			"doc",
			"cancellation",
			&processingCacheChat{identity: "model"},
			nil,
			nil,
			nil,
			func(string) error { return nil },
			func(context.Context, []chat.Message) (string, error) {
				cancel()
				return "legacy", errProcessingCacheAdmission
			},
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation converted to successful legacy output: %v", err)
		}
	}
}
