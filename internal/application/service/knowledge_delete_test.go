package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type knowledgeDeleteEventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *knowledgeDeleteEventLog) record(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *knowledgeDeleteEventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type knowledgeDeleteRepositoryStub struct {
	interfaces.KnowledgeRepository
	events            *knowledgeDeleteEventLog
	knowledgeByID     map[string]*types.Knowledge
	batch             []*types.Knowledge
	getErr            error
	batchErr          error
	beginSnapshots    map[string]*types.Knowledge
	beginErrors       map[string]error
	finalizeErrors    map[string]error
	tagRelationErrors map[string]error
}

func (r *knowledgeDeleteRepositoryStub) GetKnowledgeByID(
	_ context.Context,
	_ uint64,
	id string,
) (*types.Knowledge, error) {
	r.events.record("get:" + id)
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.knowledgeByID[id], nil
}

func (r *knowledgeDeleteRepositoryStub) GetKnowledgeBatch(
	_ context.Context,
	_ uint64,
	_ []string,
) ([]*types.Knowledge, error) {
	r.events.record("get-batch")
	if r.batchErr != nil {
		return nil, r.batchErr
	}
	return append([]*types.Knowledge(nil), r.batch...), nil
}

func (r *knowledgeDeleteRepositoryStub) BeginKnowledgeDelete(
	_ context.Context,
	_ uint64,
	knowledgeBaseID string,
	knowledgeID string,
) (*types.Knowledge, error) {
	r.events.record("begin:" + knowledgeBaseID + ":" + knowledgeID)
	if err := r.beginErrors[knowledgeID]; err != nil {
		return nil, err
	}
	return r.beginSnapshots[knowledgeID], nil
}

func (r *knowledgeDeleteRepositoryStub) FinalizeKnowledgeDelete(
	_ context.Context,
	_ uint64,
	knowledgeBaseID string,
	knowledgeID string,
) error {
	r.events.record("finalize:" + knowledgeBaseID + ":" + knowledgeID)
	return r.finalizeErrors[knowledgeID]
}

func (r *knowledgeDeleteRepositoryStub) DeleteKnowledgeTagRelations(
	_ context.Context,
	knowledgeID string,
) error {
	r.events.record("cleanup:tag:" + knowledgeID)
	return r.tagRelationErrors[knowledgeID]
}

type knowledgeDeleteKnowledgeBaseServiceStub struct {
	interfaces.KnowledgeBaseService
	events *knowledgeDeleteEventLog
}

func (s *knowledgeDeleteKnowledgeBaseServiceStub) GetKnowledgeBaseByID(
	_ context.Context,
	id string,
) (*types.KnowledgeBase, error) {
	s.events.record("cleanup:kb:" + id)
	return nil, nil
}

type knowledgeDeleteChunkRepositoryStub struct {
	interfaces.ChunkRepository
	events *knowledgeDeleteEventLog
	err    error
}

func (r *knowledgeDeleteChunkRepositoryStub) ListImageInfoByKnowledgeIDs(
	_ context.Context,
	_ uint64,
	knowledgeIDs []string,
) ([]interfaces.ChunkImageInfo, error) {
	r.events.record("cleanup:images:" + strings.Join(knowledgeIDs, ","))
	return nil, r.err
}

type knowledgeDeleteChunkServiceStub struct {
	interfaces.ChunkService
	events         *knowledgeDeleteEventLog
	repository     interfaces.ChunkRepository
	singleErr      error
	batchErr       error
	batchKnowledge []string
}

func (s *knowledgeDeleteChunkServiceStub) GetRepository() interfaces.ChunkRepository {
	return s.repository
}

func (s *knowledgeDeleteChunkServiceStub) DeleteChunksByKnowledgeID(
	_ context.Context,
	knowledgeID string,
) error {
	s.events.record("cleanup:chunks:" + knowledgeID)
	return s.singleErr
}

func (s *knowledgeDeleteChunkServiceStub) DeleteByKnowledgeList(
	_ context.Context,
	knowledgeIDs []string,
) error {
	s.batchKnowledge = append([]string(nil), knowledgeIDs...)
	s.events.record("cleanup:batch-chunks:" + strings.Join(knowledgeIDs, ","))
	return s.batchErr
}

type knowledgeDeleteTenantRepositoryStub struct {
	interfaces.TenantRepository
	events *knowledgeDeleteEventLog
}

func (r *knowledgeDeleteTenantRepositoryStub) AdjustStorageUsed(
	_ context.Context,
	_ uint64,
	_ int64,
) error {
	r.events.record("cleanup:storage")
	return nil
}

type knowledgeDeleteGraphRepositoryStub struct {
	interfaces.RetrieveGraphRepository
	events *knowledgeDeleteEventLog
	err    error
}

func (r *knowledgeDeleteGraphRepositoryStub) DelGraph(
	_ context.Context,
	namespaces []types.NameSpace,
) error {
	ids := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		ids = append(ids, namespace.Knowledge)
	}
	r.events.record("cleanup:graph:" + strings.Join(ids, ","))
	return r.err
}

