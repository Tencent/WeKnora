package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type memoryRepository struct {
	db *gorm.DB
}

// NewMemoryRepository creates the long-term memory repository.
func NewMemoryRepository(db *gorm.DB) interfaces.MemoryRepository {
	return &memoryRepository{db: db}
}

// scoped starts every query already filtered by workspace and subject. All
// reads and writes go through it so a missing scope predicate is impossible.
func (r *memoryRepository) scoped(ctx context.Context, scope interfaces.MemoryScope) *gorm.DB {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID)
}

func (r *memoryRepository) GetSubject(
	ctx context.Context, scope interfaces.MemoryScope,
) (*types.MemorySubject, error) {
	var subject types.MemorySubject
	err := r.scoped(ctx, scope).First(&subject).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &subject, nil
}

func (r *memoryRepository) EnsureSubject(
	ctx context.Context, scope interfaces.MemoryScope,
) (*types.MemorySubject, error) {
	subject := &types.MemorySubject{
		ID:        uuid.New().String(),
		TenantID:  scope.TenantID,
		SubjectID: scope.SubjectID,
		Enabled:   true,
	}
	// DoNothing plus a re-read keeps concurrent first turns from racing into a
	// unique-violation. The insert is a no-op whenever the row already exists.
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "subject_id"}},
			DoNothing: true,
		}).
		Create(subject).Error
	if err != nil {
		return nil, err
	}
	existing, err := r.GetSubject(ctx, scope)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, errors.New("memory subject vanished after upsert")
	}
	return existing, nil
}

func (r *memoryRepository) UpdateSubjectEnabled(
	ctx context.Context, scope interfaces.MemoryScope, enabled bool,
) error {
	if _, err := r.EnsureSubject(ctx, scope); err != nil {
		return err
	}
	return r.scoped(ctx, scope).
		Model(&types.MemorySubject{}).
		Updates(map[string]interface{}{"enabled": enabled, "updated_at": time.Now()}).Error
}

func (r *memoryRepository) UpdateSubjectBlock(
	ctx context.Context, scope interfaces.MemoryScope, block string, itemCount int,
) error {
	now := time.Now()
	return r.scoped(ctx, scope).
		Model(&types.MemorySubject{}).
		Updates(map[string]interface{}{
			"block_text":       block,
			"block_updated_at": now,
			"item_count":       itemCount,
			"updated_at":       now,
		}).Error
}

// EnqueuePendingSession is the whole "never drop a turn" mechanism, so it runs
// inside a transaction: reading the subject, appending the session and claiming
// the in-flight slot must not interleave with a concurrent turn, or two turns
// could both decide nobody is scheduled (two tasks) or both decide someone is
// (a turn recorded against a run that already read the queue).
func (r *memoryRepository) EnqueuePendingSession(
	ctx context.Context, scope interfaces.MemoryScope, sessionID string, inFlightTimeout time.Duration,
) (*types.MemorySubject, bool, error) {
	var (
		snapshot   types.MemorySubject
		shouldSend bool
	)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var subject types.MemorySubject
		if err := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Clauses(forUpdateClause()).
			First(&subject).Error; err != nil {
			return err
		}
		snapshot = subject

		now := time.Now()
		updates := map[string]interface{}{"updated_at": now}
		if pending := subject.PendingSessions.Append(sessionID); len(pending) != len(subject.PendingSessions) {
			updates["pending_sessions"] = pending
		}
		// A stale marker (worker crashed, task lost) must not wedge the subject
		// forever, so the claim expires.
		inFlight := subject.ExtractScheduledAt != nil &&
			now.Sub(*subject.ExtractScheduledAt) < inFlightTimeout
		if !inFlight {
			updates["extract_scheduled_at"] = now
			shouldSend = true
		}
		return tx.Model(&types.MemorySubject{}).
			Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Updates(updates).Error
	})
	if err != nil {
		return nil, false, err
	}
	return &snapshot, shouldSend, nil
}

