package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// Artifacts live in message_artifacts, not on the message row. Every loader in
// this package that returns messages goes through attachArtifacts, and every
// writer goes through writeMessageArtifacts, so callers keep working with
// Message.Artifacts as if it were a column.

// artifactLoadBatch bounds the IN list when loading artifacts for many
// messages; SQLite caps bound parameters per statement.
const artifactLoadBatch = 500

// writeMessageArtifacts replaces the stored artifacts of one message. A nil
// list means "not loaded" and leaves the rows untouched; an empty non-nil list
// clears them.
//
// A delete that already happened is re-applied on top of the incoming list, by
// position. The caller usually carries the tombstones itself (they round-trip
// through MessageArtifact.DeletedAt), but it may be holding a slice it read
// before the delete — this is a full replace, so that snapshot would resurrect
// the row while its bytes are already being reclaimed, leaving a live-looking
// entry whose download 404s. Deletion is the one property of a row that a stale
// writer must not be able to undo.
func writeMessageArtifacts(tx *gorm.DB, message *types.Message) error {
	if message == nil || message.Artifacts == nil || message.ID == "" {
		return nil
	}
	deleted, err := deletedArtifactPositions(tx, message.ID)
	if err != nil {
		return err
	}
	if err := tx.Where("message_id = ?", message.ID).
		Delete(&types.MessageArtifactRecord{}).Error; err != nil {
		return err
	}
	rows := types.NewMessageArtifactRecords(message.SessionID, message.ID, message.Artifacts)
	if len(rows) == 0 {
		return nil
	}
	for i := range rows {
		if at, ok := deleted[rows[i].Position]; ok && rows[i].DeletedAt == nil {
			rows[i].DeletedAt = at
		}
	}
	return tx.CreateInBatches(rows, 100).Error
}

