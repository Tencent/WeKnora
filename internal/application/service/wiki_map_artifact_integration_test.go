package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wikiMapChunkRepository struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (r *wikiMapChunkRepository) ListChunksByKnowledgeID(context.Context, uint64, string) ([]*types.Chunk, error) {
	return r.chunks, nil
}

func (r *wikiMapChunkRepository) ListChunksByParentIDs(context.Context, uint64, []string) ([]*types.Chunk, error) {
	return nil, nil
}

type wikiMapKnowledgeService struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
}

func (s *wikiMapKnowledgeService) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

type wikiMapPageService struct {
	interfaces.WikiPageService
	slugs             []string
	slugsErr          error
	candidates        []*types.WikiPageLite
	candidatesByQuery map[string][]*types.WikiPageLite
}

func (s wikiMapPageService) ListSlugsBySourceRef(context.Context, string, string) ([]string, error) {
	return s.slugs, s.slugsErr
}

func (s wikiMapPageService) FindSimilarPages(
	_ context.Context, _ string, query string, _ []string, _ int,
) ([]*types.WikiPageLite, error) {
	if s.candidatesByQuery != nil {
		return s.candidatesByQuery[query], nil
	}
	return s.candidates, nil
}

type wikiMapNoCallChat struct{ t *testing.T }

func (m *wikiMapNoCallChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	m.t.Fatal("map cache hit called chat model")
	return nil, nil
}

func (m *wikiMapNoCallChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	m.t.Fatal("map cache hit called streaming chat model")
	return nil, nil
}

func (m *wikiMapNoCallChat) GetModelName() string { return "chat-model" }
func (m *wikiMapNoCallChat) GetModelID() string   { return "model-1" }

type wikiMapRoutingChat struct {
	mu    sync.Mutex
	calls int
}

func (m *wikiMapRoutingChat) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	if len(messages) > 0 && strings.Contains(messages[0].Content, "<available_wiki_pages>") {
		return &types.ChatResponse{Content: "SUMMARY: Document summary\nSummary body"}, nil
	}
	return &types.ChatResponse{Content: `{"entities":[],"concepts":[]}`}, nil
}

func (m *wikiMapRoutingChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *wikiMapRoutingChat) GetModelName() string { return "chat-model" }
func (m *wikiMapRoutingChat) GetModelID() string   { return "model-1" }

type wikiMapEmptySummaryChat struct{ summary string }

func (m *wikiMapEmptySummaryChat) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	if len(messages) > 0 && strings.Contains(messages[0].Content, "<available_wiki_pages>") {
		return &types.ChatResponse{Content: m.summary}, nil
	}
	return &types.ChatResponse{Content: `{"entities":[],"concepts":[]}`}, nil
}

func (m *wikiMapEmptySummaryChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *wikiMapEmptySummaryChat) GetModelName() string { return "chat-model" }
func (m *wikiMapEmptySummaryChat) GetModelID() string   { return "model-1" }

type wikiMapOutOfOrderCitationChat struct{}

func (m *wikiMapOutOfOrderCitationChat) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	prompt := ""
	if len(messages) > 0 {
		prompt = messages[0].Content
	}
	if strings.Contains(prompt, "first-batch-marker") {
		time.Sleep(50 * time.Millisecond)
		return &types.ChatResponse{Content: `{
			"citations":{},
			"new_slugs":[{
				"type":"entity","name":"Beta First","slug":"entity/beta",
				"description":"first description","details":"first details","source_chunks":["c000"]
			}]
		}`}, nil
	}
	return &types.ChatResponse{Content: `{
		"citations":{},
		"new_slugs":[{
			"type":"entity","name":"Beta Second","slug":"entity/beta",
			"description":"second description","details":"second details","source_chunks":["c000"]
		}]
	}`}, nil
}

func (m *wikiMapOutOfOrderCitationChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *wikiMapOutOfOrderCitationChat) GetModelName() string { return "chat-model" }
func (m *wikiMapOutOfOrderCitationChat) GetModelID() string   { return "model-1" }

type wikiMapArtifactFakeStore struct {
	mu            sync.Mutex
	values        map[types.ProcessingArtifactKey][]byte
	getErr        error
	putErr        error
	invalidateErr error
	getCalls      int
	putCalls      int
	invalidated   []types.ProcessingArtifactKey
	observed      [][]byte
}

func newWikiMapArtifactFakeStore() *wikiMapArtifactFakeStore {
	return &wikiMapArtifactFakeStore{values: make(map[types.ProcessingArtifactKey][]byte)}
}

func (s *wikiMapArtifactFakeStore) Get(_ context.Context, key types.ProcessingArtifactKey) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	if s.getErr != nil {
		return nil, false, s.getErr
	}
	value, ok := s.values[key]
	return append([]byte(nil), value...), ok, nil
}

func (s *wikiMapArtifactFakeStore) PutIfAbsent(
	_ context.Context,
	key types.ProcessingArtifactKey,
	value []byte,
) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putCalls++
	if s.putErr != nil {
		return nil, false, s.putErr
	}
	if canonical, ok := s.values[key]; ok {
		return append([]byte(nil), canonical...), false, nil
	}
	s.values[key] = append([]byte(nil), value...)
	return append([]byte(nil), value...), true, nil
}

func (s *wikiMapArtifactFakeStore) Invalidate(
	_ context.Context,
	key types.ProcessingArtifactKey,
	observed []byte,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidated = append(s.invalidated, key)
	s.observed = append(s.observed, append([]byte(nil), observed...))
	if s.invalidateErr != nil {
		return s.invalidateErr
	}
	delete(s.values, key)
	return nil
}

