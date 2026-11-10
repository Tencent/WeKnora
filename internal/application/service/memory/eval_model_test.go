package memory

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// newEvalChatModel builds a bare OpenAI-compatible client from the
// environment. The eval harness deliberately does not go through ModelService:
// scoring a prompt should not require a database, a workspace or a configured
// model row — just an endpoint.
func newEvalChatModel(modelID string) (chat.Chat, error) {
	baseURL := strings.TrimSpace(firstNonEmpty(
		os.Getenv("WEKNORA_MEMORY_EVAL_BASE_URL"),
		os.Getenv("OPENAI_BASE_URL"),
	))
	apiKey := strings.TrimSpace(firstNonEmpty(
		os.Getenv("WEKNORA_MEMORY_EVAL_API_KEY"),
		os.Getenv("OPENAI_API_KEY"),
	))
	if baseURL == "" {
		return nil, errors.New("set WEKNORA_MEMORY_EVAL_BASE_URL (or OPENAI_BASE_URL)")
	}
	return chat.NewChat(&chat.ChatConfig{
		Source:    types.ModelSourceRemote,
		ModelName: modelID,
		BaseURL:   baseURL,
		APIKey:    apiKey,
	}, nil)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// runEvalExtraction issues one distillation call and parses it the same way the
// product does, so the score reflects the whole path rather than the prompt in
// isolation.
func runEvalExtraction(
	ctx context.Context, chatModel chat.Chat, userPrompt string,
) ([]extractionDecision, error) {
	response, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "system", Content: extractionSystemPrompt},
		{Role: "user", Content: userPrompt},
	}, &chat.ChatOptions{
		Temperature:         0,
		MaxCompletionTokens: 1200,
		Format:              extractionSchema,
	})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("empty response")
	}
	parsed, err := parseExtractionResponse(response.Content)
	return parsed.Memories, err
}
