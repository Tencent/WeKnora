package core

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type recursiveEngineFixture struct {
	fullSyncOps
	state     *RecursiveState
	refreshed int
}

func (o *recursiveEngineFixture) RecursiveSyncState() *RecursiveState { return o.state }
func (o *recursiveEngineFixture) RefreshUnchanged(_ context.Context, _ *Client,
	_ string, resource string,
) ([]*types.FetchedItem, error) {
	o.refreshed++
	o.state.Present["recursive:docx:new-child"] = struct{}{}
	return []*types.FetchedItem{{ExternalID: "recursive:docx:new-child", SourceResourceID: resource}}, nil
}

func TestRecursiveEngineRefreshAndDeletion(t *testing.T) {
	t.Setenv("FEISHU_RECURSIVE_LINKS", "true")
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial"}[partial], func(t *testing.T) {
			state := NewRecursiveState()
			state.LoadPrevious([]string{"recursive:docx:removed-child"})
			ops := &recursiveEngineFixture{
				fullSyncOps: fullSyncOps{nodes: map[string][]string{"resource": {"parent"}}, partial: partial},
				state:       state,
			}
			cursor := ops.EncodeCursor(map[string]map[string]string{"resource": {"parent": "100"}}, time.Now())
			handler := &CollectHandler{}
			_, err := FetchStreamEngine(context.Background(), nil,
				&types.DataSourceConfig{ResourceIDs: []string{"resource"}}, cursor, handler, ops)
			require.NoError(t, err)
			require.Equal(t, 1, ops.refreshed)
			require.Empty(t, ops.fetched, "unchanged parent must not be fetched again")
			deleted := false
			for _, item := range handler.items {
				if item.ExternalID == "recursive:docx:removed-child" && item.IsDeleted {
					deleted = true
				}
			}
			require.Equal(t, !partial, deleted, "incomplete listings must not remove prior linked resources")
		})
	}
}

func TestRecursiveCheckpointRetainsPreviousIdentities(t *testing.T) {
	state := NewRecursiveState()
	state.LoadPrevious([]string{"old"})
	state.Present["new"] = struct{}{}
	require.Equal(t, []string{"new", "old"}, state.CursorIDs())
	state.CommitPresent()
	require.Equal(t, []string{"new"}, state.CursorIDs())
}
