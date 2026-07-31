package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newGraphExtractCacheService(t *testing.T) (*ChunkExtractService, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.ToSlash(filepath.Join(t.TempDir(), "graph-cache.db")))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.DerivedArtifact{}))
	return &ChunkExtractService{artifactRepo: repository.NewDerivedArtifactRepository(db)}, db
}

func graphExtractCacheTemplate() *types.PromptTemplateStructured {
	return &types.PromptTemplateStructured{Description: "Extract a graph from the supplied content."}
}

type namedGraphObservationChat struct {
	*graphObservationChat
	id, name string
}

func (c *namedGraphObservationChat) GetModelID() string   { return c.id }
func (c *namedGraphObservationChat) GetModelName() string { return c.name }

func TestGraphExtractArtifactCacheHitSkipsProviderAndReturnsIndependentGraph(t *testing.T) {
	svc, _ := newGraphExtractCacheService(t)
	model := &graphObservationChat{response: `[
  {"entity":"Alice","entity_attributes":["person"]},
  {"entity":"Bob","entity_attributes":["person"]},
  {"entity1":"Alice","entity2":"Bob","relation":"knows"}
]`}

	first, miss, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "Alice knows Bob.")
	require.NoError(t, err)
	require.Len(t, first.Node, 2)
	require.Equal(t, string(types.IngestionCacheStatusMiss), miss["cache_status"])
	require.Equal(t, string(types.ArtifactCacheComputed), miss["artifact_cache_event"])
	require.EqualValues(t, 1, miss["request_count"])
	first.Node[0].Chunks = []string{"ephemeral-row-id"}

	second, hit, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "Alice knows Bob.")
	require.NoError(t, err)
	require.Len(t, second.Node, 2)
	require.Empty(t, second.Node[0].Chunks)
	require.Equal(t, string(types.IngestionCacheStatusHit), hit["cache_status"])
	require.Equal(t, string(types.ArtifactCacheHit), hit["artifact_cache_event"])
	require.EqualValues(t, 0, hit["request_count"])
	require.EqualValues(t, 1, hit["reused_items"])
	requests, _ := model.Snapshot()
	require.Equal(t, 1, requests)
}

func TestGraphExtractArtifactCacheScopesTenantContentModelAndPrompt(t *testing.T) {
	svc, _ := newGraphExtractCacheService(t)
	model := &graphObservationChat{response: `[{"entity":"Alice","entity_attributes":["person"]}]`}
	base := graphExtractCacheTemplate()
	_, _, err := svc.extractGraphCached(context.Background(), 1, model, base, "same text")
	require.NoError(t, err)
	_, _, err = svc.extractGraphCached(context.Background(), 2, model, base, "same text")
	require.NoError(t, err)
	_, _, err = svc.extractGraphCached(context.Background(), 1, model, base, "different text")
	require.NoError(t, err)
	changedPrompt := &types.PromptTemplateStructured{Description: base.Description, Tags: []string{"PERSON"}}
	_, _, err = svc.extractGraphCached(context.Background(), 1, model, changedPrompt, "same text")
	require.NoError(t, err)
	otherModel := &namedGraphObservationChat{graphObservationChat: model, id: "other-model-id", name: "other-model-name"}
	_, _, err = svc.extractGraphCached(context.Background(), 1, otherModel, base, "same text")
	require.NoError(t, err)
	requests, _ := model.Snapshot()
	require.Equal(t, 5, requests)
}

func TestGraphExtractArtifactCacheFailureIsRetryable(t *testing.T) {
	svc, db := newGraphExtractCacheService(t)
	model := &graphObservationChat{err: fmt.Errorf("provider unavailable")}
	_, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "retry me")
	require.Error(t, err)
	var failed types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", graphExtractArtifactKind).First(&failed).Error)
	require.Equal(t, types.DerivedArtifactFailed, failed.Status)

	model.err = nil
	model.response = `[{"entity":"Recovered","entity_attributes":["concept"]}]`
	graph, observation, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "retry me")
	require.NoError(t, err)
	require.Len(t, graph.Node, 1)
	require.Equal(t, string(types.IngestionCacheStatusMiss), observation["cache_status"])
}

