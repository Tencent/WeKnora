package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/contentkey"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type wikiMapTestChunkRepo struct {
	interfaces.ChunkRepository
	mu     sync.Mutex
	chunks []*types.Chunk
}

func (r *wikiMapTestChunkRepo) ListChunksByKnowledgeID(context.Context, uint64, string) ([]*types.Chunk, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*types.Chunk(nil), r.chunks...), nil
}
func (r *wikiMapTestChunkRepo) ListChunksByParentIDs(context.Context, uint64, []string) ([]*types.Chunk, error) {
	return nil, nil
}

type wikiMapTestKnowledgeService struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
}

func (s *wikiMapTestKnowledgeService) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

type wikiMapTestWikiService struct {
	interfaces.WikiPageService
	mu               sync.Mutex
	pages            map[string]*types.WikiPage
	creates, updates int
	createErr        error
}

func (s *wikiMapTestWikiService) ListSlugsBySourceRef(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (s *wikiMapTestWikiService) FindSimilarPages(context.Context, string, string, []string, int) ([]*types.WikiPageLite, error) {
	return nil, nil
}
func (s *wikiMapTestWikiService) GetPageBySlug(_ context.Context, _ string, slug string) (*types.WikiPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.pages[slug]; p != nil {
		cp := *p
		return &cp, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (s *wikiMapTestWikiService) CreatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return nil, s.createErr
	}
	cp := *page
	s.pages[page.Slug] = &cp
	s.creates++
	return &cp, nil
}

func TestIngestionArtifactRecovery_WikiMapSucceededReduceFailed(t *testing.T) {
	svc, _, wiki, model, db := newWikiMapIntegrationService(t)
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	batch := wikiMapIntegrationBatchContext()
	result, updates, err := svc.mapOneDocument(context.Background(), model, payload, op, batch)
	require.NoError(t, err)
	require.NotNil(t, result)
	firstCalls := model.Count()
	require.Greater(t, firstCalls, 0)
	var artifact types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", wikiMapArtifactKind).Take(&artifact).Error)
	require.Equal(t, types.DerivedArtifactSucceeded, artifact.Status)

	wiki.createErr = errors.New("wiki reduce materialization failed")
	_, _, _, err = svc.reduceSlugUpdates(context.Background(), model, "kb-1", updates[0].Slug, []SlugUpdate{updates[0]}, 1, batch, nil)
	require.ErrorContains(t, err, "wiki reduce materialization failed")
	wiki.createErr = nil

	hit, retryUpdates, err := svc.mapOneDocument(context.Background(), model, payload, op, batch)
	require.NoError(t, err)
	require.Equal(t, types.IngestionCacheStatusHit, hit.MapStats["cache_status"])
	require.Equal(t, firstCalls, model.Count(), "retry must not repeat the pure map chat")
	changed, _, _, err := svc.reduceSlugUpdates(context.Background(), model, "kb-1", retryUpdates[0].Slug, []SlugUpdate{retryUpdates[0]}, 1, batch, nil)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 1, wiki.creates)
}
func (s *wikiMapTestWikiService) UpdatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *page
	s.pages[page.Slug] = &cp
	s.updates++
	return &cp, nil
}

type wikiMapCountingChat struct {
	mu    sync.Mutex
	calls int
	id    string
	name  string
}

func (c *wikiMapCountingChat) Chat(_ context.Context, messages []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	if call == 1 {
		return &types.ChatResponse{Content: `{"entities":[],"concepts":[]}`}, nil
	}
	return &types.ChatResponse{Content: "SUMMARY: cached document\n\nCanonical summary body."}, nil
}
func (*wikiMapCountingChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c *wikiMapCountingChat) GetModelName() string {
	if c.name != "" {
		return c.name
	}
	return "wiki-map-test-model"
}
func (c *wikiMapCountingChat) GetModelID() string {
	if c.id != "" {
		return c.id
	}
	return "wiki-map-test-model-id"
}
func (c *wikiMapCountingChat) Count() int { c.mu.Lock(); defer c.mu.Unlock(); return c.calls }

