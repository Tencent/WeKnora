package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateKnowledgeQARequestParsesPromptContext(t *testing.T) {
	const promptContext = "[Host context]\n{\"error\":\"ERR_500\",\"details\":\"line one\nline two <>&\\\"\"}"
	raw, err := json.Marshal(map[string]string{
		"query":          "reset password",
		"prompt_context": promptContext,
	})
	require.NoError(t, err)

	var request CreateKnowledgeQARequest
	require.NoError(t, json.Unmarshal(raw, &request))
	assert.Equal(t, promptContext, request.PromptContext)
}

func TestBuildQARequestKeepsPromptContextSeparateFromQuery(t *testing.T) {
	const (
		query         = "How do I reset my password?"
		promptContext = "[Host context]\n{\"page\":\"checkout\",\"error\":\"ERR_500\"}"
	)
	rc := &qaRequestContext{
		query:            query,
		promptContext:    promptContext,
		assistantMessage: &types.Message{ID: "assistant-1"},
	}

	request := rc.buildQARequest()
	assert.Equal(t, query, request.Query)
	assert.Equal(t, promptContext, request.QuotedContext)
	assert.NotContains(t, request.Query, "checkout")
	assert.NotContains(t, request.Query, "ERR_500")
}

func TestQuotedContextRemainsPromptOnlyForKnowledgeQA(t *testing.T) {
	const (
		query         = "How do I reset my password?"
		promptContext = "[Host context]\n{\"page\":\"checkout\",\"error\":\"ERR_500\"}"
	)

	queryManager := chatpipeline.NewEventManager()
	chatpipeline.NewPluginQueryUnderstand(queryManager, nil, nil, &config.Config{})
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:         query,
			EnableRewrite: false,
		},
		PipelineState: types.PipelineState{QuotedContext: promptContext},
	}

	require.Nil(t, queryManager.Trigger(context.Background(), types.QUERY_UNDERSTAND, chatManage))
	assert.Equal(t, query, chatManage.RewriteQuery)
	assert.NotContains(t, chatManage.RewriteQuery, "checkout")
	assert.NotContains(t, chatManage.RewriteQuery, "ERR_500")

	promptManager := chatpipeline.NewEventManager()
	chatpipeline.NewPluginIntoChatMessage(promptManager, nil)
	chatManage.SummaryConfig.ContextTemplate = "Question: {{query}}\nSources: {{contexts}}"
	require.Nil(t, promptManager.Trigger(context.Background(), types.INTO_CHAT_MESSAGE, chatManage))
	assert.Contains(t, chatManage.UserContent, query)
	assert.Contains(t, chatManage.UserContent, promptContext)
	assert.True(t, strings.HasSuffix(chatManage.UserContent, promptContext))
}

func TestBuildQARequestWithoutPromptContextIsUnchanged(t *testing.T) {
	rc := &qaRequestContext{
		query:            "hello",
		assistantMessage: &types.Message{ID: "assistant-1"},
	}

	request := rc.buildQARequest()
	assert.Equal(t, "hello", request.Query)
	assert.Empty(t, request.QuotedContext)
}
