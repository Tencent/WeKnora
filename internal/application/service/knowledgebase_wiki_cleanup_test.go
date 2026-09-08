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

// recordingWikiRepo records calls to the four KB-scoped delete methods.
// Embedding interfaces.WikiPageRepository satisfies the interface; only the
// methods under test are overridden, the rest panic if accidentally invoked.
type recordingWikiRepo struct {
	interfaces.WikiPageRepository

	pagesErr     error
	foldersErr   error
	revisionsErr error
	issuesErr    error

	pagesCalls     []string
	foldersCalls   []string
	revisionsCalls []string
	issuesCalls    []string
}

func (r *recordingWikiRepo) DeleteByKnowledgeBaseID(_ context.Context, kbID string) error {
	r.pagesCalls = append(r.pagesCalls, kbID)
	return r.pagesErr
}

func (r *recordingWikiRepo) DeleteFoldersByKnowledgeBaseID(_ context.Context, kbID string) error {
	r.foldersCalls = append(r.foldersCalls, kbID)
	return r.foldersErr
}

func (r *recordingWikiRepo) DeleteRevisionsByKnowledgeBaseID(_ context.Context, kbID string) error {
	r.revisionsCalls = append(r.revisionsCalls, kbID)
	return r.revisionsErr
}

func (r *recordingWikiRepo) DeleteIssuesByKnowledgeBaseID(_ context.Context, kbID string) error {
	r.issuesCalls = append(r.issuesCalls, kbID)
	return r.issuesErr
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

	err := svc.ProcessKBDelete(context.Background(), kbDeletePayload(t, kbID, 1))

	require.NoError(t, err)
	assert.Equal(t, []string{kbID}, wikiRepo.pagesCalls)
	assert.Equal(t, []string{kbID}, wikiRepo.foldersCalls)
	assert.Equal(t, []string{kbID}, wikiRepo.revisionsCalls)
	assert.Equal(t, []string{kbID}, wikiRepo.issuesCalls)
}

// TestProcessKBDeleteWikiCleanupFailureDoesNotBlock verifies a wiki cleanup
// failure is logged but does not fail the whole KB delete task. The KB is
// already gone, so leftover rows are unreachable, not worth retrying.
func TestProcessKBDeleteWikiCleanupFailureDoesNotBlock(t *testing.T) {
	const kbID = "kb-wiki-fail"
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

	err := svc.ProcessKBDelete(context.Background(), kbDeletePayload(t, kbID, 1))

	require.NoError(t, err)
	// Every cleanup path was still attempted despite earlier failures.
	assert.Len(t, wikiRepo.pagesCalls, 1)
	assert.Len(t, wikiRepo.foldersCalls, 1)
	assert.Len(t, wikiRepo.revisionsCalls, 1)
	assert.Len(t, wikiRepo.issuesCalls, 1)
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
// when the KB has no knowledge entries. The cleanup lives outside the
// "if len(knowledgeList) > 0" block because a KB can have wiki data without
// any documents (pure wiki KB, or documents already deleted individually).
func TestProcessKBDeleteEmptyKBStillCleansWiki(t *testing.T) {
	const kbID = "kb-no-docs"
	wikiRepo := &recordingWikiRepo{}
	svc := &knowledgeBaseService{
		kgRepo:          emptyKBKnowledgeRepo{},
		taskInspector:   &recordingKBTaskInspector{},
		taskPendingRepo: &recordingKBPendingRepo{},
		wikiRepo:        wikiRepo,
	}

	err := svc.ProcessKBDelete(context.Background(), kbDeletePayload(t, kbID, 1))

	require.NoError(t, err)
	assert.Equal(t, []string{kbID}, wikiRepo.pagesCalls)
	assert.Equal(t, []string{kbID}, wikiRepo.foldersCalls)
	assert.Equal(t, []string{kbID}, wikiRepo.revisionsCalls)
	assert.Equal(t, []string{kbID}, wikiRepo.issuesCalls)
}

// TestProcessKBDeleteWikiCleanupIdempotent verifies calling the cleanup path
// twice doesn't accumulate state or error out. Soft-deletes re-stamp
// deleted_at; hard-deletes affect 0 rows on the second pass.
func TestProcessKBDeleteWikiCleanupIdempotent(t *testing.T) {
	const kbID = "kb-idempotent"
	wikiRepo := &recordingWikiRepo{}
	svc := &knowledgeBaseService{
		kgRepo:          emptyKBKnowledgeRepo{},
		taskInspector:   &recordingKBTaskInspector{},
		taskPendingRepo: &recordingKBPendingRepo{},
		wikiRepo:        wikiRepo,
	}
	task := kbDeletePayload(t, kbID, 1)

	require.NoError(t, svc.ProcessKBDelete(context.Background(), task))
	require.NoError(t, svc.ProcessKBDelete(context.Background(), task))

	require.Len(t, wikiRepo.pagesCalls, 2)
	require.Len(t, wikiRepo.foldersCalls, 2)
	require.Len(t, wikiRepo.revisionsCalls, 2)
	require.Len(t, wikiRepo.issuesCalls, 2)
	// All calls target the same KB.
	assert.Equal(t, []string{kbID, kbID}, wikiRepo.pagesCalls)
	assert.Equal(t, []string{kbID, kbID}, wikiRepo.foldersCalls)
	assert.Equal(t, []string{kbID, kbID}, wikiRepo.revisionsCalls)
	assert.Equal(t, []string{kbID, kbID}, wikiRepo.issuesCalls)
}
