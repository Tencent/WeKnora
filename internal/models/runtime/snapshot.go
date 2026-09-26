package runtime

// Snapshot captures provider definitions and their model catalogs together.
// It also retains the built-in baseline used when replacing deployment overrides.
type Snapshot struct {
	current map[string]*Provider
	base    map[string]*Provider
}

// SnapshotCurrent retains the current generation and its built-in baseline.
func (rt *Runtime) SnapshotCurrent() Snapshot {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return Snapshot{cloneProviders(rt.providers), cloneProviders(rt.builtins)}
}

// RestoreSnapshot restores an owned copy for rollback or isolated tests.
// Plugin vendors the snapshot does not contain are rolled back too; use
// Adopt to publish a generation while keeping them.
func (rt *Runtime) RestoreSnapshot(s Snapshot) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.providers, rt.builtins = cloneProviders(s.current), cloneProviders(s.base)
	for id := range rt.plugins {
		if _, ok := rt.builtins[id]; !ok {
			delete(rt.plugins, id)
		}
	}
}

// Adopt publishes the generation candidate holds on rt, keeping the vendors
// installed plugins registered on rt. The console catalog compiles its
// candidate from a runtime of its own that has no plugin vendors, so a plain
// restore would drop every plugin vendor on each publish or sync. An id the
// candidate defines itself keeps the candidate's definition.
func (rt *Runtime) Adopt(candidate *Runtime) {
	s := candidate.SnapshotCurrent()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for id, vendor := range rt.plugins {
		if _, ok := s.base[id]; !ok {
			s.base[id] = vendor.clone()
		}
		if _, ok := s.current[id]; !ok {
			s.current[id] = vendor.clone()
		}
	}
	rt.providers, rt.builtins = s.current, s.base
}

// SnapshotCurrent captures the default runtime for rollback or isolated tests.
func SnapshotCurrent() Snapshot { return Default().SnapshotCurrent() }

// RestoreSnapshot restores the default runtime.
func RestoreSnapshot(s Snapshot) { Default().RestoreSnapshot(s) }
