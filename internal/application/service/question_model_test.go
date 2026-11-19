package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestResolveProcessConfigQuestionModel(t *testing.T) {
	for _, tt := range []struct {
		name, configured, override, want string
		hasOverride                      bool
	}{
		{"legacy", "", "", "summary", false},
		{"independent", "questions", "", "questions", false},
		{"old upload inherits", "questions", "", "questions", true},
		{"upload override", "questions", "other", "other", true},
		{"whitespace inherits", "questions", "  ", "questions", true},
		{"trim override", "questions", " other ", "other", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kb := &types.KnowledgeBase{
				SummaryModelID: "summary", QuestionGenerationConfig: &types.QuestionGenerationConfig{
					Enabled: true, ModelID: tt.configured, QuestionCount: 3,
				},
			}
			var override *types.KnowledgeProcessOverrides
			if tt.hasOverride {
				override = &types.KnowledgeProcessOverrides{QuestionGenerationConfig: &types.QuestionGenerationConfig{
					Enabled: true, ModelID: tt.override, QuestionCount: 5,
				}}
			}
			effective := ResolveProcessConfig(kb, override)
			require.Equal(t, tt.want, effective.QuestionGenerationConfig.ModelID)
			require.Equal(t, "summary", kb.SummaryModelID)
			require.Equal(t, tt.configured, kb.QuestionGenerationConfig.ModelID)
		})
	}
	effective := ResolveProcessConfig(&types.KnowledgeBase{SummaryModelID: "summary"}, nil)
	require.Equal(t, "summary", effective.QuestionGenerationConfig.ModelID)
}

type questionSelectionModels struct {
	interfaces.ModelService
	model   *types.Model
	err     error
	ids     []string
	chatIDs []string
	chatErr error
}

func (m *questionSelectionModels) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	m.ids = append(m.ids, id)
	return m.model, m.err
}

func (m *questionSelectionModels) GetChatModel(_ context.Context, id string) (chat.Chat, error) {
	m.chatIDs = append(m.chatIDs, id)
	return nil, m.chatErr
}

func TestRegenerateChunkQuestionsUsesIndependentModel(t *testing.T) {
	f := newDocumentWriteFixture(t)
	f.kbs.values["kb"].SummaryModelID = "summary"
	f.kbs.values["kb"].QuestionGenerationConfig = &types.QuestionGenerationConfig{ModelID: "questions"}
	stop := errors.New("stop before generation")
	models := &questionSelectionModels{
		model: &types.Model{Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive}, chatErr: stop,
	}
	f.svc.modelService = models
	_, err := f.svc.RegenerateChunkQuestions(f.ctx, "chunk")
	require.ErrorIs(t, err, stop)
	require.Equal(t, []string{"questions"}, models.chatIDs)
	require.Equal(t, []string{"questions"}, models.ids)
	f.requireNoWrites(t)
	models.model.Status = types.ModelStatusDownloading
	models.chatIDs = nil
	_, err = f.svc.RegenerateChunkQuestions(f.ctx, "chunk")
	require.Error(t, err)
	require.Empty(t, models.chatIDs, "invalid explicit model must not fall back to summaries")
	f.requireNoWrites(t)
}

func TestResolveQuestionGenerationModelValidatesExplicitBindings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model *types.Model
		err   error
		valid bool
	}{
		{"active chat", &types.Model{Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive}, nil, true},
		{"embedding", &types.Model{Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive}, nil, false},
		{"inactive", &types.Model{Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusDownloading}, nil, false},
		{"missing", nil, nil, false},
		{"not visible", nil, errors.New("not visible"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models := &questionSelectionModels{model: tc.model, err: tc.err}
			kb := &types.KnowledgeBase{
				SummaryModelID:           "summary",
				QuestionGenerationConfig: &types.QuestionGenerationConfig{ModelID: " questions "},
			}
			id, err := resolveQuestionGenerationModel(context.Background(), models, kb, nil)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, "questions", id)
			} else {
				require.Error(t, err)
				require.Empty(t, id)
			}
			require.Equal(t, []string{"questions"}, models.ids)
			require.Equal(t, "summary", kb.SummaryModelID)
		})
	}
}

func TestResolveQuestionGenerationModelOverrideAndLegacy(t *testing.T) {
	models := &questionSelectionModels{
		model: &types.Model{Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
	}
	kb := &types.KnowledgeBase{SummaryModelID: "summary"}
	id, err := resolveQuestionGenerationModel(context.Background(), models, kb, nil)
	require.NoError(t, err)
	require.Equal(t, "summary", id)
	require.Empty(t, models.ids)
	overrides := &types.KnowledgeProcessOverrides{
		QuestionGenerationConfig: &types.QuestionGenerationConfig{ModelID: "override"},
	}
	id, err = resolveQuestionGenerationModel(context.Background(), models, kb, overrides)
	require.NoError(t, err)
	require.Equal(t, "override", id)
	kb.QuestionGenerationConfig = &types.QuestionGenerationConfig{ModelID: "questions"}
	overrides.QuestionGenerationConfig.ModelID = ""
	id, err = resolveQuestionGenerationModel(context.Background(), models, kb, overrides)
	require.NoError(t, err)
	require.Equal(t, "questions", id)
	require.Equal(t, []string{"override", "questions"}, models.ids)
}

func TestInvalidQuestionModelMarksAsyncTraceFailed(t *testing.T) {
	for _, path := range []string{"legacy", "batch"} {
		t.Run(path, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			tracker, db := setupSpanTrackerTest(t)
			f.svc.spanTracker = tracker
			f.svc.config = &config.Config{
				Conversation: &config.ConversationConfig{GenerateQuestionsPrompt: "generate questions"},
			}
			f.kbs.values["kb"].QuestionGenerationConfig = &types.QuestionGenerationConfig{ModelID: "inactive"}
			models := &questionSelectionModels{
				model: &types.Model{Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusDownloading},
			}
			f.svc.modelService = models
			_, attempt, err := tracker.OpenAttempt(f.ctx, "doc", "question-trace")
			require.NoError(t, err)
			tracker.BeginStage(f.ctx, "doc", attempt, types.StagePostProcess, nil)
			payload := types.QuestionGenerationPayload{
				KnowledgeID: "doc", KnowledgeBaseID: "kb", TenantID: 7, Attempt: attempt, ChunkIDs: []string{"chunk"},
			}
			name := "postprocess.question"
			if path == "batch" {
				err = f.svc.processQuestionGenerationForChunks(f.ctx, nil, payload)
				name = "postprocess.question.batch[0]"
			} else {
				err = f.svc.processQuestionGenerationForKnowledge(f.ctx, nil, payload)
			}
			require.Error(t, err)
			require.Empty(t, models.chatIDs)
			var span types.KnowledgeProcessingSpan
			require.NoError(t, db.Where("knowledge_id = ? AND name = ?", "doc", name).First(&span).Error)
			require.Equal(t, types.SpanStatusFailed, span.Status)
			require.Equal(t, "QUESTION_FAILED", span.ErrorCode)
		})
	}
}
