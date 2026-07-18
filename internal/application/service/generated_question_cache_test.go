package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type generatedQuestionChunkService struct {
	interfaces.ChunkService
	events *[]string
	err    error
}

func (s generatedQuestionChunkService) UpdateChunk(_ context.Context, _ *types.Chunk) error {
	*s.events = append(*s.events, "update")
	return s.err
}

type generatedQuestionReconciler struct {
	events  *[]string
	deleted []string
	err     error
}

func (r *generatedQuestionReconciler) DeleteBySourceIDList(
	_ context.Context, sourceIDs []string, _ int, _ string,
) error {
	*r.events = append(*r.events, "delete")
	r.deleted = append([]string(nil), sourceIDs...)
	return r.err
}

func TestGenerateQuestionsWithContextReusesChatArtifact(t *testing.T) {
	store := newChatArtifactFakeStore()
	model := &chatArtifactFakeModel{
		modelID:   "question-model",
		modelName: "question-model",
		response:  &types.ChatResponse{Content: "1. What is stable caching?\n2. How are retries handled?"},
	}
	svc := &knowledgeService{
		config: &config.Config{Conversation: &config.ConversationConfig{
			GenerateQuestionsPrompt: "Create {{question_count}} questions for {{content}} {{context}} {{doc_name}} in {{language}}.",
		}},
		artifactStore: store,
	}
	ctx := context.WithValue(context.Background(), types.LanguageContextKey, "en")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))

	first, firstHit, firstProviderCall, err := svc.generateQuestionsWithContext(
		ctx, model, "revision-1", "current", "previous", "next", "Document", 2, "Be concise.")
	require.NoError(t, err)
	second, secondHit, secondProviderCall, err := svc.generateQuestionsWithContext(
		ctx, model, "revision-1", "current", "previous", "next", "Document", 2, "Be concise.")
	require.NoError(t, err)

	assert.Equal(t, []string{"What is stable caching?", "How are retries handled?"}, first)
	assert.Equal(t, first, second)
	assert.False(t, firstHit)
	assert.True(t, secondHit)
	assert.True(t, firstProviderCall)
	assert.False(t, secondProviderCall)
	assert.Equal(t, 1, model.calls)
}

func TestGenerateQuestionsWithContextDoesNotCacheUnparseableCompletion(t *testing.T) {
	store := newChatArtifactFakeStore()
	model := &chatArtifactFakeModel{
		modelID:   "question-model",
		modelName: "question-model",
		response:  &types.ChatResponse{Content: "1. no"},
	}
	svc := &knowledgeService{
		config: &config.Config{Conversation: &config.ConversationConfig{
			GenerateQuestionsPrompt: "Create questions for {{content}}.",
		}},
		artifactStore: store,
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	first, firstHit, firstProviderCall, err := svc.generateQuestionsWithContext(
		ctx, model, "revision-1", "current", "", "", "Document", 2, "")
	require.NoError(t, err)
	second, secondHit, secondProviderCall, err := svc.generateQuestionsWithContext(
		ctx, model, "revision-1", "current", "", "", "Document", 2, "")
	require.NoError(t, err)

	assert.Empty(t, first)
	assert.Empty(t, second)
	assert.False(t, firstHit)
	assert.False(t, secondHit)
	assert.True(t, firstProviderCall)
	assert.True(t, secondProviderCall)
	assert.Equal(t, 2, model.calls)
	assert.Zero(t, store.putCalls)
}

func TestBuildGeneratedQuestionsUsesStableOccurrenceAwareIDs(t *testing.T) {
	questions := []string{" Same question? ", "Same question?", "Different question?"}
	first := buildGeneratedQuestions("chunk-1", questions)
	second := buildGeneratedQuestions("chunk-1", questions)
	otherChunk := buildGeneratedQuestions("chunk-2", questions)

	require.Len(t, first, 3)
	assert.Equal(t, first, second)
	assert.NotEqual(t, first[0].ID, first[1].ID)
	assert.NotEqual(t, first[0].ID, otherChunk[0].ID)
	assert.Equal(t, "Same question?", first[0].Question)
}

func TestWithGeneratedQuestionsPreservesUnrelatedMetadata(t *testing.T) {
	original := types.JSON(`{"_weknora_embedding_fingerprint":"keep","nested":{"answer":42},"generated_questions":[{"id":"old","question":"Old?"}]}`)
	questions := []types.GeneratedQuestion{{ID: "new", Question: "New?"}}

	updated, err := withGeneratedQuestions(original, questions)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(updated, &fields))
	assert.JSONEq(t, `"keep"`, string(fields[types.ChunkEmbeddingFingerprintMetadataKey]))
	assert.JSONEq(t, `{"answer":42}`, string(fields["nested"]))
	assert.JSONEq(t, `[{"id":"new","question":"New?"}]`, string(fields["generated_questions"]))
}

