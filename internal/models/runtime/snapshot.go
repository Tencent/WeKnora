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
func (rt *Runtime) RestoreSnapshot(s Snapshot) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.providers, rt.builtins = cloneProviders(s.current), cloneProviders(s.base)
}

// SnapshotCurrent captures the default runtime for rollback or isolated tests.
func SnapshotCurrent() Snapshot { return Default().SnapshotCurrent() }

// RestoreSnapshot restores the default runtime.
func RestoreSnapshot(s Snapshot) { Default().RestoreSnapshot(s) }
