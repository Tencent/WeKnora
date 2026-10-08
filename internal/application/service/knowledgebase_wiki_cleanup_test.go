package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wikiKBDeleteCall struct {
	tenantID uint64
	kbID     string
}

// recordingWikiRepo records calls to the four KB-scoped delete methods.
// Embedding interfaces.WikiPageRepository satisfies the interface; only the
// methods under test are overridden, the rest panic if accidentally invoked.
type recordingWikiRepo struct {
	interfaces.WikiPageRepository

	pagesErr     error
	foldersErr   error
	revisionsErr error
	issuesErr    error

	pagesCalls     []wikiKBDeleteCall
	foldersCalls   []wikiKBDeleteCall
	revisionsCalls []wikiKBDeleteCall
	issuesCalls    []wikiKBDeleteCall
}

func (r *recordingWikiRepo) DeleteByKnowledgeBaseID(_ context.Context, tenantID uint64, kbID string) error {
	r.pagesCalls = append(r.pagesCalls, wikiKBDeleteCall{tenantID, kbID})
	return r.pagesErr
}

func (r *recordingWikiRepo) DeleteFoldersByKnowledgeBaseID(_ context.Context, tenantID uint64, kbID string) error {
	r.foldersCalls = append(r.foldersCalls, wikiKBDeleteCall{tenantID, kbID})
	return r.foldersErr
}

func (r *recordingWikiRepo) DeleteRevisionsByKnowledgeBaseID(_ context.Context, tenantID uint64, kbID string) error {
	r.revisionsCalls = append(r.revisionsCalls, wikiKBDeleteCall{tenantID, kbID})
	return r.revisionsErr
}

func (r *recordingWikiRepo) DeleteIssuesByKnowledgeBaseID(_ context.Context, tenantID uint64, kbID string) error {
	r.issuesCalls = append(r.issuesCalls, wikiKBDeleteCall{tenantID, kbID})
	return r.issuesErr
}

func assertWikiCleanup(t *testing.T, wikiRepo *recordingWikiRepo, tenantID uint64, kbID string) {
	t.Helper()
	want := []wikiKBDeleteCall{{tenantID, kbID}}
	assert.Equal(t, want, wikiRepo.pagesCalls)
	assert.Equal(t, want, wikiRepo.foldersCalls)
	assert.Equal(t, want, wikiRepo.revisionsCalls)
	assert.Equal(t, want, wikiRepo.issuesCalls)
}

// kbDeletePayload builds the asynq task payload used by ProcessKBDelete.
func kbDeletePayload(t *testing.T, kbID string, tenantID uint64) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(types.KBDeletePayload{TenantID: tenantID, KnowledgeBaseID: kbID})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeKBDelete, payload)
}

// TestProcessKBDeleteCleansWikiData verifies the four wiki tables are cleaned
// up when ProcessKBDelete runs against a KB with documents.
func TestProcessKBDeleteCleansWikiData(t *testing.T) {
	const kbID = "kb-with-docs"
	const tenantID uint64 = 1
	wikiRepo := &recordingWikiRepo{}
	svc := &knowledgeBaseService{
		kgRepo: populatedKBKnowledgeRepo{items: []*types.Knowledge{
			{ID: "k1", KnowledgeBaseID: kbID, EmbeddingModelID: "m1"},
		}},
		chunkRepo:     kbCleanupChunkRepo{},
		modelService:  kbCleanupModelService{},
		taskInspector: &recordingKBTaskInspector{},
		wikiRepo:      wikiRepo,
	}

	err := svc.ProcessKBDelete(context.Background(), kbDeletePayload(t, kbID, tenantID))

	require.NoError(t, err)
	assertWikiCleanup(t, wikiRepo, tenantID, kbID)
}

// TestProcessKBDeleteWikiCleanupFailureRetries verifies a wiki cleanup
// failure fails the task so asynq retries, while still attempting every table.
func TestProcessKBDeleteWikiCleanupFailureRetries(t *testing.T) {
	const kbID = "kb-wiki-fail"
	const tenantID uint64 = 1
	wikiRepo := &recordingWikiRepo{
		pagesErr:     errors.New("pages boom"),
		foldersErr:   errors.New("folders boom"),
		revisionsErr: errors.New("revisions boom"),
		issuesErr:    errors.New("issues boom"),
	}
	svc := &knowledgeBaseService{
		kgRepo: populatedKBKnowledgeRepo{items: []*types.Knowledge{
			{ID: "k1", KnowledgeBaseID: kbID, EmbeddingModelID: "m1"},
		}},
		chunkRepo:     kbCleanupChunkRepo{},
		modelService:  kbCleanupModelService{},
		taskInspector: &recordingKBTaskInspector{},
		wikiRepo:      wikiRepo,
	}

	err := svc.ProcessKBDelete(context.Background(), kbDeletePayload(t, kbID, tenantID))

	require.Error(t, err)
	assert.ErrorContains(t, err, "pages boom")
	assert.ErrorContains(t, err, "folders boom")
	assert.ErrorContains(t, err, "revisions boom")
	assert.ErrorContains(t, err, "issues boom")
	assertWikiCleanup(t, wikiRepo, tenantID, kbID)
}

