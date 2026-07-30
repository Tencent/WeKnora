package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type fakeWikiCurrentPage struct {
	tenantID uint64
	kbID     string
	slug     string
	version  int
	content  string
}

func (r *fakeWikiProvenanceRepository) EnsureCurrentPage(_ context.Context, page *types.WikiPage) error {
	if _, ok := r.knowledgeBases[fakeKnowledgeBaseKey(page.TenantID, page.KnowledgeBaseID)]; !ok {
		return types.ErrWikiPublishScopeNotFound
	}
	key := fakePageKey(page.TenantID, page.KnowledgeBaseID, page.ID)
	if existing, ok := r.pages[key]; ok {
		if existing.slug != page.Slug {
			return types.ErrWikiPublishScopeNotFound
		}
		return nil
	}
	for _, existing := range r.pages {
		if existing.tenantID == page.TenantID && existing.kbID == page.KnowledgeBaseID && existing.slug == page.Slug {
			return types.ErrWikiPublishScopeNotFound
		}
	}
	r.pages[key] = fakeWikiCurrentPage{tenantID: page.TenantID, kbID: page.KnowledgeBaseID, slug: page.Slug}
	return nil
}

type fakeWikiProvenanceRepository struct {
	mu                 sync.Mutex
	pages              map[string]fakeWikiCurrentPage
	knowledgeBases     map[string]struct{}
	knowledges         map[string]struct{}
	knowledgeRevisions []types.KnowledgeRevision
	pageRevisions      []types.WikiProvenancePageRevision
	blocks             []types.WikiPageBlock
	sources            []types.WikiBlockSource
	pageSources        []types.WikiPageSource
	failOperation      string
}

func newFakeWikiProvenanceRepository() *fakeWikiProvenanceRepository {
	return &fakeWikiProvenanceRepository{
		pages:          make(map[string]fakeWikiCurrentPage),
		knowledgeBases: make(map[string]struct{}),
		knowledges:     make(map[string]struct{}),
	}
}

func fakeKnowledgeBaseKey(tenantID uint64, kbID string) string {
	return fmt.Sprintf("%d|%s", tenantID, kbID)
}

func fakePageKey(tenantID uint64, kbID, pageID string) string {
	return fmt.Sprintf("%d|%s|%s", tenantID, kbID, pageID)
}

func fakeKnowledgeKey(tenantID uint64, kbID, knowledgeID string) string {
	return fmt.Sprintf("%d|%s|%s", tenantID, kbID, knowledgeID)
}

func (r *fakeWikiProvenanceRepository) WithTransaction(
	_ context.Context,
	fn func(interfaces.WikiProvenanceRepository) error,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := r.clone()
	if err := fn(r); err != nil {
		r.restore(snapshot)
		return err
	}
	return nil
}

func (r *fakeWikiProvenanceRepository) GetPageProvenance(
	_ context.Context,
	_ uint64,
	_, _ string,
) (*types.WikiPageProvenanceResponse, error) {
	return nil, errors.New("not implemented by publisher fake")
}

func (r *fakeWikiProvenanceRepository) clone() *fakeWikiProvenanceRepository {
	copyRepo := newFakeWikiProvenanceRepository()
	for key, page := range r.pages {
		copyRepo.pages[key] = page
	}
	for key := range r.knowledgeBases {
		copyRepo.knowledgeBases[key] = struct{}{}
	}
	for key := range r.knowledges {
		copyRepo.knowledges[key] = struct{}{}
	}
	copyRepo.knowledgeRevisions = append([]types.KnowledgeRevision(nil), r.knowledgeRevisions...)
	copyRepo.pageRevisions = append([]types.WikiProvenancePageRevision(nil), r.pageRevisions...)
	copyRepo.blocks = append([]types.WikiPageBlock(nil), r.blocks...)
	copyRepo.sources = append([]types.WikiBlockSource(nil), r.sources...)
	copyRepo.pageSources = append([]types.WikiPageSource(nil), r.pageSources...)
	copyRepo.failOperation = r.failOperation
	return copyRepo
}

