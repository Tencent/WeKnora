package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestHasSuccessfulEvaluationChatCall(t *testing.T) {
	require.False(t, hasSuccessfulEvaluationChatCall(nil))
	require.False(t, hasSuccessfulEvaluationChatCall([]types.EvaluationModelCall{
		{ModelType: types.ModelTypeEmbedding, Success: true},
		{ModelType: types.ModelTypeKnowledgeQA, Success: false},
	}))
	require.True(t, hasSuccessfulEvaluationChatCall([]types.EvaluationModelCall{
		{ModelType: types.ModelTypeEmbedding, Success: false},
		{ModelType: types.ModelTypeKnowledgeQA, Success: true},
	}))
}
