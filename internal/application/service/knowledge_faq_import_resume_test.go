package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// quotaFailingChunkService fails CreateChunks from the nth call on, simulating
// an embedding/quota failure part-way through an import. Calls before that
// delegate to the real chunk service.
type quotaFailingChunkService struct {
	interfaces.ChunkService
	delegate  interfaces.ChunkService
	failFrom  int
	callCount int
}

func (s *quotaFailingChunkService) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	s.callCount++
	if s.callCount >= s.failFrom {
		return errors.New("embedding quota exceeded")
	}
	return s.delegate.CreateChunks(ctx, chunks)
}

// resumeRetrieveEngine is parentChildRetrieveEngine plus the vector-delete
// call the merge path makes when it re-indexes merged chunks.
type resumeRetrieveEngine struct {
	*parentChildRetrieveEngine
	deleted [][]string
}

func (e *resumeRetrieveEngine) DeleteByChunkIDList(_ context.Context, indexIDList []string, _ int, _ string) error {
	e.deleted = append(e.deleted, indexIDList)
	return nil
}

func faqImport5957Entries() []types.FAQEntryPayload {
	entries := make([]types.FAQEntryPayload, 0, 52)
	for i := 0; i < 51; i++ {
		entries = append(entries, types.FAQEntryPayload{
			StandardQuestion: fmt.Sprintf("new question %02d", i),
			Answers:          []string{fmt.Sprintf("answer %02d", i)},
			TagID:            11,
		})
	}
	// Row 52 (index 51) updates the one pre-existing FAQ entry.
	entries = append(entries, types.FAQEntryPayload{
		StandardQuestion: "existing question",
		Answers:          []string{"updated answer"},
		TagID:            11,
	})
	return entries
}

func faqImport5957Range(n int) []int {
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	return indices
}

// TestRemainingFAQEntryIndices is the unit-level regression test for #5957:
// the processed counter must never decide which input rows a retry redoes.
func TestRemainingFAQEntryIndices(t *testing.T) {
	valid := faqImport5957Range(52)
	// 50 creates (rows 0-49) plus the out-of-order merge of row 51 done.
	require.Equal(t, []int{50}, remainingFAQEntryIndices(valid, append(faqImport5957Range(50), 51)))
	require.Equal(t, valid, remainingFAQEntryIndices(valid, nil))
	require.Empty(t, remainingFAQEntryIndices(valid, valid))
	// Order is input order, and stale rows outside the input are ignored.
	require.Equal(t, []int{1, 3}, remainingFAQEntryIndices([]int{0, 1, 2, 3}, []int{0, 2, 99}))
	require.Equal(t, []int{0, 2}, intersectFAQEntryIndices([]int{0, 1, 2}, []int{2, 0, 99}))
	require.Equal(t, []int{0, 2, 1}, unionFAQEntryIndices([]int{0, 2}, []int{2, 1}))
}

// TestLegacyResumeCompletedIndices covers checkpoints written before
// CompletedEntryIndices existed: only the processed counter survives.
func TestLegacyResumeCompletedIndices(t *testing.T) {
	valid := faqImport5957Range(52)
	// #5957, old checkpoint: processed=51 counts the merge of row 51, so
	// the completed creates are rows 0-49 and row 50 must be retried.
	require.Equal(t, append(faqImport5957Range(50), 51),
		legacyResumeCompletedIndices(valid, []int{51}, 51, 0))
	require.Equal(t, []int{50}, remainingFAQEntryIndices(valid,
		legacyResumeCompletedIndices(valid, []int{51}, 51, 0)))
	// No merge involved: the counter is positional and stays valid.
	require.Equal(t, []int{0, 1, 2}, legacyResumeCompletedIndices(valid, nil, 3, 0))
	// Zero counter, or a counter fully consumed by validation failures.
	require.Empty(t, legacyResumeCompletedIndices(valid, []int{51}, 0, 0))
	require.Empty(t, legacyResumeCompletedIndices(valid, nil, 2, 2))
	// Merge indices outside the valid input are ignored; the counter then
	// counts completed creates in input order.
	require.Equal(t, []int{0, 1}, legacyResumeCompletedIndices([]int{0, 1}, []int{99}, 2, 0))
}

