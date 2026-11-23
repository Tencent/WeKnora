package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWikiPageCheckpointRollsBackPageAndRevisionOnProgressFailure(t *testing.T) {
	svc, db, _, _, _ := newWikiPageRetryBatch(t, nil)
	seedWikiLookupTask(t, svc, db, "kb-1", "k-1")
	var row types.TaskPendingOp
	require.NoError(t, db.Where("task_type = ?", wikiTaskType).First(&row).Error)
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "k-1", dbID: row.ID}
	updates := map[string][]SlugUpdate{"entity/alpha": {{Slug: "entity/alpha", KnowledgeID: "k-1", Type: "entity"}}}
	require.NoError(t, svc.checkpointWikiReduce(context.Background(), []WikiPendingOp{op}, updates,
		[]*docIngestResult{{KnowledgeID: "k-1"}}, nil, nil))
	wantErr := errors.New("simulated progress storage failure after page update")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:fail_checkpoint", func(tx *gorm.DB) {
		if tx.Statement.Table == "task_pending_ops" {
			tx.AddError(wantErr)
		}
	}))
	ctx := types.WithWikiPageCheckpoint(context.Background(), types.WikiPageCheckpoint{
		TenantID: 7, KnowledgeBaseID: "kb-1", Slug: "entity/alpha", PendingOpIDs: []int64{row.ID},
	})
	repo := apprepo.NewWikiPageRepository(db)
	page, err := repo.GetBySlug(ctx, "kb-1", "entity/alpha")
	require.NoError(t, err)
	originalVersion := page.Version
	revision := revisionFromPage(page)
	page.Content = "must roll back"
	require.ErrorIs(t, repo.UpdateWithRevision(ctx, page, revision), wantErr)
	saved, err := repo.GetBySlug(context.Background(), "kb-1", "entity/alpha")
	require.NoError(t, err)
	require.Equal(t, "ALPHA_BASE", saved.Content)
	require.Equal(t, originalVersion, saved.Version)
	var revisions int64
	require.NoError(t, db.Model(&types.WikiPageRevision{}).Where("page_id = ?", saved.ID).Count(&revisions).Error)
	require.Zero(t, revisions)
	require.NoError(t, db.First(&row, row.ID).Error)
	var restored WikiPendingOp
	require.NoError(t, json.Unmarshal(row.Payload, &restored))
	require.Len(t, restored.Resume.Updates, 1)
	require.Empty(t, restored.Resume.Applied)
}

func TestWikiPageCheckpointCreateAndMetaRollback(t *testing.T) {
	for _, create := range []bool{true, false} {
		t.Run(map[bool]string{true: "create", false: "meta"}[create], func(t *testing.T) {
			svc, db, _, _, _ := newWikiPageRetryBatch(t, nil)
			seedWikiLookupTask(t, svc, db, "kb-1", "k-1")
			var row types.TaskPendingOp
			require.NoError(t, db.Where("task_type = ?", wikiTaskType).First(&row).Error)
			slug := "entity/alpha"
			if create {
				slug = "entity/new"
			}
			updates := map[string][]SlugUpdate{slug: {{Slug: slug, KnowledgeID: "k-1", Type: "entity"}}}
			require.NoError(t, svc.checkpointWikiReduce(context.Background(), []WikiPendingOp{{
				Op: WikiOpIngest, KnowledgeID: "k-1", dbID: row.ID,
			}}, updates, []*docIngestResult{{KnowledgeID: "k-1"}}, nil, nil))
			wantErr := errors.New("progress unavailable")
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:fail_checkpoint", func(tx *gorm.DB) {
				if tx.Statement.Table == "task_pending_ops" {
					tx.AddError(wantErr)
				}
			}))
			ctx := types.WithWikiPageCheckpoint(context.Background(), types.WikiPageCheckpoint{
				TenantID: 7, KnowledgeBaseID: "kb-1", Slug: slug, PendingOpIDs: []int64{row.ID},
			})
			repo := apprepo.NewWikiPageRepository(db)
			if create {
				require.ErrorIs(t, repo.Create(ctx, &types.WikiPage{
					ID: "new", TenantID: 7, KnowledgeBaseID: "kb-1", Slug: slug, Content: "new body",
				}), wantErr)
				_, err := repo.GetBySlug(context.Background(), "kb-1", slug)
				require.ErrorIs(t, err, apprepo.ErrWikiPageNotFound)
			} else {
				page, err := repo.GetBySlug(ctx, "kb-1", slug)
				require.NoError(t, err)
				page.SourceRefs = types.StringArray{"k-1"}
				require.ErrorIs(t, repo.UpdateMeta(ctx, page), wantErr)
				saved, err := repo.GetBySlug(context.Background(), "kb-1", slug)
				require.NoError(t, err)
				require.Empty(t, saved.SourceRefs)
			}
		})
	}
}

