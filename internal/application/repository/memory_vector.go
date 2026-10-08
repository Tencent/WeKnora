package repository

import (
	"context"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// fallbackVectorScanCap bounds the in-process fallback.
//
// One subject cannot hold more active memories than the workspace capacity
// cap, which tops out in the low thousands, so this is a guard against a
// corrupted scope rather than a real limit: a subject at the cap still gets
// every one of its vectors scored, which is the whole point of the change.
const fallbackVectorScanCap = 5000

// vectorColumnReady reports whether the database can do the distance
// arithmetic itself.
//
// Checked once and cached. It is false on SQLite, and false on a PostgreSQL
// deployment that never installed pgvector because its retrieval driver does
// not use it — both of which keep working through the fallback below, just
// with every vector crossing the wire.
func (r *memoryRepository) vectorColumnReady() bool {
	r.vectorOnce.Do(func() {
		if r.db == nil || r.db.Dialector == nil || r.db.Dialector.Name() != "postgres" {
			return
		}
		r.vectorColumn = r.db.Migrator().HasColumn(&types.MemoryItemEmbedding{}, "embedding")
	})
	return r.vectorColumn
}

// writeVectorColumn keeps the database's own vector type in step with the
// stored blob. Best effort inside the caller's transaction: the blob is the
// source of truth, and a row whose vector column is behind is picked up by
// SyncVectorColumn rather than lost.
func (r *memoryRepository) writeVectorColumn(tx *gorm.DB, itemID string, raw []byte) error {
	if !r.vectorColumnReady() || itemID == "" {
		return nil
	}
	literal := types.FormatEmbeddingLiteral(types.DecodeEmbedding(raw))
	if literal == "" {
		return nil
	}
	return tx.Exec(
		`UPDATE memory_item_embeddings SET embedding = ?::halfvec WHERE item_id = ?`,
		literal, itemID,
	).Error
}

// vectorHitRow is one (item, distance) pair as the ranking query returns it.
type vectorHitRow struct {
	ItemID string
	Score  float64
}

// SearchItemsByVector ranks a subject's whole vector set against one query.
//
// The candidate set is every vector the subject has, not a window of them.
// This is the difference that matters: the previous implementation listed
// items by importance, loaded the vectors belonging to that list, and scored
// those — so relevance could only ever re-order what importance had already
// chosen, and a matching memory outside the window was unreachable.
func (r *memoryRepository) SearchItemsByVector(
	ctx context.Context, scope interfaces.MemoryScope, query interfaces.MemoryVectorQuery,
) ([]interfaces.MemoryVectorHit, error) {
	if !scope.Valid() || query.ModelID == "" || len(query.Vector) == 0 {
		return nil, nil
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}

	var rows []vectorHitRow
	var err error
	if r.vectorColumnReady() {
		rows, err = r.rankInDatabase(ctx, scope, query, limit)
	} else {
		rows, err = r.rankInProcess(ctx, scope, query, limit)
	}
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Score >= query.MinScore {
			ids = append(ids, row.ItemID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}

	// Items are loaded separately rather than joined into the ranking query so
	// that neither path has to reproduce the column list of memory_items, and
	// so the ranking query stays narrow enough to be read at a glance.
	var items []*types.MemoryItem
	if err := r.scoped(ctx, scope).Where("id IN ?", ids).Find(&items).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]*types.MemoryItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}

	hits := make([]interfaces.MemoryVectorHit, 0, len(ids))
	for _, row := range rows {
		item, ok := byID[row.ItemID]
		if !ok {
			continue
		}
		hits = append(hits, interfaces.MemoryVectorHit{Item: item, Score: row.Score})
	}
	return hits, nil
}

