package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reconcileKnowledge() *types.Knowledge {
	return &types.Knowledge{
		ID:              uuid.New().String(),
		TenantID:        1,
		KnowledgeBaseID: uuid.New().String(),
	}
}

func parsedText(content string, seq int) types.ParsedChunk {
	return types.ParsedChunk{
		Content: content,
		Seq:     seq,
		Start:   seq * 10,
		End:     seq*10 + len(content),
	}
}

func TestBuildDesiredDocumentChunksStableAcrossInsertionAndReorder(t *testing.T) {
	knowledge := reconcileKnowledge()
	first, err := buildDesiredDocumentChunks(
		knowledge,
		[]types.ParsedChunk{parsedText("alpha", 0), parsedText("beta", 1)},
		nil,
		nil,
	)
	require.NoError(t, err)
	firstIDs := map[string]string{}
	for _, chunk := range first.Text {
		firstIDs[chunk.Content] = chunk.ID
	}

	reordered, err := buildDesiredDocumentChunks(
		knowledge,
		[]types.ParsedChunk{
			parsedText("inserted", 0),
			parsedText("beta", 1),
			parsedText("alpha", 2),
		},
		nil,
		first.All,
	)
	require.NoError(t, err)
	reorderedIDs := map[string]string{}
	for _, chunk := range reordered.Text {
		reorderedIDs[chunk.Content] = chunk.ID
	}

	assert.Equal(t, firstIDs["alpha"], reorderedIDs["alpha"])
	assert.Equal(t, firstIDs["beta"], reorderedIDs["beta"])
	assert.NotEmpty(t, reorderedIDs["inserted"])
	require.Len(t, reordered.Added, 1)
	require.Empty(t, reordered.Stale)
}

func TestBuildDesiredDocumentChunksReusesUniqueLegacyRandomUUID(t *testing.T) {
	knowledge := reconcileKnowledge()
	legacy := &types.Chunk{
		ID:              uuid.New().String(),
		TenantID:        knowledge.TenantID,
		KnowledgeID:     knowledge.ID,
		KnowledgeBaseID: knowledge.KnowledgeBaseID,
		Content:         "unchanged",
		ChunkType:       types.ChunkTypeText,
	}

	desired, err := buildDesiredDocumentChunks(
		knowledge,
		[]types.ParsedChunk{parsedText("unchanged", 0)},
		nil,
		[]*types.Chunk{legacy},
	)
	require.NoError(t, err)
	require.Len(t, desired.Text, 1)
	assert.Equal(t, legacy.ID, desired.Text[0].ID)
	require.Len(t, desired.Updated, 1)
	require.Empty(t, desired.Added)
	require.Empty(t, desired.Stale)
}

func TestBuildDesiredDocumentChunksPreservesLiveEnableState(t *testing.T) {
	knowledge := reconcileKnowledge()
	first, err := buildDesiredDocumentChunks(
		knowledge,
		[]types.ParsedChunk{parsedText("keep disabled", 0)},
		nil,
		nil,
	)
	require.NoError(t, err)
	require.Len(t, first.Text, 1)
	live := *first.Text[0]
	live.IsEnabled = false

	next, err := buildDesiredDocumentChunks(
		knowledge,
		[]types.ParsedChunk{parsedText("keep disabled", 0)},
		nil,
		[]*types.Chunk{&live},
	)
	require.NoError(t, err)
	require.Len(t, next.Text, 1)
	assert.False(t, next.Text[0].IsEnabled)
}

func TestBuildDesiredDocumentChunksDoesNotGuessAmbiguousLegacyDuplicates(t *testing.T) {
	knowledge := reconcileKnowledge()
	existing := []*types.Chunk{
		{
			ID:              uuid.New().String(),
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         "repeat",
			ChunkType:       types.ChunkTypeText,
		},
		{
			ID:              uuid.New().String(),
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         "repeat",
			ChunkType:       types.ChunkTypeText,
		},
	}

	desired, err := buildDesiredDocumentChunks(
		knowledge,
		[]types.ParsedChunk{parsedText("repeat", 0), parsedText("repeat", 1)},
		nil,
		existing,
	)
	require.NoError(t, err)
	require.Len(t, desired.Text, 2)
	assert.NotEqual(t, desired.Text[0].ID, desired.Text[1].ID)
	assert.NotEqual(t, existing[0].ID, desired.Text[0].ID)
	assert.NotEqual(t, existing[1].ID, desired.Text[1].ID)
	require.Len(t, desired.Added, 2)
	require.Len(t, desired.Stale, 2)
}