func TestWikiReduceCheckpointRetractResumesOnlyFailedPage(t *testing.T) {
	svc, db, store, model, chunks := newWikiPageRetryBatch(t, nil)
	task := seedWikiLookupTask(t, svc, db, "kb-1", "k-1")
	require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
	require.NoError(t, enqueueWikiRetract(context.Background(), svc.task, svc.pendingRepo, WikiRetractPayload{
		TenantID: 7, KnowledgeBaseID: "kb-1", KnowledgeID: "k-1", DocTitle: "source", DocSummary: "summary",
	}))
	store.beforeWrite = func(_ context.Context, page *types.WikiPage, _ int) error {
		if page.Slug == "entity/alpha" {
			return apprepo.ErrWikiPageConflict
		}
		return nil
	}
	require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
	betaCalls := model.count("wiki_page_modify:entity/beta")
	betaWrites := store.count("entity/beta")
	store.beforeWrite = nil
	require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
	require.Equal(t, betaCalls, model.count("wiki_page_modify:entity/beta"))
	require.Equal(t, betaWrites, store.count("entity/beta"))
	require.EqualValues(t, 1, chunks.reads.Load())
	count, err := svc.pendingRepo.PendingCount(context.Background(), wikiTaskType, wikiTaskScope, "kb-1")
	require.NoError(t, err)
	require.Zero(t, count)
}

type wikiCrashAfterCommitStore struct {
	interfaces.WikiPageService
	crashed bool
}

func (s *wikiCrashAfterCommitStore) UpdatePage(ctx context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	result, err := s.WikiPageService.UpdatePage(ctx, page)
	if err == nil && page.Slug == "entity/beta" && !s.crashed {
		s.crashed = true
		panic("simulated worker crash after page transaction committed")
	}
	return result, err
}

func TestWikiPageCheckpointRecoversAfterCommittedPageCrash(t *testing.T) {
	svc, db, store, model, chunks := newWikiPageRetryBatch(t, nil)
	task := seedWikiLookupTask(t, svc, db, "kb-1", "k-1")
	svc.wikiService = &wikiCrashAfterCommitStore{WikiPageService: store}
	require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
	var row types.TaskPendingOp
	require.NoError(t, db.Where("task_type = ?", wikiTaskType).First(&row).Error)
	var op WikiPendingOp
	require.NoError(t, json.Unmarshal(row.Payload, &op))
	require.Contains(t, op.Resume.Applied, "entity/beta")
	for _, update := range op.Resume.Updates {
		require.NotEqual(t, "entity/beta", update.Slug)
	}
	// Recovery uses only durable queue state, even though the worker never
	// received the successful page-write result.
	require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
	require.EqualValues(t, 1, chunks.reads.Load())
	require.Equal(t, 1, model.count("wiki_page_modify:entity/beta"))
	require.Equal(t, 1, store.count("entity/beta"))
	count, err := svc.pendingRepo.PendingCount(context.Background(), wikiTaskType, wikiTaskScope, "kb-1")
	require.NoError(t, err)
	require.Zero(t, count)
}

type wikiFailingCheckpointRepo struct {
	interfaces.TaskPendingOpsRepository
	err error
}

func (r wikiFailingCheckpointRepo) CheckpointPayload(context.Context, int64, []byte) error {
	return r.err
}

