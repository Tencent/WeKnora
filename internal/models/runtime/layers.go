package runtime

// WithOverlay builds an independent candidate on top of this generation.
// It never publishes partial changes or mutates either input layer. Unlike
// Reload, this explicitly retains the deployment layer beneath a UI overlay.
func (rt *Runtime) WithOverlay(data []byte, baseDir string) (*Runtime, error) {
	snapshot := rt.SnapshotCurrent()
	candidate := &Runtime{providers: cloneProviders(snapshot.current), builtins: cloneProviders(snapshot.current)}
	if err := candidate.Reload(data, baseDir); err != nil {
		return nil, err
	}
	candidate.builtins = cloneProviders(snapshot.base)
	return candidate, nil
}