func (r *fakeWikiProvenanceRepository) restore(snapshot *fakeWikiProvenanceRepository) {
	r.pages = snapshot.pages
	r.knowledgeBases = snapshot.knowledgeBases
	r.knowledges = snapshot.knowledges
	r.knowledgeRevisions = snapshot.knowledgeRevisions
	r.pageRevisions = snapshot.pageRevisions
	r.blocks = snapshot.blocks
	r.sources = snapshot.sources
	r.pageSources = snapshot.pageSources
	r.failOperation = snapshot.failOperation
}

func (r *fakeWikiProvenanceRepository) LockPublishScope(
	_ context.Context,
	tenantID uint64,
	kbID, pageID string,
	knowledgeIDs []string,
) error {
	if _, ok := r.pages[fakePageKey(tenantID, kbID, pageID)]; !ok {
		return types.ErrWikiPublishScopeNotFound
	}
	for _, knowledgeID := range knowledgeIDs {
		if _, ok := r.knowledges[fakeKnowledgeKey(tenantID, kbID, knowledgeID)]; !ok {
			return types.ErrWikiPublishScopeNotFound
		}
	}
	return nil
}

func (r *fakeWikiProvenanceRepository) FindPageRevisionByPublishKey(
	_ context.Context,
	tenantID uint64,
	kbID, pageID, publishKey string,
) (*types.WikiProvenancePageRevision, error) {
	for i := range r.pageRevisions {
		revision := r.pageRevisions[i]
		if revision.TenantID == tenantID && revision.KnowledgeBaseID == kbID &&
			revision.PageID == pageID && revision.PublishKey == publishKey {
			return &revision, nil
		}
	}
	return nil, nil
}

func (r *fakeWikiProvenanceRepository) GetKnowledgeRevision(
	_ context.Context,
	tenantID uint64,
	kbID, revisionID string,
) (*types.KnowledgeRevision, error) {
	for i := range r.knowledgeRevisions {
		revision := r.knowledgeRevisions[i]
		if revision.TenantID == tenantID && revision.KnowledgeBaseID == kbID && revision.ID == revisionID {
			return &revision, nil
		}
	}
	return nil, nil
}

func (r *fakeWikiProvenanceRepository) FindKnowledgeRevisionByContentHash(
	_ context.Context,
	tenantID uint64,
	kbID, knowledgeID, contentHash string,
	parseAttempt int,
) (*types.KnowledgeRevision, error) {
	for i := len(r.knowledgeRevisions) - 1; i >= 0; i-- {
		revision := r.knowledgeRevisions[i]
		if revision.TenantID == tenantID && revision.KnowledgeBaseID == kbID &&
			revision.KnowledgeID == knowledgeID && revision.ContentHash == contentHash &&
			revision.ParseAttempt == parseAttempt &&
			(revision.Status == types.KnowledgeRevisionPublished || revision.Status == types.KnowledgeRevisionSuperseded) {
			return &revision, nil
		}
	}
	return nil, nil
}

func (r *fakeWikiProvenanceRepository) NextPageRevisionNo(
	_ context.Context,
	tenantID uint64,
	kbID, pageID string,
) (int, error) {
	maximum := 0
	if page, ok := r.pages[fakePageKey(tenantID, kbID, pageID)]; ok {
		maximum = page.version
	}
	for _, revision := range r.pageRevisions {
		if revision.TenantID == tenantID && revision.KnowledgeBaseID == kbID &&
			revision.PageID == pageID && revision.RevisionNo > maximum {
			maximum = revision.RevisionNo
		}
	}
	return maximum + 1, nil
}

func (r *fakeWikiProvenanceRepository) NextKnowledgeRevisionNo(
	_ context.Context,
	tenantID uint64,
	kbID, knowledgeID string,
) (int, error) {
	maximum := 0
	for _, revision := range r.knowledgeRevisions {
		if revision.TenantID == tenantID && revision.KnowledgeBaseID == kbID &&
			revision.KnowledgeID == knowledgeID && revision.RevisionNo > maximum {
			maximum = revision.RevisionNo
		}
	}
	return maximum + 1, nil
}

