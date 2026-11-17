package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fabriTextChatBase interface{ chat.Chat }

type fabriTextChat struct {
	fabriTextChatBase
	messages []chat.Message
	response *types.ChatResponse
	err      error
}

func (m *fabriTextChat) Chat(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (*types.ChatResponse, error) {
	m.messages = messages
	return m.response, m.err
}

type fabriTextModels struct {
	interfaces.ModelService
	model chat.Chat
	calls int
}

func (m *fabriTextModels) GetChatModel(context.Context, string) (chat.Chat, error) {
	m.calls++
	return m.model, nil
}

func fabriTextFixture() (*InitializationHandler, *fabriTextChat, *types.GraphSchema) {
	model := &fabriTextChat{response: &types.ChatResponse{Content: "A generated passage.", FinishReason: "stop"}}
	handler := &InitializationHandler{config: &config.Config{
		ExtractManager: &config.ExtractManagerConfig{FabriText: &config.FebriText{
			WithNoTag: "Generate any passage.", WithTag: "Generate a passage using %s.",
		}},
	}}
	schema := &types.GraphSchema{
		Enabled: true,
		EntityTypes: []types.GraphEntityType{
			{Name: "Person", Description: "A named person"}, {Name: "Company"},
		},
		RelationTypes: []types.GraphRelationType{
			{Name: "WORKS_AT", SourceType: "Person", TargetType: "Company", Description: "Employment"},
		},
	}
	return handler, model, schema
}

func TestFabriTextSchemaAndLegacyPrompts(t *testing.T) {
	h, model, schema := fabriTextFixture()
	for _, enabled := range []bool{true, false} {
		schema.Enabled = enabled
		result, err := h.fabriText(context.Background(), []string{"LegacyTag"}, schema, model)
		require.NoError(t, err)
		require.Equal(t, model.response.Content, result)
		prompt := model.messages[0].Content
		if enabled {
			data, err := json.Marshal(schema)
			require.NoError(t, err)
			require.Contains(t, prompt, string(data))
			require.Contains(t, prompt, "source_type, target_type and direction")
			require.Contains(t, prompt, "Return only the passage")
			require.NotContains(t, prompt, "LegacyTag")
		} else {
			require.Equal(t, `Generate a passage using ["LegacyTag"].`, prompt)
		}
	}
	_, err := h.fabriText(context.Background(), nil, nil, model)
	require.NoError(t, err)
	require.Equal(t, "Generate any passage.", model.messages[0].Content)
	schema.Enabled = true
	schema.RelationTypes = nil
	_, err = h.fabriText(context.Background(), nil, schema, model)
	require.NoError(t, err)
	require.Contains(t, model.messages[0].Content, "describe independent named entities")
}

func TestFabriTextRejectsInvalidSchemaBeforeModelLookup(t *testing.T) {
	h, model, _ := fabriTextFixture()
	models := &fabriTextModels{model: model}
	h.modelService = models
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(
		`{"model_id":"test","schema":{"enabled":true,"entity_types":[]}}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")
	h.FabriText(c)
	require.Len(t, c.Errors, 1)
	require.Contains(t, c.Errors[0].Error(), "invalid graph schema")
	require.Zero(t, models.calls)
}

func TestFabriTextHandlerAcceptsSchema(t *testing.T) {
	h, model, schema := fabriTextFixture()
	models := &fabriTextModels{model: model}
	h.modelService = models
	data, err := json.Marshal(FabriTextRequest{ModelID: "test", Schema: schema})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
	c.Request.Header.Set("Content-Type", "application/json")
	h.FabriText(c)
	require.Empty(t, c.Errors)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, models.calls)
	require.Contains(t, recorder.Body.String(), model.response.Content)
	require.Contains(t, model.messages[0].Content, "WORKS_AT")
}

func TestFabriTextRejectsUnusableResponses(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response *types.ChatResponse
		err      error
	}{
		{name: "nil"},
		{name: "empty", response: &types.ChatResponse{Content: " \n "}},
		{name: "truncated", response: &types.ChatResponse{Content: "partial", FinishReason: "length"}},
		{name: "provider error", err: fmt.Errorf("provider unavailable")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, model, schema := fabriTextFixture()
			model.response, model.err = tt.response, tt.err
			_, err := h.fabriText(context.Background(), nil, schema, model)
			require.Error(t, err)
		})
	}
}