// ClaimPendingSessions empties the queue and returns it together with the
// watermark to walk forward from. Emptying it here (rather than after the run)
// is deliberate: turns arriving during the run land in a fresh queue and
// trigger a follow-up, instead of being erased by the run that never saw them.
func (r *memoryRepository) ClaimPendingSessions(
	ctx context.Context, scope interfaces.MemoryScope,
) ([]string, time.Time, error) {
	var (
		pending []string
		cursor  time.Time
	)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var subject types.MemorySubject
		if err := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Clauses(forUpdateClause()).
			First(&subject).Error; err != nil {
			return err
		}
		pending = append(pending, subject.PendingSessions...)
		if subject.ExtractCursor != nil {
			cursor = *subject.ExtractCursor
		}
		return tx.Model(&types.MemorySubject{}).
			Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Updates(map[string]interface{}{
				"pending_sessions": types.MemoryPendingSessions{},
				"updated_at":       time.Now(),
			}).Error
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	return pending, cursor, nil
}

func (r *memoryRepository) FinishExtraction(
	ctx context.Context, scope interfaces.MemoryScope, cursor time.Time,
) error {
	now := time.Now()
	updates := map[string]interface{}{
		"last_extracted_at":    now,
		"extract_scheduled_at": nil,
		"updated_at":           now,
	}
	if !cursor.IsZero() {
		updates["extract_cursor"] = cursor
	}
	return r.scoped(ctx, scope).Model(&types.MemorySubject{}).Updates(updates).Error
}

func (r *memoryRepository) ReleaseExtractionSlot(
	ctx context.Context, scope interfaces.MemoryScope,
) error {
	return r.scoped(ctx, scope).
		Model(&types.MemorySubject{}).
		Updates(map[string]interface{}{"extract_scheduled_at": nil, "updated_at": time.Now()}).Error
}