func newWikiMapIntegrationService(t *testing.T) (*wikiIngestService, *wikiMapTestChunkRepo, *wikiMapTestWikiService, *wikiMapCountingChat, *gorm.DB) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.ToSlash(filepath.Join(t.TempDir(), "wiki-map.db")))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.DerivedArtifact{}))
	chunkRepo := &wikiMapTestChunkRepo{chunks: []*types.Chunk{{ID: "row-1", TenantID: 1, KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Content: "This is enough stable source text for wiki mapping.", ChunkIndex: 0, StartAt: 0, ChunkType: types.ChunkTypeText, IsEnabled: true, StableIdentity: "stable-1", IdentityVersion: contentkey.ChunkIdentityVersion}}}
	wiki := &wikiMapTestWikiService{pages: make(map[string]*types.WikiPage)}
	chatModel := &wikiMapCountingChat{}
	svc := &wikiIngestService{artifactRepo: repository.NewDerivedArtifactRepository(db), chunkRepo: chunkRepo, wikiService: wiki, knowledgeSvc: &wikiMapTestKnowledgeService{knowledge: &types.Knowledge{ID: "doc-1", Title: "Document", ParseStatus: types.ParseStatusCompleted}}}
	return svc, chunkRepo, wiki, chatModel, db
}

func wikiMapIntegrationBatchContext() *WikiBatchContext {
	return &WikiBatchContext{ExtractionGranularity: types.WikiExtractionStandard, SummaryContentByKnowledgeID: func(context.Context, string) string { return "" }, SlugTitle: func(context.Context, string) string { return "" }, SlugTitleMany: func(context.Context, []string) map[string]string { return nil }}
}

func TestWikiMapArtifactSecondRunSkipsPureMapChatAndStillReduces(t *testing.T) {
	svc, chunks, wiki, chatModel, _ := newWikiMapIntegrationService(t)
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	first, _, err := svc.mapOneDocument(context.Background(), chatModel, payload, op, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.NotNil(t, first)
	firstCalls := chatModel.Count()
	require.Greater(t, firstCalls, 0)
	require.EqualValues(t, firstCalls, first.MapStats["request_count"])
	require.Equal(t, types.IngestionCacheStatusMiss, first.MapStats["cache_status"])
	require.EqualValues(t, 1, first.MapStats["computed_items"])
	require.EqualValues(t, 0, first.MapStats["reused_items"])
	chunks.mu.Lock()
	chunks.chunks = append(chunks.chunks, &types.Chunk{ID: "derived-summary", IsEnabled: true, ChunkType: types.ChunkTypeSummary, Content: "changed derived content must not invalidate map"})
	chunks.mu.Unlock()
	second, updates, err := svc.mapOneDocument(context.Background(), chatModel, payload, op, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, firstCalls, chatModel.Count(), "cache hit must issue zero pure-map model requests")
	require.Equal(t, types.IngestionCacheStatusHit, second.MapStats["cache_status"])
	require.EqualValues(t, 0, second.MapStats["request_count"])
	require.NotEmpty(t, updates)
	changed, _, _, err := svc.reduceSlugUpdates(context.Background(), chatModel, "kb-1", updates[0].Slug, []SlugUpdate{updates[0]}, 1, wikiMapIntegrationBatchContext(), nil)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 1, wiki.creates, "cache hit contribution must still enter reduce")
}

