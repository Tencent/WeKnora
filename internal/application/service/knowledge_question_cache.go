package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/inferencecache"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

const documentQuestionCacheVersion = "v1"

var errEmptyDocumentQuestions = errors.New("question model returned no valid questions")

// documentQuestionCacheInput contains the exact provider request after prompt
// rendering plus every request option that can affect the generated questions.
// The rendered prompt already contains the chunk, its neighbors, document
// title, language, requested count and custom instructions.
type documentQuestionCacheInput struct {
	Version       string  `json:"version"`
	Prompt        string  `json:"prompt"`
	QuestionCount int     `json:"question_count"`
	Temperature   float64 `json:"temperature"`
	MaxTokens     int     `json:"max_tokens"`
	Thinking      bool    `json:"thinking"`
}

func documentQuestionCacheKey(
	ctx context.Context,
	questionModel chat.Chat,
	input documentQuestionCacheInput,
) string {
	tenantID, _ := types.TenantIDFromContext(ctx)
	input.Version = documentQuestionCacheVersion
	input.Prompt = searchutil.CanonicalizeImageURLsForModel(input.Prompt)
	encoded, _ := json.Marshal(input)
	return inferencecache.Key(
		"document.questions",
		tenantID,
		chat.FingerprintOf(questionModel),
		[]byte(documentQuestionCacheVersion),
		encoded,
	)
}

func normalizeDocumentQuestions(questions []string) ([]string, error) {
	normalized := make([]string, 0, len(questions))
	for _, question := range questions {
		question = strings.TrimSpace(question)
		if question != "" {
			normalized = append(normalized, question)
		}
	}
	if len(normalized) == 0 {
		return nil, errEmptyDocumentQuestions
	}
	return normalized, nil
}

// resolveDocumentQuestionsValue caches only validated, non-empty question
// lists. Provider/parse errors and empty results are never retained. A valid
// JSON but semantically empty legacy/corrupt entry is evicted and recomputed.
func resolveDocumentQuestionsValue(
	ctx context.Context,
	cache inferencecache.Cache,
	key string,
	loader func(context.Context) ([]string, error),
) ([]string, inferencecache.Stats, error) {
	validatedLoader := func(loadCtx context.Context) ([]string, error) {
		questions, err := loader(loadCtx)
		if err != nil {
			return nil, err
		}
		return normalizeDocumentQuestions(questions)
	}

	if cache == nil {
		questions, err := validatedLoader(ctx)
		return questions, inferencecache.Stats{}, err
	}

	questions, stats, err := inferencecache.ResolveJSON(ctx, cache, key, validatedLoader)
	if err != nil {
		return nil, stats, err
	}
	questions, validationErr := normalizeDocumentQuestions(questions)
	if validationErr == nil {
		return questions, stats, nil
	}

	_ = cache.Invalidate(ctx, key)
	questions, retryStats, err := inferencecache.ResolveJSON(ctx, cache, key, validatedLoader)
	stats.Hit = false
	stats.Coalesced = retryStats.Coalesced
	stats.WriteError = retryStats.WriteError
	if stats.ReadError == nil {
		stats.ReadError = validationErr
	}
	if err != nil {
		return nil, stats, err
	}
	questions, err = normalizeDocumentQuestions(questions)
	return questions, stats, err
}
