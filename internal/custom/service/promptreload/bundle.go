// Package promptreload provides atomic, fail-closed loading for runtime prompt bundles.
package promptreload

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Snapshot is an immutable view of one prompt bundle. Callers should retain it
// for the whole model task so a file edit cannot change prompts mid-task.
type Snapshot struct {
	Version  string
	Prompts  map[string]string
	Schemas  map[string]string
	External bool
}

// Bundle loads a complete set of prompt files as one atomic version. When Dir
// is empty, the fallback bundle is used. A configured directory must contain
// every named file before a new version can become active.
type Bundle struct {
	Dir      string
	Fallback map[string]string
	Schemas  map[string]string
	// Validate runs before an external snapshot is activated. It may compile
	// prompts or enforce a bundle manifest without exposing prompt text.
	Validate func(map[string]string) error
	// ValidateSchemas runs before an external snapshot is activated.
	ValidateSchemas func(map[string]string) error

	mu          sync.Mutex
	current     Snapshot
	initialized bool
	lastError   error
}

// New returns a bundle with defensive copies of fallback prompt content.
func New(dir string, fallback map[string]string) *Bundle {
	return NewWithSchemas(dir, fallback, nil)
}

// NewWithSchemas returns a bundle whose prompts and schemas are one atomic
// version. A configured directory must contain every fallback prompt and
// schema before a new snapshot can become active.
func NewWithSchemas(dir string, fallback, schemas map[string]string) *Bundle {
	copyFallback := make(map[string]string, len(fallback))
	for name, content := range fallback {
		copyFallback[name] = content
	}
	copySchemas := make(map[string]string, len(schemas))
	for name, content := range schemas {
		copySchemas[name] = content
	}
	return &Bundle{Dir: strings.TrimSpace(dir), Fallback: copyFallback, Schemas: copySchemas}
}

// Load returns the currently valid bundle, reloading only when the complete
// external bundle is readable and non-empty. Reload errors retain the previous
// valid snapshot and are returned for observability.
func (b *Bundle) Load() (Snapshot, error) {
	if b == nil {
		return Snapshot{}, fmt.Errorf("prompt bundle is nil")
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.Dir == "" {
		if !b.initialized {
			b.current = makeSnapshot(b.Fallback, b.Schemas, false)
			b.initialized = true
		}
		b.lastError = nil
		return cloneSnapshot(b.current), nil
	}

	names := make([]string, 0, len(b.Fallback))
	for name := range b.Fallback {
		names = append(names, name)
	}
	sort.Strings(names)
	loaded := make(map[string]string, len(names))
	for _, name := range names {
		path := filepath.Join(b.Dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return b.keepOrFallback(fmt.Errorf("read prompt %q: %w", name, err))
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			return b.keepOrFallback(fmt.Errorf("prompt %q is empty", name))
		}
		loaded[name] = content
	}

	schemaNames := make([]string, 0, len(b.Schemas))
	for name := range b.Schemas {
		schemaNames = append(schemaNames, name)
	}
	sort.Strings(schemaNames)
	loadedSchemas := make(map[string]string, len(schemaNames))
	for _, name := range schemaNames {
		path := filepath.Join(b.Dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return b.keepOrFallback(fmt.Errorf("read schema %q: %w", name, err))
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			return b.keepOrFallback(fmt.Errorf("schema %q is empty", name))
		}
		loadedSchemas[name] = content
	}

	next := makeSnapshot(loaded, loadedSchemas, true)
	if b.Validate != nil {
		if err := b.Validate(cloneSnapshot(next).Prompts); err != nil {
			return b.keepOrFallback(fmt.Errorf("validate prompt bundle: %w", err))
		}
	}
	if b.ValidateSchemas != nil {
		if err := b.ValidateSchemas(cloneSnapshot(next).Schemas); err != nil {
			return b.keepOrFallback(fmt.Errorf("validate schema bundle: %w", err))
		}
	}
	if !b.initialized || next.Version != b.current.Version {
		b.current = next
		b.initialized = true
	}
	b.lastError = nil
	return cloneSnapshot(b.current), nil
}

// LastError reports the most recent reload error without changing the active
// snapshot. It is intended for metrics/logging, not model decisions.
func (b *Bundle) LastError() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastError
}

func (b *Bundle) keepOrFallback(err error) (Snapshot, error) {
	b.lastError = err
	if b.initialized {
		return cloneSnapshot(b.current), err
	}
	if len(b.Fallback) == 0 {
		return Snapshot{}, err
	}
	b.current = makeSnapshot(b.Fallback, b.Schemas, false)
	b.initialized = true
	return cloneSnapshot(b.current), err
}

func makeSnapshot(prompts, schemas map[string]string, external bool) Snapshot {
	names := make([]string, 0, len(prompts))
	for name := range prompts {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	copyPrompts := make(map[string]string, len(prompts))
	for _, name := range names {
		content := strings.TrimSpace(prompts[name])
		copyPrompts[name] = content
		fmt.Fprintf(h, "%s\x00%s\x00", name, content)
	}
	schemaNames := make([]string, 0, len(schemas))
	for name := range schemas {
		schemaNames = append(schemaNames, name)
	}
	sort.Strings(schemaNames)
	copySchemas := make(map[string]string, len(schemas))
	for _, name := range schemaNames {
		content := strings.TrimSpace(schemas[name])
		copySchemas[name] = content
		fmt.Fprintf(h, "schema:%s\x00%s\x00", name, content)
	}
	return Snapshot{Version: fmt.Sprintf("sha256:%x", h.Sum(nil)), Prompts: copyPrompts, Schemas: copySchemas, External: external}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	copyPrompts := make(map[string]string, len(snapshot.Prompts))
	for name, content := range snapshot.Prompts {
		copyPrompts[name] = content
	}
	snapshot.Prompts = copyPrompts
	copySchemas := make(map[string]string, len(snapshot.Schemas))
	for name, content := range snapshot.Schemas {
		copySchemas[name] = content
	}
	snapshot.Schemas = copySchemas
	return snapshot
}
