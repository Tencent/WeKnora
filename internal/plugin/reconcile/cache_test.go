package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
)

// Extracted packages nothing has used for a while go; the loaded one, fresh
// ones and files that are not packages stay.
func TestSweepCacheRemovesUnusedPackages(t *testing.T) {
	ctx := context.Background()
	cache := t.TempDir()
	repo, store := plugintest.NewMemRepo(), &plugintest.MemStore{}
	r := New(Options{Repo: repo, Store: store, Registry: registry.New(), CacheDir: cache})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	loaded := r.Loaded()[0].Dir
	old := filepath.Join(cache, strings.Repeat("a", 64))
	fresh := filepath.Join(cache, strings.Repeat("b", 64))
	halfWritten := filepath.Join(cache, strings.Repeat("c", 64)+".tmp-1a2b3c4d")
	other := filepath.Join(cache, "notes.txt")
	for _, dir := range []string{old, fresh, halfWritten} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(other, []byte("x"), 0o644)
	long := time.Now().Add(-2 * cacheUnusedFor)
	for _, p := range []string{old, halfWritten, other, loaded} {
		_ = os.Chtimes(p, long, long)
	}

	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{old: false, halfWritten: false, fresh: true, other: true, loaded: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s: exists = %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}
