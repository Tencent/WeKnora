package outline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

type Connector struct{}

func NewConnector() *Connector  { return &Connector{} }
func (*Connector) Type() string { return types.ConnectorTypeOutline }

func authenticate(ctx context.Context, c *client) (identity, error) {
	result, err := c.call(ctx, "auth.info", map[string]interface{}{})
	if err != nil {
		return identity{}, err
	}
	var auth struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Team struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	if json.Unmarshal(result.Data, &auth) != nil || auth.User.ID == "" || auth.Team.ID == "" {
		return identity{}, errors.New("outline_response_invalid")
	}
	return identity{BaseURL: c.base, WorkspaceID: auth.Team.ID, ActorID: auth.User.ID}, nil
}

func selectedIDs(ids []string) ([]string, error) {
	result := slices.Clone(ids)
	for _, id := range result {
		if _, err := uuid.Parse(id); err != nil {
			return nil, errors.New("outline_invalid_collection_id")
		}
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

func collectionInfo(ctx context.Context, c *client, id string) (collection, error) {
	result, err := c.call(ctx, "collections.info", map[string]interface{}{"id": id})
	if err != nil {
		return collection{}, err
	}
	var item collection
	if json.Unmarshal(result.Data, &item) != nil || item.ID != id {
		return item, errors.New("outline_response_invalid")
	}
	return item, nil
}

func documentInfo(ctx context.Context, c *client, id string) (document, error) {
	result, err := c.call(ctx, "documents.info", map[string]interface{}{"id": id})
	if err != nil {
		return document{}, err
	}
	item, err := decodeDocument(result.Data)
	if err == nil && item.ID != id {
		err = errors.New("outline_response_invalid")
	}
	return item, err
}

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	client, err := newClient(config)
	if err != nil {
		return err
	}
	if _, err := authenticate(ctx, client); err != nil {
		return err
	}
	if _, err := c.ListResources(ctx, config, ""); err != nil {
		return err
	}
	ids, err := selectedIDs(config.ResourceIDs)
	if err != nil {
		return err
	}
	config.ResourceIDs = ids
	for _, id := range ids {
		col, err := collectionInfo(ctx, client, id)
		if err != nil {
			return err
		}
		if col.ArchivedAt != "" || col.DeletedAt != "" {
			return errors.New("outline_collection_inactive")
		}
		result, err := client.call(ctx, "documents.list", map[string]interface{}{
			"collectionId": id, "limit": 1, "offset": 0, "statusFilter": []string{"published"},
			"sort": "createdAt", "direction": "ASC",
		})
		if err != nil {
			return err
		}
		var rows []json.RawMessage
		if json.Unmarshal(result.Data, &rows) != nil {
			return errors.New("outline_response_invalid")
		}
		if len(rows) > 0 {
			d, err := decodeDocument(rows[0])
			if err != nil {
				return err
			}
			d, err = documentInfo(ctx, client, d.ID)
			if err != nil {
				return err
			}
			if d.Text == nil {
				return errors.New("outline_format_unsupported")
			}
		}
	}
	if config.SyncDeletions {
		var result *envelope
		result, err = client.call(ctx, "documents.deleted", map[string]interface{}{"limit": 1, "offset": 0})
		if err == nil {
			var rows []json.RawMessage
			if json.Unmarshal(result.Data, &rows) != nil {
				return errors.New("outline_response_invalid")
			}
		}
	}
	return err
}

func (*Connector) ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
	out := []types.Resource{}
	if parentID != "" {
		return out, nil
	}
	c, err := newClient(config)
	if err != nil {
		return nil, err
	}
	err = c.walk(ctx, "collections.list", map[string]interface{}{}, func(rows []json.RawMessage) error {
		for _, raw := range rows {
			var col collection
			if json.Unmarshal(raw, &col) != nil || col.ID == "" {
				return errors.New("outline_response_invalid")
			}
			if col.ArchivedAt != "" || col.DeletedAt != "" {
				continue
			}
			out = append(out, types.Resource{ExternalID: col.ID, Name: col.Name, Type: "collection"})
		}
		return nil
	})
	return out, err
}

func (*Connector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return []string{}, nil
}

// Batch adapters acknowledge delivery only. Production always uses FetchStream.
type collector struct{ items []types.FetchedItem }

func (h *collector) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}
func (h *collector) Checkpoint(context.Context, *types.SyncCursor) error { return nil }
func (h *collector) EmitWithResult(ctx context.Context, item types.FetchedItem) (datasource.ApplyResult, error) {
	return datasource.ApplyResult{Outcome: datasource.ApplyApplied}, h.Emit(ctx, item)
}
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, ids []string) ([]types.FetchedItem, error) {
	copy := *config
	copy.ResourceIDs = ids
	h := &collector{}
	cursor, _ := c.PrepareFullSyncCursor(nil)
	_, err := c.FetchStream(ctx, &copy, cursor, h)
	return h.items, err
}
func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	h := &collector{}
	next, err := c.FetchStream(ctx, config, cursor, h)
	return h.items, next, err
}

var _ datasource.StreamingConnector = (*Connector)(nil)

// BindIdentity verifies credential rotations before a configuration is persisted.
func (*Connector) BindIdentity(ctx context.Context, config *types.DataSourceConfig, previous *types.SyncCursor) (*types.SyncCursor, error) {
	s, err := parseCursor(previous)
	if err != nil {
		return nil, err
	}
	c, err := newClient(config)
	if err != nil {
		return nil, err
	}
	instance, err := authenticate(ctx, c)
	if err != nil {
		return nil, err
	}
	if s.Instance.BaseURL != "" && (s.Instance.BaseURL != instance.BaseURL || s.Instance.WorkspaceID != instance.WorkspaceID) {
		return nil, errors.New("outline_instance_changed")
	}
	if s.Instance.ActorID != instance.ActorID {
		s.Run = nil
	}
	s.Instance = instance
	return s.cursor(), nil
}