func TestCompleteWikiMapArtifactReusesAndRebindsCurrentChunks(t *testing.T) {
	store := newWikiMapArtifactFakeStore()
	firstRequest := testWikiMapArtifactRequest()
	computed := 0
	first, hit, status, err := completeWikiMapArtifact(
		context.Background(), store, firstRequest, nil, nil,
		func() (wikiMapArtifactValue, bool, error) {
			computed++
			return wikiMapArtifactValue{
				Entities: []extractedItem{{
					Name: "Acme", Slug: "entity/acme", Description: "desc", Details: "details",
					SourceChunks: []string{"chunk-2"},
				}},
				SummaryContent: "SUMMARY: cached",
			}, true, nil
		},
	)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, wikiMapCacheStatusStored, status)
	assert.Equal(t, []string{"chunk-2"}, first.Entities[0].SourceChunks)

	secondRequest := testWikiMapArtifactRequest()
	secondRequest.chunks = testWikiMapChunks("current-1", "current-2")
	second, hit, status, err := completeWikiMapArtifact(
		context.Background(), store, secondRequest, nil, nil,
		func() (wikiMapArtifactValue, bool, error) {
			t.Fatal("cache hit called compute")
			return wikiMapArtifactValue{}, false, nil
		},
	)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, wikiMapCacheStatusHit, status)
	assert.Equal(t, []string{"current-2"}, second.Entities[0].SourceChunks)
	assert.Equal(t, 1, computed)
	assert.Equal(t, 2, store.getCalls)
	assert.Equal(t, 1, store.putCalls)
}

func TestCompleteWikiMapArtifactRecomputesCorruptHit(t *testing.T) {
	store := newWikiMapArtifactFakeStore()
	request := testWikiMapArtifactRequest()
	key, err := newWikiMapArtifactKey(request)
	require.NoError(t, err)
	store.values[key] = []byte(`{"version":99}`)

	computed := 0
	got, hit, status, err := completeWikiMapArtifact(
		context.Background(), store, request, nil, nil,
		func() (wikiMapArtifactValue, bool, error) {
			computed++
			return wikiMapArtifactValue{SummaryContent: "fresh"}, true, nil
		},
	)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, wikiMapCacheStatusCorrupt, status)
	assert.Equal(t, "fresh", got.SummaryContent)
	assert.Equal(t, 1, computed)
	assert.Equal(t, 1, store.putCalls)
	assert.Equal(t, []types.ProcessingArtifactKey{key}, store.invalidated)
	assert.Equal(t, [][]byte{[]byte(`{"version":99}`)}, store.observed)

	got, hit, status, err = completeWikiMapArtifact(
		context.Background(), store, request, nil, nil,
		func() (wikiMapArtifactValue, bool, error) {
			computed++
			return wikiMapArtifactValue{SummaryContent: "unexpected"}, true, nil
		},
	)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, wikiMapCacheStatusHit, status)
	assert.Equal(t, "fresh", got.SummaryContent)
	assert.Equal(t, 1, computed)
}

func TestCompleteWikiMapArtifactRecomputesAndRepublishesStaleHit(t *testing.T) {
	store := newWikiMapArtifactFakeStore()
	request := testWikiMapArtifactRequest()
	key, err := newWikiMapArtifactKey(request)
	require.NoError(t, err)
	stale, err := encodeWikiMapArtifact(
		wikiMapArtifactValue{SummaryContent: "stale"}, request.chunks,
	)
	require.NoError(t, err)
	store.values[key] = stale
	computed := 0
	validated := 0

	got, hit, status, err := completeWikiMapArtifact(
		context.Background(), store, request,
		func(value wikiMapArtifactValue) bool {
			validated++
			return value.SummaryContent != "stale"
		},
		nil,
		func() (wikiMapArtifactValue, bool, error) {
			computed++
			return wikiMapArtifactValue{SummaryContent: "fresh"}, true, nil
		},
	)

	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, wikiMapCacheStatusStale, status)
	assert.Equal(t, "fresh", got.SummaryContent)
	assert.Equal(t, 1, computed)
	assert.Equal(t, []types.ProcessingArtifactKey{key}, store.invalidated)
	assert.Equal(t, [][]byte{stale}, store.observed)
	assert.Equal(t, 1, validated)
	assert.Equal(t, 1, store.putCalls)

	got, hit, status, err = completeWikiMapArtifact(
		context.Background(), store, request,
		func(value wikiMapArtifactValue) bool {
			validated++
			return value.SummaryContent != "stale"
		},
		nil,
		func() (wikiMapArtifactValue, bool, error) {
			computed++
			return wikiMapArtifactValue{SummaryContent: "unexpected"}, true, nil
		},
	)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, wikiMapCacheStatusHit, status)
	assert.Equal(t, "fresh", got.SummaryContent)
	assert.Equal(t, 1, computed)
	assert.Equal(t, 2, validated)
}

func TestCompleteWikiMapArtifactStopsWhenInvalidationFails(t *testing.T) {
	request := testWikiMapArtifactRequest()
	key, err := newWikiMapArtifactKey(request)
	require.NoError(t, err)
	store := newWikiMapArtifactFakeStore()
	store.invalidateErr = errors.New("invalidate failed")
	store.values[key] = []byte(`{"version":99}`)
	computed := 0

	value, hit, status, err := completeWikiMapArtifact(
		context.Background(), store, request, nil, nil,
		func() (wikiMapArtifactValue, bool, error) {
			computed++
			return wikiMapArtifactValue{SummaryContent: "fallback"}, true, nil
		},
	)

	assert.ErrorIs(t, err, store.invalidateErr)
	assert.Equal(t, wikiMapArtifactValue{}, value)
	assert.False(t, hit)
	assert.Equal(t, wikiMapCacheStatusCorrupt, status)
	assert.Zero(t, computed)
	assert.Equal(t, []types.ProcessingArtifactKey{key}, store.invalidated)
}

func TestCompleteWikiMapArtifactPropagatesStoreErrorsAndSkipsUncacheableResults(t *testing.T) {
	request := testWikiMapArtifactRequest()

	getErr := errors.New("get failed")
	store := newWikiMapArtifactFakeStore()
	store.getErr = getErr
	_, _, _, err := completeWikiMapArtifact(
		context.Background(), store, request, nil, nil,
		func() (wikiMapArtifactValue, bool, error) {
			t.Fatal("store read error called compute")
			return wikiMapArtifactValue{}, false, nil
		},
	)
	require.ErrorIs(t, err, getErr)

	store = newWikiMapArtifactFakeStore()
	got, hit, status, err := completeWikiMapArtifact(
		context.Background(), store, request, nil, nil,
		func() (wikiMapArtifactValue, bool, error) {
			return wikiMapArtifactValue{SummaryContent: "fallback"}, false, nil
		},
	)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, wikiMapCacheStatusUncacheable, status)
	assert.Equal(t, "fallback", got.SummaryContent)
	assert.Zero(t, store.putCalls)

	store = newWikiMapArtifactFakeStore()
	request.modelRevision = ""
	_, _, status, err = completeWikiMapArtifact(
		context.Background(), store, request, nil, nil,
		func() (wikiMapArtifactValue, bool, error) {
			return wikiMapArtifactValue{SummaryContent: "bypass"}, true, nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, wikiMapCacheStatusBypass, status)
	assert.Zero(t, store.getCalls)
	assert.Zero(t, store.putCalls)
}