func TestWikiMapArtifactProductionSpanReportsMissThenHit(t *testing.T) {
	svc, _, _, model, _ := newWikiMapIntegrationService(t)
	tracker, spanDB := setupSpanTrackerTest(t)
	svc.spanTracker = tracker
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	run := func() types.JSONMap {
		_, attempt, err := tracker.OpenAttempt(context.Background(), "doc-1", "")
		require.NoError(t, err)
		tracker.BeginStage(context.Background(), "doc-1", attempt, types.StagePostProcess, nil)
		result, updates, err := svc.mapOneDocument(context.Background(), model, payload, op, wikiMapIntegrationBatchContext())
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WikiSpan)
		changed, _, _, err := svc.reduceSlugUpdates(context.Background(), model, "kb-1", updates[0].Slug, []SlugUpdate{updates[0]}, 1, wikiMapIntegrationBatchContext(), map[string]*Span{"doc-1": result.WikiSpan})
		require.NoError(t, err)
		require.True(t, changed)
		tracker.EndSpan(context.Background(), result.WikiSpan, result.MapStats)
		var row types.KnowledgeProcessingSpan
		require.NoError(t, spanDB.Where("knowledge_id = ? AND attempt = ? AND name = ?", "doc-1", attempt, "postprocess.wiki").Take(&row).Error)
		require.Equal(t, types.SpanStatusDone, row.Status)
		return row.Output
	}
	miss := run()
	require.Equal(t, string(types.IngestionCacheStatusMiss), fmt.Sprint(miss["cache_status"]))
	require.Greater(t, int(miss["request_count"].(float64)), 0)
	hit := run()
	require.Equal(t, string(types.IngestionCacheStatusHit), fmt.Sprint(hit["cache_status"]))
	require.Equal(t, float64(0), hit["request_count"])
	require.Equal(t, float64(0), hit["computed_items"])
	require.Equal(t, float64(1), hit["reused_items"])
	require.Equal(t, wikiMapArtifactKind, hit["artifact_kind"])
}

func TestWikiMapArtifactContentModelConfigAndTenantChangesMissPrecisely(t *testing.T) {
	svc, chunks, _, first, _ := newWikiMapIntegrationService(t)
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	base := wikiMapIntegrationBatchContext()
	_, _, err := svc.mapOneDocument(context.Background(), first, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}, op, base)
	require.NoError(t, err)

	chunks.mu.Lock()
	chunks.chunks[0].Content = "Changed stable source content for wiki mapping."
	chunks.mu.Unlock()
	contentModel := &wikiMapCountingChat{}
	_, _, err = svc.mapOneDocument(context.Background(), contentModel, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}, op, base)
	require.NoError(t, err)
	require.Greater(t, contentModel.Count(), 0)

	changedConfig := wikiMapIntegrationBatchContext()
	changedConfig.ContentInstructions = "Prefer terse prose"
	configModel := &wikiMapCountingChat{}
	_, _, err = svc.mapOneDocument(context.Background(), configModel, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}, op, changedConfig)
	require.NoError(t, err)
	require.Greater(t, configModel.Count(), 0)

	modelChanged := &wikiMapCountingChat{id: "other-model-id", name: "other-model"}
	_, _, err = svc.mapOneDocument(context.Background(), modelChanged, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}, op, changedConfig)
	require.NoError(t, err)
	require.Greater(t, modelChanged.Count(), 0)

	tenantChanged := &wikiMapCountingChat{id: "other-model-id", name: "other-model"}
	_, _, err = svc.mapOneDocument(context.Background(), tenantChanged, WikiIngestPayload{TenantID: 2, KnowledgeBaseID: "kb-1"}, op, changedConfig)
	require.NoError(t, err)
	require.Greater(t, tenantChanged.Count(), 0)
}

func TestWikiMapArtifactHitCannotResurrectDeletedKnowledge(t *testing.T) {
	svc, _, _, model, _ := newWikiMapIntegrationService(t)
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	batch := wikiMapIntegrationBatchContext()
	result, _, err := svc.mapOneDocument(context.Background(), model, payload, op, batch)
	require.NoError(t, err)
	require.NotNil(t, result)
	knowledge := svc.knowledgeSvc.(*wikiMapTestKnowledgeService)
	knowledge.knowledge.ParseStatus = types.ParseStatusDeleting
	result, updates, err := svc.mapOneDocument(context.Background(), model, payload, op, batch)
	require.NoError(t, err)
	require.Nil(t, result)
	require.Nil(t, updates)
}