func (r *fakeWikiProvenanceRepository) CreateKnowledgeRevision(
	_ context.Context,
	revision *types.KnowledgeRevision,
) error {
	if r.failOperation == "create_knowledge_revision" {
		return errors.New("injected knowledge revision failure")
	}
	r.knowledgeRevisions = append(r.knowledgeRevisions, *revision)
	return nil
}

func (r *fakeWikiProvenanceRepository) CreatePageRevision(
	_ context.Context,
	revision *types.WikiProvenancePageRevision,
) error {
	if r.failOperation == "create_page_revision" {
		return errors.New("injected page revision failure")
	}
	r.pageRevisions = append(r.pageRevisions, *revision)
	return nil
}

func (r *fakeWikiProvenanceRepository) CreateBlocks(
	_ context.Context,
	blocks []types.WikiPageBlock,
) error {
	if r.failOperation == "create_blocks" {
		return errors.New("injected block failure")
	}
	r.blocks = append(r.blocks, blocks...)
	return nil
}

func (r *fakeWikiProvenanceRepository) CreateBlockSources(
	_ context.Context,
	sources []types.WikiBlockSource,
) error {
	if r.failOperation == "create_block_sources" {
		return errors.New("injected block source failure")
	}
	r.sources = append(r.sources, sources...)
	return nil
}

func (r *fakeWikiProvenanceRepository) ReplacePageSources(
	_ context.Context,
	tenantID uint64,
	kbID, pageID string,
	sources []types.WikiPageSource,
) error {
	if r.failOperation == "replace_page_sources" {
		return errors.New("injected page source projection failure")
	}
	kept := r.pageSources[:0]
	for _, source := range r.pageSources {
		if source.TenantID != tenantID || source.KnowledgeBaseID != kbID || source.PageID != pageID {
			kept = append(kept, source)
		}
	}
	r.pageSources = append(kept, sources...)
	return nil
}

func (r *fakeWikiProvenanceRepository) PublishKnowledgeRevision(
	_ context.Context,
	tenantID uint64,
	kbID, knowledgeID, revisionID string,
	at time.Time,
) error {
	for i := range r.knowledgeRevisions {
		revision := &r.knowledgeRevisions[i]
		if revision.TenantID != tenantID || revision.KnowledgeBaseID != kbID ||
			revision.KnowledgeID != knowledgeID {
			continue
		}
		if revision.ID == revisionID {
			revision.Status = types.KnowledgeRevisionPublished
			revision.PublishedAt = &at
		} else if revision.Status == types.KnowledgeRevisionPublished {
			revision.Status = types.KnowledgeRevisionSuperseded
			revision.SupersededAt = &at
		}
	}
	return nil
}

func (r *fakeWikiProvenanceRepository) PublishPageRevision(
	_ context.Context,
	tenantID uint64,
	kbID, pageID, revisionID string,
	at time.Time,
) error {
	for i := range r.pageRevisions {
		revision := &r.pageRevisions[i]
		if revision.TenantID != tenantID || revision.KnowledgeBaseID != kbID || revision.PageID != pageID {
			continue
		}
		if revision.ID == revisionID {
			revision.Status = types.WikiPageRevisionPublished
			revision.PublishedAt = &at
		} else if revision.Status == types.WikiPageRevisionPublished {
			revision.Status = types.WikiPageRevisionSuperseded
			revision.SupersededAt = &at
		}
	}
	return nil
}

func (r *fakeWikiProvenanceRepository) UpdateCurrentPage(
	_ context.Context,
	tenantID uint64,
	kbID string,
	projection *types.WikiPage,
	revision *types.WikiProvenancePageRevision,
	_ time.Time,
) error {
	key := fakePageKey(tenantID, kbID, revision.PageID)
	page, ok := r.pages[key]
	if !ok {
		return types.ErrWikiPublishScopeNotFound
	}
	page.version = revision.RevisionNo
	page.content = revision.RenderedContent
	page.slug = projection.Slug
	r.pages[key] = page
	return nil
}

