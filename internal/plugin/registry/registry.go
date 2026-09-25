// Package registry indexes the plugins a WeKnora process knows about and the
// contributions they make, so callers can list everything at one extension
// point or resolve one contribution by ID or legacy alias.
package registry

import (
	"fmt"
	"sort"
	"sync"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

// Entry is one contribution together with the plugin that provides it.
type Entry struct {
	Point manifest.Point
	// PluginID is the providing plugin; QualifiedID is "<plugin>/<local>".
	PluginID     string
	QualifiedID  string
	Contribution manifest.Contribution

	seq int // registration order, the tie-breaker after Contribution.Order
}

// Registry is safe for concurrent use. Registered manifests must not be
// mutated afterwards; the registry hands out the same pointers.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]*manifest.Manifest
	entries map[manifest.Point][]*Entry
	// index resolves qualified IDs and builtin aliases per point.
	index map[manifest.Point]map[string]*Entry
	seq   int
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{
		plugins: make(map[string]*manifest.Manifest),
		entries: make(map[manifest.Point][]*Entry),
		index:   make(map[manifest.Point]map[string]*Entry),
	}
}

// Register validates a manifest and adds it with all its contributions. It
// fails without side effects when the plugin ID is taken or a contribution ID
// or alias collides with one already registered at the same point.
func (r *Registry) Register(m *manifest.Manifest) error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("plugin %s: %w", m.ID, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plugins[m.ID]; ok {
		return fmt.Errorf("plugin %s is already registered", m.ID)
	}
	if err := r.checkKeysLocked(m, ""); err != nil {
		return err
	}
	r.addLocked(m)
	return nil
}

// Replace swaps the registered plugin with the same ID for m in one step, or
// registers m if the ID is new: an upgrade never leaves a window where the
// plugin's contributions are missing. Builtins cannot be replaced.
func (r *Registry) Replace(m *manifest.Manifest) error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("plugin %s: %w", m.ID, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.plugins[m.ID]; ok && old.Builtin {
		return fmt.Errorf("builtin plugin %s cannot be replaced", m.ID)
	}
	if err := r.checkKeysLocked(m, m.ID); err != nil {
		return err
	}
	r.removeLocked(m.ID)
	r.addLocked(m)
	return nil
}

// Unregister removes a plugin and its contributions. Builtins cannot be
// removed; removing an unknown ID is a no-op.
func (r *Registry) Unregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.plugins[id]; ok && m.Builtin {
		return fmt.Errorf("builtin plugin %s cannot be removed", id)
	}
	r.removeLocked(id)
	return nil
}

// checkKeysLocked verifies that m's IDs and aliases are free, ignoring the
// entries of the plugin being replaced.
func (r *Registry) checkKeysLocked(m *manifest.Manifest, replacing string) error {
	pending := make(map[manifest.Point]map[string]string)
	for _, info := range manifest.Points() {
		for _, c := range m.Contributes[info.Point] {
			for _, key := range lookupKeys(m.ID, c) {
				if owner, ok := r.index[info.Point][key]; ok && owner.PluginID != replacing {
					return fmt.Errorf("%s %q of plugin %s collides with %s",
						info.Point, key, m.ID, owner.QualifiedID)
				}
				if other, ok := pending[info.Point][key]; ok {
					return fmt.Errorf("%s %q is claimed twice by plugin %s (%s)", info.Point, key, m.ID, other)
				}
				if pending[info.Point] == nil {
					pending[info.Point] = make(map[string]string)
				}
				pending[info.Point][key] = c.ID
			}
		}
	}
	return nil
}

func (r *Registry) addLocked(m *manifest.Manifest) {
	r.plugins[m.ID] = m
	for _, info := range manifest.Points() {
		for _, c := range m.Contributes[info.Point] {
			r.seq++
			e := &Entry{
				Point:        info.Point,
				PluginID:     m.ID,
				QualifiedID:  manifest.QualifiedID(m.ID, c.ID),
				Contribution: c,
				seq:          r.seq,
			}
			r.entries[info.Point] = append(r.entries[info.Point], e)
			if r.index[info.Point] == nil {
				r.index[info.Point] = make(map[string]*Entry)
			}
			for _, key := range lookupKeys(m.ID, c) {
				r.index[info.Point][key] = e
			}
		}
	}
}

func (r *Registry) removeLocked(id string) {
	if _, ok := r.plugins[id]; !ok {
		return
	}
	delete(r.plugins, id)
	for point, list := range r.entries {
		kept := list[:0]
		for _, e := range list {
			if e.PluginID != id {
				kept = append(kept, e)
			}
		}
		r.entries[point] = kept
	}
	for _, idx := range r.index {
		for key, e := range idx {
			if e.PluginID == id {
				delete(idx, key)
			}
		}
	}
}

func lookupKeys(pluginID string, c manifest.Contribution) []string {
	return append([]string{manifest.QualifiedID(pluginID, c.ID)}, c.Aliases...)
}

// Plugins returns every registered plugin sorted by ID.
func (r *Registry) Plugins() []*manifest.Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*manifest.Manifest, 0, len(r.plugins))
	for _, m := range r.plugins {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Plugin returns one plugin by ID.
func (r *Registry) Plugin(id string) (*manifest.Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.plugins[id]
	return m, ok
}

// Contributions returns every contribution at a point, sorted by
// Contribution.Order and then registration order.
func (r *Registry) Contributions(point manifest.Point) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, 0, len(r.entries[point]))
	for _, e := range r.entries[point] {
		out = append(out, *e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Contribution.Order != out[j].Contribution.Order {
			return out[i].Contribution.Order < out[j].Contribution.Order
		}
		return out[i].seq < out[j].seq
	})
	return out
}

// Resolve finds a contribution by qualified ID ("acme.jira/jira") or by a
// builtin's legacy alias ("feishu").
func (r *Registry) Resolve(point manifest.Point, id string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.index[point][id]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}