func TestCompleteWikiMapArtifactConcurrentMissesConverge(t *testing.T) {
	store := newWikiMapArtifactFakeStore()
	request := testWikiMapArtifactRequest()
	start := make(chan struct{})
	results := make(chan string, 2)
	errors := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, summary := range []string{"first", "second"} {
		summary := summary
		go func() {
			ready.Done()
			<-start
			value, _, _, err := completeWikiMapArtifact(
				context.Background(), store, request, nil, nil,
				func() (wikiMapArtifactValue, bool, error) {
					return wikiMapArtifactValue{SummaryContent: summary}, true, nil
				},
			)
			if err != nil {
				errors <- err
				return
			}
			results <- value.SummaryContent
		}()
	}
	ready.Wait()
	close(start)
	first := <-results
	second := <-results
	select {
	case err := <-errors:
		require.NoError(t, err)
	default:
	}
	assert.Equal(t, first, second)
	assert.Contains(t, []string{"first", "second"}, first)
}

type wikiMapConcurrentWinnerStore struct {
	winner        []byte
	invalidateErr error
	invalidated   []types.ProcessingArtifactKey
	observed      [][]byte
	published     bool
	putCalls      int
}

type wikiMapRepairWinnerSequenceStore struct {
	winners     [][]byte
	putCalls    int
	invalidated [][]byte
}

func (s *wikiMapRepairWinnerSequenceStore) Get(
	context.Context, types.ProcessingArtifactKey,
) ([]byte, bool, error) {
	return nil, false, nil
}

func (s *wikiMapRepairWinnerSequenceStore) PutIfAbsent(
	_ context.Context, _ types.ProcessingArtifactKey, _ []byte,
) ([]byte, bool, error) {
	winner := s.winners[s.putCalls]
	s.putCalls++
	return append([]byte(nil), winner...), false, nil
}

func (s *wikiMapRepairWinnerSequenceStore) Invalidate(
	_ context.Context, _ types.ProcessingArtifactKey, observed []byte,
) error {
	s.invalidated = append(s.invalidated, append([]byte(nil), observed...))
	return nil
}

func TestCompleteWikiMapArtifactRejectsStaleWinnerAfterCorruptRepair(t *testing.T) {
	request := testWikiMapArtifactRequest()
	stale, err := encodeWikiMapArtifact(
		wikiMapArtifactValue{SummaryContent: "stale"}, request.chunks,
	)
	require.NoError(t, err)
	store := &wikiMapRepairWinnerSequenceStore{
		winners: [][]byte{[]byte(`{"version":99}`), stale},
	}
	computed := 0
	validated := 0

	got, hit, status, err := completeWikiMapArtifact(
		context.Background(),
		store,
		request,
		nil,
		func(winner, _ wikiMapArtifactValue) bool {
			validated++
			return winner.SummaryContent == "fresh"
		},
		func() (wikiMapArtifactValue, bool, error) {
			computed++
			return wikiMapArtifactValue{SummaryContent: "fresh"}, true, nil
		},
	)

	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, wikiMapCacheStatusCorrupt, status)
	assert.Equal(t, "fresh", got.SummaryContent)
	assert.Equal(t, 1, computed)
	assert.Equal(t, 1, validated)
	assert.Equal(t, 2, store.putCalls)
	assert.Equal(t, [][]byte{[]byte(`{"version":99}`)}, store.invalidated)
}

func (s *wikiMapConcurrentWinnerStore) Get(
	context.Context, types.ProcessingArtifactKey,
) ([]byte, bool, error) {
	if s.published {
		return append([]byte(nil), s.winner...), true, nil
	}
	return nil, false, nil
}

func (s *wikiMapConcurrentWinnerStore) PutIfAbsent(
	_ context.Context, _ types.ProcessingArtifactKey, candidate []byte,
) ([]byte, bool, error) {
	s.putCalls++
	if len(s.invalidated) > 0 {
		s.winner = append([]byte(nil), candidate...)
		s.published = true
		return append([]byte(nil), candidate...), true, nil
	}
	return append([]byte(nil), s.winner...), false, nil
}

func (s *wikiMapConcurrentWinnerStore) Invalidate(
	_ context.Context,
	key types.ProcessingArtifactKey,
	observed []byte,
) error {
	s.invalidated = append(s.invalidated, key)
	s.observed = append(s.observed, append([]byte(nil), observed...))
	return s.invalidateErr
}

func TestCompleteWikiMapArtifactKeepsFreshValueWhenConcurrentWinnerIsStale(t *testing.T) {
	request := testWikiMapArtifactRequest()
	winner, err := encodeWikiMapArtifact(
		wikiMapArtifactValue{SummaryContent: "stale"}, request.chunks,
	)
	require.NoError(t, err)
	computed := 0

	store := &wikiMapConcurrentWinnerStore{winner: winner}
	key, err := newWikiMapArtifactKey(request)
	require.NoError(t, err)
	got, hit, status, err := completeWikiMapArtifact(
		context.Background(), store, request,
		nil,
		func(winner, _ wikiMapArtifactValue) bool { return winner.SummaryContent == "fresh" },
		func() (wikiMapArtifactValue, bool, error) {
			computed++
			return wikiMapArtifactValue{SummaryContent: "fresh"}, true, nil
		},
	)

	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, wikiMapCacheStatusStale, status)
	assert.Equal(t, "fresh", got.SummaryContent)
	assert.Equal(t, 1, computed)
	assert.Equal(t, [][]byte{winner}, store.observed)
	assert.Equal(t, []types.ProcessingArtifactKey{key}, store.invalidated)
	assert.Equal(t, 2, store.putCalls)

	got, hit, status, err = completeWikiMapArtifact(
		context.Background(), store, request, nil, nil,
		func() (wikiMapArtifactValue, bool, error) {
			computed++
			return wikiMapArtifactValue{SummaryContent: "unexpected"}, true, nil
		},
	)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, wikiMapCacheStatusHit, status)
	assert.Equal(t, "fresh", got.SummaryContent)
	assert.Equal(t, 1, computed)
}

