package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/driver"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

// NodeStatus is one node's report on a plugin.
type NodeStatus struct {
	Node string `json:"node"`
	Status
	// SeenAt is when the node last confirmed the report.
	SeenAt time.Time `json:"seenAt"`
}

const statusKeyBase = "weknora:plugins:status"

func statusKey(pluginID string) string {
	if ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); ns != "" {
		return statusKeyBase + ":" + ns + ":" + pluginID
	}
	return statusKeyBase + ":" + pluginID
}

// NodeName identifies this node in status reports.
func (r *Reconciler) NodeName() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "node"
	}
	if r.role != "" {
		host = r.role + ":" + host
	}
	return host + "/" + r.instanceID[:8]
}

// publishStatuses reports every plugin this node knows about to Redis, so
// any node can show the whole cluster. Reports refresh on every pass; a node
// that stops reporting ages out.
func (r *Reconciler) publishStatuses(ctx context.Context) {
	if r.rdb == nil {
		return
	}
	r.statusMu.RLock()
	snapshot := make(map[string]Status, len(r.status))
	for id, s := range r.status {
		snapshot[id] = s
	}
	r.statusMu.RUnlock()
	node, now := r.NodeName(), time.Now()
	pipe := r.rdb.Pipeline()
	for id, s := range snapshot {
		b, _ := json.Marshal(NodeStatus{Node: node, Status: s, SeenAt: now})
		pipe.HSet(ctx, statusKey(id), node, b)
		pipe.Expire(ctx, statusKey(id), r.staleAfter()*2)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Warnf(ctx, "[plugin] publish node status: %v", err)
	}
}

func (r *Reconciler) forgetStatus(ctx context.Context, pluginID string) {
	if r.rdb == nil {
		return
	}
	if err := r.rdb.HDel(ctx, statusKey(pluginID), r.NodeName()).Err(); err != nil {
		logger.Warnf(ctx, "[plugin] clear node status of %s: %v", pluginID, err)
	}
}

func (r *Reconciler) staleAfter() time.Duration { return 3 * r.interval }

// NodeStatuses reports a plugin on every live node; without Redis, on this
// node only.
func (r *Reconciler) NodeStatuses(ctx context.Context, pluginID string) ([]NodeStatus, error) {
	local, hasLocal := r.Status(pluginID)
	if r.rdb == nil {
		if !hasLocal {
			return nil, nil
		}
		return []NodeStatus{{Node: r.NodeName(), Status: local, SeenAt: time.Now()}}, nil
	}
	fields, err := r.rdb.HGetAll(ctx, statusKey(pluginID)).Result()
	if err != nil {
		return nil, fmt.Errorf("read node status: %w", err)
	}
	var out []NodeStatus
	self := r.NodeName()
	for node, raw := range fields {
		var ns NodeStatus
		if json.Unmarshal([]byte(raw), &ns) != nil || node == self || time.Since(ns.SeenAt) > r.staleAfter() {
			continue
		}
		out = append(out, ns)
	}
	if hasLocal {
		out = append(out, NodeStatus{Node: self, Status: local, SeenAt: time.Now()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out, nil
}

// Driver returns the status driver for plugins the reconciler loads on every
// node (declarative and host): an instance is a node that loaded the plugin,
// with the runtime health the node reported.
func (r *Reconciler) Driver(rt manifest.RuntimeType) driver.Driver { return nodeDriver{r: r, rt: rt} }

type nodeDriver struct {
	r  *Reconciler
	rt manifest.RuntimeType
}

func (d nodeDriver) Type() manifest.RuntimeType { return d.rt }

// Ensure and Remove are the reconciler's job.
func (nodeDriver) Ensure(context.Context, *manifest.Manifest) error { return nil }
func (nodeDriver) Remove(context.Context, string, string) error     { return nil }

// Resolve is not how these plugins are reached: declarative ones have no
// endpoint and host ones are reached through the node's host manager.
func (d nodeDriver) Resolve(_ context.Context, pluginID string, _ uint64) (driver.Endpoint, error) {
	return driver.Endpoint{}, fmt.Errorf("%s plugin %s has no shared endpoint", d.rt, pluginID)
}

func (d nodeDriver) Status(ctx context.Context, pluginID string) ([]driver.InstanceStatus, error) {
	nodes, err := d.r.NodeStatuses(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	out := make([]driver.InstanceStatus, 0, len(nodes))
	for _, n := range nodes {
		state := driver.StateReady
		switch n.State {
		case StateReady:
		case StateDegraded:
			state = driver.StateDegraded
		default:
			state = driver.StateStopped
		}
		out = append(out, driver.InstanceStatus{
			Node: n.Node, Version: n.Version, State: state, Error: n.Error, UpdatedAt: n.UpdatedAt,
		})
	}
	return out, nil
}
