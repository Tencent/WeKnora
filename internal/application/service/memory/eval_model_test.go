package memory

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	_ "github.com/Tencent/WeKnora/internal/models/invoke/adapters"
)

// newEvalModelConfig builds a bare OpenAI-compatible invoke config from the
// environment. The eval harness deliberately does not go through ModelService:
// scoring a prompt should not require a database, a workspace or a configured
// model row — just an endpoint.
func newEvalModelConfig(modelID string) (*invoke.ModelConfig, error) {
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
	return &invoke.ModelConfig{
		Provider:    string(invoke.DetectProvider(baseURL)),
		ModelName:   modelID,
		BaseURL:     baseURL,
		Credentials: invoke.Credentials{APIKey: apiKey},
	}, nil
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
	ctx context.Context, cfg *invoke.ModelConfig, userPrompt string,
) ([]extractionDecision, error) {
	response, err := invoke.Chat(ctx, cfg, &invoke.ChatOptions{
		Messages: []invoke.Message{
			invoke.TextMessage("system", extractionSystemPrompt),
			invoke.TextMessage("user", userPrompt),
		},
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
