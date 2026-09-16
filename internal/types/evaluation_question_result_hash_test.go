package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvaluationQuestionResultHashIncludesRuntimeFacts(t *testing.T) {
	input := &EvaluationQuestionResultInput{SampleIndex: 1, QID: "q1", Status: EvaluationQuestionStatusSuccess}
	baseline := EvaluationQuestionResultHash(input)

	totalMs := int64(17)
	input.TotalMs = &totalMs
	require.NotEqual(t, baseline, EvaluationQuestionResultHash(input))

	withDuration := EvaluationQuestionResultHash(input)
	totalTokens := 9
	input.TotalTokens = &totalTokens
	input.UsageReported = true
	require.NotEqual(t, withDuration, EvaluationQuestionResultHash(input))
}
