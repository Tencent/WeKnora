package service

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// faqRowIndex keeps rows the way a vector store does: one row per SourceID,
// cleared either by chunk or by explicit source ID.
type faqRowIndex struct {
	interfaces.RetrieveEngineService
	mu   sync.Mutex
	rows map[string]string
}

func newFAQRowIndex(rows map[string]string) *faqRowIndex {
	return &faqRowIndex{rows: rows}
}

func (*faqRowIndex) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (*faqRowIndex) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (i *faqRowIndex) BatchIndex(
	_ context.Context, _ embedding.Embedder, indexInfoList []*types.IndexInfo,
	_ []types.RetrieverType,
) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, info := range indexInfoList {
		i.rows[info.SourceID] = info.Content
	}
	return nil
}

func (i *faqRowIndex) DeleteByChunkIDList(
	_ context.Context, chunkIDList []string, _ int, _ string,
) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for sourceID := range i.rows {
		for _, chunkID := range chunkIDList {
			if sourceID == chunkID || strings.HasPrefix(sourceID, chunkID+"-") {
				delete(i.rows, sourceID)
			}
		}
	}
	return nil
}

func (i *faqRowIndex) DeleteBySourceIDList(
	_ context.Context, sourceIDList []string, _ int, _ string,
) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, sourceID := range sourceIDList {
		delete(i.rows, sourceID)
	}
	return nil
}

// contents returns the indexed text, sorted so a failure reads as a diff.
func (i *faqRowIndex) contents() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	values := make([]string, 0, len(i.rows))
	for _, content := range i.rows {
		values = append(values, content)
	}
	sort.Strings(values)
	return values
}

// faqChunkUpdater records the chunk write the update path performs; the delete
// spy the write-access fixture installs has no UpdateChunk of its own.
type faqChunkUpdater struct {
	interfaces.ChunkService
	updated *types.Chunk
}

func (u *faqChunkUpdater) UpdateChunk(_ context.Context, chunk *types.Chunk) error {
	u.updated = chunk
	return nil
}

// A separate-mode entry names each alias row after the alias position
// (<chunk>-0, <chunk>-1, …). Positions shift whenever an alias is added or
// removed, so an edit cannot address those rows one at a time: it has to clear
// the chunk and rebuild it. Editing used to leave the position rows untouched
// and delete a content-hashed row the build never wrote, so a removed question
// kept answering searches (#5958).
func TestUpdatingSeparateIndexEntryDropsTheRemovedAliasRow(t *testing.T) {
	f := newFAQWriteFixture(t)
	f.kb.FAQConfig = &types.FAQConfig{
		IndexMode:         types.FAQIndexModeQuestionOnly,
		QuestionIndexMode: types.FAQQuestionIndexModeSeparate,
	}
	const standard = "What is the postal address for support?"
	const kept = "Copper orchid voucher ZXQ-7921"
	const removed = "Amber ledger token QK-4410"
	index := newFAQRowIndex(map[string]string{
		"one":   standard,
		"one-0": kept,
		"one-1": removed,
	})
	f.svc.retrieveEngine = &fakeFanoutRegistry{
		byStore: map[string]interfaces.RetrieveEngineService{"write-store": index},
	}
	f.svc.modelService = imageEditModels{embedding: &imageEditEmbedding{}}
	f.svc.chunkService = &faqChunkUpdater{}

	chunk, err := f.chunks.GetChunkByID(f.ctx, 7, "one")
	require.NoError(t, err)
	require.NoError(t, chunk.SetFAQMetadata(&types.FAQChunkMetadata{
		StandardQuestion: standard,
		SimilarQuestions: []string{kept, removed},
		Answers:          []string{"PO box 42."},
	}))
	require.NoError(t, f.chunks.UpdateChunk(f.ctx, chunk))

	_, err = f.svc.UpdateFAQEntry(f.ctx, "kb", 1, &types.FAQEntryPayload{
		StandardQuestion: standard,
		SimilarQuestions: []string{kept},
		Answers:          []string{"PO box 42."},
	})
	require.NoError(t, err)

	require.NotContains(t, index.contents(), removed,
		"a removed alias must not stay searchable")
	require.Contains(t, index.contents(), kept,
		"the retained alias must stay searchable")
	require.Contains(t, index.contents(), standard,
		"the standard question must stay searchable")
}
