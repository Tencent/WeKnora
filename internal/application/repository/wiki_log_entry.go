package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// wikiLogEntryRepository implements interfaces.WikiLogEntryRepository.
type wikiLogEntryRepository struct {
	db *gorm.DB
}

// NewWikiLogEntryRepository constructs a GORM-backed WikiLogEntryRepository.
func NewWikiLogEntryRepository(db *gorm.DB) interfaces.WikiLogEntryRepository {
	return &wikiLogEntryRepository{db: db}
}

// AppendBatch inserts every entry in one statement. Empty batches are a
// no-op so callers can invoke unconditionally at the end of a wiki ingest
// batch without guarding against the "no events this round" case.
func (r *wikiLogEntryRepository) AppendBatch(ctx context.Context, entries []*types.WikiLogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&entries).Error
}

// List returns up to `limit` entries strictly older than `cursor`, newest
// first. `cursor` is the stringified ID of the oldest entry from the
// previous page; an empty string starts from the newest. Callers get back
// a nextCursor string to pass on the next request — empty means no more.
//
// `limit` is clamped to [1, 200]. Values outside that range are coerced
// silently to keep the handler simple.
func (r *wikiLogEntryRepository) List(
	ctx context.Context,
	kbID string,
	cursor string,
	limit int,
) ([]*types.WikiLogEntry, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	q := r.db.WithContext(ctx).
		Where("knowledge_base_id = ?", kbID).
		Order("id DESC").
		Limit(limit)

	if cursor != "" {
		cursorID, err := strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor %q: %w", cursor, err)
		}
		q = q.Where("id < ?", cursorID)
	}

	var entries []*types.WikiLogEntry
	if err := q.Find(&entries).Error; err != nil {
		return nil, "", err
	}

	nextCursor := ""
	// Only emit a cursor when we actually filled the page — a short page
	// is guaranteed to be the tail, so returning a cursor would just cause
	// the frontend to fire a final empty request.
	if len(entries) == limit {
		nextCursor = strconv.FormatUint(entries[len(entries)-1].ID, 10)
	}
	return entries, nextCursor, nil
}

// DeleteByKB drops every log entry for a KB. No "affected rows" signal is
// surfaced — missing rows are a legitimate state (e.g., a KB that was
// created before wiki_log_entries existed) and not a failure condition.
func (r *wikiLogEntryRepository) DeleteByKB(ctx context.Context, kbID string) error {
	if kbID == "" {
		return errors.New("wiki log entries: empty kb id")
	}
	return r.db.WithContext(ctx).
		Where("knowledge_base_id = ?", kbID).
		Delete(&types.WikiLogEntry{}).Error
}
