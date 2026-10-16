package service

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestWikiPageIndexFinalizeRecovery(t *testing.T) {
	for _, failModel := range []bool{false, true} {
		name := "storage"
		if failModel {
			name = "embedding"
		}
		t.Run(name, func(t *testing.T) {
			ctx, pages, _, engine := wikiReviewFixture(t)
			page := wikiIndexPage()
			page.Content = "Target Concept explains robust retrieval."
			original, err := pages.CreatePage(ctx, page)
			require.NoError(t, err)
			target := wikiIndexPage()
			target.Slug = "concept/target"
			target.Title = "Target Concept"
			_, err = pages.CreatePage(ctx, target)
			require.NoError(t, err)
			ingest, db := newWikiLookupRecoveryService(t, page.KnowledgeBaseID, nil)
			require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}))
			require.NoError(t, db.Create(&types.KnowledgeBase{ID: page.KnowledgeBaseID, TenantID: 7}).Error)
			pages.taskPendingRepo = ingest.pendingRepo
			ingest.wikiService = pages
			ingest.kbService.(*wikiLookupKBService).kb.TenantID = 7
			payload := WikiIngestPayload{TenantID: 7, KnowledgeBaseID: page.KnowledgeBaseID}
			ingest.enqueueFinalize(ctx, payload, []string{page.Slug, target.Slug},
				map[string]string{target.Slug: target.Title}, nil, nil)
			trigger, err := json.Marshal(payload)
			require.NoError(t, err)
			task := asynq.NewTask(types.TypeWikiFinalize, trigger)
			transient := errors.New("finalize indexing temporarily unavailable")
			if failModel {
				pages.pageIndex.models.(*wikiIndexModel).err = transient
			} else {
				engine.err = transient
			}
			require.NoError(t, ingest.ProcessWikiFinalize(ctx, task))
			stored, err := pages.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
			require.NoError(t, err)
			require.Contains(t, stored.Content, "[[concept/target|Target Concept]]")
			require.Equal(t, original.Version, stored.Version)
			pending, err := ingest.pendingRepo.PendingCount(
				ctx, wikiFinalizeTaskType, wikiTaskScope, page.KnowledgeBaseID,
			)
			require.NoError(t, err)
			require.Positive(t, pending, "finalize must retain durable indexing work after the text rewrite")
			queue := ingest.task.(*wikiGuardTaskQueue)
			require.Len(t, queue.tasks, 2, "durable index recovery must schedule a follow-up")
			retryTask := queue.tasks[1]
			// Recreate the worker/repository to ensure recovery has no in-memory retry dependency.
			restarted := &wikiIngestService{
				kbService: ingest.kbService, wikiService: pages, task: queue,
				pendingRepo: repository.NewTaskPendingOpsRepository(db),
			}
			require.ErrorContains(t, restarted.ProcessWikiFinalize(ctx, retryTask), transient.Error())
			pending, err = restarted.pendingRepo.PendingCount(
				ctx, wikiFinalizeTaskType, wikiTaskScope, page.KnowledgeBaseID,
			)
			require.NoError(t, err)
			require.Positive(t, pending)
			engine.err = nil
			pages.pageIndex.models.(*wikiIndexModel).err = nil
			embeddedBefore := len(pages.pageIndex.models.(*wikiIndexModel).embedder.texts)
			require.NoError(t, restarted.ProcessWikiFinalize(ctx, retryTask))
			recovered, err := pages.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
			require.NoError(t, err)
			require.Equal(t, stored.Content, recovered.Content, "recovery must not depend on another text rewrite")
			require.Equal(t, stored.Version, recovered.Version)
			chunks, err := pages.chunkRepo.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
			require.NoError(t, err)
			require.NotEmpty(t, chunks)
			require.Len(t, pages.pageIndex.models.(*wikiIndexModel).embedder.texts, embeddedBefore+len(chunks),
				"duplicate obligations must synchronize each page only once per pass")
			for _, chunk := range chunks {
				require.Equal(t, "ready", chunk.IndexStatus)
				require.Contains(t, engine.items, chunk.ID)
			}
			pending, err = restarted.pendingRepo.PendingCount(
				ctx, wikiFinalizeTaskType, wikiTaskScope, page.KnowledgeBaseID,
			)
			require.NoError(t, err)
			require.Zero(t, pending)
		})
	}
}
