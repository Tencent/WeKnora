package repository

import (
	"context"
	"database/sql/driver"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// Lite runs in one process. Bounded, process-wide stripes cover separate
// repository/service instances without retaining one mutex per deleted knowledge base.
var wikiPageWriteLocks = func() [256]chan struct{} {
	var locks [256]chan struct{}
	for i := range locks {
		locks[i] = make(chan struct{}, 1)
	}
	return locks
}()

// WithPageWriteLock serializes page writes and embedding configuration within
// a knowledge base through BOTH the SQL write and external index replacement.
// A lease expiring during a slow embedding request must not
// let archive/delete race a still-running publisher, so PostgreSQL uses a
// session advisory lock rather than an expiring Redis lease.
func (r *wikiPageRepository) WithPageWriteLock(ctx context.Context, kbID, _ string, fn func() error) error {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte("wiki-index-kb:" + kbID))
	key := hash.Sum64()
	lock := wikiPageWriteLocks[key%uint64(len(wikiPageWriteLocks))]
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.wikiDialect() == "sqlite" {
		return fn()
	}
	if r.wikiDialect() != "postgres" {
		return fmt.Errorf("unsupported wiki lock dialect: %s", r.wikiDialect())
	}
	db, err := r.db.DB()
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	defer func() {
		// A canceled request still releases its session lock. If the connection
		// cannot be unlocked, discard it rather than returning a locked session
		// to the pool. Closing the session also releases PostgreSQL advisory locks.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, "SELECT pg_advisory_unlock($1)", int64(key)); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", int64(key)); err != nil {
		return err
	}

	return fn()
}

// UpdateDerivedMeta cannot overwrite status from a stale link/folder snapshot
// while a lifecycle write holds the page's index lock. These caches do not
// affect the content sent to retrieval and need no external index replacement.
func (r *wikiPageRepository) UpdateDerivedMeta(ctx context.Context, page *types.WikiPage, placement bool) error {
	fields := map[string]interface{}{"in_links": page.InLinks, "out_links": page.OutLinks, "updated_at": page.UpdatedAt}
	if placement {
		fields = map[string]interface{}{
			"folder_id": page.FolderID, "category_path": page.CategoryPath,
			"wiki_path": page.WikiPath, "depth": page.Depth, "parent_slug": page.ParentSlug,
			"sort_order": page.SortOrder,
			"updated_at": page.UpdatedAt,
		}
	}
	result := r.db.WithContext(ctx).Model(&types.WikiPage{}).
		Where("id = ? AND tenant_id = ?", page.ID, page.TenantID).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWikiPageNotFound
	}
	return nil
}