func TestWithGeneratedQuestionsAcceptsNullMetadata(t *testing.T) {
	questions := []types.GeneratedQuestion{{ID: "new", Question: "New?"}}

	updated, err := withGeneratedQuestions(types.JSON(`null`), questions)

	require.NoError(t, err)
	assert.JSONEq(t, `{"generated_questions":[{"id":"new","question":"New?"}]}`, string(updated))
}

func TestStaleGeneratedQuestionSourceIDs(t *testing.T) {
	old := []types.GeneratedQuestion{{ID: "random-old", Question: "Old?"}, {ID: "keep", Question: "Keep?"}}
	desired := []types.GeneratedQuestion{{ID: "keep", Question: "Keep?"}, {ID: "new", Question: "New?"}}

	assert.Equal(t, []string{"chunk-1-random-old"}, staleGeneratedQuestionSourceIDs("chunk-1", old, desired))
}

func TestApplyGeneratedQuestionUpdatesDeletesStaleIndexBeforePublishingMetadata(t *testing.T) {
	events := []string{}
	chunk := &types.Chunk{ID: "chunk-1", Metadata: types.JSON(`{"generated_questions":[{"id":"old","question":"Old?"}],"keep":true}`)}
	newQuestions := []types.GeneratedQuestion{{ID: "stable", Question: "New question?"}}
	metadata, err := withGeneratedQuestions(chunk.Metadata, newQuestions)
	require.NoError(t, err)
	update := generatedQuestionUpdate{
		chunk:        chunk,
		metadata:     metadata,
		oldQuestions: []types.GeneratedQuestion{{ID: "old", Question: "Old?"}},
		newQuestions: newQuestions,
	}
	reconciler := &generatedQuestionReconciler{events: &events}
	svc := &knowledgeService{chunkService: generatedQuestionChunkService{events: &events}}

	err = svc.applyGeneratedQuestionUpdates(
		context.Background(), reconciler, &embeddingArtifactFakeEmbedder{dimensions: 3},
		&types.KnowledgeBase{Type: "document"}, []generatedQuestionUpdate{update},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"delete", "update"}, events)
	assert.Equal(t, []string{"chunk-1-old"}, reconciler.deleted)
	assert.JSONEq(t, string(metadata), string(chunk.Metadata))
}

func TestApplyGeneratedQuestionUpdatesKeepsOldMetadataWhenStaleDeleteFails(t *testing.T) {
	events := []string{}
	original := types.JSON(`{"generated_questions":[{"id":"old","question":"Old?"}],"keep":true}`)
	chunk := &types.Chunk{ID: "chunk-1", Metadata: append(types.JSON(nil), original...)}
	newQuestions := []types.GeneratedQuestion{{ID: "stable", Question: "New question?"}}
	metadata, err := withGeneratedQuestions(chunk.Metadata, newQuestions)
	require.NoError(t, err)
	deleteErr := errors.New("delete failed")
	svc := &knowledgeService{chunkService: generatedQuestionChunkService{events: &events}}

	err = svc.applyGeneratedQuestionUpdates(
		context.Background(), &generatedQuestionReconciler{events: &events, err: deleteErr},
		&embeddingArtifactFakeEmbedder{dimensions: 3}, &types.KnowledgeBase{Type: "document"},
		[]generatedQuestionUpdate{{
			chunk: chunk, metadata: metadata,
			oldQuestions: []types.GeneratedQuestion{{ID: "old"}}, newQuestions: newQuestions,
		}},
	)
	assert.ErrorIs(t, err, deleteErr)
	assert.Equal(t, []string{"delete"}, events)
	assert.Equal(t, original, chunk.Metadata)
}