func basicWikiPublishRequest(key, content string) *types.WikiProvenancePublishRequest {
	return &types.WikiProvenancePublishRequest{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		PageID:          "page-1",
		IdempotencyKey:  key,
		PageProjection: types.WikiPage{
			ID:       "page-1",
			Slug:     "entity/page-1",
			PageType: types.WikiPageTypeEntity,
		},
		PageRevision: types.WikiProvenancePageRevision{
			Title:           "Page",
			RenderedContent: content,
		},
		Blocks: []types.WikiPageBlock{{
			BlockType: types.WikiBlockParagraph,
			SortOrder: 0,
			Content:   content,
		}},
	}
}

func setupWikiPublishService() (*wikiProvenancePublishService, *fakeWikiProvenanceRepository) {
	repo := newFakeWikiProvenanceRepository()
	repo.knowledgeBases[fakeKnowledgeBaseKey(1, "kb-1")] = struct{}{}
	repo.pages[fakePageKey(1, "kb-1", "page-1")] = fakeWikiCurrentPage{
		tenantID: 1,
		kbID:     "kb-1",
		slug:     "entity/page-1",
	}
	return &wikiProvenancePublishService{
		repo: repo,
		now:  func() time.Time { return time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC) },
	}, repo
}

func TestWikiProvenancePublishIsIdempotent(t *testing.T) {
	service, repo := setupWikiPublishService()
	ctx := context.Background()
	request := basicWikiPublishRequest("job-1", "first body")

	first, err := service.Publish(ctx, request)
	require.NoError(t, err)
	require.False(t, first.AlreadyPublished)
	second, err := service.Publish(ctx, request)
	require.NoError(t, err)
	require.True(t, second.AlreadyPublished)
	require.Equal(t, first.PageRevision.ID, second.PageRevision.ID)
	require.Len(t, repo.pageRevisions, 1)
	require.Len(t, repo.blocks, 1)

	conflicting := basicWikiPublishRequest("job-1", "different body")
	_, err = service.Publish(ctx, conflicting)
	require.ErrorIs(t, err, types.ErrWikiPublishIdempotencyConflict)
	require.Len(t, repo.pageRevisions, 1)
}

func TestWikiProvenancePublishAdvancesExistingPageVersion(t *testing.T) {
	service, repo := setupWikiPublishService()
	key := fakePageKey(1, "kb-1", "page-1")
	current := repo.pages[key]
	current.version = 7
	current.content = "manually edited body"
	repo.pages[key] = current

	result, err := service.Publish(
		context.Background(),
		basicWikiPublishRequest("job-after-manual-edit", "generated facts"),
	)
	require.NoError(t, err)
	require.Equal(t, 8, result.PageRevision.RevisionNo)
	require.Equal(t, 8, repo.pages[key].version)
}

func TestWikiProvenancePublishRollsBackEveryWrite(t *testing.T) {
	service, repo := setupWikiPublishService()
	repo.failOperation = "replace_page_sources"

	_, err := service.Publish(context.Background(), basicWikiPublishRequest("job-fails", "body"))
	require.ErrorContains(t, err, "injected page source projection failure")
	require.Empty(t, repo.pageRevisions)
	require.Empty(t, repo.blocks)
	require.Empty(t, repo.sources)
	require.Empty(t, repo.pageSources)
	current := repo.pages[fakePageKey(1, "kb-1", "page-1")]
	require.Zero(t, current.version)
	require.Empty(t, current.content)
}

func TestWikiProvenancePublishCreatesNewPageInTransaction(t *testing.T) {
	service, repo := setupWikiPublishService()
	delete(repo.pages, fakePageKey(1, "kb-1", "page-1"))

	result, err := service.Publish(context.Background(), basicWikiPublishRequest("new-page", "new body"))
	require.NoError(t, err)
	require.NotNil(t, result.PageRevision)
	require.Equal(t, 1, result.PageRevision.RevisionNo)

	current, ok := repo.pages[fakePageKey(1, "kb-1", "page-1")]
	require.True(t, ok)
	require.Equal(t, 1, current.version)
	require.Equal(t, "new body", current.content)
	require.Equal(t, "entity/page-1", current.slug)
}