type knowledgeDeleteTaskInspectorStub struct {
	interfaces.TaskInspector
	events       *knowledgeDeleteEventLog
	err          error
	knowledgeIDs []string
}

func (s *knowledgeDeleteTaskInspectorStub) CancelTasksForKnowledge(
	_ context.Context,
	knowledgeID string,
) (int, int, error) {
	s.knowledgeIDs = append(s.knowledgeIDs, knowledgeID)
	s.events.record("dequeue:" + knowledgeID)
	return 0, 0, s.err
}

type knowledgeDeleteRetrieveEngineStub struct {
	interfaces.RetrieveEngineService
}

func (*knowledgeDeleteRetrieveEngineStub) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (*knowledgeDeleteRetrieveEngineStub) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

type knowledgeDeleteRetrieveRegistryStub struct {
	interfaces.RetrieveEngineRegistry
}

func (*knowledgeDeleteRetrieveRegistryStub) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return &knowledgeDeleteRetrieveEngineStub{}, nil
}

type knowledgeDeleteServiceFixture struct {
	service   *knowledgeService
	events    *knowledgeDeleteEventLog
	repo      *knowledgeDeleteRepositoryStub
	chunks    *knowledgeDeleteChunkServiceStub
	inspector *knowledgeDeleteTaskInspectorStub
	graph     *knowledgeDeleteGraphRepositoryStub
}

func newKnowledgeDeleteServiceFixture(repo *knowledgeDeleteRepositoryStub) *knowledgeDeleteServiceFixture {
	events := repo.events
	chunkRepo := &knowledgeDeleteChunkRepositoryStub{events: events}
	chunks := &knowledgeDeleteChunkServiceStub{
		events:     events,
		repository: chunkRepo,
	}
	inspector := &knowledgeDeleteTaskInspectorStub{events: events}
	graph := &knowledgeDeleteGraphRepositoryStub{events: events}
	return &knowledgeDeleteServiceFixture{
		service: &knowledgeService{
			repo:           repo,
			kbService:      &knowledgeDeleteKnowledgeBaseServiceStub{events: events},
			chunkService:   chunks,
			tenantRepo:     &knowledgeDeleteTenantRepositoryStub{events: events},
			taskInspector:  inspector,
			graphEngine:    graph,
			retrieveEngine: &knowledgeDeleteRetrieveRegistryStub{},
		},
		events:    events,
		repo:      repo,
		chunks:    chunks,
		inspector: inspector,
		graph:     graph,
	}
}

func knowledgeDeleteTestContext() context.Context {
	tenant := &types.Tenant{
		ID: 7,
		RetrieverEngines: types.RetrieverEngines{
			Engines: []types.RetrieverEngineParams{{
				RetrieverEngineType: types.PostgresRetrieverEngineType,
				RetrieverType:       types.VectorRetrieverType,
			}},
		},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenant.ID)
	return context.WithValue(ctx, types.TenantInfoContextKey, tenant)
}

func knowledgeDeleteTestKnowledge(id, kbID, status string) *types.Knowledge {
	return &types.Knowledge{
		ID:              id,
		TenantID:        7,
		KnowledgeBaseID: kbID,
		ParseStatus:     status,
	}
}