func TestDecodeGraphExtractArtifactRejectsInvalidPayload(t *testing.T) {
	_, err := decodeGraphExtractArtifact(&types.DerivedArtifact{PayloadEncoding: "json", Payload: []byte(`{"schema_version":"wrong","graph":{}}`)})
	require.ErrorIs(t, err, interfaces.ErrArtifactCorrupt)
}

func TestGraphExtractCandidateExcludesChunkContextAndDeepCopies(t *testing.T) {
	source := &types.GraphData{
		Text: "source content must not be cached",
		Node: []*types.GraphNode{{
			Name:       "Alice",
			Chunks:     []string{"current-chunk-id"},
			Attributes: []string{"person"},
		}},
		Relation: []*types.GraphRelation{{Node1: "Alice", Node2: "Bob", Type: "knows"}},
	}

	candidate, err := graphExtractCandidate(source)
	require.NoError(t, err)
	require.Empty(t, candidate.Text)
	require.Empty(t, candidate.Node[0].Chunks)
	require.Equal(t, []string{"person"}, candidate.Node[0].Attributes)

	candidate.Node[0].Attributes[0] = "changed"
	candidate.Relation[0].Type = "changed"
	require.Equal(t, "person", source.Node[0].Attributes[0])
	require.Equal(t, "knows", source.Relation[0].Type)
}

func TestGraphExtractArtifactCachePartialHitAcrossChunks(t *testing.T) {
	svc, _ := newGraphExtractCacheService(t)
	model := &graphObservationChat{response: `[{"entity":"Node","entity_attributes":["kind"]}]`}
	for _, content := range []string{"one", "two", "three"} {
		_, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), content)
		require.NoError(t, err)
	}
	for _, content := range []string{"one", "two changed", "three"} {
		_, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), content)
		require.NoError(t, err)
	}
	requests, _ := model.Snapshot()
	require.Equal(t, 4, requests, "two unchanged chunks must hit while the changed chunk recomputes")
}

func TestGraphExtractArtifactPayloadExcludesDatabaseChunkID(t *testing.T) {
	svc, db := newGraphExtractCacheService(t)
	model := &graphObservationChat{response: `[{"entity":"Alice","entity_attributes":["person"]}]`}
	graph, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "Alice")
	require.NoError(t, err)
	graph.Node[0].Chunks = []string{"database-chunk-id"}
	var row types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", graphExtractArtifactKind).Take(&row).Error)
	require.NotContains(t, string(row.Payload), "database-chunk-id")
	var payload graphExtractArtifactPayload
	require.NoError(t, json.Unmarshal(row.Payload, &payload))
	require.Empty(t, payload.Graph.Node[0].Chunks)
}

type graphBlockingChat struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (c *graphBlockingChat) Chat(ctx context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	c.calls.Add(1)
	c.once.Do(func() { close(c.entered) })
	select {
	case <-c.release:
		return &types.ChatResponse{Content: `[{"entity":"Shared"}]`}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (*graphBlockingChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}
func (*graphBlockingChat) GetModelName() string { return "graph-blocking" }
func (*graphBlockingChat) GetModelID() string   { return "graph-blocking-id" }

func TestGraphExtractArtifactConcurrentSameInputRunsOneChat(t *testing.T) {
	svc, _ := newGraphExtractCacheService(t)
	svc.graphCacheTiming = graphExtractArtifactTiming{Lease: time.Second, Wait: time.Second, Poll: 2 * time.Millisecond}
	model := &graphBlockingChat{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 2)
	go func() {
		_, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "shared")
		done <- err
	}()
	<-model.entered
	go func() {
		_, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "shared")
		done <- err
	}()
	time.Sleep(25 * time.Millisecond)
	close(model.release)
	require.NoError(t, <-done)
	require.NoError(t, <-done)
	require.Equal(t, int32(1), model.calls.Load())
}

type graphAlwaysBusyRepo struct {
	interfaces.DerivedArtifactRepository
}

func (*graphAlwaysBusyRepo) Claim(context.Context, interfaces.ArtifactClaim) (*interfaces.ArtifactClaimResult, error) {
	return &interfaces.ArtifactClaimResult{Outcome: interfaces.ArtifactClaimBusy}, nil
}