func TestCompleteWikiMapArtifactConcurrentInitialMissUsesCanonicalWinner(t *testing.T) {
	request := testWikiMapArtifactRequest()
	winner, err := encodeWikiMapArtifact(wikiMapArtifactValue{
		SummaryContent: "canonical",
		DedupContext: wikiMapDedupContext{
			CandidateFingerprint: wikiDedupCandidateFingerprint(nil),
		},
	}, request.chunks)
	require.NoError(t, err)
	service := &wikiIngestService{wikiService: wikiMapPageService{}}
	oldSlugs := map[string]bool(nil)
	previousFingerprint := wikiPageSlugFingerprint(oldSlugs)
	winnerValue, err := decodeWikiMapArtifact(winner, request.chunks)
	require.NoError(t, err)
	winnerValue.PreviousSlugsFingerprint = previousFingerprint
	winner, err = encodeWikiMapArtifact(winnerValue, request.chunks)
	require.NoError(t, err)

	got, hit, status, err := completeWikiMapArtifact(
		context.Background(), &wikiMapConcurrentWinnerStore{winner: winner}, request,
		func(value wikiMapArtifactValue) bool {
			return service.wikiMapArtifactStateCurrent(
				context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
			)
		},
		func(winner, computed wikiMapArtifactValue) bool {
			return service.wikiMapConcurrentWinnerCurrent(
				context.Background(), "kb-1", "summary/knowledge-1", winner, computed,
			)
		},
		func() (wikiMapArtifactValue, bool, error) {
			return wikiMapArtifactValue{
				SummaryContent:           "local",
				PreviousSlugsFingerprint: previousFingerprint,
				DedupContext: wikiMapDedupContext{
					CandidateFingerprint: wikiDedupCandidateFingerprint(nil),
				},
			}, true, nil
		},
	)

	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, wikiMapCacheStatusStored, status)
	assert.Equal(t, "canonical", got.SummaryContent)
}

func TestMapOneDocumentReusesArtifactAndRebindsCurrentChunks(t *testing.T) {
	store := newWikiMapArtifactFakeStore()
	oldChunks := testWikiMapChunks("old-chunk-1", "old-chunk-2")
	content := reconstructContent(oldChunks)
	request := testWikiMapArtifactRequest()
	request.content = content
	request.chunks = oldChunks
	request.language = types.LanguageLocaleName("zh-CN")
	key, err := newWikiMapArtifactKey(request)
	require.NoError(t, err)
	payload, err := encodeWikiMapArtifact(wikiMapArtifactValue{
		Entities: []extractedItem{{
			Name: "Acme", Slug: "entity/acme", Description: "Company", Details: "Company details",
			SourceChunks: []string{"old-chunk-2"},
		}},
		SummaryContent:  "SUMMARY: Cached document\nCached body",
		CitationBatches: 1,
		DedupContext: wikiMapDedupContext{
			Entities: []extractedItem{{
				Name: "Acme", Slug: "entity/acme", Description: "Company", Details: "Company details",
			}},
			CandidateFingerprint: wikiDedupCandidateFingerprint(nil),
		},
	}, oldChunks)
	require.NoError(t, err)
	store.values[key] = payload

	currentChunks := testWikiMapChunks("current-chunk-1", "current-chunk-2")
	service := &wikiIngestService{
		chunkRepo:    &wikiMapChunkRepository{chunks: currentChunks},
		knowledgeSvc: &wikiMapKnowledgeService{knowledge: &types.Knowledge{ID: "knowledge-1", Title: "Document"}},
		wikiService: wikiMapPageService{slugs: []string{
			"summary/knowledge-1", "entity/acme",
		}},
		artifactStore: store,
	}
	batchCtx := &WikiBatchContext{
		ExtractionGranularity:       types.WikiExtractionStandard,
		ContentInstructions:         "content instruction",
		ExtractionInstructions:      "extraction instruction",
		SummaryContentByKnowledgeID: func(context.Context, string) string { return "" },
	}

	result, updates, err := service.mapOneDocument(
		context.Background(), &wikiMapNoCallChat{t: t}, "revision-1",
		WikiIngestPayload{TenantID: 11, KnowledgeBaseID: "kb-1"},
		WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "knowledge-1", Language: "zh-CN"},
		batchCtx,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, true, result.MapStats["map_cache_hit"])
	assert.Equal(t, wikiMapCacheStatusHit, result.MapStats["map_cache_status"])

	var entityUpdate *SlugUpdate
	for i := range updates {
		if updates[i].Slug == "entity/acme" {
			entityUpdate = &updates[i]
			break
		}
	}
	require.NotNil(t, entityUpdate)
	assert.Equal(t, []string{"current-chunk-2"}, entityUpdate.SourceChunks)
	assert.Equal(t, []string{"current-chunk-2"}, entityUpdate.Item.SourceChunks)
	for _, update := range updates {
		assert.NotContains(t, update.SourceChunks, "old-chunk-1")
		assert.NotContains(t, update.SourceChunks, "old-chunk-2")
	}
}

func TestMapOneDocumentDoesNotCacheWhenPreviousSlugLookupFails(t *testing.T) {
	store := newWikiMapArtifactFakeStore()
	service := &wikiIngestService{
		chunkRepo:     &wikiMapChunkRepository{chunks: testWikiMapChunks("chunk-1", "chunk-2")},
		knowledgeSvc:  &wikiMapKnowledgeService{knowledge: &types.Knowledge{ID: "knowledge-1", Title: "Document"}},
		wikiService:   wikiMapPageService{slugsErr: errors.New("lookup failed")},
		artifactStore: store,
	}
	batchCtx := &WikiBatchContext{
		ExtractionGranularity:       types.WikiExtractionStandard,
		SummaryContentByKnowledgeID: func(context.Context, string) string { return "" },
	}

	result, _, err := service.mapOneDocument(
		context.Background(), &wikiMapRoutingChat{}, "revision-1",
		WikiIngestPayload{TenantID: 11, KnowledgeBaseID: "kb-1"},
		WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "knowledge-1", Language: "en-US"},
		batchCtx,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, wikiMapCacheStatusUncacheable, result.MapStats["map_cache_status"])
	assert.Zero(t, store.putCalls)
}