// deletedArtifactPositions returns the stored deletion times of a message's
// tombstoned artifacts, keyed by position.
func deletedArtifactPositions(tx *gorm.DB, messageID string) (map[int]*time.Time, error) {
	var rows []types.MessageArtifactRecord
	if err := tx.Select("position", "deleted_at").
		Where("message_id = ? AND deleted_at IS NOT NULL", messageID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make(map[int]*time.Time, len(rows))
	for _, row := range rows {
		out[row.Position] = row.DeletedAt
	}
	return out, nil
}

// insertMessageArtifacts writes the artifacts of freshly created messages.
// Unlike writeMessageArtifacts it skips the delete, since new messages have
// no rows yet.
func insertMessageArtifacts(tx *gorm.DB, messages []*types.Message) error {
	var rows []types.MessageArtifactRecord
	for _, m := range messages {
		if m == nil || m.ID == "" {
			continue
		}
		rows = append(rows, types.NewMessageArtifactRecords(m.SessionID, m.ID, m.Artifacts)...)
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.CreateInBatches(rows, 100).Error
}

// loadArtifactsByMessage returns each message's artifacts in position order.
// Every requested id is present in the result, with an empty list when the
// message has none.
//
// Tombstones (deleted_at set) are deliberately included: position is the index
// the download endpoint addresses a file by, so skipping a deleted row here
// would renumber every later file in the message. Callers that render a list
// filter with MessageArtifacts.Live.
func loadArtifactsByMessage(
	ctx context.Context, db *gorm.DB, messageIDs []string,
) (map[string]types.MessageArtifacts, error) {
	out := make(map[string]types.MessageArtifacts, len(messageIDs))
	for _, id := range messageIDs {
		out[id] = types.MessageArtifacts{}
	}
	for start := 0; start < len(messageIDs); start += artifactLoadBatch {
		end := min(start+artifactLoadBatch, len(messageIDs))
		var rows []types.MessageArtifactRecord
		if err := db.WithContext(ctx).
			Where("message_id IN ?", messageIDs[start:end]).
			Order("message_id ASC, position ASC").
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[row.MessageID] = append(out[row.MessageID], row.Artifact())
		}
	}
	return out, nil
}

// attachArtifacts fills Message.Artifacts on every message in place.
func attachArtifacts(ctx context.Context, db *gorm.DB, messages ...*types.Message) error {
	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		if m != nil && m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	byMessage, err := loadArtifactsByMessage(ctx, db, ids)
	if err != nil {
		return err
	}
	for _, m := range messages {
		if m != nil && m.ID != "" {
			m.Artifacts = byMessage[m.ID]
		}
	}
	return nil
}

// attachArtifactsWithSession is attachArtifacts for search-style results.
func attachArtifactsWithSession(ctx context.Context, db *gorm.DB, results []*types.MessageWithSession) error {
	messages := make([]*types.Message, 0, len(results))
	for _, r := range results {
		if r != nil {
			messages = append(messages, &r.Message)
		}
	}
	return attachArtifacts(ctx, db, messages...)
}

// GetSessionArtifacts returns every skill-produced MessageArtifact recorded
// against a live message of the session, in message then position order.
//
// User-deleted artifacts are included as tombstones. ArtifactCollector builds
// its de-duplication set from this call, and a tombstone is exactly what stops
// it re-attaching a file the user deleted while the sandbox copy — same path,
// same mtime — is still sitting there. Display paths filter with
// MessageArtifacts.Live.
func (r *messageRepository) GetSessionArtifacts(
	ctx context.Context, sessionID string,
) (types.MessageArtifacts, error) {
	if sessionID == "" {
		return nil, nil
	}
	var rows []types.MessageArtifactRecord
	if err := r.db.WithContext(ctx).
		Table("message_artifacts AS ma").
		Select("ma.*").
		Joins("JOIN messages m ON m.id = ma.message_id AND m.deleted_at IS NULL").
		Where("ma.session_id = ?", sessionID).
		Order("m.created_at ASC, m.id ASC, ma.position ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(types.MessageArtifacts, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.Artifact())
	}
	return result, nil
}

// ListLiveArtifactsByMessageIDs returns undeleted artifact rows for the given
// messages. It does not join messages, so rewind can tombstone files after
// those messages have already been soft-deleted.
func (r *messageRepository) ListLiveArtifactsByMessageIDs(
	ctx context.Context, sessionID string, messageIDs []string,
) ([]types.MessageArtifactRecord, error) {
	if sessionID == "" || len(messageIDs) == 0 {
		return nil, nil
	}
	var rows []types.MessageArtifactRecord
	if err := r.db.WithContext(ctx).
		Where("session_id = ? AND message_id IN ? AND deleted_at IS NULL", sessionID, messageIDs).
		Order("message_id ASC, position ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// RecordRestoredArtifactMtime stamps a same-content sandbox restore's mtime
// onto this session's artifacts at sourcePath whose content already hashed to
// hash, mirroring MessageArtifacts.WithRestoredMtime: other versions at the
// path and empty-hash legacy rows are left alone.
//
// Tombstones are left alone too. A tombstone records what was deleted and when;
// its (path, mtime) pair is the key that keeps the collector from re-attaching
// the sandbox copy, so moving that mtime forward would quietly re-open the
// door it is there to hold shut.
func (r *messageRepository) RecordRestoredArtifactMtime(
	ctx context.Context, sessionID, sourcePath string, mod time.Time, hash string,
) error {
	if sessionID == "" || sourcePath == "" || hash == "" {
		return nil
	}
	var rows []types.MessageArtifactRecord
	if err := r.db.WithContext(ctx).
		Where("session_id = ? AND source_path = ? AND content_hash = ? AND deleted_at IS NULL",
			sessionID, sourcePath, hash).
		Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if types.ParseArtifactModTime(row.ModTime).Equal(mod) {
			continue
		}
		if err := r.db.WithContext(ctx).
			Model(&types.MessageArtifactRecord{}).
			Where("id = ?", row.ID).
			Update("mod_time", types.FormatArtifactModTime(mod)).Error; err != nil {
			return err
		}
	}
	return nil
}

// ListArtifactLibrary returns the latest version of every artifact in the
// sessions the caller can see, newest first.
//
// User-deleted artifacts are excluded, versions included: deleting the newest
// version of a file surfaces the one before it, which is why the delete path
// tombstones every version of a library row at once.
//
// Session visibility mirrors the home sidebar, i.e. the "web" bucket of
// sessionRepository.QueryPaged: same tenant, not deleted, owned by the user or
// a legacy tenant-level row, not a skill-maintenance session, and not an IM,
// embed or API session. IM sessions are created without an owner, so without
// the web predicate every member would see every IM chat's files. Artifacts
// of soft-deleted messages are hidden. Versions are grouped by (session, source path); artifacts without a
// source path stand alone. A later answer that references an earlier file
// stores another row with the same URL, so versions are counted as distinct
// URLs (the max DENSE_RANK over url, since window functions reject DISTINCT).
func (r *messageRepository) ListArtifactLibrary(
	ctx context.Context, q *types.ArtifactLibraryQuery,
) ([]*types.ArtifactLibraryItem, int64, error) {
	var (
		where []string
		args  []any
	)
	where = append(where, "s.tenant_id = ?", "s.deleted_at IS NULL", "ma.deleted_at IS NULL")
	args = append(args, q.TenantID)
	if q.UserID != "" {
		where = append(where, "(s.user_id = ? OR s.user_id IS NULL OR s.user_id = '')")
		args = append(args, q.UserID)
	}
	where = append(where, "(s.description IS NULL OR s.description NOT LIKE ?)", webSessionPredicate)
	args = append(args, types.SkillMaintenanceSessionMarker+"%")
	args = append(args, webSessionPredicateArgs()...)
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		where = append(where, "LOWER(ma.file_name) LIKE LOWER(?) ESCAPE ?")
		args = append(args, "%"+escapeLikeKeyword(kw)+"%", likeEscapeChar)
	}
	if len(q.FileTypes) > 0 {
		where = append(where, "LOWER(ma.file_type) IN ?")
		args = append(args, q.FileTypes)
	}

	grouped := `SELECT ma.session_id, ma.message_id, ma.position, ma.url, ma.file_name,
			ma.file_type, ma.file_size, ma.source_path, ma.created_at, ma.id,
			s.title AS session_title,
			CASE WHEN ma.source_path = '' THEN ma.id ELSE ma.source_path END AS version_key,
			ROW_NUMBER() OVER (
				PARTITION BY ma.session_id, CASE WHEN ma.source_path = '' THEN ma.id ELSE ma.source_path END
				ORDER BY ma.created_at DESC, m.created_at DESC, ma.position DESC
			) AS version_rank,
			DENSE_RANK() OVER (
				PARTITION BY ma.session_id, CASE WHEN ma.source_path = '' THEN ma.id ELSE ma.source_path END
				ORDER BY ma.url
			) AS url_rank
		FROM message_artifacts ma
		JOIN messages m ON m.id = ma.message_id AND m.deleted_at IS NULL
		JOIN sessions s ON s.id = ma.session_id
		LEFT JOIN im_channel_sessions ics ON ics.session_id = s.id
		WHERE ` + strings.Join(where, " AND ")
	ranked := `SELECT g.*, MAX(g.url_rank) OVER (PARTITION BY g.session_id, g.version_key) AS version_count
		FROM (` + grouped + `) g`

	var total int64
	if err := r.db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM ("+ranked+") latest WHERE version_rank = 1", args...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	page := max(q.Page, 1)
	size := q.PageSize
	if size < 1 {
		size = 20
	}
	items := make([]*types.ArtifactLibraryItem, 0)
	listArgs := append(append([]any{}, args...), size, (page-1)*size)
	if err := r.db.WithContext(ctx).
		Raw("SELECT * FROM ("+ranked+") latest WHERE version_rank = 1 "+
			"ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?", listArgs...).
		Scan(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// FindSessionArtifact returns one live artifact row of the session addressed by
// (message, position) — the same coordinates the download endpoint uses.
// A tombstone is reported as not found, so deleting twice is a 404 rather than
// a second blob reclaim.
func (r *messageRepository) FindSessionArtifact(
	ctx context.Context, sessionID, messageID string, position int,
) (*types.MessageArtifactRecord, error) {
	if sessionID == "" || messageID == "" || position < 0 {
		return nil, nil
	}
	var row types.MessageArtifactRecord
	err := r.db.WithContext(ctx).
		Table("message_artifacts AS ma").
		Select("ma.*").
		Joins("JOIN messages m ON m.id = ma.message_id AND m.deleted_at IS NULL").
		Where("ma.session_id = ? AND ma.message_id = ? AND ma.position = ? AND ma.deleted_at IS NULL",
			sessionID, messageID, position).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// FindSessionArtifactVersions returns every live row of the session that shares
// sourcePath, i.e. the regenerations the artifact library folds into one entry.
// An empty sourcePath has no version group — the artifact stands alone — so the
// caller gets nothing back and deletes just the row it started from.
func (r *messageRepository) FindSessionArtifactVersions(
	ctx context.Context, sessionID, sourcePath string,
) ([]types.MessageArtifactRecord, error) {
	if sessionID == "" || sourcePath == "" {
		return nil, nil
	}
	var rows []types.MessageArtifactRecord
	if err := r.db.WithContext(ctx).
		Table("message_artifacts AS ma").
		Select("ma.*").
		Joins("JOIN messages m ON m.id = ma.message_id AND m.deleted_at IS NULL").
		Where("ma.session_id = ? AND ma.source_path = ? AND ma.deleted_at IS NULL", sessionID, sourcePath).
		Order("ma.created_at ASC, ma.id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// SoftDeleteSessionArtifacts tombstones the given rows and returns the subset it
// actually marked. Rows another caller already tombstoned are left out, so the
// caller reclaims exactly the blobs it took ownership of and a concurrent
// duplicate delete reclaims none of them twice.
//
// url is left on the tombstone on purpose: if the blob reclaim that follows
// fails, the storage path is still on record for a later GC pass.
func (r *messageRepository) SoftDeleteSessionArtifacts(
	ctx context.Context, sessionID string, refs []types.ArtifactRef, at time.Time,
) ([]types.ArtifactRef, error) {
	if sessionID == "" || len(refs) == 0 {
		return nil, nil
	}
	marked := make([]types.ArtifactRef, 0, len(refs))
	// All or nothing. Deleting a file's versions is several statements, and a
	// failure partway through would leave some versions hidden from every
	// listing with their bytes never reclaimed, while the caller reports an
	// error and the user's retry 404s on the row that did get marked.
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		marked = marked[:0]
		// One statement per ref: the pairs are a handful at most (a message's
		// artifacts, or one file's versions), and a composite IN list is not
		// portable across PostgreSQL and SQLite.
		for _, ref := range refs {
			if ref.MessageID == "" || ref.Position < 0 {
				continue
			}
			res := tx.Model(&types.MessageArtifactRecord{}).
				Where("session_id = ? AND message_id = ? AND position = ? AND deleted_at IS NULL",
					sessionID, ref.MessageID, ref.Position).
				Update("deleted_at", at.UTC())
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected > 0 {
				marked = append(marked, ref)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return marked, nil
}

// CountLiveArtifactsByURL counts artifact rows that still point at a stored
// object and have not been deleted, across every session.
//
// It is the last guard before a delete reclaims bytes. The catalog's binding
// count is the primary one, but it does not cover every way two rows come to
// share a URL: a forked session's copied rows carry the parent's storage URLs
// without a binding of their own (CreateForked copies the rows, it does not
// re-Bind them), a deployment may store raw provider paths that the catalog
// never registered, and a later answer that re-attaches an earlier file writes
// another row with the same URL. Any of those still needs the bytes.
//
// Soft-deleted messages are counted on purpose: their rows can come back, and
// keeping a blob costs less than a restored message pointing at nothing.
func (r *messageRepository) CountLiveArtifactsByURL(ctx context.Context, url string) (int64, error) {
	if url == "" {
		return 0, nil
	}
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&types.MessageArtifactRecord{}).
		Where("url = ? AND deleted_at IS NULL", url).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
