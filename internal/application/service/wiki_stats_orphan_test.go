package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// GetStats reads CountOrphans, whose predicate tests in_links for emptiness.
// On PostgreSQL that predicate has to compare the column as jsonb: the column
// is json, which has no equality operator, so the whole call used to abort
// with SQLSTATE 42883 before any row was read. Both backends run here because
// only postgres builds that SQL — the sqlite branch never did.
func TestGetStatsCountsOrphansOnEveryBackend(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		// in_links NULL and in_links '[]' take different sides of the
		// predicate, and both must survive the planner.
		seedBacklinkPage(t, h, "entity/null-in-links", "没有入链的页面。", nil, nil)
		seedBacklinkPage(t, h, "entity/empty-in-links", "入链是空数组的页面。", []string{}, nil)
		seedBacklinkPage(t, h, "entity/linked", "被引用的页面。", []string{"entity/source"}, nil)

		stats, err := h.svc.GetStats(context.Background(), backlinkTestKB)
		require.NoError(t, err)
		require.EqualValues(t, 3, stats.TotalPages)
		require.EqualValues(t, 2, stats.OrphanCount)
	})
}
