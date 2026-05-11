// Package secrets stores and retrieves credentials.
//
// v0.0 ships a file-only Store implementation (0600 perms under
// $XDG_CONFIG_HOME/weknora/secrets/). The OS keyring backend lands in v0.1
// once auth login is needed in TTY contexts (current PR is foundation only).
//
// Namespace convention: "weknora:<context>:<key>" where key is "access" or "refresh".
package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/cli/internal/xdg"
)

// ErrNotFound is returned when the requested secret does not exist.
var ErrNotFound = errors.New("secret: not found")

// Store is the abstraction CLI commands depend on; tests inject in-memory impls.
//
// Ref returns a stable URI (e.g. file://<context>/<key> or
// keychain://weknora/<context>/<key>) describing where a saved secret lives.
// Backends own their scheme so commands never need to type-assert the
// concrete implementation.
type Store interface {
	Get(context, key string) (string, error)
	Set(context, key, value string) error
	Delete(context, key string) error
	Ref(context, key string) string
}

// FileStore writes 0600 plain-text files under $XDG_CONFIG_HOME/weknora/secrets/<context>.
// It is the headless / CI default and the keychain fallback. Real keychain support
// is wired in v0.1 (see ADR-17 for namespace strategy).
type FileStore struct {
	root string
}

// NewFileStore returns a FileStore rooted at $XDG_CONFIG_HOME/weknora/secrets
// (or ~/.config/weknora/secrets if XDG_CONFIG_HOME is unset). Same convention
// as config.Path — see that file for the rationale (CLI convention).
func NewFileStore() (*FileStore, error) {
	root, err := defaultRoot()
	if err != nil {
		return nil, err
	}
	return &FileStore{root: root}, nil
}

func defaultRoot() (string, error) {
	return xdg.Path("XDG_CONFIG_HOME", ".config", "secrets")
}

func (f *FileStore) path(context, key string) string {
	return filepath.Join(f.root, context, key)
}

func (f *FileStore) Get(context, key string) (string, error) {
	data, err := os.ReadFile(f.path(context, key))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}
	return string(data), nil
}

func (f *FileStore) Set(context, key, value string) error {
	dir := filepath.Join(f.root, context)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir secrets dir: %w", err)
	}
	if err := os.WriteFile(f.path(context, key), []byte(value), 0o600); err != nil {
		return fmt.Errorf("write secret: %w", err)
	}
	return nil
}

func (f *FileStore) Delete(context, key string) error {
	err := os.Remove(f.path(context, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Ref returns the file:// URI under which a secret would be stored. Path
// component is forward-slash-normalized per RFC 8089 (file:///path on Unix,
// file:///C:/... on Windows) — the wire format must not depend on platform
// path separator since this string is persisted to config.yaml and may be
// consumed by tooling on a different OS.
func (f *FileStore) Ref(context, key string) string {
	p := filepath.ToSlash(f.path(context, key))
	if !strings.HasPrefix(p, "/") {
		// Windows absolute path "C:/..." needs a leading slash to form a
		// proper file:/// URI ("file:///C:/...").
		p = "/" + p
	}
	return "file://" + p
}

// MemStore is an in-memory implementation of Store used by tests across
// packages. It is intentionally exported (not _test.go-only) so that
// downstream packages can compose it without copying the same 20 lines.
type MemStore struct{ m map[string]string }

// NewMemStore returns an empty in-memory secrets store.
func NewMemStore() *MemStore { return &MemStore{m: map[string]string{}} }

func (m *MemStore) k(c, key string) string { return c + ":" + key }

func (m *MemStore) Get(c, key string) (string, error) {
	v, ok := m.m[m.k(c, key)]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *MemStore) Set(c, key, value string) error { m.m[m.k(c, key)] = value; return nil }

func (m *MemStore) Delete(c, key string) error { delete(m.m, m.k(c, key)); return nil }

// Ref returns a mem:// URI; meaningful only inside tests.
func (m *MemStore) Ref(c, key string) string { return "mem://" + c + "/" + key }