func TestGraphExtractArtifactBusyTimeoutAndCancellation(t *testing.T) {
	svc := &ChunkExtractService{artifactRepo: &graphAlwaysBusyRepo{}, graphCacheTiming: graphExtractArtifactTiming{Lease: time.Second, Wait: 20 * time.Millisecond, Poll: 2 * time.Millisecond}}
	model := &graphObservationChat{}
	_, observation, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "busy")
	require.ErrorContains(t, err, "busy wait timed out")
	require.Equal(t, string(types.ArtifactCacheFailed), observation["artifact_cache_event"])
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = svc.extractGraphCached(ctx, 1, model, graphExtractCacheTemplate(), "cancel")
	require.ErrorIs(t, err, context.Canceled)
}

func TestGraphExtractArtifactClaimantCancellationPersistsFailedState(t *testing.T) {
	svc, db := newGraphExtractCacheService(t)
	svc.graphCacheTiming = graphExtractArtifactTiming{Lease: time.Second, Wait: time.Second, Poll: time.Millisecond, Cleanup: 200 * time.Millisecond}
	model := &graphBlockingChat{entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := svc.extractGraphCached(ctx, 1, model, graphExtractCacheTemplate(), "cancel owner")
		done <- err
	}()
	<-model.entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	var row types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", graphExtractArtifactKind).Take(&row).Error)
	require.Equal(t, types.DerivedArtifactFailed, row.Status)
}

type graphRenewCountingRepo struct {
	interfaces.DerivedArtifactRepository
	renews atomic.Int32
}

type graphLostRenewRepo struct {
	interfaces.DerivedArtifactRepository
	lost chan struct{}
	once sync.Once
}

type graphFailRenewRepo struct {
	interfaces.DerivedArtifactRepository
	failed chan struct{}
	once   sync.Once
}

func (r *graphFailRenewRepo) RenewLease(context.Context, uint64, string, string, time.Time, time.Duration) error {
	r.once.Do(func() { close(r.failed) })
	return errors.New("injected renew failure")
}

func (r *graphLostRenewRepo) RenewLease(context.Context, uint64, string, string, time.Time, time.Duration) error {
	r.once.Do(func() { close(r.lost) })
	return interfaces.ErrArtifactLostOwnership
}

func (r *graphRenewCountingRepo) RenewLease(ctx context.Context, tenant uint64, key, owner string, now time.Time, lease time.Duration) error {
	r.renews.Add(1)
	return r.DerivedArtifactRepository.RenewLease(ctx, tenant, key, owner, now, lease)
}

func TestGraphExtractArtifactHeartbeatRenewsLease(t *testing.T) {
	svc, _ := newGraphExtractCacheService(t)
	wrapped := &graphRenewCountingRepo{DerivedArtifactRepository: svc.artifactRepo}
	svc.artifactRepo = wrapped
	svc.graphCacheTiming = graphExtractArtifactTiming{Lease: 45 * time.Millisecond, Wait: time.Second, Poll: time.Millisecond}
	model := &graphBlockingChat{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "slow")
		done <- err
	}()
	<-model.entered
	time.Sleep(65 * time.Millisecond)
	require.Greater(t, wrapped.renews.Load(), int32(0))
	close(model.release)
	require.NoError(t, <-done)
}

func TestGraphExtractArtifactRenewFailureMarksClaimFailed(t *testing.T) {
	svc, db := newGraphExtractCacheService(t)
	wrapped := &graphFailRenewRepo{DerivedArtifactRepository: svc.artifactRepo, failed: make(chan struct{})}
	svc.artifactRepo = wrapped
	svc.graphCacheTiming = graphExtractArtifactTiming{Lease: 45 * time.Millisecond, Wait: time.Second, Poll: time.Millisecond, Cleanup: 200 * time.Millisecond}
	model := &graphBlockingChat{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "renew fail")
		done <- err
	}()
	<-model.entered
	<-wrapped.failed
	close(model.release)
	require.ErrorContains(t, <-done, "lease")
	var row types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", graphExtractArtifactKind).Take(&row).Error)
	require.Equal(t, types.DerivedArtifactFailed, row.Status)
}

