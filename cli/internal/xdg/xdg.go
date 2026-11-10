// Package xdg consolidates the XDG-rooted file path lookup and atomic-write
// idioms used by config / compat / secrets / projectlink. Centralizing means
// a single place to fix behavior (mode bits, fallback dirs, mkdir order,
// error wrapping) instead of copy-pasting across stores.
package xdg

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Path resolves an XDG-rooted file. envVar is one of "XDG_CONFIG_HOME" /
// "XDG_CACHE_HOME" / "XDG_DATA_HOME". fallbackDir is the dot-prefixed dir
// under $HOME used when the env var is unset (".config" / ".cache" / etc.).
// parts join under "weknora/" inside the chosen root.
//
// Honors the XDG vars on every OS, even macOS - where os.UserConfigDir
// would otherwise return ~/Library/Application Support.
func Path(envVar, fallbackDir string, parts ...string) (string, error) {
	if x := os.Getenv(envVar); x != "" {
		return filepath.Join(append([]string{x, "weknora"}, parts...)...), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(append([]string{home, fallbackDir, "weknora"}, parts...)...), nil
}

// WriteAtomicYAML marshals v to YAML and writes it atomically at p with mode
// 0600 (user-only). Creates parent dirs with mode 0700. Atomicity via
// CreateTemp + chmod + rename so partial writes never expose a half-baked
// file and concurrent writers never trample each other's tmp.
//
// 0600 is the appropriate floor even for non-secret stores (cache, project
// link) since they may sit alongside secrets in the same dir tree.
//
// On any failure after the tmp is created, the tmp is cleaned up so we don't
// litter the dir with `*.tmp.NNNN` artifacts on crash / cross-device errors.
func WriteAtomicYAML(p string, v any) error {
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	// CreateTemp picks a unique name in dir so concurrent calls never collide
	// on a shared `<p>.tmp`. Pattern reserves the suffix slot.
	tmp, err := os.CreateTemp(dir, filepath.Base(p)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create tmp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup on any failure path; no-op once Rename succeeds.
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, p, err)
	}
	committed = true
	return nil
}
