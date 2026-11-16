package service

import (
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"strconv"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type ingestAckConnector struct {
	deletedItemConnector
	items  []types.FetchedItem
	starts []int
}

func ingestAckCursor(offset int) *types.SyncCursor {
	return &types.SyncCursor{ConnectorCursor: map[string]interface{}{"offset": offset}}
}

func ingestAckOffset(cursor *types.SyncCursor) int {
	if cursor == nil {
		return 0
	}
	offset, _ := strconv.Atoi(fmt.Sprint(cursor.ConnectorCursor["offset"]))
	return offset
}

func (c *ingestAckConnector) FetchIncremental(
	_ context.Context, _ *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	offset := ingestAckOffset(cursor)
	c.starts = append(c.starts, offset)
	return c.items[offset:], ingestAckCursor(len(c.items)), nil
}

type ingestAckStream struct{ *ingestAckConnector }

func (c *ingestAckStream) FetchStream(
	ctx context.Context, cfg *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	items, next, _ := c.FetchIncremental(ctx, cfg, cursor)
	offset := ingestAckOffset(cursor)
	for i, item := range items {
		if err := h.Emit(ctx, item); err != nil {
			return nil, err
		}
		if err := h.Checkpoint(ctx, ingestAckCursor(offset+i+1)); err != nil {
			return nil, err
		}
	}
	return next, nil
}

// ignoringIngestAckStream exercises the service's final cursor guard when a
// connector fails to propagate a handler error.
type ignoringIngestAckStream struct{ *ingestAckConnector }

func (c *ignoringIngestAckStream) FetchStream(
	ctx context.Context, cfg *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	items, next, _ := c.FetchIncremental(ctx, cfg, cursor)
	for i, item := range items {
		_ = h.Emit(ctx, item)
		_ = h.Checkpoint(ctx, ingestAckCursor(i+1))
	}
	return next, nil
}

type ingestAckKnowledgeService struct {
	*sweepFakeKS
	failure   error
	stored    map[string]bool
	attempted []string
}

func (s *ingestAckKnowledgeService) CreateKnowledgeFromFile(
	_ context.Context, _ string, _ *multipart.FileHeader, metadata map[string]string,
	_ *bool, _ string, _ []string, _ string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	id := metadata["external_id"]
	s.attempted = append(s.attempted, id)
	if id == "blocked" && s.failure != nil {
		return nil, s.failure
	}
	s.stored[id] = true
	return &types.Knowledge{ID: id}, nil
}

type ingestAckHarness struct {
	*syncDeletionHarness
	connector *ingestAckConnector
	sink      *ingestAckKnowledgeService
	task      *asynq.Task
}

func newIngestAckHarness(t *testing.T, streaming bool) *ingestAckHarness {
	t.Helper()
	h := newSyncDeletionHarness(t, false, "source-ack", "log-ack", nil, nil)
	h.ds.SyncMode = types.SyncModeIncremental
	cursor, err := ingestAckCursor(0).ToJSON()
	require.NoError(t, err)
	h.ds.LastSyncCursor = cursor
	c := &ingestAckConnector{items: []types.FetchedItem{
		{ExternalID: "first", FileName: "first.md", Content: []byte("first")},
		{ExternalID: "blocked", FileName: "blocked.md", Content: []byte("blocked")},
		{ExternalID: "last", FileName: "last.md", Content: []byte("last")},
	}}
	var connector datasource.Connector = c
	if streaming {
		connector = &ingestAckStream{c}
	}
	require.NoError(t, h.svc.connectorRegistry.Register(connector))
	sink := &ingestAckKnowledgeService{
		sweepFakeKS: &sweepFakeKS{repo: &deletionLookupKnowledgeRepo{}}, stored: map[string]bool{},
	}
	h.svc.knowledgeService = sink
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: h.ds.ID, TenantID: h.ds.TenantID, SyncLogID: h.syncLogID,
	})
	require.NoError(t, err)
	return &ingestAckHarness{h, c, sink, asynq.NewTask(types.TypeDataSourceSync, payload)}
}