// TestProcessKBDeleteWikiCleanupNilRepoSafe guards backward compatibility:
// tests (and any other callers) that construct knowledgeBaseService without
// wikiRepo must not panic on the cleanup path.
func TestProcessKBDeleteWikiCleanupNilRepoSafe(t *testing.T) {
	svc := &knowledgeBaseService{
		kgRepo: populatedKBKnowledgeRepo{items: []*types.Knowledge{
			{ID: "k1", KnowledgeBaseID: "kb-nil", EmbeddingModelID: "m1"},
		}},
		chunkRepo:     kbCleanupChunkRepo{},
		modelService:  kbCleanupModelService{},
		taskInspector: &recordingKBTaskInspector{},
		// wikiRepo intentionally left nil
	}

	err := svc.ProcessKBDelete(context.Background(), kbDeletePayload(t, "kb-nil", 1))

	require.NoError(t, err)
}

// TestProcessKBDeleteEmptyKBStillCleansWiki verifies wiki cleanup runs even
// when the KB has no knowledge entries. A KB can have wiki data without any
// documents (pure wiki KB, or documents already deleted individually).
func TestProcessKBDeleteEmptyKBStillCleansWiki(t *testing.T) {
	const kbID = "kb-no-docs"
	const tenantID uint64 = 1
	wikiRepo := &recordingWikiRepo{}
	svc := &knowledgeBaseService{
		kgRepo:          emptyKBKnowledgeRepo{},
		taskInspector:   &recordingKBTaskInspector{},
		taskPendingRepo: &recordingKBPendingRepo{},
		wikiRepo:        wikiRepo,
	}

	err := svc.ProcessKBDelete(context.Background(), kbDeletePayload(t, kbID, tenantID))

	require.NoError(t, err)
	assertWikiCleanup(t, wikiRepo, tenantID, kbID)
}

// TestProcessKBDeleteWikiCleanupIdempotent verifies calling the cleanup path
// twice doesn't error out. Each attempt still issues the four scoped deletes.
func TestProcessKBDeleteWikiCleanupIdempotent(t *testing.T) {
	const kbID = "kb-idempotent"
	const tenantID uint64 = 1
	wikiRepo := &recordingWikiRepo{}
	svc := &knowledgeBaseService{
		kgRepo:          emptyKBKnowledgeRepo{},
		taskInspector:   &recordingKBTaskInspector{},
		taskPendingRepo: &recordingKBPendingRepo{},
		wikiRepo:        wikiRepo,
	}
	task := kbDeletePayload(t, kbID, tenantID)

	require.NoError(t, svc.ProcessKBDelete(context.Background(), task))
	require.NoError(t, svc.ProcessKBDelete(context.Background(), task))

	want := []wikiKBDeleteCall{{tenantID, kbID}, {tenantID, kbID}}
	assert.Equal(t, want, wikiRepo.pagesCalls)
	assert.Equal(t, want, wikiRepo.foldersCalls)
	assert.Equal(t, want, wikiRepo.revisionsCalls)
	assert.Equal(t, want, wikiRepo.issuesCalls)
}

// TestProcessKBDeleteCleansWikiWhenVectorStoreForbidden verifies wiki cleanup
// still runs when engine resolution returns SkipRetry. Wiki rows do not
// depend on the vector store and must not be left behind.
func TestProcessKBDeleteCleansWikiWhenVectorStoreForbidden(t *testing.T) {
	const kbID = "kb-skip"
	const tenantID uint64 = 1
	const storeID = "00000000-0000-0000-0000-0000000000ff"
	storeIDPtr := storeID
	wikiRepo := &recordingWikiRepo{}
	repo := &kbDeleteTrackingKnowledgeRepo{populatedKBKnowledgeRepo: populatedKBKnowledgeRepo{items: []*types.Knowledge{
		{ID: "k1", KnowledgeBaseID: kbID, EmbeddingModelID: "m1"},
	}}}
	svc := &knowledgeBaseService{
		kgRepo:        repo,
		chunkRepo:     kbCleanupChunkRepo{},
		modelService:  kbCleanupModelService{},
		taskInspector: &recordingKBTaskInspector{},
		ownership:     &kbDeleteOwnership{owned: map[string]uint64{}},
		wikiRepo:      wikiRepo,
	}

	payload, err := json.Marshal(types.KBDeletePayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		VectorStoreID:   &storeIDPtr,
	})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payload))

	require.ErrorIs(t, err, asynq.SkipRetry)
	assert.Equal(t, 0, repo.deleteCalls)
	assertWikiCleanup(t, wikiRepo, tenantID, kbID)
}

func TestProcessKBDeleteCleansWikiWhenVectorStoreUnavailable(t *testing.T) {
	const kbID = "kb-unavailable"
	const tenantID uint64 = 1
	const storeID = "00000000-0000-0000-0000-0000000000aa"
	storeIDPtr := storeID
	wikiRepo := &recordingWikiRepo{}
	svc := &knowledgeBaseService{
		kgRepo: populatedKBKnowledgeRepo{items: []*types.Knowledge{
			{ID: "k1", KnowledgeBaseID: kbID, EmbeddingModelID: "m1"},
		}},
		chunkRepo:      kbCleanupChunkRepo{},
		modelService:   kbCleanupModelService{},
		taskInspector:  &recordingKBTaskInspector{},
		retrieveEngine: kbDeleteDeferredRegistry{err: retriever.ErrVectorStoreUnavailable},
		ownership:      &kbDeleteOwnership{owned: map[string]uint64{storeID: tenantID}},
		wikiRepo:       wikiRepo,
	}

	payload, err := json.Marshal(types.KBDeletePayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		VectorStoreID:   &storeIDPtr,
	})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payload))

	require.ErrorIs(t, err, retriever.ErrVectorStoreUnavailable)
	assertWikiCleanup(t, wikiRepo, tenantID, kbID)
}
