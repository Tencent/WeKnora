package interfaces

import (
	"context"
	"time"
)

// ContentCacheRepository is a content-addressed cache store for deterministic
// pipeline products. Keys are derived from the computation inputs (content
// hash + model id + prompt/config version), so hits are only possible when
// the exact same input was computed before. Implementations must be safe for
// concurrent readers/writers (best-effort: a Set racing another Set for the
// same key may keep either value, since the payload is a pure function of the
// key).
type ContentCacheRepository interface {
	// Get returns the cached payload for cacheKey. found is false when the
	// key has no row.
	Get(ctx context.Context, cacheKey string) (payload []byte, found bool, err error)
	// Set upserts the payload for cacheKey.
	Set(ctx context.Context, cacheKey, kind string, payload []byte) error
	// Delete removes a single cache row.
	Delete(ctx context.Context, cacheKey string) error
	// PruneBefore deletes rows whose updated_at is older than before and
	// returns the number of rows removed. limit bounds the batch so a single
	// sweep cannot lock the table for minutes.
	PruneBefore(ctx context.Context, before time.Time, limit int) (int, error)
}
