// Package plugintest has in-memory fakes and sample packages for plugin tests.
package plugintest

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/types"
)

// MemRepo is an in-memory interfaces.PluginRepository.
type MemRepo struct {
	mu       sync.Mutex
	plugins  map[string]types.InstalledPlugin
	versions map[string]types.PluginVersion
}

// NewMemRepo returns an empty in-memory PluginRepository.
func NewMemRepo() *MemRepo {
	return &MemRepo{plugins: map[string]types.InstalledPlugin{}, versions: map[string]types.PluginVersion{}}
}

// ListPlugins returns every row.
func (m *MemRepo) ListPlugins(context.Context) ([]types.InstalledPlugin, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []types.InstalledPlugin
	for _, p := range m.plugins {
		out = append(out, p)
	}
	return out, nil
}

// GetPlugin returns (nil, nil) for an unknown ID.
func (m *MemRepo) GetPlugin(_ context.Context, id string) (*types.InstalledPlugin, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.plugins[id]; ok {
		return &p, nil
	}
	return nil, nil
}

// SavePlugin upserts a row.
func (m *MemRepo) SavePlugin(_ context.Context, p *types.InstalledPlugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plugins[p.ID] = *p
	return nil
}

// DeletePlugin removes a row and its versions.
func (m *MemRepo) DeletePlugin(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.plugins, id)
	for k, v := range m.versions {
		if v.PluginID == id {
			delete(m.versions, k)
		}
	}
	return nil
}

// ListVersions returns a plugin's versions.
func (m *MemRepo) ListVersions(_ context.Context, id string) ([]types.PluginVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []types.PluginVersion
	for _, v := range m.versions {
		if v.PluginID == id {
			out = append(out, v)
		}
	}
	return out, nil
}

// GetVersion returns (nil, nil) for an unknown version.
func (m *MemRepo) GetVersion(_ context.Context, id, version string) (*types.PluginVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.versions[id+"@"+version]; ok {
		return &v, nil
	}
	return nil, nil
}

// SaveVersion upserts a version.
func (m *MemRepo) SaveVersion(_ context.Context, v *types.PluginVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.versions[v.PluginID+"@"+v.Version] = *v
	return nil
}

// MemStore is an in-memory package store.
type MemStore struct {
	mu    sync.Mutex
	Blobs map[string][]byte
}

// Put stores a blob under mem://<digest>.
func (s *MemStore) Put(_ context.Context, digest string, data []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Blobs == nil {
		s.Blobs = map[string][]byte{}
	}
	uri := "mem://" + digest
	s.Blobs[uri] = data
	return uri, nil
}

// Get returns a stored blob.
func (s *MemStore) Get(_ context.Context, uri string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.Blobs[uri]
	if !ok {
		return nil, fmt.Errorf("%s not found", uri)
	}
	return b, nil
}

// Delete drops a blob.
func (s *MemStore) Delete(_ context.Context, uri string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Blobs, uri)
	return nil
}

// KitPackage builds the acme.kit declarative package at a version: one
// skill, skills/triage. The same version always yields the same bytes.
func KitPackage(t testing.TB, version string) []byte {
	return KitPackageWith(t, version, "")
}

// KitPackageWith is KitPackage with a different skill body and extra
// top-level manifest lines.
func KitPackageWith(t testing.TB, version, skillBody string, manifestLines ...string) []byte {
	t.Helper()
	if skillBody == "" {
		skillBody = version
	}
	manifest := "schemaVersion: 1\nid: acme.kit\nversion: " + version + "\n" +
		"name: { en-US: ACME Kit }\npublisher: { id: acme }\nruntime: { type: declarative }\n"
	for _, line := range manifestLines {
		manifest += line + "\n"
	}
	manifest += "contributes:\n  skills:\n    - { id: triage, name: Triage, path: skills/triage }\n"
	return Zip(t, map[string]string{
		"plugin.yaml":            manifest,
		"skills/triage/SKILL.md": "---\nname: triage\ndescription: Triage issues by severity.\n---\n" + skillBody,
	})
}

// Zip builds an archive from name → content, entries in name order.
func Zip(t testing.TB, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Install stores a package version and points the plugin row at it.
func Install(t testing.TB, repo *MemRepo, store *MemStore, data []byte, state string) {
	t.Helper()
	ctx := context.Background()
	p, err := pkg.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	uri, _ := store.Put(ctx, p.Digest, data)
	_ = repo.SaveVersion(ctx, &types.PluginVersion{
		PluginID: p.Manifest.ID, Version: p.Manifest.Version, Digest: p.Digest, PackageURI: uri,
	})
	_ = repo.SavePlugin(ctx, &types.InstalledPlugin{
		ID: p.Manifest.ID, ActiveVersion: p.Manifest.Version, DesiredState: state, Runtime: "declarative",
	})
}

// MemTenantSettings is an in-memory interfaces.PluginTenantSettingRepository.
type MemTenantSettings struct {
	mu   sync.Mutex
	rows map[string]types.PluginTenantSetting
}

func tenantKey(tenantID uint64, pluginID string) string {
	return fmt.Sprintf("%d/%s", tenantID, pluginID)
}

// List returns a tenant's rows.
func (m *MemTenantSettings) List(_ context.Context, tenantID uint64) ([]types.PluginTenantSetting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []types.PluginTenantSetting
	for _, r := range m.rows {
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	return out, nil
}

// Get returns (nil, nil) for a row never written.
func (m *MemTenantSettings) Get(
	_ context.Context, tenantID uint64, pluginID string,
) (*types.PluginTenantSetting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[tenantKey(tenantID, pluginID)]; ok {
		return &r, nil
	}
	return nil, nil
}

// Upsert inserts a row or updates the given columns.
func (m *MemTenantSettings) Upsert(_ context.Context, s *types.PluginTenantSetting, columns ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]types.PluginTenantSetting{}
	}
	key := tenantKey(s.TenantID, s.PluginID)
	row, ok := m.rows[key]
	if !ok || len(columns) == 0 {
		m.rows[key] = *s
		return nil
	}
	for _, c := range columns {
		switch c {
		case "enabled":
			row.Enabled = s.Enabled
		case "config":
			row.Config = s.Config
		}
	}
	row.UpdatedBy, row.UpdatedAt = s.UpdatedBy, s.UpdatedAt
	m.rows[key] = row
	return nil
}
