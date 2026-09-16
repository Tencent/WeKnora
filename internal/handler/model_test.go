package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubUpdateModelService struct {
	interfaces.ModelService
	stored  *types.Model
	updated *types.Model
}

func (s *stubUpdateModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	return s.stored, nil
}

func (s *stubUpdateModelService) UpdateModel(_ context.Context, model *types.Model) error {
	s.updated = model
	return nil
}

func TestModelUpdateRequestDisplayNamePresence(t *testing.T) {
	var omitted UpdateModelRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"gpt-4o"}`), &omitted))
	assert.Nil(t, omitted.DisplayName)

	var cleared UpdateModelRequest
	require.NoError(t, json.Unmarshal([]byte(`{"display_name":""}`), &cleared))
	require.NotNil(t, cleared.DisplayName)
	assert.Equal(t, "", *cleared.DisplayName)
}

func TestUpdateModelMergesExtraConfigForSameProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &stubUpdateModelService{stored: &types.Model{
		ID:   "model-1",
		Name: "model",
		Parameters: types.ModelParameters{
			Provider: "generic",
			ExtraConfig: map[string]string{
				"thinking_control": "none",
				"api_version":      "2024-10-21",
			},
		},
	}}
	body, err := json.Marshal(UpdateModelRequest{
		Name: "model",
		Parameters: types.ModelParameters{
			Provider:    "generic",
			ExtraConfig: map[string]string{"thinking_control": "enabled"},
		},
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/models/model-1", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "model-1"}}

	NewModelHandler(service).UpdateModel(c)

	require.Empty(t, c.Errors)
	require.NotNil(t, service.updated)
	assert.Equal(t, "enabled", service.updated.Parameters.ExtraConfig["thinking_control"])
	assert.Equal(t, "2024-10-21", service.updated.Parameters.ExtraConfig["api_version"])
}

func TestUpdateModelDoesNotMergeExtraConfigAcrossProviders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &stubUpdateModelService{stored: &types.Model{
		ID:   "model-1",
		Name: "model",
		Parameters: types.ModelParameters{
			Provider:    "azure",
			ExtraConfig: map[string]string{"api_version": "2024-10-21"},
		},
	}}
	body, err := json.Marshal(UpdateModelRequest{
		Name: "model",
		Parameters: types.ModelParameters{
			Provider:    "generic",
			ExtraConfig: map[string]string{"thinking_control": "enabled"},
		},
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/models/model-1", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "model-1"}}

	NewModelHandler(service).UpdateModel(c)

	require.Empty(t, c.Errors)
	require.NotNil(t, service.updated)
	assert.Equal(t, map[string]string{"thinking_control": "enabled"}, service.updated.Parameters.ExtraConfig)
}

func TestParseModelDebugOptionsPreservesExplicitThinkingFalse(t *testing.T) {
	opts, err := parseModelDebugOptions(`{"thinking":false,"temperature":0,"max_tokens":256}`)
	require.NoError(t, err)
	require.NotNil(t, opts.Thinking)
	assert.False(t, *opts.Thinking)
	require.NotNil(t, opts.Temperature)
	assert.Zero(t, *opts.Temperature)
	require.NotNil(t, opts.MaxTokens)
	assert.Equal(t, 256, *opts.MaxTokens)
}

func TestParseModelDebugOptionsRejectsOutOfRangeValues(t *testing.T) {
	_, err := parseModelDebugOptions(`{"top_p":0}`)
	require.ErrorContains(t, err, "top_p")
}

func TestRedactedDebugConfig(t *testing.T) {
	got := redactedDebugConfig(map[string]string{
		"thinking_control": "enable_thinking",
		"secret_key":       "do-not-leak",
		"access_token":     "do-not-leak-either",
	})
	assert.Equal(t, "enable_thinking", got["thinking_control"])
	assert.Equal(t, "[REDACTED]", got["secret_key"])
	assert.Equal(t, "[REDACTED]", got["access_token"])
}

func TestConsumeModelDebugChatStream(t *testing.T) {
	stream := make(chan types.StreamResponse, 5)
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeThinking, Content: "reason "}
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeThinking, Content: "more", Done: true}
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "answer "}
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "done"}
	stream <- types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer,
		Done:         true,
		FinishReason: "stop",
		Usage:        &types.TokenUsage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
	}
	close(stream)

	got, err := consumeModelDebugChatStream(stream)
	require.NoError(t, err)
	assert.Equal(t, "reason more", got.ReasoningContent)
	assert.Equal(t, "answer done", got.Content)
	assert.Equal(t, "stop", got.FinishReason)
	require.NotNil(t, got.Usage)
	assert.Equal(t, 7, got.Usage.TotalTokens)
	assert.Len(t, got.StreamEvents, 5)
}
