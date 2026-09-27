package activate

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
)

// registrations tracks the IDs an activator registered in a domain registry
// for each plugin, so a new version takes over from the previous one
// without a moment where neither is registered.
type registrations struct {
	mu  sync.Mutex
	ids map[string][]string // plugin ID → registered IDs
}

func newRegistrations() registrations { return registrations{ids: map[string][]string{}} }

// entry is one thing to register: its ID and how to register it. register
// replaces an entry with the same ID; its checks ran when staging, so an
// error here is logged, not returned.
type entry struct {
	id       string
	register func() error
}

// stage returns the commit of a plugin version's entries: it registers them
// all, then removes those of the previous version the new one dropped.
func (r *registrations) stage(domain, pluginID string, entries []entry, unregister func(string)) reconcile.Staged {
	return reconcile.Swap{OnCommit: func() {
		keep := make(map[string]bool, len(entries))
		ids := make([]string, 0, len(entries))
		for _, e := range entries {
			if err := e.register(); err != nil {
				logger.Warnf(context.Background(), "[plugin] %s %s: %v", domain, e.id, err)
				continue
			}
			keep[e.id] = true
			ids = append(ids, e.id)
		}
		r.mu.Lock()
		old := r.ids[pluginID]
		if len(ids) == 0 {
			delete(r.ids, pluginID)
		} else {
			r.ids[pluginID] = ids
		}
		r.mu.Unlock()
		for _, id := range old {
			if !keep[id] {
				unregister(id)
			}
		}
	}}
}

// remove unregisters everything registered for a plugin.
func (r *registrations) remove(pluginID string, unregister func(string)) {
	r.mu.Lock()
	ids := r.ids[pluginID]
	delete(r.ids, pluginID)
	r.mu.Unlock()
	for _, id := range ids {
		unregister(id)
	}
}