// TestFAQImportResumeDoesNotSkipEntryAfterOutOfOrderMerge reproduces #5957:
// 1 pre-existing FAQ + 51 new questions + 1 update row (52 rows total).
// The update row is merged before any create runs, so the processed counter
// reaches 51 after only 50 creates; the next create then fails (quota).
// A retry must still import the one remaining new question instead of
// reporting completed with it silently missing.
func TestFAQImportResumeDoesNotSkipEntryAfterOutOfOrderMerge(t *testing.T) {
	for _, seedIdentity := range []bool{false, true} {
		t.Run(fmt.Sprintf("identity_checkpoint=%t", seedIdentity), func(t *testing.T) {
			f := newFAQWriteFixture(t)
			sqlDB, err := f.db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

			f.svc.redisClient = redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
			t.Cleanup(func() { _ = f.svc.redisClient.Close() })
			f.svc.tenantRepo = &faqImportTenantLookup{}
			f.svc.modelService = parentChildModelService{embedder: parentChildEmbedder{}}
			engine := &resumeRetrieveEngine{parentChildRetrieveEngine: &parentChildRetrieveEngine{}}
			f.svc.retrieveEngine = &fakeFanoutRegistry{
				byStore: map[string]interfaces.RetrieveEngineService{*f.kb.VectorStoreID: engine},
			}
			realChunks := &chunkService{
				chunkRepository: f.chunks, knowledgeRepo: f.svc.repo,
				kbRepository: &imageReaderKBRepo{kb: f.kb},
			}
			failingChunks := &quotaFailingChunkService{delegate: realChunks, failFrom: 2}
			f.svc.chunkService = failingChunks

			// The one pre-existing FAQ entry the last row updates.
			var existing types.Chunk
			require.NoError(t, f.db.Where("id = ?", "one").First(&existing).Error)
			existingMeta := &types.FAQChunkMetadata{
				StandardQuestion: "existing question",
				Answers:          []string{"old answer"},
			}
			require.NoError(t, existing.SetFAQMetadata(existingMeta))
			existing.ContentHash = types.CalculateFAQContentHash(existingMeta)
			existing.Content = buildFAQChunkContent(existingMeta, types.FAQIndexModeQuestionOnly)
			existing.Status = int(types.ChunkStatusIndexed)
			require.NoError(t, f.db.Save(&existing).Error)

			entries := faqImport5957Entries()
			taskID := "resume-5957"

			// First attempt: the merge of row 52 runs first, 50 creates land,
			// then the next create fails on quota.
			setupProgress := &types.FAQImportProgress{TaskID: taskID, KBID: f.kb.ID, KnowledgeID: "faq"}
			err = f.svc.executeFAQImport(f.ctx, taskID, f.kb.ID,
				&types.FAQBatchUpsertPayload{Mode: types.FAQBatchModeAppend, Entries: entries},
				f.kb.TenantID, 0, setupProgress, faqImport5957Range(len(entries)))
			require.ErrorContains(t, err, "embedding quota exceeded")

			var storedAfterSetup []types.Chunk
			require.NoError(t, f.db.Where("knowledge_id = ?", "faq").Find(&storedAfterSetup).Error)
			require.Len(t, storedAfterSetup, 51, "50 new entries + the merged pre-existing one")
			var missing types.Chunk
			require.Error(t, f.db.Where("knowledge_id = ?", "faq").
				Where("content LIKE ?", "%new question 50%").First(&missing).Error,
				"the 51st new question must not exist yet")

			// Seed the checkpoint exactly as the failing attempt left it:
			// processed=51 counts the out-of-order merge, so a count-based
			// resume would skip input row 51 (index 50).
			seed := &types.FAQImportProgress{
				TaskID:            taskID,
				KBID:              f.kb.ID,
				KnowledgeID:       "faq",
				Status:            types.FAQImportStatusProcessing,
				Total:             len(entries),
				Processed:         51,
				ValidEntryIndices: faqImport5957Range(len(entries)),
				MergeEntryIndices: []int{51},
			}
			if seedIdentity {
				// Checkpoint shape written by the fixed code: completed work
				// recorded by input-row identity (50 creates + the merge).
				seed.CompletedEntryIndices = append(faqImport5957Range(50), 51)
				seed.MergedCount = 1
			}
			require.NoError(t, f.svc.saveFAQImportProgress(f.ctx, seed))
			require.NoError(t, f.svc.setRunningFAQImportInfo(f.ctx, f.kb.ID,
				&runningFAQImportInfo{TaskID: taskID, InstanceID: "instance-1"}))

			// Retry with a healthy chunk service.
			f.svc.chunkService = realChunks
			payload := types.FAQImportPayload{
				TenantID: 7, KBID: f.kb.ID, KnowledgeID: "faq", TaskID: taskID,
				InstanceID: "instance-1", Mode: types.FAQBatchModeAppend, Entries: entries,
			}
			require.NoError(t, f.svc.ProcessFAQImport(f.ctx,
				asynq.NewTask(types.TypeFAQImport, mustWriteJSON(t, payload))))

			progress, err := f.svc.GetFAQImportProgress(f.ctx, taskID)
			require.NoError(t, err)
			require.Equal(t, types.FAQImportStatusCompleted, progress.Status)
			require.Equal(t, 52, progress.SuccessCount)

			var stored []types.Chunk
			require.NoError(t, f.db.Where("knowledge_id = ?", "faq").Find(&stored).Error)
			require.Len(t, stored, 52, "all 51 new questions plus the merged pre-existing one")
			for _, chunk := range stored {
				require.Equal(t, int(types.ChunkStatusIndexed), chunk.Status,
					"chunk %s must be indexed, not left unindexed", chunk.ID)
			}
			var recovered types.Chunk
			require.NoError(t, f.db.Where("knowledge_id = ?", "faq").
				Where("content LIKE ?", "%new question 50%").First(&recovered).Error,
				"the skipped 51st new question must be imported by the retry")

			var merged types.Chunk
			require.NoError(t, f.db.Where("id = ?", "one").First(&merged).Error)
			mergedMeta, err := merged.FAQMetadata()
			require.NoError(t, err)
			require.Equal(t, []string{"updated answer"}, mergedMeta.Answers,
				"the update row must still be applied")
		})
	}
}