func TestGraphExtractArtifactLeaseTakeoverWinsOverLostOwner(t *testing.T) {
	oldSvc, _ := newGraphExtractCacheService(t)
	base := oldSvc.artifactRepo
	lost := &graphLostRenewRepo{DerivedArtifactRepository: base, lost: make(chan struct{})}
	oldSvc.artifactRepo = lost
	oldSvc.graphCacheTiming = graphExtractArtifactTiming{Lease: 45 * time.Millisecond, Wait: time.Second, Poll: time.Millisecond}
	oldModel := &graphBlockingChat{entered: make(chan struct{}), release: make(chan struct{})}
	oldDone := make(chan error, 1)
	go func() {
		_, _, err := oldSvc.extractGraphCached(context.Background(), 1, oldModel, graphExtractCacheTemplate(), "takeover")
		oldDone <- err
	}()
	<-oldModel.entered
	<-lost.lost
	time.Sleep(55 * time.Millisecond)
	newSvc := &ChunkExtractService{artifactRepo: base, graphCacheTiming: oldSvc.graphCacheTiming}
	newModel := &graphObservationChat{response: `[{"entity":"Winner"}]`}
	_, _, err := newSvc.extractGraphCached(context.Background(), 1, newModel, graphExtractCacheTemplate(), "takeover")
	require.NoError(t, err)
	close(oldModel.release)
	require.ErrorIs(t, <-oldDone, interfaces.ErrArtifactLostOwnership)
	hitModel := &graphObservationChat{response: `[{"entity":"MustNotRun"}]`}
	graph, _, err := newSvc.extractGraphCached(context.Background(), 1, hitModel, graphExtractCacheTemplate(), "takeover")
	require.NoError(t, err)
	require.Equal(t, "Winner", graph.Node[0].Name)
	requests, _ := hitModel.Snapshot()
	require.Zero(t, requests)
}

type graphCompleteFailRepo struct {
	interfaces.DerivedArtifactRepository
	once atomic.Bool
}

func (r *graphCompleteFailRepo) Complete(ctx context.Context, completion interfaces.ArtifactCompletion) error {
	if !r.once.Swap(true) {
		return errors.New("injected complete failure")
	}
	return r.DerivedArtifactRepository.Complete(ctx, completion)
}

func TestGraphExtractArtifactCompleteFailureCleansClaimAndRetries(t *testing.T) {
	svc, db := newGraphExtractCacheService(t)
	svc.artifactRepo = &graphCompleteFailRepo{DerivedArtifactRepository: svc.artifactRepo}
	model := &graphObservationChat{response: `[{"entity":"Retry"}]`}
	_, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "complete retry")
	require.ErrorContains(t, err, "complete")
	var row types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", graphExtractArtifactKind).Take(&row).Error)
	require.Equal(t, types.DerivedArtifactFailed, row.Status)
	_, _, err = svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "complete retry")
	require.NoError(t, err)
}

func TestGraphExtractArtifactCorruptRecomputeReportsCacheError(t *testing.T) {
	svc, db := newGraphExtractCacheService(t)
	model := &graphObservationChat{response: `[{"entity":"Valid"}]`}
	_, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "corrupt")
	require.NoError(t, err)
	bad := []byte(`{"schema_version":"wrong","graph":{}}`)
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Where("artifact_kind = ?", graphExtractArtifactKind).Updates(map[string]any{"payload": bad, "payload_digest": artifactkey.DigestBytes(bad)}).Error)
	_, observation, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "corrupt")
	require.NoError(t, err)
	require.Equal(t, string(types.IngestionCacheStatusError), observation["cache_status"])
	require.Equal(t, string(types.ArtifactCacheFailed), observation["artifact_cache_event"])
	require.EqualValues(t, 1, observation["request_count"])
}

type graphRevisionModelService struct {
	interfaces.ModelService
	model *types.Model
}

func (s *graphRevisionModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	return s.model, nil
}

