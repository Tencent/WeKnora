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
