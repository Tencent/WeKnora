package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type fullSyncOps struct {
	nodes   map[string][]string
	partial bool
	fail    map[string]bool
	fetched []string
}

func (o *fullSyncOps) List(_ context.Context, _ *Client, resource string) ([]string, error, error) {
	if o.partial {
		return o.nodes[resource], errors.New("partial listing"), nil
	}
	return o.nodes[resource], nil, nil
}
func (*fullSyncOps) Token(n string) string  { return n }
func (*fullSyncOps) Title(n string) string  { return n }
func (*fullSyncOps) ObjType(string) string  { return "file" }
func (*fullSyncOps) EditTime(string) string { return "100" }
func (o *fullSyncOps) Fetch(_ context.Context, _ *Client, n, resource string) ([]*types.FetchedItem, error) {
	o.fetched = append(o.fetched, n)
	if o.fail[n] {
		return nil, errors.New("transient fetch failure")
	}
	return []*types.FetchedItem{{ExternalID: n, SourceResourceID: resource}}, nil
}
func (*fullSyncOps) ListFailureItems(string, error) []types.FetchedItem { return nil }
func (*fullSyncOps) ResourceNoun() string                               { return "nodes" }
func (*fullSyncOps) EmptyResourceIDsError() string                      { return "empty resources" }
func (*fullSyncOps) LogTag() string                                     { return "[test]" }
func (*fullSyncOps) DecodeCursorTimes(m map[string]interface{}) map[string]map[string]string {
	return m["times"].(map[string]map[string]string)
}

func (*fullSyncOps) EncodeCursor(m map[string]map[string]string, _ time.Time) *types.SyncCursor {
	snapshot := make(map[string]map[string]string)
	for resource, times := range m {
		snapshot[resource] = make(map[string]string)
		for k, v := range times {
			snapshot[resource][k] = v
		}
	}
	return &types.SyncCursor{ConnectorCursor: map[string]interface{}{"times": snapshot}}
}

type fullSyncHandler struct {
	CollectHandler
	checkpoint          *types.SyncCursor
	stopAfterCheckpoint bool
}

func (h *fullSyncHandler) Emit(ctx context.Context, item types.FetchedItem) error {
	if h.stopAfterCheckpoint && h.checkpoint != nil {
		return errors.New("interrupted")
	}
	return h.CollectHandler.Emit(ctx, item)
}

func (h *fullSyncHandler) Checkpoint(_ context.Context, cur *types.SyncCursor) error {
	h.checkpoint = cur
	return nil
}

func TestFullSyncRefetchesAndReconcilesDeletions(t *testing.T) {
	ops := &fullSyncOps{nodes: map[string][]string{"r": {"kept"}}}
	cfg := &types.DataSourceConfig{ResourceIDs: []string{"r"}}
	old := ops.EncodeCursor(map[string]map[string]string{"r": {"kept": "100", "gone": "100"}}, time.Now())
	h := &fullSyncHandler{}
	next, err := FetchFullStreamEngine(context.Background(), nil, cfg, old, h, ops)
	require.NoError(t, err)
	require.Equal(t, []string{"kept"}, ops.fetched)
	require.Len(t, h.items, 2)
	require.True(t, h.items[1].IsDeleted)
	require.Equal(t, "gone", h.items[1].ExternalID)
	require.Equal(t, map[string]string{"kept": "100"}, ops.DecodeCursorTimes(next.ConnectorCursor)["r"])
	require.Contains(t, ops.DecodeCursorTimes(old.ConnectorCursor)["r"], "gone", "input baseline must remain immutable")
}

func TestFullSyncFailuresRetryWithoutLosingDeletionIdentity(t *testing.T) {
	ops := &fullSyncOps{nodes: map[string][]string{"r": {"kept"}}, partial: true, fail: map[string]bool{"kept": true}}
	cfg := &types.DataSourceConfig{ResourceIDs: []string{"r"}}
	old := ops.EncodeCursor(map[string]map[string]string{"r": {"kept": "100", "unseen": "100"}}, time.Now())
	h := &fullSyncHandler{}
	next, err := FetchFullStreamEngine(context.Background(), nil, cfg, old, h, ops)
	require.NoError(t, err)
	require.Len(t, h.items, 1)
	require.NotEmpty(t, h.items[0].Metadata["error"])
	require.Contains(t, ops.DecodeCursorTimes(next.ConnectorCursor)["r"], "unseen")
	ops.partial = false
	ops.fail = nil
	ops.fetched = nil
	h = &fullSyncHandler{}
	_, err = FetchStreamEngine(context.Background(), nil, cfg, next, h, ops)
	require.NoError(t, err)
	require.Equal(t, []string{"kept"}, ops.fetched,
		"failed full fetch must retry despite an unchanged source timestamp")
	require.Len(t, h.items, 2)
	require.True(t, h.items[1].IsDeleted)
	require.Equal(t, "unseen", h.items[1].ExternalID)
}

func TestFullSyncCheckpointRetainsUnseenDeletionBaseline(t *testing.T) {
	ops := &fullSyncOps{nodes: map[string][]string{"first": {}}}
	for i := 0; i < FeishuStreamCheckpointInterval+1; i++ {
		ops.nodes["first"] = append(ops.nodes["first"], fmt.Sprintf("node%d", i))
	}
	cfg := &types.DataSourceConfig{ResourceIDs: []string{"first", "later"}}
	old := ops.EncodeCursor(map[string]map[string]string{
		"first": {"gone": "100"}, "later": {"gone-later": "100"},
	}, time.Now())
	h := &fullSyncHandler{stopAfterCheckpoint: true}
	_, err := FetchFullStreamEngine(context.Background(), nil, cfg, old, h, ops)
	require.Error(t, err)
	require.NotNil(t, h.checkpoint)
	require.Equal(t, true, h.checkpoint.ConnectorCursor[fullSyncPending])
	ops.fetched = nil
	resume := &fullSyncHandler{}
	next, err := FetchFullStreamEngine(context.Background(), nil, cfg, h.checkpoint, resume, ops)
	require.NoError(t, err)
	deleted := []string{}
	for _, item := range resume.items {
		if item.IsDeleted {
			deleted = append(deleted, item.ExternalID)
		}
	}
	require.ElementsMatch(t, []string{"gone", "gone-later"}, deleted)
	require.Equal(t, []string{fmt.Sprintf("node%d", FeishuStreamCheckpointInterval)}, ops.fetched,
		"a retried full pass must not repeat successfully checkpointed fetches")
	require.NotContains(t, next.ConnectorCursor, fullSyncPending)
	ops.fetched = nil
	_, err = FetchFullStreamEngine(context.Background(), nil, cfg, next, &fullSyncHandler{}, ops)
	require.NoError(t, err)
	require.Len(t, ops.fetched, len(ops.nodes["first"]),
		"a later independent full sync must refetch everything")
}