func TestWikiProvenancePublishRollsBackNewPageShell(t *testing.T) {
	service, repo := setupWikiPublishService()
	delete(repo.pages, fakePageKey(1, "kb-1", "page-1"))
	repo.failOperation = "create_page_revision"

	_, err := service.Publish(context.Background(), basicWikiPublishRequest("new-page-fails", "body"))
	require.ErrorContains(t, err, "injected page revision failure")
	_, exists := repo.pages[fakePageKey(1, "kb-1", "page-1")]
	require.False(t, exists)
	require.Empty(t, repo.pageRevisions)
	require.Empty(t, repo.blocks)
}

func TestWikiProvenancePublishBuildsSourceProjection(t *testing.T) {
	service, repo := setupWikiPublishService()
	repo.knowledges[fakeKnowledgeKey(1, "kb-1", "knowledge-1")] = struct{}{}
	request := basicWikiPublishRequest("job-with-source", "supported fact")
	request.KnowledgeRevisions = []types.KnowledgeRevision{{
		ID:          "revision-alias",
		KnowledgeID: "knowledge-1",
		ContentHash: "source-content-hash",
	}}
	request.Blocks[0].ID = "block-alias"
	request.Sources = []types.WikiBlockSource{{
		BlockID:             "block-alias",
		KnowledgeID:         "knowledge-1",
		KnowledgeRevisionID: "revision-alias",
		SourceStart:         -1,
		SourceEnd:           -1,
		SourceRole:          types.WikiSourceSupporting,
		ValidationStatus:    types.WikiSourceValidationVerified,
		Confidence:          0.95,
	}}

	_, err := service.Publish(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, repo.knowledgeRevisions, 1)
	require.Equal(t, types.KnowledgeRevisionPublished, repo.knowledgeRevisions[0].Status)
	require.NotEqual(t, "revision-alias", repo.knowledgeRevisions[0].ID)
	require.Len(t, repo.sources, 1)
	require.Equal(t, repo.knowledgeRevisions[0].ID, repo.sources[0].KnowledgeRevisionID)
	require.NotEqual(t, "block-alias", repo.sources[0].BlockID)
	require.Len(t, repo.pageSources, 1)
	require.Equal(t, 1, repo.pageSources[0].SupportedBlockCount)
	require.Equal(t, types.WikiSourceMappingBlock, repo.pageSources[0].MappingGranularity)
	require.Equal(t, types.WikiSourceValidationVerified, repo.pageSources[0].ValidationStatus)
	require.Equal(t, repo.knowledgeRevisions[0].ID, *repo.pageSources[0].LastKnowledgeRevisionID)
}

func TestWikiProvenancePublishReusesKnowledgeRevisionByContentHash(t *testing.T) {
	service, repo := setupWikiPublishService()
	repo.knowledges[fakeKnowledgeKey(1, "kb-1", "knowledge-1")] = struct{}{}

	buildRequest := func(key, content string) *types.WikiProvenancePublishRequest {
		request := basicWikiPublishRequest(key, content)
		request.KnowledgeRevisions = []types.KnowledgeRevision{{
			ID:          "revision-alias",
			KnowledgeID: "knowledge-1",
			ContentHash: "unchanged-source-hash",
		}}
		request.Blocks[0].ID = "block-alias"
		request.Sources = []types.WikiBlockSource{{
			BlockID:             "block-alias",
			KnowledgeID:         "knowledge-1",
			KnowledgeRevisionID: "revision-alias",
			SourceStart:         -1,
			SourceEnd:           -1,
			SourceRole:          types.WikiSourceSupporting,
			ValidationStatus:    types.WikiSourceValidationVerified,
		}}
		return request
	}

	first, err := service.Publish(context.Background(), buildRequest("first-source-use", "body one"))
	require.NoError(t, err)
	second, err := service.Publish(context.Background(), buildRequest("second-source-use", "body two"))
	require.NoError(t, err)

	require.Len(t, repo.knowledgeRevisions, 1)
	require.Len(t, repo.pageRevisions, 2)
	require.Len(t, repo.sources, 2)
	reusedID := repo.knowledgeRevisions[0].ID
	require.Equal(t, reusedID, first.KnowledgeRevisions["knowledge-1"])
	require.Equal(t, reusedID, second.KnowledgeRevisions["knowledge-1"])
	require.Equal(t, reusedID, repo.sources[1].KnowledgeRevisionID)
}

