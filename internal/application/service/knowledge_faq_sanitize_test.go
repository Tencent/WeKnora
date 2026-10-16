package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSanitizeFAQEntryPayloadCleansQuestionLists(t *testing.T) {
	meta, err := sanitizeFAQEntryPayload(&types.FAQEntryPayload{
		StandardQuestion:  "  How do I reset my password?  ",
		SimilarQuestions:  []string{"", "  Forgot password  ", "Forgot password", "   "},
		NegativeQuestions: []string{" ", "Reset username", " Reset username "},
		Answers:           []string{"  Use the reset link.  ", "", "Use the reset link."},
	})
	require.NoError(t, err)
	require.Equal(t, "How do I reset my password?", meta.StandardQuestion)
	require.Equal(t, []string{"Forgot password"}, meta.SimilarQuestions)
	require.Equal(t, []string{"Reset username"}, meta.NegativeQuestions)
	require.Equal(t, []string{"Use the reset link."}, meta.Answers)
}

func TestSanitizeFAQEntryPayloadKeepsOriginalText(t *testing.T) {
	// Storage keeps what the user wrote: only whitespace and duplicates are
	// dropped, no lower-casing or punctuation stripping (that is Normalize,
	// which is for hashing and indexing).
	meta, err := sanitizeFAQEntryPayload(&types.FAQEntryPayload{
		StandardQuestion: "What is WeKnora?",
		SimilarQuestions: []string{"Tell me about WeKnora!"},
		Answers:          []string{"A RAG framework."},
	})
	require.NoError(t, err)
	require.Equal(t, "What is WeKnora?", meta.StandardQuestion)
	require.Equal(t, []string{"Tell me about WeKnora!"}, meta.SimilarQuestions)
}

func TestSanitizeFAQEntryPayloadRejectsBlankAnswers(t *testing.T) {
	_, err := sanitizeFAQEntryPayload(&types.FAQEntryPayload{
		StandardQuestion: "How do I reset my password?",
		Answers:          []string{"  ", ""},
	})
	require.Error(t, err)
}
