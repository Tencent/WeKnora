package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deleteWikiUnionRepository struct {
	interfaces.WikiPageRepository
	interfaces.WikiProvenanceRepository

	legacyPages []*types.WikiPage
	blockRefs   []*types.WikiKnowledgeBlockReference
	legacyCalls int
	blockCalls  int
}

func (r *deleteWikiUnionRepository) ListBySourceRef(
	_ context.Context, _, _ string,
) ([]*types.WikiPage, error) {
	r.legacyCalls++
	return r.legacyPages, nil
}

func (r *deleteWikiUnionRepository) ListBlockReferencesByKnowledge(
	_ context.Context, _, _ string,
) ([]*types.WikiKnowledgeBlockReference, error) {
	r.blockCalls++
	return r.blockRefs, nil
}

type deleteWikiRemoval struct {
	slug        string
	knowledgeID string
}

type deleteWikiUnionService struct {
	interfaces.WikiPageService

	pages         map[string]*types.WikiPage
	loadedSlugs   []string
	removals      []deleteWikiRemoval
	updatedSlugs  []string
	deletedSlugs  []string
	removeHandled bool
	removeErr     error
}

func (s *deleteWikiUnionService) GetPageBySlug(
	_ context.Context, _, slug string,
) (*types.WikiPage, error) {
	s.loadedSlugs = append(s.loadedSlugs, slug)
	return s.pages[slug], nil
}

func (s *deleteWikiUnionService) UpdatePageMeta(_ context.Context, page *types.WikiPage) error {
	s.updatedSlugs = append(s.updatedSlugs, page.Slug)
	return nil
}

func (s *deleteWikiUnionService) DeletePage(_ context.Context, _, slug string) error {
	s.deletedSlugs = append(s.deletedSlugs, slug)
	return nil
}

func (s *deleteWikiUnionService) GetPageWithSources(
	context.Context, string, string,
) (*types.WikiPageDetailResponse, error) {
	return nil, nil
}

func (s *deleteWikiUnionService) SavePageWithProvenance(
	_ context.Context, page *types.WikiPage, _ *types.WikiPageBlockSet,
) (*types.WikiPage, error) {
	return page, nil
}

func (s *deleteWikiUnionService) GetCurrentPageBlockSet(
	context.Context, string, string,
) (*types.WikiPageBlockSet, error) {
	return nil, nil
}

func (s *deleteWikiUnionService) ListPageSlugsByKnowledgeSource(
	context.Context, string, string,
) ([]string, error) {
	return nil, nil
}

func (s *deleteWikiUnionService) RemoveKnowledgeFromPage(
	_ context.Context, _, slug, knowledgeID string,
) (bool, bool, error) {
	s.removals = append(s.removals, deleteWikiRemoval{slug: slug, knowledgeID: knowledgeID})
	return s.removeHandled, false, s.removeErr
}

type deleteWikiPendingRepository struct {
	interfaces.TaskPendingOpsRepository
	ops        []*types.TaskPendingOp
	enqueueErr error
}

func (r *deleteWikiPendingRepository) Enqueue(_ context.Context, op *types.TaskPendingOp) error {
	if r.enqueueErr != nil {
		return r.enqueueErr
	}
	r.ops = append(r.ops, op)
	return nil
}

func (r *deleteWikiPendingRepository) DeleteByDedupKey(
	context.Context, string, string, string, string, string,
) error {
	return nil
}

type deleteWikiTaskQueue struct {
	interfaces.TaskEnqueuer
	tasks []*asynq.Task
}

func (q *deleteWikiTaskQueue) Enqueue(
	task *asynq.Task, _ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{ID: "wiki-delete-test", Type: task.Type()}, nil
}