func TestMapOneDocumentReusesSummaryOnlyArtifact(t *testing.T) {
	store := newWikiMapArtifactFakeStore()
	service := &wikiIngestService{
		chunkRepo:     &wikiMapChunkRepository{chunks: testWikiMapChunks("chunk-1", "chunk-2")},
		knowledgeSvc:  &wikiMapKnowledgeService{knowledge: &types.Knowledge{ID: "knowledge-1", Title: "Document"}},
		wikiService:   wikiMapPageService{slugs: []string{"summary/knowledge-1"}},
		artifactStore: store,
	}
	batchCtx := &WikiBatchContext{
		ExtractionGranularity:       types.WikiExtractionStandard,
		SummaryContentByKnowledgeID: func(context.Context, string) string { return "" },
	}
	payload := WikiIngestPayload{TenantID: 11, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "knowledge-1", Language: "en-US"}

	first, _, err := service.mapOneDocument(
		context.Background(), &wikiMapRoutingChat{}, "revision-1", payload, op, batchCtx,
	)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, wikiMapCacheStatusStored, first.MapStats["map_cache_status"])

	second, _, err := service.mapOneDocument(
		context.Background(), &wikiMapNoCallChat{t: t}, "revision-1", payload, op, batchCtx,
	)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, true, second.MapStats["map_cache_hit"])
	assert.Equal(t, wikiMapCacheStatusHit, second.MapStats["map_cache_status"])
}

func TestMapOneDocumentRejectsBlankSummary(t *testing.T) {
	for _, summary := range []string{" \t\n", "SUMMARY:", "SUMMARY：\n"} {
		t.Run(summary, func(t *testing.T) {
			store := newWikiMapArtifactFakeStore()
			service := &wikiIngestService{
				chunkRepo:     &wikiMapChunkRepository{chunks: testWikiMapChunks("chunk-1", "chunk-2")},
				knowledgeSvc:  &wikiMapKnowledgeService{knowledge: &types.Knowledge{ID: "knowledge-1", Title: "Document"}},
				wikiService:   wikiMapPageService{},
				artifactStore: store,
			}
			batchCtx := &WikiBatchContext{
				ExtractionGranularity:       types.WikiExtractionStandard,
				SummaryContentByKnowledgeID: func(context.Context, string) string { return "" },
			}

			result, updates, err := service.mapOneDocument(
				context.Background(), &wikiMapEmptySummaryChat{summary: summary}, "revision-1",
				WikiIngestPayload{TenantID: 11, KnowledgeBaseID: "kb-1"},
				WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "knowledge-1", Language: "en-US"},
				batchCtx,
			)

			require.Error(t, err)
			assert.Nil(t, result)
			assert.Empty(t, updates)
			assert.Zero(t, store.putCalls)
		})
	}
}

func TestNewWikiIngestServiceInjectsArtifactStore(t *testing.T) {
	store := newWikiMapArtifactFakeStore()
	handler := NewWikiIngestService(
		nil, nil, nil, nil, nil, nil, store, nil, nil, nil, nil, nil, nil,
	)
	service, ok := handler.(*wikiIngestService)
	require.True(t, ok)
	assert.Same(t, store, service.artifactStore)
}

func TestWikiMapArtifactStateRejectsChangedWikiDependencies(t *testing.T) {
	value := wikiMapArtifactValue{
		Entities: []extractedItem{{Name: "Acme", Slug: "entity/acme"}},
		DedupContext: wikiMapDedupContext{
			Entities:             []extractedItem{{Name: "Acme", Slug: "entity/acme"}},
			CandidateFingerprint: wikiDedupCandidateFingerprint(nil),
		},
	}
	oldSlugs := map[string]bool{"summary/knowledge-1": true, "entity/acme": true}
	service := &wikiIngestService{wikiService: wikiMapPageService{}}
	assert.True(t, service.wikiMapArtifactStateCurrent(
		context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
	))
	service.wikiService = wikiMapPageService{candidates: []*types.WikiPageLite{{
		Slug: "entity/acme", Title: "Acme", PageType: types.WikiPageTypeEntity,
	}}}
	assert.True(t, service.wikiMapArtifactStateCurrent(
		context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
	))

	service.wikiService = wikiMapPageService{candidates: []*types.WikiPageLite{{
		Slug: "entity/existing", Title: "Existing", PageType: types.WikiPageTypeEntity,
	}}}
	assert.False(t, service.wikiMapArtifactStateCurrent(
		context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
	))

	delete(oldSlugs, "entity/acme")
	service.wikiService = wikiMapPageService{}
	assert.False(t, service.wikiMapArtifactStateCurrent(
		context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
	))
}

func TestWikiMapArtifactStateIgnoresOnlyNewSiblingPages(t *testing.T) {
	pageA := &types.WikiPageLite{Slug: "entity/a", Title: "A", PageType: types.WikiPageTypeEntity}
	pageB := &types.WikiPageLite{Slug: "entity/b", Title: "B", PageType: types.WikiPageTypeEntity}
	oldSlugs := map[string]bool{
		"summary/knowledge-1": true,
		"entity/a":            true,
		"entity/b":            true,
	}
	service := &wikiIngestService{wikiService: wikiMapPageService{candidatesByQuery: map[string][]*types.WikiPageLite{
		"A": {pageB},
		"B": {pageA},
	}}}
	value := wikiMapArtifactValue{
		Entities: []extractedItem{{Name: "A", Slug: "entity/a"}, {Name: "B", Slug: "entity/b"}},
		DedupContext: wikiMapDedupContext{
			Entities:                    []extractedItem{{Name: "A", Slug: "entity/a"}, {Name: "B", Slug: "entity/b"}},
			CandidateFingerprint:        wikiDedupCandidateFingerprint(nil),
			ReducedCandidateFingerprint: wikiDedupCandidateFingerprint(nil),
			OutputPageFingerprints: map[string]string{
				"entity/a": wikiDedupPageFingerprint(pageA),
				"entity/b": wikiDedupPageFingerprint(pageB),
			},
		},
	}

	assert.True(t, service.wikiMapArtifactStateCurrent(
		context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
	))
	pageB.Title = "B changed"
	assert.False(t, service.wikiMapArtifactStateCurrent(
		context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
	))
	pageB.Title = "B"

	value.DedupContext.CandidateSlugs = []string{"entity/b"}
	value.DedupContext.CandidateFingerprint = wikiDedupCandidateFingerprint(
		map[string]*types.WikiPageLite{"entity/b": pageB},
	)
	value.DedupContext.ReducedCandidateFingerprint = value.DedupContext.CandidateFingerprint
	assert.True(t, service.wikiMapArtifactStateCurrent(
		context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
	))

	pageB.Title = "B changed"
	assert.False(t, service.wikiMapArtifactStateCurrent(
		context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
	))
}