func TestGraphExtractArtifactUsesConfiguredModelRevision(t *testing.T) {
	svc, db := newGraphExtractCacheService(t)
	configured := &types.Model{ID: "graph-chat-test", Name: "stable-runtime-name", Parameters: types.ModelParameters{ExtraConfig: map[string]string{"model_revision": "rev-a"}}}
	svc.modelService = &graphRevisionModelService{model: configured}
	model := &graphObservationChat{response: `[{"entity":"Revision"}]`}
	_, _, err := svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "revision input")
	require.NoError(t, err)
	configured.Parameters.ExtraConfig["model_revision"] = "rev-b"
	_, _, err = svc.extractGraphCached(context.Background(), 1, model, graphExtractCacheTemplate(), "revision input")
	require.NoError(t, err)
	var rows []types.DerivedArtifact
	require.NoError(t, db.Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, "rev-a", rows[0].ModelRevision)
	require.Equal(t, "rev-b", rows[1].ModelRevision)
	requests, _ := model.Snapshot()
	require.Equal(t, 2, requests)
}

func TestGraphExtractCandidateDeterministicSortAndDedupe(t *testing.T) {
	graph := &types.GraphData{Node: []*types.GraphNode{{Name: " Bob ", Attributes: []string{"z", "a"}}, {Name: "Alice", Attributes: []string{"person"}}, {Name: "Bob", Attributes: []string{"a", "m"}}}, Relation: []*types.GraphRelation{{Node1: "Bob", Node2: "Alice", Type: "knows"}, {Node1: " Bob ", Node2: "Alice", Type: "knows"}, {Node1: "Alice", Node2: "Bob", Type: "likes"}}}
	candidate, err := graphExtractCandidate(graph)
	require.NoError(t, err)
	require.Equal(t, []string{"Alice", "Bob"}, []string{candidate.Node[0].Name, candidate.Node[1].Name})
	require.Equal(t, []string{"a", "m", "z"}, candidate.Node[1].Attributes)
	require.Len(t, candidate.Relation, 2)
	first, _ := json.Marshal(candidate)
	second, _ := json.Marshal(candidate)
	require.Equal(t, first, second)
}

type graphHandleChunkRepo struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
}

func (r *graphHandleChunkRepo) GetChunkByID(_ context.Context, _ uint64, id string) (*types.Chunk, error) {
	chunk := *r.chunks[id]
	return &chunk, nil
}

type graphHandleKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r *graphHandleKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

type graphHandleModelService struct {
	interfaces.ModelService
	model chat.Chat
}

func (s *graphHandleModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.model, nil
}

func (*graphHandleModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	return nil, nil
}

type graphHandleEngine struct {
	interfaces.RetrieveGraphRepository
	mu     sync.Mutex
	graphs []*types.GraphData
	fail   error
}

func (e *graphHandleEngine) AddGraph(_ context.Context, _ types.NameSpace, graphs []*types.GraphData) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail != nil {
		return e.fail
	}
	data, _ := graphExtractCandidate(graphs[0])
	for i, node := range graphs[0].Node {
		data.Node[i].Chunks = append([]string(nil), node.Chunks...)
	}
	e.graphs = append(e.graphs, data)
	return nil
}

func TestIngestionArtifactRecovery_GraphExtractSucceededAddGraphFailed(t *testing.T) {
	svc, db := newGraphExtractCacheService(t)
	model := &graphObservationChat{response: `[{"entity":"Alice","entity_attributes":["person"]}]`}
	chunks := &graphHandleChunkRepo{chunks: map[string]*types.Chunk{
		"current-id": {ID: "current-id", TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "kb", Content: "same durable graph content"},
	}}
	engine := &graphHandleEngine{fail: errors.New("graph materialization failed")}
	svc.template = graphExtractCacheTemplate()
	svc.chunkRepo = chunks
	svc.knowledgeBaseRepo = &graphHandleKBRepo{kb: &types.KnowledgeBase{ID: "kb", ExtractConfig: &types.ExtractConfig{Enabled: true}, IndexingStrategy: types.IndexingStrategy{GraphEnabled: true}}}
	svc.modelService = &graphHandleModelService{model: model}
	svc.graphEngine = engine
	payload, err := json.Marshal(types.ExtractChunkPayload{TenantID: 1, ChunkID: "current-id", ModelID: "model", KnowledgeID: "doc", Attempt: 1})
	require.NoError(t, err)
	task := asynq.NewTask(types.TypeChunkExtract, payload)
	require.ErrorContains(t, svc.Handle(context.Background(), task), "graph materialization failed")
	requests, _ := model.Snapshot()
	require.Equal(t, 1, requests)
	var artifact types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", graphExtractArtifactKind).Take(&artifact).Error)
	require.Equal(t, types.DerivedArtifactSucceeded, artifact.Status)

	engine.mu.Lock()
	engine.fail = nil
	engine.mu.Unlock()
	require.NoError(t, svc.Handle(context.Background(), task))
	requests, _ = model.Snapshot()
	require.Equal(t, 1, requests, "retry must hit the extraction artifact")
	engine.mu.Lock()
	require.Len(t, engine.graphs, 1)
	require.Equal(t, []string{"current-id"}, engine.graphs[0].Node[0].Chunks)
	engine.mu.Unlock()
}

