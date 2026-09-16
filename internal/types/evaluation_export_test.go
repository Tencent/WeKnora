package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeEvaluationExportFormat(t *testing.T) {
	require.Equal(t, 500, EvaluationExportPageSize)
	require.Equal(t, 50_000, EvaluationExportMaxQuestions)
	require.Equal(t, int64(256*1024*1024), EvaluationExportMaxBytes)
	format, err := NormalizeEvaluationExportFormat(" JSON ")
	require.NoError(t, err)
	require.Equal(t, EvaluationExportFormatJSON, format)

	_, err = NormalizeEvaluationExportFormat("xlsx")
	require.ErrorIs(t, err, ErrEvaluationExportFormatInvalid)
}

func TestIsEvaluationTerminalStatus(t *testing.T) {
	for _, status := range []EvaluationStatue{
		EvaluationStatueSuccess,
		EvaluationStatueFailed,
		EvaluationStatueTimedOut,
		EvaluationStatueInterrupted,
		EvaluationStatueCanceled,
	} {
		require.True(t, IsEvaluationTerminalStatus(status))
	}
	for _, status := range []EvaluationStatue{
		EvaluationStatuePending,
		EvaluationStatueRunning,
	} {
		require.False(t, IsEvaluationTerminalStatus(status))
	}
}
