package hostapi

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// ScopeDataSources lets a plugin see the workspace's data sources of its
// own connectors and start their syncs, e.g. when a webhook says the
// source changed.
const ScopeDataSources = "datasources"

// runningSyncWindow is how long a running sync keeps new requests from
// queueing another; older runs are presumed stuck.
const runningSyncWindow = 30 * time.Minute

// DataSourceStore finds data sources by type.
type DataSourceStore interface {
	// FindByTenantAndTypePrefix lists a tenant's data sources whose type
	// starts with prefix.
	FindByTenantAndTypePrefix(ctx context.Context, tenantID uint64, prefix string) ([]*types.DataSource, error)
}

// DataSourceSyncer starts syncs and reads their history.
type DataSourceSyncer interface {
	ManualSync(ctx context.Context, dsID string) (*types.SyncLog, error)
	GetSyncLogs(ctx context.Context, dsID string, limit, offset int) ([]*types.SyncLog, error)
}

// DataSources serves the datasources scope. A plugin only ever sees the
// data sources of its own connectors ("<plugin>/<connector>") in the
// token's tenant.
type DataSources struct {
	store  DataSourceStore
	syncer DataSourceSyncer
	now    func() time.Time
}

// NewDataSources creates the scope.
func NewDataSources(store DataSourceStore, syncer DataSourceSyncer) *DataSources {
	return &DataSources{store: store, syncer: syncer, now: time.Now}
}

func (d *DataSources) own(ctx context.Context, c *Claims) ([]*types.DataSource, error) {
	return d.store.FindByTenantAndTypePrefix(ctx, c.TenantID, c.PluginID+"/")
}

// List lists the plugin's data sources in the tenant.
func (d *DataSources) List(ctx context.Context, c *Claims) (*pluginapi.DataSourceList, error) {
	list, err := d.own(ctx, c)
	if err != nil {
		return nil, err
	}
	out := &pluginapi.DataSourceList{DataSources: make([]pluginapi.DataSourceInfo, 0, len(list))}
	for _, ds := range list {
		info := pluginapi.DataSourceInfo{
			ID: ds.ID, Name: ds.Name, KnowledgeBaseID: ds.KnowledgeBaseID,
			Connector: strings.TrimPrefix(ds.Type, c.PluginID+"/"), Status: ds.Status, LastSyncAt: ds.LastSyncAt,
			ResourceIDs: []string{},
		}
		if cfg, err := ds.ParseConfig(); err == nil && cfg != nil && cfg.ResourceIDs != nil {
			info.ResourceIDs = cfg.ResourceIDs
		}
		out.DataSources = append(out.DataSources, info)
	}
	return out, nil
}

// Sync starts an incremental sync of one of the plugin's data sources,
// unless one started recently and is still running: bursts of webhooks
// then cost one sync, and the next scheduled or triggered sync picks up
// what that one missed.
func (d *DataSources) Sync(ctx context.Context, c *Claims, id string) (*pluginapi.SyncStarted, error) {
	list, err := d.own(ctx, c)
	if err != nil {
		return nil, err
	}
	var ds *types.DataSource
	for _, candidate := range list {
		if candidate.ID == id {
			ds = candidate
		}
	}
	if ds == nil {
		return nil, pluginapi.Errorf(pluginapi.CodeNotFound, "no data source %q of this plugin", id)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, c.TenantID)
	if logs, err := d.syncer.GetSyncLogs(ctx, id, 1, 0); err == nil && len(logs) > 0 {
		last := logs[0]
		if last.Status == types.SyncLogStatusRunning && d.now().Sub(last.StartedAt) < runningSyncWindow {
			return &pluginapi.SyncStarted{Status: pluginapi.SyncRunning, SyncLogID: last.ID}, nil
		}
	}
	started, err := d.syncer.ManualSync(ctx, id)
	if err != nil {
		return nil, pluginapi.Errorf(pluginapi.CodeUnavailable, "start sync: %v", err)
	}
	return &pluginapi.SyncStarted{Status: pluginapi.SyncQueued, SyncLogID: started.ID}, nil
}