// rankInDatabase orders by cosine distance in SQL and returns only the top k.
func (r *memoryRepository) rankInDatabase(
	ctx context.Context, scope interfaces.MemoryScope, query interfaces.MemoryVectorQuery, limit int,
) ([]vectorHitRow, error) {
	literal := types.FormatEmbeddingLiteral(query.Vector)
	if literal == "" {
		return nil, nil
	}
	sql := `
		SELECT e.item_id AS item_id,
		       1 - (e.embedding::halfvec <=> ?::halfvec) AS score
		FROM memory_item_embeddings e
		JOIN memory_items i
		  ON i.id = e.item_id AND i.tenant_id = e.tenant_id AND i.subject_id = e.subject_id
		WHERE e.tenant_id = ? AND e.subject_id = ?
		  AND e.model_id = ? AND e.dims = ? AND e.embedding IS NOT NULL
		  AND i.status = ?
		  AND (i.expires_at IS NULL OR i.expires_at > ?)`
	args := []interface{}{
		literal, scope.TenantID, scope.SubjectID,
		query.ModelID, len(query.Vector), types.MemoryStatusActive, time.Now(),
	}
	if len(query.Kinds) > 0 {
		sql += ` AND i.kind IN ?`
		args = append(args, query.Kinds)
	}
	// Ordering by the operator rather than by the computed score keeps the
	// expression identical to the one an index would be built on.
	sql += ` ORDER BY e.embedding::halfvec <=> ?::halfvec LIMIT ?`
	args = append(args, literal, limit)

	var rows []vectorHitRow
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// rankInProcess is the portable path: read this subject's vectors and score
// them here. Bounded by the same capacity cap that bounds the subject itself,
// so it still sees everything — it just pays to transfer it.
func (r *memoryRepository) rankInProcess(
	ctx context.Context, scope interfaces.MemoryScope, query interfaces.MemoryVectorQuery, limit int,
) ([]vectorHitRow, error) {
	type storedVector struct {
		ItemID string
		Vector []byte
	}
	sql := `
		SELECT e.item_id AS item_id, e.vector AS vector
		FROM memory_item_embeddings e
		JOIN memory_items i
		  ON i.id = e.item_id AND i.tenant_id = e.tenant_id AND i.subject_id = e.subject_id
		WHERE e.tenant_id = ? AND e.subject_id = ?
		  AND e.model_id = ? AND e.dims = ?
		  AND i.status = ?
		  AND (i.expires_at IS NULL OR i.expires_at > ?)`
	args := []interface{}{
		scope.TenantID, scope.SubjectID,
		query.ModelID, len(query.Vector), types.MemoryStatusActive, time.Now(),
	}
	if len(query.Kinds) > 0 {
		sql += ` AND i.kind IN ?`
		args = append(args, query.Kinds)
	}
	sql += ` LIMIT ?`
	args = append(args, fallbackVectorScanCap)

	var stored []storedVector
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&stored).Error; err != nil {
		return nil, err
	}

	rows := make([]vectorHitRow, 0, len(stored))
	for _, row := range stored {
		vector := types.DecodeEmbedding(row.Vector)
		if len(vector) == 0 {
			continue
		}
		rows = append(rows, vectorHitRow{
			ItemID: row.ItemID,
			Score:  types.CosineSimilarity(query.Vector, vector),
		})
	}
	sortVectorHitsDesc(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// sortVectorHitsDesc puts the closest match first.
func sortVectorHitsDesc(rows []vectorHitRow) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Score > rows[j].Score })
}

// SyncVectorColumn moves already-embedded rows into the database's vector
// type. Rows written before migration 000095 hold only the blob, and a row the
// SQL ranking cannot see is a memory that semantic recall cannot find — so this
// has to drain without waiting for an embedding model to be called again.
func (r *memoryRepository) SyncVectorColumn(
	ctx context.Context, scope interfaces.MemoryScope, limit int,
) (int, error) {
	if !r.vectorColumnReady() || !scope.Valid() {
		return 0, nil
	}
	if limit <= 0 {
		limit = 500
	}
	type pending struct {
		ItemID string
		Vector []byte
	}
	var rows []pending
	err := r.db.WithContext(ctx).Raw(`
		SELECT item_id, vector FROM memory_item_embeddings
		WHERE tenant_id = ? AND subject_id = ?
		  AND embedding IS NULL AND vector IS NOT NULL
		LIMIT ?`, scope.TenantID, scope.SubjectID, limit).Scan(&rows).Error
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, row := range rows {
		if err := r.writeVectorColumn(r.db.WithContext(ctx), row.ItemID, row.Vector); err != nil {
			logger.Warnf(ctx, "memory: sync vector column failed for %s: %v", row.ItemID, err)
			continue
		}
		moved++
	}
	return moved, nil
}