func TestWikiMapArtifactStateAcceptsPreAndPostReduceCandidateState(t *testing.T) {
	before := &types.WikiPageLite{
		Slug: "entity/existing", Title: "Existing", PageType: types.WikiPageTypeEntity,
	}
	after := &types.WikiPageLite{
		Slug: "entity/existing", Title: "Existing", PageType: types.WikiPageTypeEntity,
		Aliases: types.StringArray{"Acme"},
	}
	value := wikiMapArtifactValue{
		Entities: []extractedItem{{Name: "Acme", Slug: "entity/existing", Aliases: []string{"Acme"}}},
		DedupContext: wikiMapDedupContext{
			Entities:                    []extractedItem{{Name: "Acme", Slug: "entity/acme", Aliases: []string{"Acme"}}},
			CandidateSlugs:              []string{"entity/existing"},
			CandidateFingerprint:        wikiDedupPageFingerprint(before),
			ReducedCandidateFingerprint: wikiDedupPageFingerprint(after),
		},
	}
	oldSlugs := map[string]bool{"summary/knowledge-1": true, "entity/existing": true}

	for _, candidate := range []*types.WikiPageLite{before, after} {
		service := &wikiIngestService{wikiService: wikiMapPageService{candidates: []*types.WikiPageLite{candidate}}}
		assert.True(t, service.wikiMapArtifactStateCurrent(
			context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
		))
	}

	changed := *after
	changed.Title = "Externally renamed"
	service := &wikiIngestService{wikiService: wikiMapPageService{candidates: []*types.WikiPageLite{&changed}}}
	assert.False(t, service.wikiMapArtifactStateCurrent(
		context.Background(), "kb-1", "summary/knowledge-1", oldSlugs, value,
	))
}

func TestFindWikiDedupCandidatesExcludesOnlyCurrentItemSlug(t *testing.T) {
	existingB := &types.WikiPageLite{
		Slug: "entity/b", Title: "B", PageType: types.WikiPageTypeEntity,
	}
	service := &wikiIngestService{wikiService: wikiMapPageService{
		candidatesByQuery: map[string][]*types.WikiPageLite{
			"A": {existingB},
			"B": {existingB},
		},
	}}

	candidates, complete := service.findWikiDedupCandidates(
		context.Background(), "kb-1",
		[]extractedItem{{Name: "A", Slug: "entity/a"}, {Name: "B", Slug: "entity/b"}},
		nil,
	)

	assert.True(t, complete)
	assert.Equal(t, map[string]*types.WikiPageLite{"entity/b": existingB}, candidates)
}