func TestCleanupWikiOnKnowledgeDeleteUnionsBlockSourcesAndLegacySourceRefs(t *testing.T) {
	const (
		kbID        = "kb-1"
		knowledgeID = "knowledge-delete"
	)

	repo := &deleteWikiUnionRepository{
		legacyPages: []*types.WikiPage{
			{
				ID:              "page-legacy",
				KnowledgeBaseID: kbID,
				Slug:            "concept/legacy",
				PageType:        types.WikiPageTypeConcept,
				SourceRefs: types.StringArray{
					knowledgeID + "|Deleted document",
					"knowledge-keep|Kept document",
				},
			},
			{
				ID:                "page-overlap",
				KnowledgeBaseID:   kbID,
				Slug:              "concept/structured-overlap",
				PageType:          types.WikiPageTypeConcept,
				CurrentBlockSetID: "set-overlap",
			},
		},
		blockRefs: []*types.WikiKnowledgeBlockReference{
			{PageID: "page-overlap", PageSlug: "concept/structured-overlap", BlockSetID: "set-overlap", BlockID: "block-1"},
			{PageID: "page-drift", PageSlug: "concept/structured-drift", BlockSetID: "set-drift", BlockID: "block-2"},
			{PageID: "page-drift", PageSlug: "concept/structured-drift", BlockSetID: "set-drift", BlockID: "block-3"},
		},
	}
	wikiService := &deleteWikiUnionService{
		pages: map[string]*types.WikiPage{
			"concept/structured-drift": {
				ID:              "page-drift",
				KnowledgeBaseID: kbID,
				Slug:            "concept/structured-drift",
				PageType:        types.WikiPageTypeConcept,
				// Deliberately empty: the authoritative reverse lookup must
				// still classify this source_refs-drifted page as structured.
				CurrentBlockSetID: "",
			},
		},
		removeHandled: true,
	}
	pendingRepo := &deleteWikiPendingRepository{}
	queue := &deleteWikiTaskQueue{}
	svc := &knowledgeService{
		wikiRepo:        repo,
		wikiService:     wikiService,
		taskPendingRepo: pendingRepo,
		task:            queue,
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	require.NoError(t, svc.cleanupWikiOnKnowledgeDelete(ctx, &types.Knowledge{
		ID:              knowledgeID,
		TenantID:        7,
		KnowledgeBaseID: kbID,
		Title:           "Deleted document",
	}))

	assert.Equal(t, 1, repo.legacyCalls)
	assert.Equal(t, 1, repo.blockCalls)
	assert.Equal(t, []string{"concept/structured-drift"}, wikiService.loadedSlugs)
	assert.Equal(t, []deleteWikiRemoval{
		{slug: "concept/structured-overlap", knowledgeID: knowledgeID},
		{slug: "concept/structured-drift", knowledgeID: knowledgeID},
	}, wikiService.removals)
	assert.Equal(t, []string{"concept/legacy"}, wikiService.updatedSlugs)
	assert.Empty(t, wikiService.deletedSlugs)

	require.Len(t, pendingRepo.ops, 1)
	var retract WikiPendingOp
	require.NoError(t, json.Unmarshal(pendingRepo.ops[0].Payload, &retract))
	assert.Equal(t, WikiOpRetract, retract.Op)
	assert.Equal(t, []string{
		"concept/legacy",
		"concept/structured-overlap",
		"concept/structured-drift",
	}, retract.PageSlugs)
	require.Len(t, queue.tasks, 1)
}

func TestCleanupWikiOnKnowledgeDeleteRetriesStructuredFailureWithoutLegacyMutation(t *testing.T) {
	const (
		kbID        = "kb-structured-retry"
		knowledgeID = "knowledge-delete"
		slug        = "concept/structured"
	)
	repo := &deleteWikiUnionRepository{legacyPages: []*types.WikiPage{{
		ID: "page-structured", KnowledgeBaseID: kbID, Slug: slug,
		PageType: types.WikiPageTypeConcept, CurrentBlockSetID: "set-structured",
		SourceRefs: types.StringArray{knowledgeID},
	}}}
	wikiService := &deleteWikiUnionService{
		removeHandled: true,
		removeErr:     errors.New("temporary publish conflict"),
	}
	pendingRepo := &deleteWikiPendingRepository{}
	svc := &knowledgeService{
		wikiRepo: repo, wikiService: wikiService,
		taskPendingRepo: pendingRepo, task: &deleteWikiTaskQueue{},
	}
	require.NoError(t, svc.cleanupWikiOnKnowledgeDelete(context.Background(), &types.Knowledge{
		ID: knowledgeID, TenantID: 7, KnowledgeBaseID: kbID, Title: "Deleted document",
	}))

	assert.Empty(t, wikiService.updatedSlugs)
	assert.Empty(t, wikiService.deletedSlugs)
	require.Len(t, pendingRepo.ops, 1)
	var retract WikiPendingOp
	require.NoError(t, json.Unmarshal(pendingRepo.ops[0].Payload, &retract))
	assert.Equal(t, []string{slug}, retract.PageSlugs)
}

func TestCleanupWikiOnKnowledgeDeleteFailsWhenRetractIsNotDurable(t *testing.T) {
	const (
		kbID        = "kb-strict-retract"
		knowledgeID = "knowledge-delete"
	)
	repo := &deleteWikiUnionRepository{legacyPages: []*types.WikiPage{{
		ID: "page-legacy", KnowledgeBaseID: kbID, Slug: "concept/must-stay",
		PageType: types.WikiPageTypeConcept, SourceRefs: types.StringArray{knowledgeID},
	}}}
	pendingRepo := &deleteWikiPendingRepository{
		enqueueErr: errors.New("pending store unavailable"),
	}
	wikiService := &deleteWikiUnionService{}
	svc := &knowledgeService{
		wikiRepo: repo, wikiService: wikiService,
		taskPendingRepo: pendingRepo, task: &deleteWikiTaskQueue{},
	}

	err := svc.cleanupWikiOnKnowledgeDelete(context.Background(), &types.Knowledge{
		ID: knowledgeID, TenantID: 7, KnowledgeBaseID: kbID, Title: "Deleted document",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist Wiki retract")
	assert.Empty(t, pendingRepo.ops)
	assert.Empty(t, wikiService.updatedSlugs, "outbox failure must not mutate legacy pages")
	assert.Empty(t, wikiService.deletedSlugs, "outbox failure must not delete pages")
	assert.Empty(t, wikiService.removals, "outbox failure must not remove structured blocks")
}

var _ interfaces.WikiPageRepository = (*deleteWikiUnionRepository)(nil)
var _ interfaces.WikiProvenanceRepository = (*deleteWikiUnionRepository)(nil)
var _ interfaces.WikiPageService = (*deleteWikiUnionService)(nil)
var _ interfaces.WikiProvenanceService = (*deleteWikiUnionService)(nil)
