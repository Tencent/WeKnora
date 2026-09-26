// Package pkg reads plugin packages: zip archives (.wkp) with plugin.yaml at
// the root. Opening a package validates the manifest, checks that the files it
// names exist, and fingerprints the archive, so an installer only ever stores
// packages that will load.
package pkg

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/pluginsdk/pluginsign"
)

// ManifestFile is the manifest's name at the package root.
const ManifestFile = "plugin.yaml"

// Limits keep a hostile archive from exhausting memory or disk.
const (
	MaxArchiveBytes      = 64 << 20  // compressed upload
	MaxUncompressedBytes = 256 << 20 // all files together
	MaxFileBytes         = 64 << 20  // one file
	MaxFiles             = 4000
)

// Package is an opened, validated plugin package.
type Package struct {
	Manifest *manifest.Manifest
	// Digest is "sha256:<hex>" of the archive bytes; packages are stored and
	// cached under it.
	Digest string
	// Size is the archive size in bytes.
	Size int64
	// Signature is the package's plugin.sig, nil when it is unsigned. It is
	// only a claim until VerifySignature checks it against a trusted key.
	Signature *pluginsign.Signature
	files     map[string][]byte
}

// VerifySignature checks the package's signature with the public key of the
// key it names.
func (p *Package) VerifySignature(pub ed25519.PublicKey) error {
	if p.Signature == nil {
		return errors.New("the package is not signed")
	}
	return p.Signature.Verify(p.files, pub)
}

// Open reads and validates a package archive. Archives whose content sits in
// a single top-level directory (as GitHub source archives do) are accepted;
// that directory becomes the package root.
func Open(data []byte) (*Package, error) {
	if len(data) > MaxArchiveBytes {
		return nil, fmt.Errorf("package is %d bytes, over the %d byte limit", len(data), MaxArchiveBytes)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("package is not a zip archive: %w", err)
	}
	if len(zr.File) > MaxFiles {
		return nil, fmt.Errorf("package has %d files, over the %d file limit", len(zr.File), MaxFiles)
	}

	files := make(map[string][]byte, len(zr.File))
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name, err := cleanName(f.Name)
		if err != nil {
			return nil, err
		}
		if f.FileInfo().Mode().Type() != 0 {
			return nil, fmt.Errorf("package entry %s is not a regular file", name)
		}
		if f.UncompressedSize64 > MaxFileBytes {
			return nil, fmt.Errorf("package entry %s is over the %d byte limit", name, MaxFileBytes)
		}
		b, err := readEntry(f)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		total += int64(len(b))
		if total > MaxUncompressedBytes {
			return nil, fmt.Errorf("package expands to over %d bytes", MaxUncompressedBytes)
		}
		files[name] = b
	}
	files = pluginsign.StripSingleRoot(files)

	raw, ok := files[ManifestFile]
	if !ok {
		return nil, fmt.Errorf("package has no %s at its root", ManifestFile)
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return nil, err
	}
	sig, err := pluginsign.Read(files)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	p := &Package{
		Manifest: m, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(data)),
		Signature: sig, files: files,
	}
	if err := p.checkReferences(); err != nil {
		return nil, err
	}
	p.Manifest.IconData = p.iconData(p.Manifest.Icon)
	if err := p.loadConfigSchemas(); err != nil {
		return nil, err
	}
	return p, nil
}

