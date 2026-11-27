package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

var errFAQAppendEmbeddingUnavailable = errors.New("test: embedding unavailable")

type faqAppendIndex struct {
	parentChildRetrieveEngine
	unavailable bool
	deletions   int
}

func (e *faqAppendIndex) DeleteByChunkIDList(context.Context, []string, int, string) error {
	e.deletions++
	e.indexed = nil
	return nil
}

func (e *faqAppendIndex) BatchIndex(
	ctx context.Context, embedder embedding.Embedder, entries []*types.IndexInfo, kinds []types.RetrieverType,
) error {
	if e.unavailable {
		return errFAQAppendEmbeddingUnavailable
	}
	return e.parentChildRetrieveEngine.BatchIndex(ctx, embedder, entries, kinds)
}

func TestFAQAppendSkipsUnchangedEntries(t *testing.T) {
	for _, scenario := range []string{
		"identical", "identical-after-validation-removal", "mixed",
		"changed-answer-order", "changed-enabled", "changed-recommendation",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newFAQWriteFixture(t)
			sqlDB, err := f.db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, f.db.Where("id = ?", "one").Delete(&types.Chunk{}).Error)
			engine := &faqAppendIndex{}
			f.svc.retrieveEngine = &fakeFanoutRegistry{
				byStore: map[string]interfaces.RetrieveEngineService{*f.kb.VectorStoreID: engine},
			}
			f.svc.modelService = parentChildModelService{embedder: parentChildEmbedder{}}
			f.svc.fileSvc = &fakeFileService{saveErr: errors.New("test: no object storage")}
			f.svc.chunkService = &chunkService{
				chunkRepository: f.chunks, knowledgeRepo: f.svc.repo,
				kbRepository: &imageReaderKBRepo{kb: f.kb},
			}

			enabled, disabled := true, false
			entry := types.FAQEntryPayload{
				StandardQuestion:  "How is a ticket opened?",
				SimilarQuestions:  []string{"How do I file a ticket?"},
				NegativeQuestions: []string{"How do I close a ticket?"},
				Answers:           []string{"Open the support page.", "Fill in the form."},
				TagID:             11, IsEnabled: &enabled, IsRecommended: &enabled,
			}
			appendEntry := func(taskID string) (*types.FAQImportProgress, error) {
				progress := &types.FAQImportProgress{
					TaskID: taskID, KBID: f.kb.ID, KnowledgeID: "faq",
					Total: 1, ValidEntryIndices: []int{0},
				}
				entries := []types.FAQEntryPayload{entry}
				if scenario == "mixed" && taskID == "same" {
					entries = append(entries, types.FAQEntryPayload{
						StandardQuestion: "Where is the support page?",
						Answers:          []string{"On the help site."},
					})
					progress.Total = len(entries)
					progress.ValidEntryIndices = []int{0, 1}
				}
				if scenario == "identical-after-validation-removal" && taskID == "same" {
					progress.PartialFailedCount = 1
					progress.FailedEntries = []types.FAQFailedEntry{{Index: 0, IsPartialFailure: true}}
				}
				err := f.svc.executeFAQImport(f.ctx, taskID, f.kb.ID,
					&types.FAQBatchUpsertPayload{
						Mode: types.FAQBatchModeAppend, Entries: entries,
					},
					f.kb.TenantID, 0, progress)
				return progress, err
			}
			_, err = appendEntry("initial")
			require.NoError(t, err)
			require.Len(t, engine.indexed, 1)
			chunkID := engine.indexed[0].ChunkID

			engine.unavailable = scenario != "mixed"
			switch scenario {
			case "identical", "identical-after-validation-removal", "mixed":
				progress, err := appendEntry("same")
				require.NoError(t, err)
				require.Zero(t, engine.deletions)
				require.Zero(t, progress.MergedCount)
				audit := &captureKBActivityAudit{}
				f.svc.audit = audit
				ctx := withKBActivityTask(f.ctx, progress.TaskID, "user")
				require.NoError(t, f.svc.finalizeFAQValidation(ctx, &types.FAQImportPayload{
					TaskID: progress.TaskID, KBID: f.kb.ID, KnowledgeID: "faq", TenantID: f.kb.TenantID,
					Mode: types.FAQBatchModeAppend,
				}, progress, progress.Total))
				wantSkipped, wantAdded := 1, 0
				wantOutcome, wantStatus := types.AuditOutcomeSuccess, "completed"
				if scenario == "identical-after-validation-removal" {
					wantSkipped = 0
					wantOutcome, wantStatus = types.AuditOutcomePartial, "partial"
					require.Equal(t, 1, progress.PartialFailedCount)
				} else {
					require.Contains(t, progress.Message, "跳过 1 条")
				}
				if scenario == "mixed" {
					wantAdded = 1
				} else {
					require.Len(t, engine.indexed, 1)
				}
				require.NotNil(t, audit.entry)
				require.Equal(t, wantOutcome, audit.entry.Outcome)
				var details map[string]any
				require.NoError(t, json.Unmarshal(audit.entry.Details, &details))
				require.Equal(t, wantStatus, details["processing_status"])
				if wantSkipped > 0 {
					require.Equal(t, float64(wantSkipped), details["skipped"])
				} else {
					require.NotContains(t, details, "skipped")
				}
				require.Equal(t, float64(wantAdded), details["count"])
				require.Equal(t, wantSkipped, progress.SkippedCount)
				require.Equal(t, wantAdded, progress.AddedCount)
				kb, err := f.svc.repo.GetKnowledgeByID(f.ctx, f.kb.TenantID, "faq")
				require.NoError(t, err)
				result, err := kb.GetLastFAQImportResult()
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, wantSkipped, result.SkippedCount)
				var stored types.Chunk
				require.NoError(t, f.db.First(&stored, "id = ?", chunkID).Error)
				meta, err := stored.FAQMetadata()
				require.NoError(t, err)
				require.Equal(t, 1, meta.Version)
				return
			case "changed-answer-order":
				entry.Answers = []string{"Fill in the form.", "Open the support page."}
			case "changed-enabled":
				entry.IsEnabled = &disabled
			case "changed-recommendation":
				entry.IsRecommended = &disabled
			}

			_, err = appendEntry("change")
			require.ErrorIs(t, err, errFAQAppendEmbeddingUnavailable)
			require.Equal(t, 1, engine.deletions)
			engine.unavailable = false
			progress, err := appendEntry("change")
			require.NoError(t, err)
			require.Equal(t, 1, progress.MergedCount)
			require.Equal(t, 2, engine.deletions)
			require.Len(t, engine.indexed, 1)
			require.Equal(t, chunkID, engine.indexed[0].ChunkID)
			require.Equal(t, *entry.IsEnabled, engine.indexed[0].IsEnabled)
			require.Equal(t, *entry.IsRecommended, engine.indexed[0].IsRecommended)
			var stored types.Chunk
			require.NoError(t, f.db.First(&stored, "id = ?", chunkID).Error)
			meta, err := stored.FAQMetadata()
			require.NoError(t, err)
			require.Equal(t, entry.Answers, meta.Answers)
			require.Equal(t, 2, meta.Version)

			engine.unavailable = true
			progress, err = appendEntry("same-after-retry")
			require.NoError(t, err)
			require.Equal(t, 1, progress.SkippedCount)
			require.Equal(t, 2, engine.deletions)
		})
	}
}