func (r *memoryRepository) CreateItem(ctx context.Context, item *types.MemoryItem) error {
	if item.ID == "" {
		item.ID = uuid.New().String()
	}
	if item.ValidFrom.IsZero() {
		item.ValidFrom = time.Now()
	}
	if item.Status == "" {
		item.Status = types.MemoryStatusActive
	}
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *memoryRepository) GetItem(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) (*types.MemoryItem, error) {
	var item types.MemoryItem
	err := r.scoped(ctx, scope).Where("id = ?", id).First(&item).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

// notExpired excludes items whose usefulness has a stated end. Applying it in
// the query rather than after the fact means an expired task cannot slip into
// a prompt through a code path that forgot to filter.
func notExpired(query *gorm.DB) *gorm.DB {
	return query.Where("expires_at IS NULL OR expires_at > ?", time.Now())
}

func (r *memoryRepository) ListActiveByKinds(
	ctx context.Context, scope interfaces.MemoryScope, kinds []string, limit int,
) ([]*types.MemoryItem, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	var items []*types.MemoryItem
	query := notExpired(r.scoped(ctx, scope).
		Where("status = ?", types.MemoryStatusActive).
		Where("kind IN ?", kinds)).
		Order("importance DESC, valid_from DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ListActiveResident returns what the resident block is built from.
//
// Standing traits qualify by kind. An explicitly requested memory qualifies
// regardless of kind: the user said "remember this", and making that depend on
// their later question happening to share words with it is the fastest way to
// lose their trust in the feature.
func (r *memoryRepository) ListActiveResident(
	ctx context.Context, scope interfaces.MemoryScope, limit int,
) ([]*types.MemoryItem, error) {
	var items []*types.MemoryItem
	query := notExpired(r.scoped(ctx, scope).
		Where("status = ?", types.MemoryStatusActive).
		Where("kind IN ? OR origin = ?",
			types.ResidentMemoryKinds,
			types.MemoryOriginExplicit)).
		Order("importance DESC, valid_from DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (r *memoryRepository) ListItems(
	ctx context.Context, scope interfaces.MemoryScope, status string, limit, offset int,
) ([]*types.MemoryItem, int64, error) {
	query := r.scoped(ctx, scope).Model(&types.MemoryItem{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	var items []*types.MemoryItem
	// id breaks ties so paging stays deterministic. A distillation run writes
	// several items at once, and ordering those by valid_from alone lets the
	// database return them in a different order per page, which both repeats
	// and skips rows across an offset walk.
	err := query.Order("valid_from DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListLive returns the items of one kind that the user can currently see:
// in use, plus proposed and awaiting their decision. Deduplication has to
// consider both, or confirming a proposal can leave a duplicate behind.
func (r *memoryRepository) ListLive(
	ctx context.Context, scope interfaces.MemoryScope, kind string, limit int,
) ([]*types.MemoryItem, error) {
	var items []*types.MemoryItem
	query := notExpired(r.scoped(ctx, scope).
		Where("status IN ?", []string{types.MemoryStatusActive, types.MemoryStatusPending}).
		Where("kind = ?", kind)).
		Order("importance DESC, valid_from DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (r *memoryRepository) FindActiveByKey(
	ctx context.Context, scope interfaces.MemoryScope, normalizedKey string,
) (*types.MemoryItem, error) {
	if normalizedKey == "" {
		return nil, nil
	}
	var item types.MemoryItem
	// Pending counts as live here. A memory awaiting confirmation is one the
	// user can already see, and ignoring it meant every re-derivation of the
	// same inference stacked another copy in their review list.
	err := r.scoped(ctx, scope).
		Where("status IN ? AND normalized_key = ?",
			[]string{types.MemoryStatusActive, types.MemoryStatusPending}, normalizedKey).
		Order("valid_from DESC").
		First(&item).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (r *memoryRepository) UpdateItemContent(
	ctx context.Context, scope interfaces.MemoryScope, id, content, normalizedKey string, importance int,
) error {
	return r.scoped(ctx, scope).
		Model(&types.MemoryItem{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"content":        content,
			"normalized_key": normalizedKey,
			"importance":     importance,
			"origin":         types.MemoryOriginManual,
			"updated_at":     time.Now(),
		}).Error
}

func (r *memoryRepository) SupersedeItem(
	ctx context.Context, scope interfaces.MemoryScope, id, supersededBy string,
) error {
	now := time.Now()
	return r.scoped(ctx, scope).
		Model(&types.MemoryItem{}).
		Where("id = ? AND status = ?", id, types.MemoryStatusActive).
		Updates(map[string]interface{}{
			"status":        types.MemoryStatusSuperseded,
			"invalid_at":    now,
			"superseded_by": supersededBy,
			"updated_at":    now,
		}).Error
}

func (r *memoryRepository) DeleteItem(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) error {
	return r.scoped(ctx, scope).Where("id = ?", id).Delete(&types.MemoryItem{}).Error
}

func (r *memoryRepository) DeleteAll(
	ctx context.Context, scope interfaces.MemoryScope,
) (int64, error) {
	result := r.scoped(ctx, scope).Delete(&types.MemoryItem{})
	return result.RowsAffected, result.Error
}

func (r *memoryRepository) TouchUsed(
	ctx context.Context, scope interfaces.MemoryScope, ids []string,
) error {
	if len(ids) == 0 {
		return nil
	}
	return r.scoped(ctx, scope).
		Model(&types.MemoryItem{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{
			"last_used_at": time.Now(),
			"use_count":    gorm.Expr("use_count + 1"),
		}).Error
}

// ArchiveLowestRanked keeps the `keep` best active items and archives the
// rest. Ranking is importance first, then recency of use, then recency of
// creation — no decay curve, because a half-life that silently buries a
// correct memory is worse than a hard cap the user can see in the list.
func (r *memoryRepository) ArchiveLowestRanked(
	ctx context.Context, scope interfaces.MemoryScope, keep int,
) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	var survivors []string
	err := r.scoped(ctx, scope).
		Model(&types.MemoryItem{}).
		Where("status = ?", types.MemoryStatusActive).
		Order("importance DESC, COALESCE(last_used_at, valid_from) DESC, valid_from DESC").
		Limit(keep).
		Pluck("id", &survivors).Error
	if err != nil {
		return 0, err
	}
	query := r.scoped(ctx, scope).
		Model(&types.MemoryItem{}).
		Where("status = ?", types.MemoryStatusActive)
	if len(survivors) > 0 {
		query = query.Where("id NOT IN ?", survivors)
	}
	result := query.Updates(map[string]interface{}{
		"status":     types.MemoryStatusArchived,
		"updated_at": time.Now(),
	})
	return result.RowsAffected, result.Error
}

func (r *memoryRepository) AddTombstone(
	ctx context.Context, scope interfaces.MemoryScope, topic, fingerprint, sourceMessageID string,
) error {
	if fingerprint == "" {
		return nil
	}
	tombstone := &types.MemoryTombstone{
		ID:              uuid.New().String(),
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		Topic:           topic,
		Fingerprint:     fingerprint,
		SourceMessageID: sourceMessageID,
	}
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "fingerprint"},
			},
			DoNothing: true,
		}).
		Create(tombstone).Error
	if err != nil {
		return err
	}
	return r.trimTombstones(ctx, scope)
}

// trimTombstones keeps the list bounded. A rejection from long ago matters less
// than this table growing without limit.
func (r *memoryRepository) trimTombstones(ctx context.Context, scope interfaces.MemoryScope) error {
	var keep []string
	err := r.scoped(ctx, scope).
		Model(&types.MemoryTombstone{}).
		Order("created_at DESC").
		Limit(types.MaxMemoryTombstones).
		Pluck("id", &keep).Error
	if err != nil {
		return err
	}
	if len(keep) < types.MaxMemoryTombstones {
		return nil
	}
	return r.scoped(ctx, scope).
		Where("id NOT IN ?", keep).
		Delete(&types.MemoryTombstone{}).Error
}

func (r *memoryRepository) ListTombstones(
	ctx context.Context, scope interfaces.MemoryScope, limit int,
) ([]*types.MemoryTombstone, error) {
	var tombstones []*types.MemoryTombstone
	query := r.scoped(ctx, scope).
		Model(&types.MemoryTombstone{}).
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&tombstones).Error; err != nil {
		return nil, err
	}
	return tombstones, nil
}

func (r *memoryRepository) HasTombstone(
	ctx context.Context, scope interfaces.MemoryScope, fingerprint string,
) (bool, error) {
	if fingerprint == "" {
		return false, nil
	}
	var count int64
	err := r.scoped(ctx, scope).
		Model(&types.MemoryTombstone{}).
		Where("fingerprint = ?", fingerprint).
		Count(&count).Error
	return count > 0, err
}

func (r *memoryRepository) HasTombstoneForMessage(
	ctx context.Context, scope interfaces.MemoryScope, sourceMessageID string, within time.Duration,
) (bool, error) {
	if sourceMessageID == "" {
		return false, nil
	}
	query := r.scoped(ctx, scope).
		Model(&types.MemoryTombstone{}).
		Where("source_message_id = ?", sourceMessageID)
	if within > 0 {
		query = query.Where("created_at > ?", time.Now().Add(-within))
	}
	var count int64
	err := query.Count(&count).Error
	return count > 0, err
}

func (r *memoryRepository) ExpireOverdue(
	ctx context.Context, scope interfaces.MemoryScope,
) (int64, error) {
	now := time.Now()
	result := r.scoped(ctx, scope).
		Model(&types.MemoryItem{}).
		Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", types.MemoryStatusActive, now).
		Updates(map[string]interface{}{
			"status":     types.MemoryStatusArchived,
			"updated_at": now,
		})
	return result.RowsAffected, result.Error
}

func (r *memoryRepository) UpsertItemEmbedding(
	ctx context.Context, scope interfaces.MemoryScope, embedding *types.MemoryItemEmbedding,
) error {
	if embedding == nil || embedding.ItemID == "" || len(embedding.Vector) == 0 {
		return nil
	}
	embedding.TenantID = scope.TenantID
	embedding.SubjectID = scope.SubjectID
	now := time.Now()
	embedding.UpdatedAt = now
	if embedding.CreatedAt.IsZero() {
		embedding.CreatedAt = now
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "item_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"model_id", "dims", "vector", "updated_at"}),
		}).
		Create(embedding).Error
}

// DeleteItemEmbedding drops one memory's vector so the backfill rebuilds it.
func (r *memoryRepository) DeleteItemEmbedding(
	ctx context.Context, scope interfaces.MemoryScope, itemID string,
) error {
	if itemID == "" {
		return nil
	}
	return r.scoped(ctx, scope).
		Where("item_id = ?", itemID).
		Delete(&types.MemoryItemEmbedding{}).Error
}

func (r *memoryRepository) ItemEmbeddings(
	ctx context.Context, scope interfaces.MemoryScope, itemIDs []string, modelID string,
) (map[string][]float32, error) {
	if len(itemIDs) == 0 || modelID == "" {
		return nil, nil
	}
	var rows []*types.MemoryItemEmbedding
	err := r.scoped(ctx, scope).
		Model(&types.MemoryItemEmbedding{}).
		Where("item_id IN ? AND model_id = ?", itemIDs, modelID).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	vectors := make(map[string][]float32, len(rows))
	for _, row := range rows {
		if vector := types.DecodeEmbedding(row.Vector); len(vector) > 0 {
			vectors[row.ItemID] = vector
		}
	}
	return vectors, nil
}

// ItemsMissingEmbeddings finds the backlog.
//
// Every memory written before an embedding model was configured, and every one
// written while the model was unreachable, has no vector — and a memory with no
// vector is invisible to semantic recall. Without a backfill the feature would
// only ever work for memories created after it was switched on.
func (r *memoryRepository) ItemsMissingEmbeddings(
	ctx context.Context, scope interfaces.MemoryScope, modelID string, limit int,
) ([]*types.MemoryItem, error) {
	if limit <= 0 {
		limit = 20
	}
	var items []*types.MemoryItem
	err := r.scoped(ctx, scope).
		Model(&types.MemoryItem{}).
		Where("status IN ?", []string{types.MemoryStatusActive, types.MemoryStatusPending}).
		Where(`id NOT IN (
			SELECT item_id FROM memory_item_embeddings
			WHERE tenant_id = ? AND subject_id = ? AND model_id = ?
		)`, scope.TenantID, scope.SubjectID, modelID).
		Order("valid_from DESC").
		Limit(limit).
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (r *memoryRepository) MarkConsolidated(
	ctx context.Context, scope interfaces.MemoryScope,
) error {
	now := time.Now()
	return r.scoped(ctx, scope).
		Model(&types.MemorySubject{}).
		Updates(map[string]interface{}{"consolidated_at": now, "updated_at": now}).Error
}

func (r *memoryRepository) MarkForcedConsolidated(
	ctx context.Context, scope interfaces.MemoryScope,
) error {
	now := time.Now()
	return r.scoped(ctx, scope).
		Model(&types.MemorySubject{}).
		Updates(map[string]interface{}{"forced_consolidated_at": now, "updated_at": now}).Error
}

func (r *memoryRepository) SetItemStatus(
	ctx context.Context, scope interfaces.MemoryScope, id, status string,
) error {
	return r.scoped(ctx, scope).
		Model(&types.MemoryItem{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{"status": status, "updated_at": time.Now()}).Error
}

// BumpTopic counts one more sighting. The insert-then-increment shape keeps two
// concurrent turns from both deciding the topic is new.
func (r *memoryRepository) BumpTopic(
	ctx context.Context, scope interfaces.MemoryScope, topic, normalizedKey, alias string,
) (*types.MemoryTopicStat, error) {
	if normalizedKey == "" {
		return nil, nil
	}
	now := time.Now()
	stat := &types.MemoryTopicStat{
		ID:            uuid.New().String(),
		TenantID:      scope.TenantID,
		SubjectID:     scope.SubjectID,
		NormalizedKey: normalizedKey,
		Topic:         topic,
		Hits:          0,
		LastSeenAt:    now,
	}
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "normalized_key"},
			},
			DoNothing: true,
		}).
		Create(stat).Error
	if err != nil {
		return nil, err
	}
	err = r.scoped(ctx, scope).
		Model(&types.MemoryTopicStat{}).
		Where("normalized_key = ?", normalizedKey).
		Updates(map[string]interface{}{
			"hits":         gorm.Expr("hits + 1"),
			"last_seen_at": now,
			"updated_at":   now,
		}).Error
	if err != nil {
		return nil, err
	}
	var updated types.MemoryTopicStat
	if err := r.scoped(ctx, scope).
		Where("normalized_key = ?", normalizedKey).
		First(&updated).Error; err != nil {
		return nil, err
	}

	// Record the wording this sighting arrived as, so the same phrasing
	// resolves by exact match next time instead of being re-adjudicated.
	if alias != "" && !updated.Aliases.Has(alias) &&
		types.NormalizeTopicKey(alias) != updated.NormalizedKey {
		aliases := append(updated.Aliases, alias)
		if len(aliases) > 12 {
			aliases = aliases[len(aliases)-12:]
		}
		if err := r.scoped(ctx, scope).
			Model(&types.MemoryTopicStat{}).
			Where("normalized_key = ?", normalizedKey).
			Updates(map[string]interface{}{"aliases": aliases, "updated_at": now}).Error; err != nil {
			return nil, err
		}
		updated.Aliases = aliases
	}
	return &updated, nil
}

// RenameTopic gives a subject a better canonical label.
//
// The old label becomes an alias rather than being discarded: it is what every
// earlier sighting was counted under, and dropping it would make the next
// occurrence of that wording look like a brand new subject. Returns false when
// the new key already belongs to another row, in which case the rename is
// skipped — folding two rows together is a different operation with different
// risks, and doing it as a side effect of a rename would lose counts.
func (r *memoryRepository) RenameTopic(
	ctx context.Context, scope interfaces.MemoryScope, oldKey, newKey, newLabel string,
) (bool, error) {
	if oldKey == "" || newKey == "" || oldKey == newKey {
		return false, nil
	}

	var clash int64
	err := r.scoped(ctx, scope).
		Model(&types.MemoryTopicStat{}).
		Where("normalized_key = ?", newKey).
		Count(&clash).Error
	if err != nil {
		return false, err
	}
	if clash > 0 {
		return false, nil
	}

	var current types.MemoryTopicStat
	if err := r.scoped(ctx, scope).
		Where("normalized_key = ?", oldKey).
		First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}

	// The incoming wording was recorded as an alias before the rename decided
	// to adopt it. Leaving it there would list the canonical label as an alias
	// of itself.
	aliases := make(types.MemoryTopicAliases, 0, len(current.Aliases)+1)
	for _, alias := range current.Aliases {
		if types.NormalizeTopicKey(alias) == newKey {
			continue
		}
		aliases = append(aliases, alias)
	}
	if current.Topic != "" && !aliases.Has(current.Topic) {
		aliases = append(aliases, current.Topic)
	}
	if len(aliases) > 12 {
		aliases = aliases[len(aliases)-12:]
	}

	err = r.scoped(ctx, scope).
		Model(&types.MemoryTopicStat{}).
		Where("normalized_key = ?", oldKey).
		Updates(map[string]interface{}{
			"topic":          newLabel,
			"normalized_key": newKey,
			"aliases":        aliases,
			"updated_at":     time.Now(),
		}).Error
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *memoryRepository) MarkTopicPromoted(
	ctx context.Context, scope interfaces.MemoryScope, normalizedKey string,
) error {
	now := time.Now()
	return r.scoped(ctx, scope).
		Model(&types.MemoryTopicStat{}).
		Where("normalized_key = ?", normalizedKey).
		Updates(map[string]interface{}{"promoted_at": now, "updated_at": now}).Error
}

// TopicByKey returns one subject's statistics, or nil when it is not tracked.
func (r *memoryRepository) TopicByKey(
	ctx context.Context, scope interfaces.MemoryScope, normalizedKey string,
) (*types.MemoryTopicStat, error) {
	if normalizedKey == "" {
		return nil, nil
	}
	var stat types.MemoryTopicStat
	err := r.scoped(ctx, scope).
		Where("normalized_key = ?", normalizedKey).
		First(&stat).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &stat, nil
}

func (r *memoryRepository) TopTopics(
	ctx context.Context, scope interfaces.MemoryScope, limit int,
) ([]*types.MemoryTopicStat, error) {
	var stats []*types.MemoryTopicStat
	query := r.scoped(ctx, scope).
		Model(&types.MemoryTopicStat{}).
		Order("hits DESC, last_seen_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&stats).Error; err != nil {
		return nil, err
	}
	return stats, nil
}

func (r *memoryRepository) TopicByID(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) (*types.MemoryTopicStat, error) {
	if id == "" {
		return nil, nil
	}
	var stat types.MemoryTopicStat
	err := r.scoped(ctx, scope).Where("id = ?", id).First(&stat).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &stat, nil
}

func (r *memoryRepository) ListUnpromotedTopics(
	ctx context.Context, scope interfaces.MemoryScope, limit, offset int,
) ([]*types.MemoryTopicStat, int64, error) {
	query := r.scoped(ctx, scope).
		Model(&types.MemoryTopicStat{}).
		Where("promoted_at IS NULL")
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	var stats []*types.MemoryTopicStat
	err := query.Order("hits DESC, last_seen_at DESC").
		Limit(limit).Offset(offset).Find(&stats).Error
	if err != nil {
		return nil, 0, err
	}
	return stats, total, nil
}

func (r *memoryRepository) DeleteTopic(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) error {
	return r.scoped(ctx, scope).Where("id = ?", id).Delete(&types.MemoryTopicStat{}).Error
}

func (r *memoryRepository) DeleteAllTopics(
	ctx context.Context, scope interfaces.MemoryScope,
) error {
	return r.scoped(ctx, scope).Delete(&types.MemoryTopicStat{}).Error
}

func (r *memoryRepository) BumpDocAffinity(
	ctx context.Context, scope interfaces.MemoryScope, docs []types.MemoryDocAffinity,
) error {
	now := time.Now()
	for _, doc := range docs {
		if doc.KnowledgeID == "" {
			continue
		}
		row := &types.MemoryDocAffinity{
			ID:              uuid.New().String(),
			TenantID:        scope.TenantID,
			SubjectID:       scope.SubjectID,
			KnowledgeID:     doc.KnowledgeID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			Title:           doc.Title,
			Hits:            0,
			LastUsedAt:      now,
		}
		if err := r.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "knowledge_id"},
				},
				DoNothing: true,
			}).
			Create(row).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{
			"hits":         gorm.Expr("hits + 1"),
			"last_used_at": now,
			"updated_at":   now,
		}
		if doc.Title != "" {
			updates["title"] = doc.Title
		}
		if doc.KnowledgeBaseID != "" {
			updates["knowledge_base_id"] = doc.KnowledgeBaseID
		}
		if err := r.scoped(ctx, scope).
			Model(&types.MemoryDocAffinity{}).
			Where("knowledge_id = ?", doc.KnowledgeID).
			Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *memoryRepository) DocAffinity(
	ctx context.Context, scope interfaces.MemoryScope, knowledgeIDs []string,
) (map[string]int, error) {
	if len(knowledgeIDs) == 0 {
		return nil, nil
	}
	var rows []*types.MemoryDocAffinity
	err := r.scoped(ctx, scope).
		Model(&types.MemoryDocAffinity{}).
		Where("knowledge_id IN ?", knowledgeIDs).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	affinity := make(map[string]int, len(rows))
	for _, row := range rows {
		affinity[row.KnowledgeID] = row.Hits
	}
	return affinity, nil
}