func knowledgeDeleteEventsWithPrefix(events []string, prefix string) []string {
	var matched []string
	for _, event := range events {
		if strings.HasPrefix(event, prefix) {
			matched = append(matched, event)
		}
	}
	return matched
}

func knowledgeDeleteEventIndex(events []string, event string) int {
	for index, candidate := range events {
		if candidate == event {
			return index
		}
	}
	return -1
}

func TestRemoveChunkRefs(t *testing.T) {
	got := removeChunkRefs(
		types.StringArray{"chunk-a", "chunk-b", "chunk-c"},
		map[string]bool{"chunk-b": true},
	)

	require.Equal(t, types.StringArray{"chunk-a", "chunk-c"}, got)
}

func TestRemoveChunkRefsNoRemovedSet(t *testing.T) {
	refs := types.StringArray{"chunk-a", "chunk-b"}

	got := removeChunkRefs(refs, nil)

	require.Equal(t, refs, got)
}

func TestDeleteKnowledgeUsesBeginSnapshotForDequeueCleanupAndFinalize(t *testing.T) {
	tests := []struct {
		name           string
		preReadStatus  string
		snapshotStatus string
		expectDequeue  bool
	}{
		{
			name:           "pending",
			preReadStatus:  types.ParseStatusCompleted,
			snapshotStatus: types.ParseStatusPending,
			expectDequeue:  true,
		},
		{
			name:           "processing",
			preReadStatus:  types.ParseStatusCompleted,
			snapshotStatus: types.ParseStatusProcessing,
			expectDequeue:  true,
		},
		{
			name:           "already deleting",
			preReadStatus:  types.ParseStatusPending,
			snapshotStatus: types.ParseStatusDeleting,
		},
		{
			name:           "finalizing",
			preReadStatus:  types.ParseStatusPending,
			snapshotStatus: types.ParseStatusFinalizing,
		},
		{
			name:           "completed",
			preReadStatus:  types.ParseStatusPending,
			snapshotStatus: types.ParseStatusCompleted,
		},
		{
			name:           "failed",
			preReadStatus:  types.ParseStatusPending,
			snapshotStatus: types.ParseStatusFailed,
		},
		{
			name:           "cancelled",
			preReadStatus:  types.ParseStatusPending,
			snapshotStatus: types.ParseStatusCancelled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := &knowledgeDeleteEventLog{}
			repo := &knowledgeDeleteRepositoryStub{
				events: events,
				knowledgeByID: map[string]*types.Knowledge{
					"knowledge-1": knowledgeDeleteTestKnowledge(
						"knowledge-1",
						"kb-pre-read",
						test.preReadStatus,
					),
				},
				beginSnapshots: map[string]*types.Knowledge{
					"knowledge-1": knowledgeDeleteTestKnowledge(
						"knowledge-1",
						"kb-snapshot",
						test.snapshotStatus,
					),
				},
				beginErrors:       map[string]error{},
				finalizeErrors:    map[string]error{},
				tagRelationErrors: map[string]error{},
			}
			fixture := newKnowledgeDeleteServiceFixture(repo)

			err := fixture.service.DeleteKnowledge(knowledgeDeleteTestContext(), "knowledge-1")

			require.NoError(t, err)
			recorded := events.snapshot()
			begin := knowledgeDeleteEventIndex(recorded, "begin:kb-pre-read:knowledge-1")
			cleanup := knowledgeDeleteEventIndex(recorded, "cleanup:kb:kb-snapshot")
			finalize := knowledgeDeleteEventIndex(recorded, "finalize:kb-snapshot:knowledge-1")
			require.NotEqual(t, -1, begin)
			require.NotEqual(t, -1, cleanup)
			require.NotEqual(t, -1, finalize)
			assert.Less(t, begin, cleanup)
			assert.Less(t, cleanup, finalize)
			if test.expectDequeue {
				dequeue := knowledgeDeleteEventIndex(recorded, "dequeue:knowledge-1")
				require.NotEqual(t, -1, dequeue)
				assert.Less(t, begin, dequeue)
				assert.Less(t, dequeue, cleanup)
			} else {
				assert.Empty(t, fixture.inspector.knowledgeIDs)
			}
		})
	}
}