type graphHandleSpanTracker struct {
	SpanTracker
	parent  *Span
	mu      sync.Mutex
	outputs []types.JSONMap
}

func (*graphHandleSpanTracker) LatestAttempt(context.Context, string) int { return 1 }
func (t *graphHandleSpanTracker) LookupStage(context.Context, string, int, string) *Span {
	return t.parent
}
func (*graphHandleSpanTracker) BeginSubSpan(_ context.Context, _ *Span, name, kind string, _ types.JSONMap) *Span {
	return &Span{Name: name, Kind: kind}
}
func (t *graphHandleSpanTracker) EndSpan(_ context.Context, _ *Span, output types.JSONMap) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.outputs = append(t.outputs, output)
}

func TestChunkExtractHandleCacheHitMaterializesCurrentChunkIDAndSpanObservation(t *testing.T) {
	svc, db := newGraphExtractCacheService(t)
	model := &graphObservationChat{response: `[{"entity":"Alice","entity_attributes":["person"]}]`}
	chunks := &graphHandleChunkRepo{chunks: map[string]*types.Chunk{
		"old-id": {ID: "old-id", TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "kb", Content: "same content"},
		"new-id": {ID: "new-id", TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "kb", Content: "same content"},
	}}
	engine := &graphHandleEngine{}
	tracker := &graphHandleSpanTracker{parent: &Span{KnowledgeID: "doc", Attempt: 1}}
	svc.template = graphExtractCacheTemplate()
	svc.chunkRepo = chunks
	svc.knowledgeBaseRepo = &graphHandleKBRepo{kb: &types.KnowledgeBase{ID: "kb", ExtractConfig: &types.ExtractConfig{Enabled: true}, IndexingStrategy: types.IndexingStrategy{GraphEnabled: true}}}
	svc.modelService = &graphHandleModelService{model: model}
	svc.graphEngine = engine
	svc.spanTracker = tracker
	for index, id := range []string{"old-id", "new-id"} {
		payload, err := json.Marshal(types.ExtractChunkPayload{TenantID: 1, ChunkID: id, ModelID: "model", KnowledgeID: "doc", Attempt: 1, ChunkIndex: index})
		require.NoError(t, err)
		require.NoError(t, svc.Handle(context.Background(), asynq.NewTask(types.TypeChunkExtract, payload)))
	}
	requests, _ := model.Snapshot()
	require.Equal(t, 1, requests)
	require.Len(t, engine.graphs, 2)
	require.Equal(t, []string{"old-id"}, engine.graphs[0].Node[0].Chunks)
	require.Equal(t, []string{"new-id"}, engine.graphs[1].Node[0].Chunks)
	require.Len(t, tracker.outputs, 2)
	require.Equal(t, string(types.IngestionCacheStatusHit), tracker.outputs[1]["cache_status"])
	require.EqualValues(t, 0, tracker.outputs[1]["request_count"])
	var artifact types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", graphExtractArtifactKind).Take(&artifact).Error)
	require.NotContains(t, string(artifact.Payload), "old-id")
	require.NotContains(t, string(artifact.Payload), "new-id")
}
