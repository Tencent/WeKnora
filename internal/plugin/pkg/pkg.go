// Package pkg reads plugin packages: zip archives (.wkp) with plugin.yaml at
// the root. Opening a package validates the manifest, checks that the files it
// names exist, and fingerprints the archive, so an installer only ever stores
// packages that will load.
package pkg

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
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
	Size  int64
	files map[string][]byte
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
	files = stripSingleRoot(files)

	raw, ok := files[ManifestFile]
	if !ok {
		return nil, fmt.Errorf("package has no %s at its root", ManifestFile)
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	p := &Package{Manifest: m, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(data)), files: files}
	if err := p.checkReferences(); err != nil {
		return nil, err
	}
	return p, nil
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

// stripSingleRoot drops a single top-level directory shared by every file,
// unless the manifest already sits at the root.
func stripSingleRoot(files map[string][]byte) map[string][]byte {
	if _, ok := files[ManifestFile]; ok || len(files) == 0 {
		return files
	}
	var root string
	for name := range files {
		top, _, found := strings.Cut(name, "/")
		if !found || (root != "" && top != root) {
			return files
		}
		root = top
	}
	out := make(map[string][]byte, len(files))
	for name, b := range files {
		out[strings.TrimPrefix(name, root+"/")] = b
	}
	return out
}
