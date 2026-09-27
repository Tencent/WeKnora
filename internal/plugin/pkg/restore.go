package pkg

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/Tencent/WeKnora/internal/utils"
)

// Restore makes dir, where p was extracted, hold exactly p's files again: a
// file missing or changed since (a temp directory cleaner, another process
// of WeKnora's user) is written again, and anything the package does not
// hold is removed, so nothing planted there runs with the plugin (a
// sitecustomize.py on a Python plugin's path would). It returns what it
// changed, sorted.
func Restore(p *Package, dir string) ([]string, error) {
	var changed []string
	want := map[string]bool{}
	for _, name := range p.Files("") {
		want[filepath.FromSlash(name)] = true
		data, _ := p.ReadFile(name)
		target, err := utils.SafeJoinUnderBase(dir, name)
		if err != nil {
			return changed, err
		}
		if cur, err := os.ReadFile(target); err == nil && bytes.Equal(cur, data) {
			continue
		}
		if err := writeAtomic(target, data); err != nil {
			return changed, err
		}
		changed = append(changed, name)
	}
	var extra []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			if !wantsDir(want, rel) {
				extra = append(extra, rel)
				return filepath.SkipDir
			}
			return nil
		}
		if !want[rel] {
			extra = append(extra, rel)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return changed, err
	}
	for _, rel := range extra {
		if err := os.RemoveAll(filepath.Join(dir, rel)); err != nil {
			return changed, err
		}
		changed = append(changed, filepath.ToSlash(rel))
	}
	sort.Strings(changed)
	return changed, nil
}

// wantsDir reports whether a directory holds any of the package's files.
func wantsDir(want map[string]bool, rel string) bool {
	prefix := rel + string(filepath.Separator)
	for name := range want {
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// writeAtomic replaces a file whole, so a process reading it meanwhile
// sees the old or the new content, never a mix.
func writeAtomic(target string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".restore-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o644)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), target)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}
