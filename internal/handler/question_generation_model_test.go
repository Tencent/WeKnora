package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type questionConfigModels struct {
	interfaces.ModelService
	model *types.Model
	err   error
	ids   []string
}

func (m *questionConfigModels) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	m.ids = append(m.ids, id)
	return m.model, m.err
}

func TestQuestionModelConfigPreservesOmittedAndClearsExplicit(t *testing.T) {
	previous := &types.QuestionGenerationConfig{ModelID: "questions"}
	h := &InitializationHandler{}
	for _, tt := range []struct{ body, want string }{
		{`{"questionGeneration":{"enabled":true,"questionCount":5}}`, "questions"},
		{`{"questionGeneration":{"enabled":true,"modelId":""}}`, ""},
	} {
		var req KBModelConfigRequest
		require.NoError(t, json.Unmarshal([]byte(tt.body), &req))
		id, err := h.resolveQuestionGenerationModel(context.Background(), req.QuestionGeneration.ModelID, previous)
		require.NoError(t, err)
		require.Equal(t, tt.want, id)
		require.Equal(t, "questions", previous.ModelID)
	}
}

func TestQuestionModelConfigValidatesVisibleActiveChatModel(t *testing.T) {
	for _, tt := range []struct {
		name  string
		model *types.Model
		err   error
		valid bool
	}{
		{"active chat", &types.Model{Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive}, nil, true},
		{"embedding", &types.Model{Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive}, nil, false},
		{"inactive", &types.Model{Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusDownloading}, nil, false},
		{"missing", nil, nil, false},
		{"inaccessible", nil, errors.New("not visible"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			models := &questionConfigModels{model: tt.model, err: tt.err}
			h := &InitializationHandler{modelService: models}
			requested := " questions "
			id, err := h.resolveQuestionGenerationModel(context.Background(), &requested, nil)
			if tt.valid {
				require.NoError(t, err)
				require.Equal(t, "questions", id)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, []string{"questions"}, models.ids)
		})
	}
}

func TestQuestionModelConfigResponseRoundTrip(t *testing.T) {
	kb := &types.KnowledgeBase{SummaryModelID: "summary", QuestionGenerationConfig: &types.QuestionGenerationConfig{
		Enabled: true, ModelID: "questions", QuestionCount: 3,
	}}
	h := &InitializationHandler{}
	response := h.buildConfigResponse(context.Background(), nil, kb, false)
	question := response["questionGeneration"].(map[string]interface{})
	require.Equal(t, "questions", question["modelId"])
	require.Equal(t, "summary", kb.SummaryModelID)
	raw, err := kb.QuestionGenerationConfig.Value()
	require.NoError(t, err)
	var decoded types.QuestionGenerationConfig
	require.NoError(t, decoded.Scan(raw))
	require.Equal(t, *kb.QuestionGenerationConfig, decoded)
}

func TestUpdateKnowledgeBaseQuestionModelPresence(t *testing.T) {
	for _, tc := range []struct {
		body     string
		provided bool
		id       string
	}{
		{`{"name":"kb","config":{"question_generation_config":{"enabled":true}}}`, false, ""},
		{`{"name":"kb","config":{"question_generation_config":{"model_id":""}}}`, true, ""},
		{`{"name":"kb","config":{"question_generation_config":{"model_id":"questions"}}}`, true, "questions"},
	} {
		var req UpdateKnowledgeBaseRequest
		require.NoError(t, json.Unmarshal([]byte(tc.body), &req))
		require.Equal(t, tc.provided, req.questionModelProvided)
		require.Equal(t, tc.id, req.Config.QuestionGenerationConfig.ModelID)
	}
	kb := &types.KnowledgeBase{QuestionGenerationConfig: &types.QuestionGenerationConfig{ModelID: "questions"}}
	merged, err := applyKnowledgeBasePatch(kb, PatchKnowledgeBaseRequest{}, map[string]json.RawMessage{
		"question_generation_config": json.RawMessage(`{"enabled":true,"question_count":5}`),
	})
	require.NoError(t, err)
	require.Equal(t, "questions", merged.Config.QuestionGenerationConfig.ModelID)
	merged, err = applyKnowledgeBasePatch(kb, PatchKnowledgeBaseRequest{}, map[string]json.RawMessage{
		"question_generation_config": json.RawMessage(`{"model_id":""}`),
	})
	require.NoError(t, err)
	require.Empty(t, merged.Config.QuestionGenerationConfig.ModelID)
}