func TestWikiProvenancePublishCreatesNewKnowledgeRevisionForNewParseAttempt(t *testing.T) {
	service, repo := setupWikiPublishService()
	repo.knowledges[fakeKnowledgeKey(1, "kb-1", "knowledge-1")] = struct{}{}

	buildRequest := func(key string, parseAttempt int) *types.WikiProvenancePublishRequest {
		request := basicWikiPublishRequest(key, key)
		request.KnowledgeRevisions = []types.KnowledgeRevision{{
			ID:           "revision-alias",
			KnowledgeID:  "knowledge-1",
			ContentHash:  "same-file-hash",
			ParseAttempt: parseAttempt,
		}}
		request.Blocks[0].ID = "block-alias"
		request.Sources = []types.WikiBlockSource{{
			BlockID:             "block-alias",
			KnowledgeID:         "knowledge-1",
			KnowledgeRevisionID: "revision-alias",
			SourceStart:         -1,
			SourceEnd:           -1,
			SourceRole:          types.WikiSourceSupporting,
			ValidationStatus:    types.WikiSourceValidationVerified,
		}}
		return request
	}

	_, err := service.Publish(context.Background(), buildRequest("attempt-1", 1))
	require.NoError(t, err)
	_, err = service.Publish(context.Background(), buildRequest("attempt-2", 2))
	require.NoError(t, err)
	require.Len(t, repo.knowledgeRevisions, 2)
	require.Equal(t, 1, repo.knowledgeRevisions[0].ParseAttempt)
	require.Equal(t, 2, repo.knowledgeRevisions[1].ParseAttempt)
}

func TestWikiProvenancePublishEnforcesTenantIsolation(t *testing.T) {
	service, repo := setupWikiPublishService()
	request := basicWikiPublishRequest("other-tenant", "body")
	request.TenantID = 2

	_, err := service.Publish(context.Background(), request)
	require.ErrorIs(t, err, types.ErrWikiPublishScopeNotFound)
	require.Empty(t, repo.pageRevisions)

	request = basicWikiPublishRequest("mixed-scope", "body")
	request.PageRevision.TenantID = 2
	_, err = service.Publish(context.Background(), request)
	require.ErrorContains(t, err, "outside the publish tenant")
	require.Empty(t, repo.pageRevisions)
}

func TestWikiProvenanceConcurrentPublishesAreSerialized(t *testing.T) {
	service, repo := setupWikiPublishService()
	const publishCount = 20
	results := make(chan *types.WikiProvenancePublishResult, publishCount)
	errorsCh := make(chan error, publishCount)
	var workers sync.WaitGroup

	for i := 0; i < publishCount; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			result, err := service.Publish(
				context.Background(),
				basicWikiPublishRequest(fmt.Sprintf("job-%02d", index), fmt.Sprintf("body-%02d", index)),
			)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- result
		}(i)
	}
	workers.Wait()
	close(results)
	close(errorsCh)

	for err := range errorsCh {
		require.NoError(t, err)
	}
	revisionNumbers := make([]int, 0, publishCount)
	for result := range results {
		revisionNumbers = append(revisionNumbers, result.PageRevision.RevisionNo)
	}
	sort.Ints(revisionNumbers)
	for i := 0; i < publishCount; i++ {
		require.Equal(t, i+1, revisionNumbers[i])
	}
	require.Len(t, repo.pageRevisions, publishCount)
	published := 0
	for _, revision := range repo.pageRevisions {
		if revision.Status == types.WikiPageRevisionPublished {
			published++
		}
	}
	require.Equal(t, 1, published)
	require.Equal(t, publishCount, repo.pages[fakePageKey(1, "kb-1", "page-1")].version)
}

var _ interfaces.WikiProvenanceRepository = (*fakeWikiProvenanceRepository)(nil)