func TestBuildDesiredDocumentChunksParentIdentityAffectsChildren(t *testing.T) {
	knowledge := reconcileKnowledge()
	parents := []types.ParsedParentChunk{
		{Content: "parent A"},
		{Content: "parent B"},
	}
	desired, err := buildDesiredDocumentChunks(
		knowledge,
		[]types.ParsedChunk{
			{Content: "same child", ParentIndex: 0},
			{Content: "same child", ParentIndex: 1},
		},
		parents,
		nil,
	)
	require.NoError(t, err)
	require.Len(t, desired.Text, 2)
	assert.NotEqual(t, desired.Text[0].ID, desired.Text[1].ID)
	assert.NotEqual(t, desired.Text[0].ParentChunkID, desired.Text[1].ParentChunkID)
}

func TestStableGeneratedQuestionsSurviveReordering(t *testing.T) {
	knowledge := reconcileKnowledge()
	parent := &types.Chunk{ID: uuid.New().String()}

	first, err := stableGeneratedQuestions(
		knowledge.ID,
		parent,
		[]string{"What is alpha?", "What is beta?"},
	)
	require.NoError(t, err)
	second, err := stableGeneratedQuestions(
		knowledge.ID,
		parent,
		[]string{"What is beta?", "What is alpha?"},
	)
	require.NoError(t, err)

	firstIDs := map[string]string{}
	for _, question := range first {
		firstIDs[question.Question] = question.ID
	}
	for _, question := range second {
		assert.Equal(t, firstIDs[question.Question], question.ID)
	}
}

func TestStableGeneratedQuestionsReusesUniqueLegacyID(t *testing.T) {
	knowledge := reconcileKnowledge()
	parent := &types.Chunk{ID: uuid.New().String()}
	require.NoError(t, parent.SetDocumentMetadata(&types.DocumentChunkMetadata{
		GeneratedQuestions: []types.GeneratedQuestion{{
			ID:       "q-legacy",
			Question: "What is alpha?",
		}},
	}))

	questions, err := stableGeneratedQuestions(
		knowledge.ID,
		parent,
		[]string{"What is alpha?"},
	)
	require.NoError(t, err)
	require.Len(t, questions, 1)
	assert.Equal(t, "q-legacy", questions[0].ID)
}

func TestStaleGeneratedQuestionSourceIDsAreExact(t *testing.T) {
	parent := &types.Chunk{ID: uuid.New().String()}
	require.NoError(t, parent.SetDocumentMetadata(&types.DocumentChunkMetadata{
		GeneratedQuestions: []types.GeneratedQuestion{
			{ID: "keep", Question: "kept"},
			{ID: "stale", Question: "removed"},
		},
	}))

	stale := staleGeneratedQuestionSourceIDs(parent, []types.GeneratedQuestion{{
		ID:       "keep",
		Question: "kept",
	}})
	assert.Equal(t, []string{parent.ID + "-stale"}, stale)
}

func TestStableImageDerivedChunkID(t *testing.T) {
	knowledge := reconcileKnowledge()
	parent := &types.Chunk{
		ID:          uuid.New().String(),
		KnowledgeID: knowledge.ID,
	}
	first, err := stableImageDerivedChunkID(
		parent,
		types.ChunkTypeImageOCR,
		"provider://images/one",
		"recognized text",
	)
	require.NoError(t, err)
	retry, err := stableImageDerivedChunkID(
		parent,
		types.ChunkTypeImageOCR,
		"provider://images/one",
		"recognized text",
	)
	require.NoError(t, err)
	changed, err := stableImageDerivedChunkID(
		parent,
		types.ChunkTypeImageOCR,
		"provider://images/one",
		"changed text",
	)
	require.NoError(t, err)

	assert.Equal(t, first, retry)
	assert.NotEqual(t, first, changed)
}

func TestDataTableDerivedChunkIDsAreStable(t *testing.T) {
	knowledge := reconcileKnowledge()
	service := &DataTableSummaryService{}
	resources := &extractionResources{knowledge: knowledge}

	first, err := service.buildChunks(resources, "summary", "columns")
	require.NoError(t, err)
	retry, err := service.buildChunks(resources, "summary", "columns")
	require.NoError(t, err)
	changed, err := service.buildChunks(resources, "new summary", "columns")
	require.NoError(t, err)

	require.Len(t, first, 2)
	assert.Equal(t, first[0].ID, retry[0].ID)
	assert.Equal(t, first[1].ID, retry[1].ID)
	assert.NotEqual(t, first[0].ID, changed[0].ID)
	assert.NotEqual(t, first[1].ID, changed[1].ID)
	assert.Equal(t, first[0].ID, first[1].ParentChunkID)
}