func TestDeleteKnowledgeBeginFailureStopsAllLaterWork(t *testing.T) {
	beginErr := errors.New("begin failed")
	events := &knowledgeDeleteEventLog{}
	repo := &knowledgeDeleteRepositoryStub{
		events: events,
		knowledgeByID: map[string]*types.Knowledge{
			"knowledge-1": knowledgeDeleteTestKnowledge(
				"knowledge-1",
				"kb-1",
				types.ParseStatusPending,
			),
		},
		beginSnapshots:    map[string]*types.Knowledge{},
		beginErrors:       map[string]error{"knowledge-1": beginErr},
		finalizeErrors:    map[string]error{},
		tagRelationErrors: map[string]error{},
	}
	fixture := newKnowledgeDeleteServiceFixture(repo)

	err := fixture.service.DeleteKnowledge(knowledgeDeleteTestContext(), "knowledge-1")

	require.ErrorIs(t, err, beginErr)
	assert.Equal(
		t,
		[]string{"get:knowledge-1", "begin:kb-1:knowledge-1"},
		events.snapshot(),
	)
	assert.Empty(t, fixture.inspector.knowledgeIDs)
}

func TestDeleteKnowledgeInspectorFailureIsBestEffort(t *testing.T) {
	events := &knowledgeDeleteEventLog{}
	repo := &knowledgeDeleteRepositoryStub{
		events: events,
		knowledgeByID: map[string]*types.Knowledge{
			"knowledge-1": knowledgeDeleteTestKnowledge(
				"knowledge-1",
				"kb-1",
				types.ParseStatusCompleted,
			),
		},
		beginSnapshots: map[string]*types.Knowledge{
			"knowledge-1": knowledgeDeleteTestKnowledge(
				"knowledge-1",
				"kb-1",
				types.ParseStatusPending,
			),
		},
		beginErrors:       map[string]error{},
		finalizeErrors:    map[string]error{},
		tagRelationErrors: map[string]error{},
	}
	fixture := newKnowledgeDeleteServiceFixture(repo)
	fixture.inspector.err = errors.New("inspector unavailable")

	err := fixture.service.DeleteKnowledge(knowledgeDeleteTestContext(), "knowledge-1")

	require.NoError(t, err)
	assert.Equal(t, []string{"knowledge-1"}, fixture.inspector.knowledgeIDs)
	assert.Contains(t, events.snapshot(), "finalize:kb-1:knowledge-1")
}

func TestDeleteKnowledgeCleanupFailureSkipsFinalize(t *testing.T) {
	cleanupErr := errors.New("chunk cleanup failed")
	events := &knowledgeDeleteEventLog{}
	repo := &knowledgeDeleteRepositoryStub{
		events: events,
		knowledgeByID: map[string]*types.Knowledge{
			"knowledge-1": knowledgeDeleteTestKnowledge(
				"knowledge-1",
				"kb-1",
				types.ParseStatusCompleted,
			),
		},
		beginSnapshots: map[string]*types.Knowledge{
			"knowledge-1": knowledgeDeleteTestKnowledge(
				"knowledge-1",
				"kb-1",
				types.ParseStatusCompleted,
			),
		},
		beginErrors:       map[string]error{},
		finalizeErrors:    map[string]error{},
		tagRelationErrors: map[string]error{},
	}
	fixture := newKnowledgeDeleteServiceFixture(repo)
	fixture.chunks.singleErr = cleanupErr

	err := fixture.service.DeleteKnowledge(knowledgeDeleteTestContext(), "knowledge-1")

	require.ErrorIs(t, err, cleanupErr)
	assert.Empty(
		t,
		knowledgeDeleteEventsWithPrefix(events.snapshot(), "finalize:"),
	)
}

