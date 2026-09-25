package activate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"

	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
)

// ModelVendors registers the model vendors of installed plugins with the
// model runtime. A plugin vendor's ID is its qualified contribution ID
// ("acme.ai/acme"), so it can never shadow a built-in vendor and the tenant
// plugin switch resolves it directly.
type ModelVendors struct {
	rt *modelruntime.Runtime

	mu         sync.Mutex
	registered map[string][]string // plugin ID → vendor IDs
}

// NewModelVendors creates the model vendor activator on the default runtime.
func NewModelVendors() *ModelVendors {
	return &ModelVendors{rt: modelruntime.Default(), registered: map[string][]string{}}
}

// Name implements reconcile.Activator.
func (a *ModelVendors) Name() string { return "modelVendors" }

// Activate implements reconcile.Activator. It registers all of a plugin's
// vendors or none.
func (a *ModelVendors) Activate(_ context.Context, l *reconcile.Loaded) error {
	var ids []string
	for _, c := range l.Manifest.Contributes[manifest.PointModelVendors] {
		id := manifest.QualifiedID(l.Manifest.ID, c.ID)
		def, err := vendorDefinition(l.Package, c)
		if err == nil {
			err = a.rt.RegisterPlugin(id, def, filepath.Join(l.Dir, filepath.FromSlash(path.Dir(c.Path))))
		}
		if err != nil {
			for _, done := range ids {
				a.rt.Unregister(done)
			}
			return fmt.Errorf("model vendor %s: %w", c.ID, err)
		}
		ids = append(ids, id)
	}
	a.mu.Lock()
	a.registered[l.Manifest.ID] = ids
	a.mu.Unlock()
	return nil
}

// Deactivate implements reconcile.Activator.
func (a *ModelVendors) Deactivate(_ context.Context, pluginID string) error {
	a.mu.Lock()
	ids := a.registered[pluginID]
	delete(a.registered, pluginID)
	a.mu.Unlock()
	for _, id := range ids {
		a.rt.Unregister(id)
	}
	return nil
}

// vendorDefinition reads one vendor file (YAML or JSON) as overlay JSON,
// taking the name and description from the manifest when the file has none.
func vendorDefinition(p *pkg.Package, c manifest.Contribution) ([]byte, error) {
	raw, ok := p.ReadFile(c.Path)
	if !ok {
		return nil, fmt.Errorf("%s is not in the package", c.Path)
	}
	var def map[string]any
	if err := yaml.Unmarshal(raw, &def); err != nil {
		return nil, fmt.Errorf("parse %s: %w", c.Path, err)
	}
	if def == nil {
		return nil, fmt.Errorf("%s is empty", c.Path)
	}
	if _, ok := def["name"]; !ok && c.Name.Default != "" {
		def["name"] = c.Name.Default
		if len(c.Name.Locales) > 0 {
			def["names"] = c.Name.Locales
		}
	}
	if _, ok := def["description"]; !ok && c.Description.Default != "" {
		def["description"] = c.Description.Default
		if len(c.Description.Locales) > 0 {
			def["descriptions"] = c.Description.Locales
		}
	}
	return json.Marshal(def)
}

// CheckModelVendors verifies at install time that every vendor a package
// declares would register, against a scratch runtime.
func CheckModelVendors(p *pkg.Package) error {
	contribs := p.Manifest.Contributes[manifest.PointModelVendors]
	if len(contribs) == 0 {
		return nil
	}
	dir, err := os.MkdirTemp("", "weknora-vendor-check-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// Icons are read from disk, so the files they may name are written out.
	for _, name := range p.Files("") {
		data, _ := p.ReadFile(name)
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	scratch := modelruntime.New()
	var errs []error
	for _, c := range contribs {
		def, err := vendorDefinition(p, c)
		if err == nil {
			err = scratch.RegisterPlugin(manifest.QualifiedID(p.Manifest.ID, c.ID), def,
				filepath.Join(dir, filepath.FromSlash(path.Dir(c.Path))))
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("model vendor %s: %w", c.ID, err))
		}
	}
	return errors.Join(errs...)
}