func TestDeduplicateExtractedBatchDoesNotCacheRejectedMerges(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "invalid target type", response: `{"merges":{"entity/new":"concept/existing"}}`},
		{name: "unknown source", response: `{"merges":{"entity/unknown":"entity/existing"}}`},
		{name: "null object", response: `null`},
		{name: "empty object", response: `{}`},
		{name: "missing merges", response: `{"other":{}}`},
		{name: "null merges", response: `{"merges":null}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &wikiIngestService{wikiService: wikiMapPageService{
				candidates: []*types.WikiPageLite{
					{Slug: "entity/existing", Title: "Existing entity", PageType: types.WikiPageTypeEntity},
					{Slug: "concept/existing", Title: "Existing concept", PageType: types.WikiPageTypeConcept},
				},
			}}
			model := &templateCaptureChatModel{response: tt.response}

			entities, concepts, _, complete := service.deduplicateExtractedBatch(
				context.Background(), model, "kb-1",
				[]extractedItem{{Name: "New", Slug: "entity/new"}}, nil,
			)

			assert.False(t, complete)
			assert.Equal(t, []extractedItem{{Name: "New", Slug: "entity/new"}}, entities)
			assert.Empty(t, concepts)
		})
	}
}

func TestDeduplicateExtractedBatchCoalescesManyToOneMerges(t *testing.T) {
	service := &wikiIngestService{wikiService: wikiMapPageService{candidates: []*types.WikiPageLite{{
		Slug: "entity/existing", Title: "Existing entity", PageType: types.WikiPageTypeEntity,
	}}}}
	model := &templateCaptureChatModel{response: `{"merges":{
		"entity/alpha":"entity/existing",
		"entity/beta":"entity/existing"
	}}`}

	entities, concepts, dedupContext, complete := service.deduplicateExtractedBatch(
		context.Background(), model, "kb-1",
		[]extractedItem{
			{Name: "Alpha", Slug: "entity/alpha", Aliases: []string{"A"}, Description: "alpha description", Details: "alpha details"},
			{Name: "Beta", Slug: "entity/beta", Aliases: []string{"B"}, Description: "beta description", Details: "beta details"},
		}, nil,
	)

	assert.True(t, complete)
	assert.Empty(t, concepts)
	require.Len(t, entities, 1)
	assert.Equal(t, "entity/existing", entities[0].Slug)
	assert.Equal(t, "Alpha", entities[0].Name)
	assert.ElementsMatch(t, []string{"A", "B", "Beta"}, entities[0].Aliases)
	assert.Contains(t, entities[0].Description, "alpha description")
	assert.Contains(t, entities[0].Description, "beta description")
	assert.Contains(t, entities[0].Details, "alpha details")
	assert.Contains(t, entities[0].Details, "beta details")
	assert.Len(t, dedupContext.Entities, 2)

	entities, _, _ = mergeCitationsIntoItems(
		entities, nil, map[string][]string{"entity/existing": {"chunk-1"}}, nil,
	)
	encoded, err := encodeWikiMapArtifact(wikiMapArtifactValue{
		Entities: entities, SummaryContent: "SUMMARY: valid", DedupContext: dedupContext,
	}, testWikiMapChunks("chunk-1"))
	require.NoError(t, err)
	decoded, err := decodeWikiMapArtifact(encoded, testWikiMapChunks("current-1"))
	require.NoError(t, err)
	require.Len(t, decoded.Entities, 1)
	assert.Equal(t, []string{"current-1"}, decoded.Entities[0].SourceChunks)
}

func TestClassifyChunkCitationsDoesNotCacheUnknownAliases(t *testing.T) {
	service := &wikiIngestService{}
	model := &templateCaptureChatModel{response: `{
		"citations":{"entity/acme":["c999"]},
		"new_slugs":[{
			"type":"entity","name":"Beta","slug":"entity/beta",
			"description":"desc","details":"details","source_chunks":["c999"]
		}]
	}`}

	_, newSlugs, _, complete := service.classifyChunkCitations(
		context.Background(), model, "- entity/acme", testWikiCandidateSlugSet("entity/acme"),
		testWikiMapChunks("chunk-1"),
		"English", &WikiBatchContext{},
	)

	assert.False(t, complete)
	assert.Empty(t, newSlugs)
}

func TestClassifyChunkCitationsKeepsOnlyValidAliasesFromCompleteNewSlug(t *testing.T) {
	service := &wikiIngestService{}
	model := &templateCaptureChatModel{response: `{
		"citations":{},
		"new_slugs":[{
			"type":"entity","name":"Beta","slug":"entity/beta",
			"description":"desc","details":"details","source_chunks":["c000","c999"]
		}]
	}`}

	_, newSlugs, _, complete := service.classifyChunkCitations(
		context.Background(), model, "- entity/acme", testWikiCandidateSlugSet("entity/acme"),
		testWikiMapChunks("chunk-1"),
		"English", &WikiBatchContext{},
	)

	assert.False(t, complete)
	require.Len(t, newSlugs, 1)
	assert.Equal(t, []string{"chunk-1"}, newSlugs[0].SourceChunks)
}

func TestClassifyChunkCitationsDeduplicatesNewSlugAliases(t *testing.T) {
	service := &wikiIngestService{}
	model := &templateCaptureChatModel{response: `{
		"citations":{},
		"new_slugs":[{
			"type":"entity","name":"Beta","slug":"entity/beta",
			"description":"desc","details":"details","source_chunks":["c000","c000"]
		}]
	}`}

	_, newSlugs, _, complete := service.classifyChunkCitations(
		context.Background(), model, "- entity/acme", testWikiCandidateSlugSet("entity/acme"),
		testWikiMapChunks("chunk-1"), "English", &WikiBatchContext{},
	)

	assert.True(t, complete)
	require.Len(t, newSlugs, 1)
	assert.Equal(t, []string{"chunk-1"}, newSlugs[0].SourceChunks)
}

func TestClassifyChunkCitationsDoesNotCacheIncompleteNewSlugs(t *testing.T) {
	tests := []struct {
		name    string
		newSlug string
	}{
		{name: "missing type", newSlug: `{"name":"Beta","slug":"entity/beta","description":"desc","details":"details","source_chunks":["c000"]}`},
		{name: "type prefix mismatch", newSlug: `{"type":"concept","name":"Beta","slug":"entity/beta","description":"desc","details":"details","source_chunks":["c000"]}`},
		{name: "missing description", newSlug: `{"type":"entity","name":"Beta","slug":"entity/beta","details":"details","source_chunks":["c000"]}`},
		{name: "missing details", newSlug: `{"type":"entity","name":"Beta","slug":"entity/beta","description":"desc","source_chunks":["c000"]}`},
		{name: "missing source chunks", newSlug: `{"type":"entity","name":"Beta","slug":"entity/beta","description":"desc","details":"details"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &wikiIngestService{}
			model := &templateCaptureChatModel{response: `{"citations":{},"new_slugs":[` + tt.newSlug + `]}`}

			_, newSlugs, _, complete := service.classifyChunkCitations(
				context.Background(), model, "- entity/acme", testWikiCandidateSlugSet("entity/acme"),
				testWikiMapChunks("chunk-1"),
				"English", &WikiBatchContext{},
			)

			assert.False(t, complete)
			assert.Empty(t, newSlugs)
		})
	}
}

func TestExtractCandidateSlugsRejectsIncompleteResponses(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "null object", response: `null`},
		{name: "empty object", response: `{}`},
		{name: "missing entities", response: `{"concepts":[]}`},
		{name: "missing concepts", response: `{"entities":[]}`},
		{name: "null entities", response: `{"entities":null,"concepts":[]}`},
		{name: "null concepts", response: `{"entities":[],"concepts":null}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &wikiIngestService{}
			model := &templateCaptureChatModel{response: tt.response}

			_, _, _, _, complete, err := service.extractCandidateSlugs(
				context.Background(), model, "kb-1", "document", "English", nil,
				&WikiBatchContext{},
			)

			require.Error(t, err)
			assert.False(t, complete)
		})
	}
}

func TestExtractCandidateSlugsRejectsInvalidItems(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{
			name:     "entity with concept prefix",
			response: `{"entities":[{"name":"A","slug":"concept/a","description":"desc","details":"details"}],"concepts":[]}`,
		},
		{
			name:     "concept with entity prefix",
			response: `{"entities":[],"concepts":[{"name":"A","slug":"entity/a","description":"desc","details":"details"}]}`,
		},
		{
			name:     "missing description",
			response: `{"entities":[{"name":"A","slug":"entity/a","details":"details"}],"concepts":[]}`,
		},
		{
			name:     "missing details",
			response: `{"entities":[{"name":"A","slug":"entity/a","description":"desc"}],"concepts":[]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &wikiIngestService{}
			model := &templateCaptureChatModel{response: tt.response}

			_, _, _, _, complete, err := service.extractCandidateSlugs(
				context.Background(), model, "kb-1", "document", "English", nil,
				&WikiBatchContext{},
			)

			require.Error(t, err)
			assert.False(t, complete)
		})
	}
}

