package repository

import (
	"context"
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
func writeMessageArtifacts(tx *gorm.DB, message *types.Message) error {
	if message == nil || message.Artifacts == nil || message.ID == "" {
		return nil
	}
	if err := tx.Where("message_id = ?", message.ID).
		Delete(&types.MessageArtifactRecord{}).Error; err != nil {
		return err
	}
	rows := types.NewMessageArtifactRecords(message.SessionID, message.ID, message.Artifacts)
	if len(rows) == 0 {
		return nil
	}
	return tx.CreateInBatches(rows, 100).Error
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

// RecordRestoredArtifactMtime stamps a same-content sandbox restore's mtime
// onto this session's artifacts at sourcePath whose content already hashed to
// hash, mirroring MessageArtifacts.WithRestoredMtime: other versions at the
// path and empty-hash legacy rows are left alone.
func (r *messageRepository) RecordRestoredArtifactMtime(
	ctx context.Context, sessionID, sourcePath string, mod time.Time, hash string,
) error {
	if sessionID == "" || sourcePath == "" || hash == "" {
		return nil
	}
	var rows []types.MessageArtifactRecord
	if err := r.db.WithContext(ctx).
		Where("session_id = ? AND source_path = ? AND content_hash = ?", sessionID, sourcePath, hash).
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
	where = append(where, "s.tenant_id = ?", "s.deleted_at IS NULL")
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
