package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// RegisterPlugin adds a vendor an installed plugin declares. The definition
// uses the deployment overlay format for one provider, with two limits: it
// is taken literally (no ${ENV} expansion, which would hand a plugin the
// server's environment) and it carries no deployment API key, since every
// workspace brings its own. It cannot replace a built-in or operator
// vendor; registering the same plugin vendor again replaces it. baseDir
// resolves the icon path inside the plugin package.
//
// The vendor joins the baseline, so later deployment overlay reloads keep
// it and may patch it like a built-in.
func (rt *Runtime) RegisterPlugin(id string, definition []byte, baseDir string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return errors.New("vendor id is empty")
	}
	var p OverlayProvider
	dec := json.NewDecoder(bytesReader(definition))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return fmt.Errorf("parse vendor definition: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("parse vendor definition: expected a single JSON document")
	}
	if p.APIKey != "" {
		return errors.New("a plugin vendor cannot set api_key; workspaces supply their own keys")
	}
	vendor := newOverlayVendor(id)
	if err := applyOverlayProvider(vendor, p, baseDir, func(s string) string { return s }); err != nil {
		return err
	}
	normalizeProvider(vendor)
	if err := validateDefinition(vendor); err != nil {
		return err
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if _, taken := rt.providers[id]; taken && !rt.plugins[id] {
		return fmt.Errorf("vendor %s already exists", id)
	}
	if rt.plugins == nil {
		rt.plugins = map[string]bool{}
	}
	rt.plugins[id] = true
	rt.providers[id] = vendor
	rt.builtins[id] = vendor.clone()
	return nil
}

// Unregister removes a vendor RegisterPlugin added. Other vendors stay.
func (rt *Runtime) Unregister(id string) {
	id = strings.ToLower(strings.TrimSpace(id))
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !rt.plugins[id] {
		return
	}
	delete(rt.plugins, id)
	delete(rt.providers, id)
	delete(rt.builtins, id)
}

// RegisterPlugin adds a plugin vendor to the default runtime.
func RegisterPlugin(id string, definition []byte, baseDir string) error {
	return Default().RegisterPlugin(id, definition, baseDir)
}

// Unregister removes a plugin vendor from the default runtime.
func Unregister(id string) { Default().Unregister(id) }