func TestClassifyChunkCitationsRejectsUnknownCandidateSlugs(t *testing.T) {
	service := &wikiIngestService{}
	model := &templateCaptureChatModel{response: `{
		"citations":{"entity/unknown":["c000"]},
		"new_slugs":[]
	}`}

	citations, _, _, complete := service.classifyChunkCitations(
		context.Background(), model, "- entity/acme", testWikiCandidateSlugSet("entity/acme"),
		testWikiMapChunks("chunk-1"),
		"English", &WikiBatchContext{},
	)

	assert.False(t, complete)
	assert.NotContains(t, citations, "entity/unknown")
}

func TestClassifyChunkCitationsRecoversRediscoveredCandidateReferences(t *testing.T) {
	service := &wikiIngestService{}
	model := &templateCaptureChatModel{response: `{
		"citations":{},
		"new_slugs":[{
			"type":"entity","name":"Acme","slug":"entity/acme",
			"description":"desc","details":"details","source_chunks":["c000"]
		}]
	}`}

	citations, newSlugs, _, complete := service.classifyChunkCitations(
		context.Background(), model, "- entity/acme", testWikiCandidateSlugSet("entity/acme"),
		testWikiMapChunks("chunk-1"), "English", &WikiBatchContext{},
	)

	assert.False(t, complete)
	assert.Equal(t, []string{"chunk-1"}, citations["entity/acme"])
	assert.Empty(t, newSlugs)
}

func TestClassifyChunkCitationsRecoversRediscoveredCandidateWithIncompleteMetadata(t *testing.T) {
	service := &wikiIngestService{}
	model := &templateCaptureChatModel{response: `{
		"citations":{},
		"new_slugs":[{"slug":"entity/acme","source_chunks":["c000"]}]
	}`}

	citations, newSlugs, _, complete := service.classifyChunkCitations(
		context.Background(), model, "- entity/acme", testWikiCandidateSlugSet("entity/acme"),
		testWikiMapChunks("chunk-1"), "English", &WikiBatchContext{},
	)

	assert.False(t, complete)
	assert.Equal(t, []string{"chunk-1"}, citations["entity/acme"])
	assert.Empty(t, newSlugs)
}

func TestClassifyChunkCitationsOrdersEqualIndexesByDocumentPosition(t *testing.T) {
	chunks := []*types.Chunk{
		{ID: "chunk-late", ChunkIndex: 0, StartAt: 100, ChunkType: types.ChunkTypeText, Content: "late"},
		{ID: "chunk-early", ChunkIndex: 0, StartAt: 0, ChunkType: types.ChunkTypeText, Content: "early"},
	}
	service := &wikiIngestService{}
	model := &templateCaptureChatModel{response: `{
		"citations":{"entity/acme":["c000","c001"]},
		"new_slugs":[]
	}`}

	for range 50 {
		citations, _, _, complete := service.classifyChunkCitations(
			context.Background(), model, "- entity/acme", testWikiCandidateSlugSet("entity/acme"),
			chunks, "English", &WikiBatchContext{},
		)

		assert.True(t, complete)
		assert.Equal(t, []string{"chunk-early", "chunk-late"}, citations["entity/acme"])
	}
}

func TestSplitChunksIntoCitationBatchesCanonicalizesPositionTies(t *testing.T) {
	alpha := &types.Chunk{
		ID: "chunk-alpha", Content: "alpha", ChunkIndex: 0, StartAt: 0, ChunkType: types.ChunkTypeText,
	}
	beta := &types.Chunk{
		ID: "chunk-beta", Content: "beta", ChunkIndex: 0, StartAt: 0, ChunkType: types.ChunkTypeText,
	}

	for _, chunks := range [][]*types.Chunk{{beta, alpha}, {alpha, beta}} {
		batches := splitChunksIntoCitationBatches(chunks)
		require.Len(t, batches, 1)
		assert.Equal(t, "chunk-alpha", batches[0].aliasToID["c000"])
		assert.Equal(t, "chunk-beta", batches[0].aliasToID["c001"])
	}
}

func TestClassifyChunkCitationsDeterministicallyMergesConflictingNewSlugs(t *testing.T) {
	chunks := []*types.Chunk{
		{
			ID: "chunk-first", ChunkIndex: 0, ChunkType: types.ChunkTypeText,
			Content: "first-batch-marker" + strings.Repeat("a", maxRunesPerCitationBatch),
		},
		{
			ID: "chunk-second", ChunkIndex: 1, ChunkType: types.ChunkTypeText,
			Content: "second-batch-marker" + strings.Repeat("b", maxRunesPerCitationBatch),
		},
	}
	service := &wikiIngestService{}

	_, newSlugs, batchCount, complete := service.classifyChunkCitations(
		context.Background(), &wikiMapOutOfOrderCitationChat{}, "- entity/acme",
		testWikiCandidateSlugSet("entity/acme"), chunks, "English", &WikiBatchContext{},
	)
	entities, _, _ := mergeCitationsIntoItems(nil, nil, nil, newSlugs)

	assert.Equal(t, 2, batchCount)
	assert.False(t, complete)
	require.Len(t, entities, 1)
	assert.Equal(t, "Beta First", entities[0].Name)
	assert.Equal(t, "first description", entities[0].Description)
	assert.Equal(t, []string{"chunk-first", "chunk-second"}, entities[0].SourceChunks)
}

func TestClassifyChunkCitationsDoesNotCacheIncompleteResponses(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "null object", response: `null`},
		{name: "empty object", response: `{}`},
		{name: "missing citations", response: `{"new_slugs":[]}`},
		{name: "missing new slugs", response: `{"citations":{}}`},
		{name: "null citations", response: `{"citations":null,"new_slugs":[]}`},
		{name: "null new slugs", response: `{"citations":{},"new_slugs":null}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &wikiIngestService{}
			model := &templateCaptureChatModel{response: tt.response}

			_, _, _, complete := service.classifyChunkCitations(
				context.Background(), model, "- entity/acme", testWikiCandidateSlugSet("entity/acme"),
				testWikiMapChunks("chunk-1"),
				"English", &WikiBatchContext{},
			)

			assert.False(t, complete)
		})
	}
}

func testWikiCandidateSlugSet(slugs ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(slugs))
	for _, slug := range slugs {
		set[slug] = struct{}{}
	}
	return set
}
