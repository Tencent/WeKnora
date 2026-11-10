package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The table metadata calls and the auto-tag classifier ride the knowledge
// base summary model and used to carry fixed budgets (512 / 2048 / 1024) that
// KNOWLEDGE_BASE_PROFILE_MAX_TOKENS could not reach. Reasoning-style summary
// models can spend a small fixed budget on thinking and return an empty
// completion, which fails the whole table ingestion (see
// knowledgebase_profile.go and issue #4221). These tests pin all three call
// sites to the shared, operator-tunable budget.

func TestTableMetadataBudgetDefaultAndEnv(t *testing.T) {
	service := &DataTableSummaryService{}

	t.Run("table description", func(t *testing.T) {
		t.Setenv(knowledgeBaseProfileMaxTokensEnv, "")
		model := &budgetCaptureChatModel{response: "monthly order volume by region"}
		_, err := service.generateTableDescription(context.Background(), model,
			"orders", "id INT, region TEXT", "id=1,region=alpha", "")
		require.NoError(t, err)
		require.Len(t, model.options, 1)
		assert.Equal(t, knowledgeBaseProfileDefaultMaxTokens, model.options[0].MaxTokens,
			"default budget reaches the table description call")
		require.NotNil(t, model.options[0].Thinking)
		assert.False(t, *model.options[0].Thinking, "thinking stays disabled")

		t.Setenv(knowledgeBaseProfileMaxTokensEnv, "8192")
		model = &budgetCaptureChatModel{response: "monthly order volume by region"}
		_, err = service.generateTableDescription(context.Background(), model,
			"orders", "id INT, region TEXT", "id=1,region=alpha", "")
		require.NoError(t, err)
		require.Len(t, model.options, 1)
		assert.Equal(t, 8192, model.options[0].MaxTokens,
			"env override reaches the table description call")
	})

	t.Run("column descriptions", func(t *testing.T) {
		t.Setenv(knowledgeBaseProfileMaxTokensEnv, "")
		model := &budgetCaptureChatModel{response: "id: primary key"}
		_, err := service.generateColumnDescriptions(context.Background(), model,
			"orders", "id INT, region TEXT", "id=1,region=alpha", "")
		require.NoError(t, err)
		require.Len(t, model.options, 1)
		assert.Equal(t, knowledgeBaseProfileDefaultMaxTokens, model.options[0].MaxTokens,
			"default budget reaches the column descriptions call")
		require.NotNil(t, model.options[0].Thinking)
		assert.False(t, *model.options[0].Thinking, "thinking stays disabled")

		t.Setenv(knowledgeBaseProfileMaxTokensEnv, "8192")
		model = &budgetCaptureChatModel{response: "id: primary key"}
		_, err = service.generateColumnDescriptions(context.Background(), model,
			"orders", "id INT, region TEXT", "id=1,region=alpha", "")
		require.NoError(t, err)
		require.Len(t, model.options, 1)
		assert.Equal(t, 8192, model.options[0].MaxTokens,
			"env override reaches the column descriptions call")
	})
}

func TestAutoTagClassificationBudgetDefaultAndEnv(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "t1", Name: "alpha"}, {ID: "t2", Name: "beta"}}

	t.Setenv(knowledgeBaseProfileMaxTokensEnv, "")
	model := &budgetCaptureChatModel{response: `{"matches":[{"index":1,"confidence":0.9}]}`}
	_, err := classifyExistingTags(context.Background(), model, tags, "document body", 5)
	require.NoError(t, err)
	require.Len(t, model.options, 1)
	assert.Equal(t, knowledgeBaseProfileDefaultMaxTokens, model.options[0].MaxTokens,
		"default budget reaches the classifier")
	require.NotNil(t, model.options[0].Thinking)
	assert.False(t, *model.options[0].Thinking, "thinking stays disabled")

	t.Setenv(knowledgeBaseProfileMaxTokensEnv, "4096")
	model = &budgetCaptureChatModel{response: `{"matches":[]}`}
	_, err = classifyExistingTags(context.Background(), model, tags, "document body", 5)
	require.NoError(t, err)
	require.Len(t, model.options, 1)
	assert.Equal(t, 4096, model.options[0].MaxTokens,
		"env override reaches the classifier")
}

// An empty completion still has to fail the table metadata calls — that is
// the ingestion failure operators see in the logs; the shared budget is the
// knob that prevents it.
func TestTableMetadataEmptyCompletionStillFails(t *testing.T) {
	service := &DataTableSummaryService{}

	blank := &budgetCaptureChatModel{response: "   "}
	_, err := service.generateTableDescription(context.Background(), blank,
		"orders", "id INT, region TEXT", "id=1,region=alpha", "")
	require.Error(t, err)

	empty := &budgetCaptureChatModel{response: ""}
	_, err = service.generateColumnDescriptions(context.Background(), empty,
		"orders", "id INT, region TEXT", "id=1,region=alpha", "")
	require.Error(t, err)
}