func TestDeleteKnowledgeReturnsFinalizeFailure(t *testing.T) {
	finalizeErr := errors.New("finalize failed")
	events := &knowledgeDeleteEventLog{}
	repo := &knowledgeDeleteRepositoryStub{
		events: events,
		knowledgeByID: map[string]*types.Knowledge{
			"knowledge-1": knowledgeDeleteTestKnowledge(
				"knowledge-1",
				"kb-1",
				types.ParseStatusCompleted,
			),
		},
		beginSnapshots: map[string]*types.Knowledge{
			"knowledge-1": knowledgeDeleteTestKnowledge(
				"knowledge-1",
				"kb-1",
				types.ParseStatusCompleted,
			),
		},
		beginErrors:       map[string]error{},
		finalizeErrors:    map[string]error{"knowledge-1": finalizeErr},
		tagRelationErrors: map[string]error{},
	}
	fixture := newKnowledgeDeleteServiceFixture(repo)

	err := fixture.service.DeleteKnowledge(knowledgeDeleteTestContext(), "knowledge-1")

	require.ErrorIs(t, err, finalizeErr)
	assert.Contains(t, events.snapshot(), "cleanup:tag:knowledge-1")
}

func TestDeleteKnowledgeListReturnsNilForEmptyActiveSubset(t *testing.T) {
	events := &knowledgeDeleteEventLog{}
	repo := &knowledgeDeleteRepositoryStub{
		events:            events,
		knowledgeByID:     map[string]*types.Knowledge{},
		beginSnapshots:    map[string]*types.Knowledge{},
		beginErrors:       map[string]error{},
		finalizeErrors:    map[string]error{},
		tagRelationErrors: map[string]error{},
	}
	fixture := newKnowledgeDeleteServiceFixture(repo)

	err := fixture.service.DeleteKnowledgeList(
		knowledgeDeleteTestContext(),
		[]string{"knowledge-soft-deleted", "knowledge-missing"},
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"get-batch"}, events.snapshot())
	assert.Empty(t, fixture.chunks.batchKnowledge)
}

func TestDeleteKnowledgeListJoinsAllBeginFailuresInStableOrderWithoutCleanup(t *testing.T) {
	beginErrA := errors.New("begin-a")
	beginErrB := errors.New("begin-b")
	beginErrC := errors.New("begin-c")
	events := &knowledgeDeleteEventLog{}
	repo := &knowledgeDeleteRepositoryStub{
		events: events,
		batch: []*types.Knowledge{
			knowledgeDeleteTestKnowledge("knowledge-c", "kb-b", types.ParseStatusCompleted),
			knowledgeDeleteTestKnowledge("knowledge-b", "kb-a", types.ParseStatusCompleted),
			knowledgeDeleteTestKnowledge("knowledge-a", "kb-a", types.ParseStatusCompleted),
		},
		beginSnapshots: map[string]*types.Knowledge{},
		beginErrors: map[string]error{
			"knowledge-a": beginErrA,
			"knowledge-b": beginErrB,
			"knowledge-c": beginErrC,
		},
		finalizeErrors:    map[string]error{},
		tagRelationErrors: map[string]error{},
	}
	fixture := newKnowledgeDeleteServiceFixture(repo)

	err := fixture.service.DeleteKnowledgeList(
		knowledgeDeleteTestContext(),
		[]string{"knowledge-c", "knowledge-b", "knowledge-a"},
	)

	require.Error(t, err)
	require.ErrorIs(t, err, beginErrA)
	require.ErrorIs(t, err, beginErrB)
	require.ErrorIs(t, err, beginErrC)
	for _, beginErr := range []error{beginErrA, beginErrB, beginErrC} {
		require.Contains(t, err.Error(), beginErr.Error())
	}
	assert.Less(
		t,
		strings.Index(err.Error(), beginErrA.Error()),
		strings.Index(err.Error(), beginErrB.Error()),
	)
	assert.Less(
		t,
		strings.Index(err.Error(), beginErrB.Error()),
		strings.Index(err.Error(), beginErrC.Error()),
	)
	assert.Equal(
		t,
		[]string{
			"begin:kb-a:knowledge-a",
			"begin:kb-a:knowledge-b",
			"begin:kb-b:knowledge-c",
		},
		knowledgeDeleteEventsWithPrefix(events.snapshot(), "begin:"),
	)
	assert.Empty(t, fixture.inspector.knowledgeIDs)
	assert.Empty(t, fixture.chunks.batchKnowledge)
	assert.Empty(
		t,
		knowledgeDeleteEventsWithPrefix(events.snapshot(), "cleanup:"),
	)
	assert.Empty(
		t,
		knowledgeDeleteEventsWithPrefix(events.snapshot(), "finalize:"),
	)
}

