package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
	"strings"
	"sync"
	"testing"
)

func (m *processingCacheChat) GetModelID() string   { return "same-row" }
func (m *processingCacheChat) GetModelName() string { return "test-model" }
func TestWikiProcessingCacheCandidateImmediateReparse(t *testing.T) {
	s := &wikiIngestService{processingCache: newStageTestRuntime()}
	m := &processingCacheChat{identity: "model", response: &types.ChatResponse{Content: `{"entities":[{"name":"Alpha","slug":"entity/alpha"}],"concepts":[]}`, FinishReason: "stop"}}
	ctx := withWikiProcessingScope(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), "doc", "kb")
	for _, old := range []map[string]bool{nil, {"entity/alpha": true}} {
		a, _, _, err := s.extractCandidateSlugs(ctx, m, "kb", "Alpha is an example concept in this document.", "English", old, &WikiBatchContext{})
		if err != nil || len(a) != 1 {
			t.Fatalf("%v %v", a, err)
		}
	}
	if m.calls != 1 {
		t.Fatalf("first immediate reparse calls=%d", m.calls)
	}
}
func TestWikiProcessingCacheSummaryCurrentURLs(t *testing.T) {
	s := &wikiIngestService{processingCache: newStageTestRuntime()}
	m := &processingCacheChat{identity: "model", response: &types.ChatResponse{Content: "SUMMARY: Diagram\n![diagram](wkimg:0001)", FinishReason: "stop"}}
	ctx := withWikiProcessingScope(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), "doc", "kb")
	for _, url := range []string{"https://old/image.png", "https://current/image.png"} {
		got, err := s.generateWithTemplate(ctx, m, agent.WikiSummaryPrompt, map[string]string{"Content": "An architecture diagram ![diagram](" + url + ")", "Language": "English"})
		if err != nil || !strings.Contains(got, url) {
			t.Fatalf("%s %v", got, err)
		}
	}
	if m.calls != 1 {
		t.Fatalf("summary calls=%d", m.calls)
	}
}
func TestWikiProcessingCacheCitationRebindsHandles(t *testing.T) {
	s := &wikiIngestService{processingCache: newStageTestRuntime()}
	m := &processingCacheChat{identity: "model", response: &types.ChatResponse{Content: `{"citations":{"entity/alpha":["c000"]},"new_slugs":[]}`, FinishReason: "stop"}}
	ctx := withWikiProcessingScope(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), "doc", "kb")
	for _, id := range []string{"old-UUID", "current-UUID"} {
		got, _, _ := s.classifyChunkCitations(ctx, m, "alpha", []*types.Chunk{{ID: id, Content: "Alpha is described here", ChunkType: types.ChunkTypeText}}, "English", &WikiBatchContext{})
		if len(got["entity/alpha"]) != 1 || got["entity/alpha"][0] != id {
			t.Fatalf("stale citations: %v", got)
		}
	}
	if m.calls != 1 {
		t.Fatalf("classify calls=%d", m.calls)
	}
}

type wikiCacheLivePages struct {
	stubNormalizedTitleWiki
	old []string
}

func (s *wikiCacheLivePages) FindSimilarPages(context.Context, string, string, []string, int) ([]*types.WikiPageLite, error) {
	return s.pages, nil
}
func (s *wikiCacheLivePages) ListSlugsBySourceRef(context.Context, string, string) ([]string, error) {
	return s.old, nil
}

type wikiCacheMapChunks struct {
	summaryImageInfoChunkRepo
	chunks []*types.Chunk
}

func (s *wikiCacheMapChunks) ListChunksByKnowledgeIDAndTypes(context.Context, uint64, string, []types.ChunkType) ([]*types.Chunk, error) {
	return s.chunks, nil
}

type wikiCacheMapModel struct {
	embeddedCacheChat
	mu    sync.Mutex
	calls map[string]int
}

