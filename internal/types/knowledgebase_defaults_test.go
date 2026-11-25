package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A FAQ knowledge base created through the API arrives without FAQConfig.
// EnsureDefaults must still apply the indexing-strategy defaults: retrieval
// gates vector recall on IsVectorEnabled(), and a FAQ KB whose strategy stays
// all-false is never retrieved by /faq/search or knowledge chat.
func TestEnsureDefaults_FAQKeepsDefaultIndexingStrategy(t *testing.T) {
	kb := &KnowledgeBase{Type: KnowledgeBaseTypeFAQ}

	kb.EnsureDefaults()

	require.NotNil(t, kb.FAQConfig)
	require.Equal(t, FAQIndexModeQuestionAnswer, kb.FAQConfig.IndexMode)
	require.Equal(t, FAQQuestionIndexModeCombined, kb.FAQConfig.QuestionIndexMode)
	require.True(t, kb.IndexingStrategy.VectorEnabled, "FAQ KB must keep vector indexing enabled")
	require.True(t, kb.IndexingStrategy.KeywordEnabled, "FAQ KB must keep keyword indexing enabled")
}

// An explicitly configured strategy must survive EnsureDefaults.
func TestEnsureDefaults_FAQKeepsExplicitIndexingStrategy(t *testing.T) {
	kb := &KnowledgeBase{
		Type:             KnowledgeBaseTypeFAQ,
		IndexingStrategy: IndexingStrategy{VectorEnabled: true, KeywordEnabled: false},
	}

	kb.EnsureDefaults()

	require.True(t, kb.IndexingStrategy.VectorEnabled)
	require.False(t, kb.IndexingStrategy.KeywordEnabled)
}