func (r *memoryRepository) TopDocAffinity(
	ctx context.Context, scope interfaces.MemoryScope, limit int,
) ([]*types.MemoryDocAffinity, error) {
	var rows []*types.MemoryDocAffinity
	query := r.scoped(ctx, scope).
		Model(&types.MemoryDocAffinity{}).
		Order("hits DESC, last_used_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *memoryRepository) DocAffinityByID(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) (*types.MemoryDocAffinity, error) {
	if id == "" {
		return nil, nil
	}
	var row types.MemoryDocAffinity
	err := r.scoped(ctx, scope).Where("id = ?", id).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *memoryRepository) ListFamiliarDocs(
	ctx context.Context, scope interfaces.MemoryScope, minHits, limit, offset int,
) ([]*types.MemoryDocAffinity, int64, error) {
	if minHits < 1 {
		minHits = types.MemoryDocAffinityMinHits
	}
	query := r.scoped(ctx, scope).
		Model(&types.MemoryDocAffinity{}).
		Where("hits >= ?", minHits)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	var rows []*types.MemoryDocAffinity
	err := query.Order("hits DESC, last_used_at DESC").
		Limit(limit).Offset(offset).Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *memoryRepository) DeleteDocAffinity(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) error {
	return r.scoped(ctx, scope).Where("id = ?", id).Delete(&types.MemoryDocAffinity{}).Error
}

func (r *memoryRepository) DeleteAllDocAffinity(
	ctx context.Context, scope interfaces.MemoryScope,
) error {
	return r.scoped(ctx, scope).Delete(&types.MemoryDocAffinity{}).Error
}

func (r *memoryRepository) CountActive(
	ctx context.Context, scope interfaces.MemoryScope,
) (int64, error) {
	var count int64
	err := r.scoped(ctx, scope).
		Model(&types.MemoryItem{}).
		Where("status = ?", types.MemoryStatusActive).
		Count(&count).Error
	return count, err
}