func TestWikiMapArtifactLegacyChunkIdentitySafelyBypassesCache(t *testing.T) {
	svc, chunks, _, _, db := newWikiMapIntegrationService(t)
	chunks.mu.Lock()
	chunks.chunks[0].StableIdentity = ""
	chunks.chunks[0].IdentityVersion = ""
	chunks.mu.Unlock()
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	for i := 0; i < 2; i++ {
		model := &wikiMapCountingChat{}
		result, _, err := svc.mapOneDocument(context.Background(), model, payload, op, wikiMapIntegrationBatchContext())
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Greater(t, model.Count(), 0)
		require.Equal(t, types.IngestionCacheStatusNotSupported, result.MapStats["cache_status"])
	}
	var count int64
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Where("artifact_kind = ?", wikiMapArtifactKind).Count(&count).Error)
	require.Zero(t, count)
}

func TestWikiMapArtifactHitRebindsStableIdentityToRebuiltRowID(t *testing.T) {
	svc, chunks, _, _, db := newWikiMapIntegrationService(t)
	model := &wikiMapCitationChat{}
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	_, firstUpdates, err := svc.mapOneDocument(context.Background(), model, payload, op, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.Equal(t, []string{"row-1"}, sourceChunksForSlug(firstUpdates, "entity/acme"))
	var artifact types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", wikiMapArtifactKind).Take(&artifact).Error)
	require.NotContains(t, string(artifact.Payload), "row-1")
	require.Contains(t, string(artifact.Payload), "stable-1")
	firstCalls := model.calls.Load()
	chunks.mu.Lock()
	chunks.chunks[0].ID = "rebuilt-row-2"
	chunks.mu.Unlock()
	_, secondUpdates, err := svc.mapOneDocument(context.Background(), model, payload, op, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.Equal(t, firstCalls, model.calls.Load())
	require.Equal(t, []string{"rebuilt-row-2"}, sourceChunksForSlug(secondUpdates, "entity/acme"))
}

type wikiMapCitationChat struct{ calls atomic.Int32 }

func (c *wikiMapCitationChat) Chat(_ context.Context, messages []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	c.calls.Add(1)
	prompt := messages[0].Content
	switch {
	case strings.Contains(prompt, "You are a knowledge extraction system"):
		return &types.ChatResponse{Content: `{"entities":[{"name":"Acme","slug":"entity/acme","description":"company"}],"concepts":[]}`}, nil
	case strings.Contains(prompt, "You are a precise citation system"):
		return &types.ChatResponse{Content: `{"citations":{"entity/acme":["c000"]},"new_slugs":[]}`}, nil
	default:
		return &types.ChatResponse{Content: "SUMMARY: Acme document\n\nAbout [[entity/acme]]."}, nil
	}
}
func (*wikiMapCitationChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (*wikiMapCitationChat) GetModelName() string { return "wiki-map-citation-model" }
func (*wikiMapCitationChat) GetModelID() string   { return "wiki-map-citation-model-id" }

func sourceChunksForSlug(updates []SlugUpdate, slug string) []string {
	for _, update := range updates {
		if update.Slug == slug && update.Type != "retract" && update.Type != "retractStale" {
			return update.SourceChunks
		}
	}
	return nil
}

type wikiMapFailingChat struct {
	err   error
	calls atomic.Int32
}

type wikiMapCompleteFailRepo struct {
	interfaces.DerivedArtifactRepository
	calls atomic.Int32
}

func (r *wikiMapCompleteFailRepo) Complete(context.Context, interfaces.ArtifactCompletion) error {
	r.calls.Add(1)
	return fmt.Errorf("injected complete failure")
}

func TestWikiMapArtifactCompleteFailureSettlesClaimForRetry(t *testing.T) {
	svc, _, _, model, db := newWikiMapIntegrationService(t)
	base := svc.artifactRepo
	failing := &wikiMapCompleteFailRepo{DerivedArtifactRepository: base}
	svc.artifactRepo = failing
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	_, _, err := svc.mapOneDocument(context.Background(), model, payload, op, wikiMapIntegrationBatchContext())
	require.ErrorContains(t, err, "complete wiki map artifact")
	var row types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", wikiMapArtifactKind).Take(&row).Error)
	require.Equal(t, types.DerivedArtifactFailed, row.Status)
	svc.artifactRepo = base
	retry := &wikiMapCountingChat{}
	result, _, err := svc.mapOneDocument(context.Background(), retry, payload, op, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, retry.Count(), 0)
}

func TestWikiMapArtifactStableReferenceFailureSettlesClaim(t *testing.T) {
	svc, _, _, _, db := newWikiMapIntegrationService(t)
	key := artifactkey.DigestText("stable-ref-failure")
	owner := "owner-stable-ref"
	claim, err := svc.artifactRepo.Claim(context.Background(), interfaces.ArtifactClaim{TenantID: 1, ArtifactKey: key, ArtifactKind: wikiMapArtifactKind, InputDigest: artifactkey.DigestText("input"), ModelID: "wiki-map-citation-model-id", PromptVersion: wikiMapPromptVersion, ConfigDigest: artifactkey.DigestText("config"), ProducerVersion: wikiMapProducerVersion, OwnerToken: owner, LeaseDuration: time.Second})
	require.NoError(t, err)
	require.Equal(t, interfaces.ArtifactClaimClaimed, claim.Outcome)
	_, _, err = svc.computeWikiMapArtifact(context.Background(), &wikiMapCitationChat{}, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}, WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}, wikiMapIntegrationBatchContext(), key, owner, map[string]wikiMapChunkRef{})
	require.ErrorContains(t, err, "no current stable identity")
	var row types.DerivedArtifact
	require.NoError(t, db.Where("artifact_key = ?", key).Take(&row).Error)
	require.Equal(t, types.DerivedArtifactFailed, row.Status)
}

func (c *wikiMapFailingChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	c.calls.Add(1)
	return nil, c.err
}
func (*wikiMapFailingChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (*wikiMapFailingChat) GetModelName() string { return "wiki-map-test-model" }
func (*wikiMapFailingChat) GetModelID() string   { return "wiki-map-test-model-id" }

func TestWikiMapArtifactProviderFailureMarksFailedAndRetryCanCompute(t *testing.T) {
	svc, _, _, _, db := newWikiMapIntegrationService(t)
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	failing := &wikiMapFailingChat{err: fmt.Errorf("provider rejected request")}
	_, _, err := svc.mapOneDocument(context.Background(), failing, payload, op, wikiMapIntegrationBatchContext())
	require.Error(t, err)
	require.Greater(t, failing.calls.Load(), int32(0))
	var row types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", wikiMapArtifactKind).Take(&row).Error)
	require.Equal(t, types.DerivedArtifactFailed, row.Status)
	retry := &wikiMapCountingChat{}
	result, _, err := svc.mapOneDocument(context.Background(), retry, payload, op, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, retry.Count(), 0)
}

func TestWikiMapArtifactCorruptPayloadIsNeverReturnedToReduce(t *testing.T) {
	svc, _, _, initial, db := newWikiMapIntegrationService(t)
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	_, _, err := svc.mapOneDocument(context.Background(), initial, payload, op, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	corrupt := []byte(`{"schema_version":"wrong"}`)
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Where("artifact_kind = ?", wikiMapArtifactKind).Updates(map[string]any{"payload": corrupt, "payload_digest": artifactkey.DigestBytes(corrupt)}).Error)
	recompute := &wikiMapCountingChat{}
	result, updates, err := svc.mapOneDocument(context.Background(), recompute, payload, op, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, updates)
	require.Greater(t, recompute.Count(), 0)
	require.Equal(t, types.IngestionCacheStatusError, result.MapStats["cache_status"])
}

type wikiMapBlockingChat struct {
	entered chan struct{}
	once    sync.Once
}

func (c *wikiMapBlockingChat) Chat(ctx context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	c.once.Do(func() { close(c.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*wikiMapBlockingChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (*wikiMapBlockingChat) GetModelName() string { return "wiki-map-test-model" }
func (*wikiMapBlockingChat) GetModelID() string   { return "wiki-map-test-model-id" }

func TestWikiMapArtifactCancellationPersistsFailedState(t *testing.T) {
	svc, _, _, _, db := newWikiMapIntegrationService(t)
	svc.wikiMapTiming = wikiMapArtifactTiming{Lease: time.Second, Wait: time.Second, Poll: time.Millisecond, Cleanup: 200 * time.Millisecond}
	blocking := &wikiMapBlockingChat{entered: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := svc.mapOneDocument(ctx, blocking, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}, WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}, wikiMapIntegrationBatchContext())
		done <- err
	}()
	<-blocking.entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	var row types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", wikiMapArtifactKind).Take(&row).Error)
	require.Equal(t, types.DerivedArtifactFailed, row.Status)
}

type wikiMapRenewCountingRepo struct {
	interfaces.DerivedArtifactRepository
	renews atomic.Int32
}

func (r *wikiMapRenewCountingRepo) RenewLease(ctx context.Context, tenant uint64, key, owner string, now time.Time, lease time.Duration) error {
	r.renews.Add(1)
	return r.DerivedArtifactRepository.RenewLease(ctx, tenant, key, owner, now, lease)
}

type wikiMapReleasableChat struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (c *wikiMapReleasableChat) Chat(ctx context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	n := c.calls.Add(1)
	c.once.Do(func() { close(c.entered) })
	select {
	case <-c.release:
		if n == 1 {
			return &types.ChatResponse{Content: `{"entities":[],"concepts":[]}`}, nil
		}
		return &types.ChatResponse{Content: "SUMMARY: renewed\n\nbody"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (*wikiMapReleasableChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (*wikiMapReleasableChat) GetModelName() string { return "wiki-map-test-model" }
func (*wikiMapReleasableChat) GetModelID() string   { return "wiki-map-test-model-id" }

func TestWikiMapArtifactRenewsLeaseDuringSlowProviderCall(t *testing.T) {
	svc, _, _, _, _ := newWikiMapIntegrationService(t)
	wrapped := &wikiMapRenewCountingRepo{DerivedArtifactRepository: svc.artifactRepo}
	svc.artifactRepo = wrapped
	svc.wikiMapTiming = wikiMapArtifactTiming{Lease: 60 * time.Millisecond, Wait: time.Second, Poll: time.Millisecond, Cleanup: 100 * time.Millisecond}
	model := &wikiMapReleasableChat{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, _, err := svc.mapOneDocument(context.Background(), model, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}, WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}, wikiMapIntegrationBatchContext())
		done <- err
	}()
	<-model.entered
	time.Sleep(90 * time.Millisecond)
	require.Greater(t, wrapped.renews.Load(), int32(0))
	close(model.release)
	require.NoError(t, <-done)
}

func TestWikiMapArtifactConcurrentRequestsShareOneComputation(t *testing.T) {
	svc, _, _, _, _ := newWikiMapIntegrationService(t)
	svc.wikiMapTiming = wikiMapArtifactTiming{Lease: time.Second, Wait: time.Second, Poll: 2 * time.Millisecond, Cleanup: 100 * time.Millisecond}
	model := &wikiMapReleasableChat{entered: make(chan struct{}), release: make(chan struct{})}
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	results := make(chan error, 2)
	go func() {
		_, _, err := svc.mapOneDocument(context.Background(), model, payload, op, wikiMapIntegrationBatchContext())
		results <- err
	}()
	<-model.entered
	go func() {
		_, _, err := svc.mapOneDocument(context.Background(), model, payload, op, wikiMapIntegrationBatchContext())
		results <- err
	}()
	time.Sleep(30 * time.Millisecond)
	close(model.release)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.Equal(t, int32(2), model.calls.Load(), "busy waiter must reuse extract+summary from the claimant")
}

type wikiMapAlwaysBusyRepo struct {
	interfaces.DerivedArtifactRepository
}

func (*wikiMapAlwaysBusyRepo) Claim(context.Context, interfaces.ArtifactClaim) (*interfaces.ArtifactClaimResult, error) {
	return &interfaces.ArtifactClaimResult{Outcome: interfaces.ArtifactClaimBusy}, nil
}

func TestWikiMapArtifactBusyWaitIsBounded(t *testing.T) {
	svc, _, _, model, _ := newWikiMapIntegrationService(t)
	svc.artifactRepo = &wikiMapAlwaysBusyRepo{}
	svc.wikiMapTiming = wikiMapArtifactTiming{Lease: time.Second, Wait: 20 * time.Millisecond, Poll: 2 * time.Millisecond, Cleanup: time.Second}
	_, _, err := svc.mapOneDocument(context.Background(), model, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}, WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}, wikiMapIntegrationBatchContext())
	require.ErrorContains(t, err, "busy wait timed out")
	require.Equal(t, 0, model.Count())
}

type wikiMapLostRenewRepo struct {
	interfaces.DerivedArtifactRepository
	lost chan struct{}
	once sync.Once
}

type wikiMapRenewFailRepo struct {
	interfaces.DerivedArtifactRepository
	failed chan struct{}
	once   sync.Once
}

func (r *wikiMapRenewFailRepo) RenewLease(context.Context, uint64, string, string, time.Time, time.Duration) error {
	r.once.Do(func() { close(r.failed) })
	return fmt.Errorf("injected renew failure")
}

func TestWikiMapArtifactOrdinaryRenewFailureMarksClaimFailed(t *testing.T) {
	svc, _, _, _, db := newWikiMapIntegrationService(t)
	wrapped := &wikiMapRenewFailRepo{DerivedArtifactRepository: svc.artifactRepo, failed: make(chan struct{})}
	svc.artifactRepo = wrapped
	svc.wikiMapTiming = wikiMapArtifactTiming{Lease: 45 * time.Millisecond, Wait: time.Second, Poll: time.Millisecond, Cleanup: 100 * time.Millisecond}
	model := &wikiMapReleasableChat{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, _, err := svc.mapOneDocument(context.Background(), model, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}, WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}, wikiMapIntegrationBatchContext())
		done <- err
	}()
	<-model.entered
	<-wrapped.failed
	close(model.release)
	require.ErrorContains(t, <-done, "lease")
	var row types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", wikiMapArtifactKind).Take(&row).Error)
	require.Equal(t, types.DerivedArtifactFailed, row.Status)
}

func (r *wikiMapLostRenewRepo) RenewLease(context.Context, uint64, string, string, time.Time, time.Duration) error {
	r.once.Do(func() { close(r.lost) })
	return interfaces.ErrArtifactLostOwnership
}

func TestWikiMapArtifactLeaseTakeoverWinsOverOldOwner(t *testing.T) {
	oldSvc, _, _, _, _ := newWikiMapIntegrationService(t)
	base := oldSvc.artifactRepo
	lostRepo := &wikiMapLostRenewRepo{DerivedArtifactRepository: base, lost: make(chan struct{})}
	oldSvc.artifactRepo = lostRepo
	oldSvc.wikiMapTiming = wikiMapArtifactTiming{Lease: 45 * time.Millisecond, Wait: time.Second, Poll: time.Millisecond, Cleanup: 100 * time.Millisecond}
	oldModel := &wikiMapReleasableChat{entered: make(chan struct{}), release: make(chan struct{})}
	payload := WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"}
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc-1", Language: "en"}
	oldDone := make(chan error, 1)
	go func() {
		_, _, err := oldSvc.mapOneDocument(context.Background(), oldModel, payload, op, wikiMapIntegrationBatchContext())
		oldDone <- err
	}()
	<-oldModel.entered
	<-lostRepo.lost
	time.Sleep(55 * time.Millisecond)
	newSvc := &wikiIngestService{artifactRepo: base, chunkRepo: oldSvc.chunkRepo, wikiService: oldSvc.wikiService, knowledgeSvc: oldSvc.knowledgeSvc, wikiMapTiming: oldSvc.wikiMapTiming}
	newModel := &wikiMapCountingChat{}
	_, _, err := newSvc.mapOneDocument(context.Background(), newModel, payload, op, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.Greater(t, newModel.Count(), 0)
	close(oldModel.release)
	require.ErrorIs(t, <-oldDone, interfaces.ErrArtifactLostOwnership)
	hitModel := &wikiMapCountingChat{}
	_, _, err = newSvc.mapOneDocument(context.Background(), hitModel, payload, op, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.Equal(t, 0, hitModel.Count())
}
