package activate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Connectors registers code plugins' data source connectors. A plugin
// connector's instanceSchema splits in two: fields in x-group "settings"
// are non-secret options (DataSourceConfig.Settings), everything else is
// credentials (encrypted at rest).
type Connectors struct {
	iv       *Invoker
	registry *datasource.ConnectorRegistry

	mu         sync.Mutex
	registered map[string][]string
}

// NewConnectors creates the connector activator.
func NewConnectors(iv *Invoker, registry *datasource.ConnectorRegistry) *Connectors {
	return &Connectors{iv: iv, registry: registry, registered: map[string][]string{}}
}

// Name implements reconcile.Activator.
func (a *Connectors) Name() string { return "connectors" }

// settingsGroup marks instanceSchema fields that are settings, not
// credentials.
const settingsGroup = "settings"

// Activate implements reconcile.Activator: all of a plugin's connectors or
// none.
func (a *Connectors) Activate(_ context.Context, l *reconcile.Loaded) error {
	var ids []string
	for _, c := range l.Manifest.Contributes[manifest.PointConnectors] {
		id := manifest.QualifiedID(l.Manifest.ID, c.ID)
		meta, err := connectorMetadata(l, c, id)
		if err == nil {
			rc := &remoteConnector{iv: a.iv, m: l.Manifest, local: c.ID, typeID: id}
			err = a.registry.RegisterPlugin(rc, meta)
		}
		if err != nil {
			for _, done := range ids {
				a.registry.Unregister(done)
			}
			return fmt.Errorf("connector %s: %w", c.ID, err)
		}
		ids = append(ids, id)
	}
	a.mu.Lock()
	a.registered[l.Manifest.ID] = ids
	a.mu.Unlock()
	return nil
}

// Deactivate implements reconcile.Activator.
func (a *Connectors) Deactivate(_ context.Context, pluginID string) error {
	a.mu.Lock()
	ids := a.registered[pluginID]
	delete(a.registered, pluginID)
	a.mu.Unlock()
	for _, id := range ids {
		a.registry.Unregister(id)
	}
	return nil
}

func connectorMetadata(
	l *reconcile.Loaded,
	c manifest.Contribution,
	id string,
) (datasource.ConnectorMetadata, error) {
	src, err := instanceSchema(l.Package, c)
	if err != nil {
		return datasource.ConnectorMetadata{}, err
	}
	creds, settings := splitConnectorSchema(src)
	icon, err := iconDataURI(l.Package, c.Icon)
	if err != nil {
		return datasource.ConnectorMetadata{}, err
	}
	caps := c.Capabilities
	if caps == nil {
		caps = []string{}
	}
	return datasource.ConnectorMetadata{
		Type: id, Name: c.Name.Default, Names: c.Name.Locales,
		Description: c.Description.Default, Descriptions: c.Description.Locales,
		Icon: icon, Priority: 1000 + c.Order, AuthType: "plugin", Capabilities: caps,
		ConfigSchema: creds, SettingsSchema: settings, PluginID: l.Manifest.ID,
		PluginVersion: l.Manifest.Version, Editor: c.Editor,
	}, nil
}

// splitConnectorSchema separates settings fields from credential fields.
func splitConnectorSchema(src *configschema.Schema) (creds, settings *configschema.Schema) {
	if src == nil {
		return nil, nil
	}
	required := map[string]bool{}
	for _, k := range src.Required {
		required[k] = true
	}
	creds, settings = configschema.Object(), configschema.Object()
	keys := make([]string, 0, len(src.Properties))
	for k := range src.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		prop := *src.Properties[k]
		if prop.Group == settingsGroup && !prop.Secret {
			prop.Group = ""
			settings.Set(k, &prop, required[k])
			continue
		}
		creds.Set(k, &prop, required[k])
	}
	if len(creds.Properties) == 0 {
		creds = nil
	}
	if len(settings.Properties) == 0 {
		settings = nil
	}
	return creds, settings
}

// remoteConnector is a plugin connector. It streams, so the sync service
// interleaves fetch, ingest and checkpoint and resumes after a timeout.
type remoteConnector struct {
	iv     *Invoker
	m      *manifest.Manifest
	local  string
	typeID string
}

