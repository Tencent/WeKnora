package types

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeEvaluationTaskLabels(t *testing.T) {
	labels, err := NormalizeEvaluationTaskLabels([]string{"  Baseline ", "检索"}, 20)
	require.NoError(t, err)
	assert.Equal(t, []string{"baseline", "检索"}, labels)

	_, err = NormalizeEvaluationTaskLabels([]string{"Baseline", " baseline "}, 20)
	require.ErrorIs(t, err, ErrEvaluationTaskLabelInvalid)

	_, err = NormalizeEvaluationTaskLabels([]string{strings.Repeat("界", 22)}, 20)
	require.ErrorIs(t, err, ErrEvaluationTaskLabelInvalid)
}

func TestEvaluationTaskListCursorBindsAllFilters(t *testing.T) {
	status := EvaluationStatueSuccess
	startedFrom := time.Date(2026, 8, 30, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	filters := EvaluationTaskListFilters{
		Status:           &status,
		DatasetID:        "dataset",
		DatasetVersionID: "version",
		ModelID:          "model",
		StartedFrom:      &startedFrom,
		Labels:           []string{"baseline", "retrieval"},
	}
	cursor, err := EncodeEvaluationTaskListCursor(
		EvaluationTaskKeyset{StartTime: startedFrom, ID: "task-2"},
		filters,
	)
	require.NoError(t, err)

	keyset, err := DecodeEvaluationTaskListCursor(cursor, filters)
	require.NoError(t, err)
	assert.Equal(t, "task-2", keyset.ID)
	assert.Equal(t, startedFrom.UTC(), keyset.StartTime)

	filters.Labels = []string{"baseline"}
	_, err = DecodeEvaluationTaskListCursor(cursor, filters)
	require.ErrorIs(t, err, ErrEvaluationTaskListInvalidCursor)
}
