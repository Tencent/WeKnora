package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type Package struct {
	Root         string
	ManifestPath string
	Manifest     Manifest
}

func DiscoverPackages(roots []string) ([]Package, error) {
	var paths []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("stat plugin root %q: %w", root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("plugin root %q is not a directory", root)
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			switch entry.Name() {
			case "plugin.yaml", "plugin.yml", "plugin.json":
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("discover plugins in %q: %w", root, err)
		}
	}
	sort.Strings(paths)
	packages := make([]Package, 0, len(paths))
	seen := make(map[string]string)
	for _, path := range paths {
		manifest, err := LoadManifest(path)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", path, err)
		}
		if previous, exists := seen[manifest.ID]; exists {
			return nil, fmt.Errorf("duplicate plugin id %q in %s and %s", manifest.ID, previous, path)
		}
		seen[manifest.ID] = path
		manifest.SourceDir = filepath.Dir(path)
		packages = append(packages, Package{Root: filepath.Dir(path), ManifestPath: path, Manifest: manifest})
	}
	return packages, nil
}