var _ datasource.FullStreamingConnector = (*remoteConnector)(nil)

func (r *remoteConnector) Type() string { return r.typeID }

func instanceOf(cfg *types.DataSourceConfig) map[string]any {
	if cfg == nil {
		return map[string]any{}
	}
	return map[string]any{
		"credentials": nonNil(cfg.Credentials),
		"settings":    nonNil(cfg.Settings),
		"resourceIds": cfg.ResourceIDs,
	}
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Validate implements datasource.Connector.
func (r *remoteConnector) Validate(ctx context.Context, cfg *types.DataSourceConfig) error {
	ctx, cancel := withDefaultTimeout(ctx, metadataTimeout)
	defer cancel()
	return r.iv.Call(ctx, r.m, pluginapi.ConnectorValidatePath(r.local), instanceOf(cfg), nil, nil)
}

// ListResources implements datasource.Connector.
func (r *remoteConnector) ListResources(
	ctx context.Context, cfg *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	ctx, cancel := withDefaultTimeout(ctx, metadataTimeout)
	defer cancel()
	var out pluginapi.ListResourcesOutput
	err := r.iv.Call(ctx, r.m, pluginapi.ConnectorListResourcesPath(r.local), instanceOf(cfg),
		pluginapi.ListResourcesInput{ParentID: parentID}, &out)
	if err != nil {
		return nil, err
	}
	res := make([]types.Resource, 0, len(out.Resources))
	for _, x := range out.Resources {
		item := types.Resource{
			ExternalID: x.ExternalID, Name: x.Name, Type: x.Type, Description: x.Description, URL: x.URL,
			ParentID: x.ParentID, HasChildren: x.HasChildren, Metadata: x.Metadata,
		}
		if x.ModifiedAt != nil {
			item.ModifiedAt = *x.ModifiedAt
		}
		res = append(res, item)
	}
	return res, nil
}

// ResolveResourceAncestors implements datasource.Connector.
func (r *remoteConnector) ResolveResourceAncestors(
	ctx context.Context, cfg *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	ctx, cancel := withDefaultTimeout(ctx, metadataTimeout)
	defer cancel()
	var out pluginapi.ResolveAncestorsOutput
	err := r.iv.Call(ctx, r.m, pluginapi.ConnectorResolveAncestorsPath(r.local), instanceOf(cfg),
		pluginapi.ResolveAncestorsInput{ResourceIDs: resourceIDs}, &out)
	return out.Ancestors, err
}

// collector gathers a stream for the non-streaming entry points.
type collector struct {
	items  []types.FetchedItem
	cursor *types.SyncCursor
}

func (c *collector) Emit(_ context.Context, item types.FetchedItem) error {
	c.items = append(c.items, item)
	return nil
}

func (c *collector) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	c.cursor = cursor
	return nil
}

// FetchAll implements datasource.Connector.
func (r *remoteConnector) FetchAll(
	ctx context.Context, cfg *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	c := &collector{}
	scoped := *cfg
	if len(resourceIDs) > 0 {
		scoped.ResourceIDs = resourceIDs
	}
	_, err := r.fetch(ctx, &scoped, pluginapi.FetchFull, nil, c)
	return c.items, err
}

// FetchIncremental implements datasource.Connector.
func (r *remoteConnector) FetchIncremental(
	ctx context.Context, cfg *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	c := &collector{}
	next, err := r.fetch(ctx, cfg, pluginapi.FetchIncremental, cursor, c)
	return c.items, next, err
}

// FetchStream implements datasource.StreamingConnector: incremental from a
// cursor, full without one.
func (r *remoteConnector) FetchStream(
	ctx context.Context, cfg *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	mode := pluginapi.FetchIncremental
	if cursor == nil {
		mode = pluginapi.FetchFull
	}
	return r.fetch(ctx, cfg, mode, cursor, h)
}

// FetchFullStream implements datasource.FullStreamingConnector: every item
// again, with the previous cursor for deletion reconciliation.
func (r *remoteConnector) FetchFullStream(
	ctx context.Context, cfg *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return r.fetch(ctx, cfg, pluginapi.FetchFull, cursor, h)
}

