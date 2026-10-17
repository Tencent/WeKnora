package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitBatchesNoLimitsKeepsOneRequest(t *testing.T) {
	got, err := SplitBatches([]string{"a", "b", "c"}, 0, BatchLimits{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 0, got[0].Start)
	assert.Len(t, got[0].Items, 3)
}

func TestSplitBatchesSplitsOnItemCount(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	got, err := SplitBatches(items, 0, BatchLimits{MaxItems: 2})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []Batch{
		{Start: 0, Items: items[0:2]},
		{Start: 2, Items: items[2:4]},
		{Start: 4, Items: items[4:5]},
	}, got)
}

// The query travels in every request, so it is charged to every batch rather
// than once to the whole call.
func TestSplitBatchesChargesTheQueryToEveryBatch(t *testing.T) {
	items := []string{strings.Repeat("x", 30), strings.Repeat("y", 30), strings.Repeat("z", 30)}
	got, err := SplitBatches(items, 40, BatchLimits{MaxTotalRunes: 100})
	require.NoError(t, err)
	// 40 + 30 + 30 = 100 fits; adding the third would reach 130.
	require.Len(t, got, 2)
	assert.Len(t, got[0].Items, 2)
	assert.Equal(t, 2, got[1].Start)
}

func TestSplitBatchesRejectsAnItemThatCannotFit(t *testing.T) {
	_, err := SplitBatches([]string{"ok", strings.Repeat("x", 50)}, 0, BatchLimits{MaxItemRunes: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index 1")
}

// A document that fits the per-document limit but not beside the query is
// still unsendable; saying so beats looping forever or silently dropping it.
func TestSplitBatchesRejectsAnItemThatCannotFitBesideTheQuery(t *testing.T) {
	_, err := SplitBatches([]string{strings.Repeat("x", 80)}, 40, BatchLimits{MaxTotalRunes: 100})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not fit beside")
}

func TestSplitBatchesRejectsAQueryThatFillsTheBudget(t *testing.T) {
	_, err := SplitBatches([]string{"a"}, 100, BatchLimits{MaxTotalRunes: 100})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must also fit a document")
}

// Runes, not bytes: a CJK document is counted the way the vendor counts it.
func TestSplitBatchesCountsRunesNotBytes(t *testing.T) {
	got, err := SplitBatches([]string{strings.Repeat("中", 10)}, 0, BatchLimits{MaxItemRunes: 10})
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestSplitBatchesEmptyInput(t *testing.T) {
	got, err := SplitBatches(nil, 0, BatchLimits{MaxItems: 5})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSplitBatchesRepeatedQueryBudget(t *testing.T) {
	items := []string{strings.Repeat("中", 30), strings.Repeat("文", 30), strings.Repeat("字", 30)}
	for _, tc := range []struct {
		name   string
		limits BatchLimits
		starts []int
	}{
		{"per batch unchanged", BatchLimits{MaxTotalRunes: 100}, []int{0, 2}},
		{"per pair", BatchLimits{MaxTotalRunes: 100, RepeatFixedRunes: true}, []int{0, 1, 2}},
		{"exact pair budget", BatchLimits{MaxTotalRunes: 140, RepeatFixedRunes: true}, []int{0, 2}},
		{"item cap", BatchLimits{MaxItems: 1, MaxTotalRunes: 140, RepeatFixedRunes: true}, []int{0, 1, 2}},
		{"document cap excludes query", BatchLimits{
			MaxItemRunes: 30, MaxTotalRunes: 140, RepeatFixedRunes: true,
		}, []int{0, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SplitBatches(items, 40, tc.limits)
			require.NoError(t, err)
			var starts []int
			var restored []string
			for _, batch := range got {
				starts = append(starts, batch.Start)
				restored = append(restored, batch.Items...)
			}
			assert.Equal(t, tc.starts, starts)
			assert.Equal(t, items, restored)
		})
	}
	_, err := SplitBatches(items[:1], 40, BatchLimits{MaxTotalRunes: 69, RepeatFixedRunes: true})
	require.ErrorContains(t, err, "does not fit beside")
}