func TestDeleteKnowledgeListContinuesAfterBeginFailureAndCleansOnlySucceededSnapshots(t *testing.T) {
	beginErr := errors.New("begin knowledge-b failed")
	events := &knowledgeDeleteEventLog{}
	rows := []*types.Knowledge{
		knowledgeDeleteTestKnowledge("knowledge-d", "kb-b", types.ParseStatusProcessing),
		knowledgeDeleteTestKnowledge("knowledge-c", "kb-b", types.ParseStatusDeleting),
		knowledgeDeleteTestKnowledge("knowledge-b", "kb-a", types.ParseStatusCompleted),
		knowledgeDeleteTestKnowledge("knowledge-a", "kb-a", types.ParseStatusPending),
	}
	repo := &knowledgeDeleteRepositoryStub{
		events: events,
		batch:  rows,
		beginSnapshots: map[string]*types.Knowledge{
			"knowledge-a": knowledgeDeleteTestKnowledge(
				"knowledge-a", "kb-a", types.ParseStatusPending,
			),
			"knowledge-c": knowledgeDeleteTestKnowledge(
				"knowledge-c", "kb-b", types.ParseStatusDeleting,
			),
			"knowledge-d": knowledgeDeleteTestKnowledge(
				"knowledge-d", "kb-b", types.ParseStatusProcessing,
			),
		},
		beginErrors:       map[string]error{"knowledge-b": beginErr},
		finalizeErrors:    map[string]error{},
		tagRelationErrors: map[string]error{},
	}
	fixture := newKnowledgeDeleteServiceFixture(repo)

	err := fixture.service.DeleteKnowledgeList(
		knowledgeDeleteTestContext(),
		[]string{"knowledge-d", "knowledge-c", "knowledge-b", "knowledge-a"},
	)

	require.ErrorIs(t, err, beginErr)
	recorded := events.snapshot()
	assert.Equal(
		t,
		[]string{
			"begin:kb-a:knowledge-a",
			"begin:kb-a:knowledge-b",
			"begin:kb-b:knowledge-c",
			"begin:kb-b:knowledge-d",
		},
		knowledgeDeleteEventsWithPrefix(recorded, "begin:"),
	)
	assert.Equal(t, []string{"knowledge-a", "knowledge-d"}, fixture.inspector.knowledgeIDs)
	assert.Equal(
		t,
		[]string{"knowledge-a", "knowledge-c", "knowledge-d"},
		fixture.chunks.batchKnowledge,
	)
	assert.Equal(
		t,
		[]string{
			"finalize:kb-a:knowledge-a",
			"finalize:kb-b:knowledge-c",
			"finalize:kb-b:knowledge-d",
		},
		knowledgeDeleteEventsWithPrefix(recorded, "finalize:"),
	)
	assert.NotContains(t, recorded, "cleanup:tag:knowledge-b")
}

func TestDeleteKnowledgeListCleanupFailureJoinsBeginErrorAndSkipsAllFinalize(t *testing.T) {
	beginErr := errors.New("begin-a")
	cleanupErr := errors.New("batch-cleanup")
	events := &knowledgeDeleteEventLog{}
	repo := &knowledgeDeleteRepositoryStub{
		events: events,
		batch: []*types.Knowledge{
			knowledgeDeleteTestKnowledge("knowledge-b", "kb-1", types.ParseStatusCompleted),
			knowledgeDeleteTestKnowledge("knowledge-a", "kb-1", types.ParseStatusCompleted),
		},
		beginSnapshots: map[string]*types.Knowledge{
			"knowledge-b": knowledgeDeleteTestKnowledge(
				"knowledge-b", "kb-1", types.ParseStatusCompleted,
			),
		},
		beginErrors:       map[string]error{"knowledge-a": beginErr},
		finalizeErrors:    map[string]error{},
		tagRelationErrors: map[string]error{},
	}
	fixture := newKnowledgeDeleteServiceFixture(repo)
	fixture.chunks.batchErr = cleanupErr

	err := fixture.service.DeleteKnowledgeList(
		knowledgeDeleteTestContext(),
		[]string{"knowledge-b", "knowledge-a"},
	)

	require.Error(t, err)
	require.ErrorIs(t, err, beginErr)
	require.ErrorIs(t, err, cleanupErr)
	require.Contains(t, err.Error(), beginErr.Error())
	require.Contains(t, err.Error(), cleanupErr.Error())
	assert.Less(
		t,
		strings.Index(err.Error(), beginErr.Error()),
		strings.Index(err.Error(), cleanupErr.Error()),
	)
	assert.Equal(t, []string{"knowledge-b"}, fixture.chunks.batchKnowledge)
	assert.Empty(
		t,
		knowledgeDeleteEventsWithPrefix(events.snapshot(), "finalize:"),
	)
}