func (m *wikiCacheMapModel) GetModelID() string              { return "map" }
func (m *wikiCacheMapModel) GetModelName() string            { return "map" }
func (m *wikiCacheMapModel) ProcessingCacheIdentity() string { return "map-effective-model" }
func (m *wikiCacheMapModel) Chat(ctx context.Context, messages []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	purpose, _ := types.LLMCallMetadataFromContext(ctx)
	m.mu.Lock()
	m.calls[purpose]++
	m.mu.Unlock()
	content := ""
	switch purpose {
	case "wiki_candidate_slug":
		content = `{"entities":[{"name":"Alpha","slug":"entity/alpha"}],"concepts":[]}`
	case "wiki_summary":
		content = "SUMMARY: Alpha overview\nAlpha has important documented details."
	case "wiki_chunk_citation":
		slug := "entity/alpha"
		if strings.Contains(messages[0].Content, "slug: entity/current") {
			slug = "entity/current"
		}
		content = `{"citations":{"` + slug + `":["c000"]},"new_slugs":[]}`
	default:
		return nil, fmt.Errorf("unexpected model purpose %s", purpose)
	}
	return &types.ChatResponse{Content: content, FinishReason: "stop"}, nil
}
func TestWikiProcessingCacheMapImmediateReparseAndLiveIdentity(t *testing.T) {
	wiki := &wikiCacheLivePages{}
	chunk := &types.Chunk{ID: "old-UUID", KnowledgeID: "doc", IsEnabled: true, Content: "Alpha is an important architecture component with durable evidence and detailed behavior.", ChunkType: types.ChunkTypeText}
	chunks := &wikiCacheMapChunks{chunks: []*types.Chunk{chunk}}
	model := &wikiCacheMapModel{calls: map[string]int{}}
	s := &wikiIngestService{processingCache: newStageTestRuntime(), wikiService: wiki, chunkRepo: chunks, knowledgeSvc: &wikiKnowledgeLookupStub{lookup: func(context.Context, string) (*types.Knowledge, error) {
		return &types.Knowledge{ID: "doc", KnowledgeBaseID: "kb", Title: "Document", ParseStatus: types.ParseStatusCompleted}, nil
	}}}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	run := func(id, wantSlug string) {
		chunk.ID = id
		_, updates, err := s.mapOneDocument(ctx, model, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb"}, WikiPendingOp{KnowledgeID: "doc", Language: "en"}, &WikiBatchContext{SummaryContentByKnowledgeID: func(context.Context, string) string { return "previous summary" }})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, u := range updates {
			if u.Type == types.WikiPageTypeEntity {
				found = true
				if u.Slug != wantSlug || len(u.SourceChunks) != 1 || u.SourceChunks[0] != id {
					t.Fatalf("stale identity or UUID: %#v", u)
				}
			}
		}
		if !found {
			t.Fatal("no entity update")
		}
	}
	run("old-UUID", "entity/alpha")
	wiki.pages = []*types.WikiPageLite{{Slug: "entity/alpha", Title: "Alpha", PageType: types.WikiPageTypeEntity}}
	wiki.old = []string{"entity/alpha"}
	run("current-UUID", "entity/alpha")
	for _, purpose := range []string{"wiki_candidate_slug", "wiki_summary", "wiki_chunk_citation"} {
		if model.calls[purpose] != 1 {
			t.Fatalf("immediate reparse %s calls=%d", purpose, model.calls[purpose])
		}
	}
	wiki.pages = []*types.WikiPageLite{{Slug: "entity/current", Title: "Alpha", PageType: types.WikiPageTypeEntity}}
	wiki.old = []string{"entity/current"}
	run("third-UUID", "entity/current")
	if model.calls["wiki_candidate_slug"] != 1 || model.calls["wiki_summary"] != 2 || model.calls["wiki_chunk_citation"] != 2 {
		t.Fatalf("live candidate invalidation: %v", model.calls)
	}
	wiki.pages = nil
	wiki.old = nil
	run("fourth-UUID", "entity/alpha")
}
func TestWikiProcessingCacheLegacyAdmissionAndFailures(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	s := &wikiIngestService{processingCache: processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})}
	m := &processingCacheChat{identity: "model", response: &types.ChatResponse{Content: `{"entities":[{"name":"Alpha","slug":"entity/alpha"}]}`, FinishReason: "stop"}}
	ctx := withWikiProcessingScope(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), "doc", "kb")
	for i := 0; i < 2; i++ {
		a, _, _, err := s.extractCandidateSlugs(ctx, m, "kb", "content", "English", nil, &WikiBatchContext{})
		if err != nil || len(a) != 1 {
			t.Fatalf("legacy partial schema behavior changed: %v %v", a, err)
		}
	}
	if m.calls != 2 || len(store.rows) != 0 {
		t.Fatal("legacy partial schema cached")
	}
	m.response = &types.ChatResponse{Content: `{"entities":[],"concepts":[]}`, FinishReason: "length"}
	for i := 0; i < 2; i++ {
		s.generateWithTemplate(ctx, m, agent.WikiCandidateSlugPrompt, map[string]string{"Content": "truncated"})
	}
	if m.calls != 4 || len(store.rows) != 0 {
		t.Fatal("truncated output cached")
	}
	m.response = &types.ChatResponse{Content: `{"entities":[],"concepts":[]}`, FinishReason: "stop"}
	s.generateWithTemplate(ctx, m, agent.WikiCandidateSlugPrompt, map[string]string{"Content": "valid empty"})
	if len(store.rows) != 1 {
		t.Fatal("valid empty not cached")
	}
	s.processingCache = processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})
	s.generateWithTemplate(ctx, m, agent.WikiCandidateSlugPrompt, map[string]string{"Content": "valid empty"})
	if m.calls != 5 {
		t.Fatal("new runtime did not reuse")
	}
}
func TestWikiProcessingCacheInvalidationAndReduceLive(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	s := &wikiIngestService{processingCache: processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})}
	m := &processingCacheChat{identity: "model-v1", response: &types.ChatResponse{Content: "SUMMARY: Complete text\nA complete body", FinishReason: "stop"}}
	ctx := withWikiProcessingScope(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), "doc", "kb")
	call := func(ctx context.Context, lang string) {
		if _, err := s.generateWithTemplate(ctx, m, agent.WikiSummaryPrompt, map[string]string{"Content": "stable content", "Language": lang}); err != nil {
			t.Fatal(err)
		}
	}
	call(ctx, "English")
	call(ctx, "English")
	if m.calls != 1 {
		t.Fatal("cache miss")
	}
	m.identity = "model-v2"
	call(ctx, "English")
	call(ctx, "Chinese")
	call(withWikiProcessingScope(ctx, "doc", "other-kb"), "Chinese")
	if m.calls != 4 {
		t.Fatalf("model/language/KB invalidation calls=%d", m.calls)
	}
	for _, r := range store.rows {
		r.Payload = []byte("bad checksum")
	}
	call(ctx, "Chinese")
	if m.calls != 5 {
		t.Fatal("corrupt cache not repaired")
	}
	before := len(store.rows)
	for i := 0; i < 2; i++ {
		if _, err := s.generateWithTemplate(ctx, m, agent.WikiPageModifyUserPrompt, map[string]string{"PageSlug": "entity/alpha"}); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 7 || len(store.rows) != before {
		t.Fatal("reduce was cached")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.generateWithTemplate(canceled, m, agent.WikiSummaryPrompt, map[string]string{"Content": "canceled"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestWikiProcessingCacheDeterministicInputs(t *testing.T) {
	s := &wikiIngestService{}
	m := &processingCacheChat{}
	got := s.wikiPreviousSlugInput(context.Background(), m, map[string]bool{"entity/z": true, "concept/b": true, "entity/a": true, "summary/doc": true})
	if got != "- concept/b\n- entity/a\n- entity/z\n" {
		t.Fatalf("unsorted previous slugs: %s", got)
	}
	a := []extractedItem{{Name: "Z", Slug: "entity/z", Aliases: []string{"b", "a"}}, {Name: "A", Slug: "entity/a"}}
	b := []extractedItem{{Name: "A", Slug: "entity/a"}, {Name: "Z", Slug: "entity/z", Aliases: []string{"a", "b"}}}
	if renderCandidateSlugsXML(a, nil) != renderCandidateSlugsXML(b, nil) {
		t.Fatal("unordered candidates changed actual prompt")
	}
}
func TestWikiProcessingCacheDedupFiltersAndInvalidates(t *testing.T) {
	s := &wikiIngestService{processingCache: newStageTestRuntime()}
	m := &processingCacheChat{identity: "model", response: &types.ChatResponse{Content: `{"merges":{"entity/new":"entity/current","entity/other":"entity/unrelated"}}`, FinishReason: "stop"}}
	ctx := withWikiProcessingScope(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), "doc", "kb")
	candidates := `<item slug="entity/new" type="entity"><name>New</name><candidates><page slug="entity/current" type="entity"><name>Current</name></page></candidates></item>`
	for i := 0; i < 2; i++ {
		raw, err := s.generateWithTemplate(ctx, m, agent.WikiDeduplicationPrompt, map[string]string{"Candidates": candidates})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(raw, "unrelated") {
			t.Fatal("persisted invalid merge")
		}
	}
	if m.calls != 1 {
		t.Fatal("valid merges did not cache")
	}
	candidates = strings.ReplaceAll(candidates, "Current", "Changed page identity")
	s.generateWithTemplate(ctx, m, agent.WikiDeduplicationPrompt, map[string]string{"Candidates": candidates})
	if m.calls != 2 {
		t.Fatal("live candidate change did not invalidate")
	}
}
func TestWikiProcessingCacheDisabledRuntimeKeepsPreviousSlugs(t *testing.T) {
	ctx := withWikiProcessingScope(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), "doc", "kb")
	m := &processingCacheChat{identity: "model"}
	for _, runtime := range []*processingcache.Runtime{nil, processingcache.New(nil, processingcache.Options{ReadEnabled: true}), processingcache.New(&imageCacheStore{rows: map[string]*processingcache.Record{}}, processingcache.Options{})} {
		if runtime.Enabled() {
			t.Fatal("unusable runtime marked enabled")
		}
		s := &wikiIngestService{processingCache: runtime}
		got := s.wikiPreviousSlugInput(ctx, m, map[string]bool{"entity/previous": true})
		if got != "- entity/previous\n" {
			t.Fatalf("disabled cache changed historical prompt: %q", got)
		}
	}
}
