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
	v, err := PreparePlugin(id, definition, baseDir)
	if err != nil {
		return err
	}
	return rt.InstallPlugin(v)
}

// PluginVendor is a plugin vendor definition PreparePlugin parsed and
// checked, ready for InstallPlugin.
type PluginVendor struct {
	id     string
	vendor *Provider
}

// ID is the vendor's normalized ID.
func (v *PluginVendor) ID() string { return v.id }

// PreparePlugin parses and checks a plugin vendor definition without
// touching any runtime, so a bad definition fails before anything changes.
func PreparePlugin(id string, definition []byte, baseDir string) (*PluginVendor, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return nil, errors.New("vendor id is empty")
	}
	var p OverlayProvider
	dec := json.NewDecoder(bytesReader(definition))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parse vendor definition: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("parse vendor definition: expected a single JSON document")
	}
	if p.APIKey != "" {
		return nil, errors.New("a plugin vendor cannot set api_key; workspaces supply their own keys")
	}
	vendor := newOverlayVendor(id)
	if err := applyOverlayProvider(vendor, p, baseDir, func(s string) string { return s }); err != nil {
		return nil, err
	}
	normalizeProvider(vendor)
	if err := validateDefinition(vendor); err != nil {
		return nil, err
	}
	return &PluginVendor{id: id, vendor: vendor}, nil
}

// CheckPlugin reports whether InstallPlugin would take a vendor ID: it
// refuses a built-in or operator vendor's.
func (rt *Runtime) CheckPlugin(id string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.checkPluginLocked(id)
}

func (rt *Runtime) checkPluginLocked(id string) error {
	if _, taken := rt.providers[id]; taken && rt.plugins[id] == nil {
		return fmt.Errorf("vendor %s already exists", id)
	}
	return nil
}

// InstallPlugin adds a prepared plugin vendor, replacing one with the same
// ID.
func (rt *Runtime) InstallPlugin(v *PluginVendor) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if err := rt.checkPluginLocked(v.id); err != nil {
		return err
	}
	if rt.plugins == nil {
		rt.plugins = map[string]*Provider{}
	}
	rt.plugins[v.id] = v.vendor.clone()
	rt.providers[v.id] = v.vendor.clone()
	rt.builtins[v.id] = v.vendor.clone()
	return nil
}

// Unregister removes a vendor RegisterPlugin added. Other vendors stay.
func (rt *Runtime) Unregister(id string) {
	id = strings.ToLower(strings.TrimSpace(id))
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.plugins[id] == nil {
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