// loadConfigSchemas parses the config schema files (JSON or YAML) into the
// manifest and checks that every ${scope.key} a template uses is declared.
func (p *Package) loadConfigSchemas() error {
	m := p.Manifest
	var errs []error
	load := func(file string) (*configschema.Schema, json.RawMessage) {
		if file == "" {
			return nil, nil
		}
		raw, err := yamlToJSON(p.files[file])
		if err != nil {
			errs = append(errs, fmt.Errorf("config schema %s: %w", file, err))
			return nil, nil
		}
		s, err := configschema.Parse(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("config schema %s: %w", file, err))
			return nil, nil
		}
		return s, raw
	}
	system, systemRaw := load(m.Config.System)
	tenant, tenantRaw := load(m.Config.Tenant)
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	m.Config.SystemSchema, m.Config.TenantSchema = systemRaw, tenantRaw
	schemas := []*configschema.Schema{system, tenant}
	for point, list := range m.Contributes {
		for i := range list {
			if list[i].InstanceSchema == "" {
				continue
			}
			s, raw := load(list[i].InstanceSchema)
			m.Contributes[point][i].InstanceSchemaJSON = raw
			schemas = append(schemas, s)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	declared := func(s *configschema.Schema, key string) bool {
		return s != nil && s.Properties[key] != nil
	}
	for _, c := range m.Contributes[manifest.PointMCPServers] {
		if c.MCP == nil {
			continue
		}
		for name, value := range c.MCP.Headers {
			for _, ref := range manifest.TemplateRefs(value) {
				ok := (ref.Scope == manifest.ScopeConfig && declared(tenant, ref.Key)) ||
					(ref.Scope == manifest.ScopeSystem && declared(system, ref.Key))
				if !ok {
					which := "config.tenant"
					if ref.Scope == manifest.ScopeSystem {
						which = "config.system"
					}
					errs = append(errs, fmt.Errorf(
						"mcpServers.%s header %s uses ${%s.%s}, which the %s schema does not declare",
						c.ID, name, ref.Scope, ref.Key, which))
				}
			}
		}
	}
	// An OAuth app is the platform's: its client ID and secret come from the
	// system configuration.
	for _, s := range schemas {
		for path, spec := range s.OAuthFields() {
			for _, v := range []string{spec.ClientID, spec.ClientSecret} {
				for _, ref := range manifest.TemplateRefs(v) {
					if ref.Scope != manifest.ScopeSystem || !declared(system, ref.Key) {
						errs = append(errs, fmt.Errorf(
							"%s: x-oauth client credentials may only use ${system.<key>} the config.system "+
								"schema declares, not ${%s.%s}",
							path, ref.Scope, ref.Key))
					}
				}
			}
		}
	}
	return errors.Join(errs...)
}

// yamlToJSON accepts a YAML (or JSON) document and returns it as JSON.
func yamlToJSON(data []byte) (json.RawMessage, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// ReadFile returns one file of the package.
func (p *Package) ReadFile(name string) ([]byte, bool) {
	b, ok := p.files[name]
	return b, ok
}

// Files returns the files under a directory (or the file itself), sorted,
// with paths relative to the package root. An empty dir lists every file.
func (p *Package) Files(dir string) []string {
	dir = strings.TrimSuffix(dir, "/")
	var out []string
	for name := range p.files {
		if dir == "" || name == dir || strings.HasPrefix(name, dir+"/") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// checkReferences verifies that the files a manifest names are in the package.
func (p *Package) checkReferences() error {
	var errs []error
	need := func(what, name string) {
		if _, ok := p.files[name]; !ok {
			errs = append(errs, fmt.Errorf("%s %s is not in the package", what, name))
		}
	}
	m := p.Manifest
	if m.Icon != "" {
		need("icon", m.Icon)
	}
	if m.Config.System != "" {
		need("config schema", m.Config.System)
	}
	if m.Config.Tenant != "" {
		need("config schema", m.Config.Tenant)
	}
	for _, info := range manifest.Points() {
		for _, c := range m.Contributes[info.Point] {
			switch info.Point {
			case manifest.PointSkills:
				need("skill", path.Join(c.Path, "SKILL.md"))
			case manifest.PointModelVendors:
				need("model vendor definition", c.Path)
			}
			if manifest.IsUIPoint(info.Point) && c.Entry != "" {
				need("page", c.Entry)
			}
			if c.Editor != "" {
				need("editor page", c.Editor)
			}
			for _, v := range c.ToolViews {
				if v.View == manifest.ToolViewPage {
					need("tool result page", v.Entry)
				}
			}
			if c.Icon != "" {
				need("icon", c.Icon)
			}
			if c.InstanceSchema != "" {
				need("config schema", c.InstanceSchema)
			}
		}
	}
	return errors.Join(errs...)
}

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	// Read one byte past the limit so a lying header cannot sneak by.
	b, err := io.ReadAll(io.LimitReader(rc, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFileBytes {
		return nil, errors.New("entry is over the size limit")
	}
	return b, nil
}

// cleanName normalizes an archive entry name and rejects any that would
// escape the package root.
func cleanName(name string) (string, error) {
	n := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(n, "/") || strings.Contains(n, ":") {
		return "", fmt.Errorf("package entry %q has an absolute path", name)
	}
	clean := path.Clean(n)
	if clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
		return "", fmt.Errorf("package entry %q escapes the package root", name)
	}
	return clean, nil
}

// maxIconBytes caps the icon carried in the manifest; a larger one is left
// out and lists show the plugin's initial.
const maxIconBytes = 64 << 10

// iconData is a package icon as a data: URI, or "" when there is none or it
// is not a small SVG, PNG, JPEG or WebP image.
func (p *Package) iconData(file string) string {
	b, ok := p.ReadFile(file)
	if !ok || len(b) == 0 || len(b) > maxIconBytes {
		return ""
	}
	mime := map[string]string{
		".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".webp": "image/webp",
	}[strings.ToLower(path.Ext(file))]
	if mime == "" {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}
