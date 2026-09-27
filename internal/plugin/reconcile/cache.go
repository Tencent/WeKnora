package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// cacheUnusedFor is how long an extracted package goes unused before it is
// removed: long enough for a process handing over to finish, and for
// another WeKnora process sharing the cache directory to mark what it uses.
var cacheUnusedFor = time.Hour

// cacheEntry matches what extract writes: a package directory named by its
// digest, or one being written. Nothing else in the directory is touched.
var cacheEntry = regexp.MustCompile(`^[0-9a-f]{64}(\.tmp-[0-9a-f-]+)?$`)

// sweepCache marks the extracted packages this node uses and removes those
// no process has used for a while: every version installed, upgraded to or
// rolled back leaves one, and a rollback extracts again if it needs to.
// Processes sharing the directory keep theirs by marking them each pass.
func (r *Reconciler) sweepCache(ctx context.Context) {
	now := r.now()
	inUse := map[string]bool{}
	mark := func(dir string) {
		if dir == "" {
			return
		}
		inUse[filepath.Base(dir)] = true
		_ = os.Chtimes(dir, now, now)
	}
	for _, l := range r.loaded {
		mark(l.Dir)
	}
	for _, st := range r.staging {
		mark(st.l.Dir)
	}
	entries, err := os.ReadDir(r.cacheDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if inUse[name] || !cacheEntry.MatchString(name) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < cacheUnusedFor {
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.cacheDir, name)); err != nil {
			logger.Warnf(ctx, "[plugin] remove unused extracted package %s: %v", name, err)
		}
	}
}