func TestDeleteKnowledgeListContinuesAfterFinalizeFailuresInStableOrder(t *testing.T) {
	finalizeErrA := errors.New("finalize-a")
	finalizeErrB := errors.New("finalize-b")
	events := &knowledgeDeleteEventLog{}
	repo := &knowledgeDeleteRepositoryStub{
		events: events,
		batch: []*types.Knowledge{
			knowledgeDeleteTestKnowledge("knowledge-c", "kb-1", types.ParseStatusCompleted),
			knowledgeDeleteTestKnowledge("knowledge-b", "kb-1", types.ParseStatusCompleted),
			knowledgeDeleteTestKnowledge("knowledge-a", "kb-1", types.ParseStatusCompleted),
		},
		beginSnapshots: map[string]*types.Knowledge{
			"knowledge-a": knowledgeDeleteTestKnowledge(
				"knowledge-a", "kb-1", types.ParseStatusCompleted,
			),
			"knowledge-b": knowledgeDeleteTestKnowledge(
				"knowledge-b", "kb-1", types.ParseStatusCompleted,
			),
			"knowledge-c": knowledgeDeleteTestKnowledge(
				"knowledge-c", "kb-1", types.ParseStatusCompleted,
			),
		},
		beginErrors: map[string]error{},
		finalizeErrors: map[string]error{
			"knowledge-a": finalizeErrA,
			"knowledge-b": finalizeErrB,
		},
		tagRelationErrors: map[string]error{},
	}
	fixture := newKnowledgeDeleteServiceFixture(repo)

	err := fixture.service.DeleteKnowledgeList(
		knowledgeDeleteTestContext(),
		[]string{"knowledge-c", "knowledge-b", "knowledge-a"},
	)

	require.Error(t, err)
	require.ErrorIs(t, err, finalizeErrA)
	require.ErrorIs(t, err, finalizeErrB)
	assert.Equal(
		t,
		[]string{
			"finalize:kb-1:knowledge-a",
			"finalize:kb-1:knowledge-b",
			"finalize:kb-1:knowledge-c",
		},
		knowledgeDeleteEventsWithPrefix(events.snapshot(), "finalize:"),
	)
	assert.Less(
		t,
		strings.Index(err.Error(), finalizeErrA.Error()),
		strings.Index(err.Error(), finalizeErrB.Error()),
	)
}

func TestDeleteKnowledgeListRetrySkipsRowsOutsideActiveSubset(t *testing.T) {
	events := &knowledgeDeleteEventLog{}
	repo := &knowledgeDeleteRepositoryStub{
		events: events,
		batch: []*types.Knowledge{
			knowledgeDeleteTestKnowledge("knowledge-active", "kb-1", types.ParseStatusDeleting),
		},
		beginSnapshots: map[string]*types.Knowledge{
			"knowledge-active": knowledgeDeleteTestKnowledge(
				"knowledge-active", "kb-1", types.ParseStatusDeleting,
			),
		},
		beginErrors:       map[string]error{},
		finalizeErrors:    map[string]error{},
		tagRelationErrors: map[string]error{},
	}
	fixture := newKnowledgeDeleteServiceFixture(repo)

	err := fixture.service.DeleteKnowledgeList(
		knowledgeDeleteTestContext(),
		[]string{"knowledge-soft-deleted", "knowledge-active"},
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{"begin:kb-1:knowledge-active"},
		knowledgeDeleteEventsWithPrefix(events.snapshot(), "begin:"),
	)
	assert.Empty(t, fixture.inspector.knowledgeIDs)
	assert.Equal(t, []string{"knowledge-active"}, fixture.chunks.batchKnowledge)
}