func TestWikiReduceCheckpointFailureDoesNotWritePagesOrConsumeOp(t *testing.T) {
	svc, db, store, _, _ := newWikiPageRetryBatch(t, nil)
	task := seedWikiLookupTask(t, svc, db, "kb-1", "k-1")
	wantErr := errors.New("checkpoint unavailable")
	svc.pendingRepo = wikiFailingCheckpointRepo{TaskPendingOpsRepository: svc.pendingRepo, err: wantErr}
	require.ErrorIs(t, svc.ProcessWikiIngest(context.Background(), task), wantErr)
	require.Zero(t, store.count("entity/alpha"))
	require.Zero(t, store.count("entity/beta"))
	count, err := svc.pendingRepo.PendingCount(context.Background(), wikiTaskType, wikiTaskScope, "kb-1")
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestWikiReduceCheckpointResumesWithoutMapOrSuccessfulPage(t *testing.T) {
	svc, db, store, model, chunks := newWikiPageRetryBatch(t, nil)
	task := seedWikiLookupTask(t, svc, db, "kb-1", "k-1")
	store.beforeWrite = func(_ context.Context, page *types.WikiPage, _ int) error {
		if page.Slug == "entity/alpha" {
			return apprepo.ErrWikiPageConflict
		}
		return nil
	}
	require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
	var pending types.TaskPendingOp
	require.NoError(t, db.Where("task_type = ?", wikiTaskType).First(&pending).Error)
	var op WikiPendingOp
	require.NoError(t, json.Unmarshal(pending.Payload, &op))
	require.NotNil(t, op.Resume)
	require.NotEmpty(t, op.Resume.Updates)
	for _, update := range op.Resume.Updates {
		require.Equal(t, "entity/alpha", update.Slug)
	}

	// A new handler invocation reads progress from the database, not memory.
	store.beforeWrite = nil
	require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
	require.EqualValues(t, 1, chunks.reads.Load())
	for _, purpose := range []string{"wiki_candidate_slug", "wiki_summary", "wiki_chunk_citation"} {
		require.Equal(t, 1, model.count(purpose), purpose)
	}
	require.Equal(t, 1, model.count("wiki_page_modify:entity/beta"))
	require.Equal(t, 1, store.count("entity/beta"))
	count, err := svc.pendingRepo.PendingCount(context.Background(), wikiTaskType, wikiTaskScope, "kb-1")
	require.NoError(t, err)
	require.Zero(t, count)

	// Reparse queues a new operation without the previous row's checkpoint.
	fresh, err := newWikiIngestPendingOp(context.Background(), 7, "kb-1", "k-1")
	require.NoError(t, err)
	require.NoError(t, svc.pendingRepo.Enqueue(context.Background(), fresh))
	require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
	require.EqualValues(t, 2, chunks.reads.Load())
	require.Equal(t, 2, model.count("wiki_page_modify:entity/beta"))
}

func TestWikiBusyOpDoesNotConsumeFailureBudget(t *testing.T) {
	svc, db := newWikiLookupRecoveryService(t, "kb-1", nil)
	seedWikiLookupTask(t, svc, db, "kb-1", "k-1")
	var row types.TaskPendingOp
	require.NoError(t, db.First(&row).Error)
	for i := 0; i < wikiMaxFailRetries+2; i++ {
		require.NoError(t, svc.requeueFailedOps(context.Background(), WikiIngestPayload{}, []WikiPendingOp{{
			KnowledgeID: "k-1", Op: WikiOpIngest, dbID: row.ID, deferred: true,
		}}))
	}
	require.NoError(t, db.First(&row, row.ID).Error)
	require.Zero(t, row.FailCount)
}

func TestWikiReduceCheckpointKeepsPendingRetractions(t *testing.T) {
	for _, operation := range []string{WikiOpIngest, WikiOpRetract} {
		t.Run(operation, func(t *testing.T) {
			svc, db := newWikiLookupRecoveryService(t, "kb-1", nil)
			seedWikiLookupTask(t, svc, db, "kb-1", "k-1")
			var row types.TaskPendingOp
			require.NoError(t, db.First(&row).Error)
			op := WikiPendingOp{Op: operation, KnowledgeID: "k-1", Attempt: 3, dbID: row.ID}
			updates := map[string][]SlugUpdate{
				"entity/alpha": {{Slug: "entity/alpha", Type: "retract", KnowledgeID: "k-1"}},
				"entity/beta":  {{Slug: "entity/beta", Type: "retract", KnowledgeID: "k-1"}},
			}
			var results []*docIngestResult
			if operation == WikiOpIngest {
				updates["entity/alpha"] = append(updates["entity/alpha"], SlugUpdate{
					Slug: "entity/alpha", Type: types.WikiPageTypeEntity, KnowledgeID: "k-1",
				})
				results = []*docIngestResult{{KnowledgeID: "k-1", Summary: "cached summary"}}
			}
			require.NoError(t, svc.checkpointWikiReduce(context.Background(), []WikiPendingOp{op},
				updates, results, map[string]bool{"entity/beta": true}, []string{"folder-1"}))
			require.NoError(t, db.First(&row, row.ID).Error)
			var restored WikiPendingOp
			require.NoError(t, json.Unmarshal(row.Payload, &restored))
			require.Equal(t, 3, restored.Attempt)
			require.NotNil(t, restored.Resume)
			require.Equal(t, updates["entity/alpha"], restored.Resume.Updates)
			require.Equal(t, []string{"entity/beta"}, restored.Resume.Applied)
			require.Equal(t, []string{"folder-1"}, restored.Resume.FolderIDs)
		})
	}
}
