package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// budgetCaptureChatModel records the options the real call sites pass so the
// budget plumbing can be asserted end-to-end through generateQuestionsWithContext.
type budgetCaptureChatModel struct {
	response string
	options  []*chat.ChatOptions
}

func (m *budgetCaptureChatModel) Chat(
	_ context.Context, _ []chat.Message, opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	if opts != nil {
		copyOpts := *opts
		m.options = append(m.options, &copyOpts)
	}
	return &types.ChatResponse{Content: m.response}, nil
}

func (m *budgetCaptureChatModel) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}
func (m *budgetCaptureChatModel) GetModelName() string { return "budget" }
func (m *budgetCaptureChatModel) GetModelID() string   { return "budget" }

func newQuestionBudgetService() *knowledgeService {
	return &knowledgeService{config: &config.Config{Conversation: &config.ConversationConfig{
		GenerateQuestionsPrompt: "Generate {{question_count}} questions:\n{{content}}",
	}}}
}

func TestGenerateQuestionsMaxTokensDefaultAndEnv(t *testing.T) {
	t.Setenv(generateQuestionsMaxTokensEnv, "")
	assert.Equal(t, 2048, generateQuestionsMaxTokens(), "default when unset")

	t.Setenv(generateQuestionsMaxTokensEnv, "4096")
	assert.Equal(t, 4096, generateQuestionsMaxTokens(), "explicit override applied")

	for _, bad := range []string{"abc", "0", "-5", "3.5", " "} {
		t.Setenv(generateQuestionsMaxTokensEnv, bad)
		assert.Equal(t, 2048, generateQuestionsMaxTokens(),
			"invalid value %q must fall back to the default", bad)
	}
}

func TestGenerateQuestionsWithContextUsesConfiguredBudget(t *testing.T) {
	s := newQuestionBudgetService()

	t.Setenv(generateQuestionsMaxTokensEnv, "")
	model := &budgetCaptureChatModel{response: "1. First question?\n2. Second question?"}
	_, err := s.generateQuestionsWithContext(context.Background(), model,
		"chunk body text", "", "", "doc.pdf", 2, "")
	require.NoError(t, err)
	require.Len(t, model.options, 1)
	assert.Equal(t, 2048, model.options[0].MaxTokens, "default budget reaches the model call")

	t.Setenv(generateQuestionsMaxTokensEnv, "8192")
	model = &budgetCaptureChatModel{response: "1. First question?\n2. Second question?"}
	_, err = s.generateQuestionsWithContext(context.Background(), model,
		"chunk body text", "", "", "doc.pdf", 2, "")
	require.NoError(t, err)
	require.Len(t, model.options, 1)
	assert.Equal(t, 8192, model.options[0].MaxTokens, "env override reaches the model call")
}

// An empty completion must keep returning (no questions, no error): the batch
// callers count it and surface a warning, they do not fail the task.
func TestGenerateQuestionsWithContextEmptyCompletionIsCountedNotFailed(t *testing.T) {
	s := newQuestionBudgetService()
	model := &budgetCaptureChatModel{response: ""}
	questions, err := s.generateQuestionsWithContext(context.Background(), model,
		"chunk body text", "", "", "doc.pdf", 3, "")
	require.NoError(t, err)
	assert.Empty(t, questions)
}