func (r *remoteConnector) fetch(
	ctx context.Context, cfg *types.DataSourceConfig, mode string, cursor *types.SyncCursor,
	h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	in := pluginapi.FetchInput{Mode: mode, Cursor: toPluginCursor(cursor)}
	if cfg != nil {
		in.ResourceIDs = cfg.ResourceIDs
	}
	end, err := r.iv.Stream(ctx, r.m, pluginapi.ConnectorFetchPath(r.local), instanceOf(cfg), in,
		func(ev pluginapi.Event) error {
			switch ev.Type {
			case pluginapi.EventItem:
				var it pluginapi.FetchedItem
				if err := json.Unmarshal(ev.Data, &it); err != nil {
					return fmt.Errorf("decode fetched item: %w", err)
				}
				return h.Emit(ctx, fromPluginItem(it))
			case pluginapi.EventCheckpoint:
				var c pluginapi.Cursor
				if err := json.Unmarshal(ev.Data, &c); err != nil {
					return fmt.Errorf("decode checkpoint: %w", err)
				}
				return h.Checkpoint(ctx, fromPluginCursor(&c))
			case pluginapi.EventLog, pluginapi.EventProgress:
				r.logEvent(ctx, ev)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	var final pluginapi.Cursor
	if len(end) > 0 {
		if err := json.Unmarshal(end, &final); err != nil {
			return nil, fmt.Errorf("decode final cursor: %w", err)
		}
	}
	next := fromPluginCursor(&final)
	if next.LastSyncTime.IsZero() {
		next.LastSyncTime = time.Now()
	}
	return next, nil
}

// maxPluginLogRunes bounds a line a plugin writes into WeKnora's log.
const maxPluginLogRunes = 2000

// logEvent writes a sync's log line into WeKnora's log at the plugin's
// level, and its progress at debug level, tagged with the connector.
func (r *remoteConnector) logEvent(ctx context.Context, ev pluginapi.Event) {
	msg := logger.TruncateRunes(strings.TrimSpace(ev.Message), maxPluginLogRunes)
	if msg == "" {
		return
	}
	const format = "[plugin] %s sync: %s"
	if ev.Type == pluginapi.EventProgress {
		logger.Debugf(ctx, format, r.typeID, msg)
		return
	}
	switch strings.ToLower(ev.Level) {
	case "debug":
		logger.Debugf(ctx, format, r.typeID, msg)
	case "warn", "warning":
		logger.Warnf(ctx, format, r.typeID, msg)
	case "error":
		logger.Errorf(ctx, format, r.typeID, msg)
	default:
		logger.Infof(ctx, format, r.typeID, msg)
	}
}

func toPluginCursor(c *types.SyncCursor) *pluginapi.Cursor {
	if c == nil {
		return nil
	}
	out := &pluginapi.Cursor{State: c.ConnectorCursor}
	if !c.LastSyncTime.IsZero() {
		t := c.LastSyncTime
		out.LastSyncTime = &t
	}
	return out
}

func fromPluginCursor(c *pluginapi.Cursor) *types.SyncCursor {
	out := &types.SyncCursor{ConnectorCursor: c.State}
	if c.LastSyncTime != nil {
		out.LastSyncTime = *c.LastSyncTime
	}
	if out.ConnectorCursor == nil {
		out.ConnectorCursor = map[string]any{}
	}
	return out
}

func fromPluginItem(it pluginapi.FetchedItem) types.FetchedItem {
	out := types.FetchedItem{
		ExternalID: it.ExternalID, Title: it.Title, Content: it.Content, ContentType: it.ContentType,
		FileName: it.FileName, URL: it.URL, Metadata: it.Metadata, IsDeleted: it.IsDeleted,
		SourceResourceID: it.SourceResourceID,
	}
	if it.UpdatedAt != nil {
		out.UpdatedAt = *it.UpdatedAt
	}
	if it.CreatedAt != nil {
		out.CreatedAt = *it.CreatedAt
	}
	if out.ContentType == "" {
		out.ContentType = "text/markdown"
	}
	return out
}
