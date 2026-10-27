package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// WikiRecoveryRepository reads document results and atomically restores failed
// Wiki operations without modifying source chunks, indexes, or Wiki pages.
type WikiRecoveryRepository interface {
	ListDocuments(context.Context, uint64, string, string, int, int) (*types.WikiDocumentList, error)
	RetryDocuments(context.Context, uint64, string, []string) (int, error)
}

// ErrWikiRecoveryNotReady reports missing or unfinished source artifacts.
var ErrWikiRecoveryNotReady = errors.New("source document is not ready for Wiki recovery")

type wikiRecoveryRepository struct{ db *gorm.DB }

// NewWikiRecoveryRepository constructs the durable Wiki recovery repository.
func NewWikiRecoveryRepository(db *gorm.DB) WikiRecoveryRepository {
	return &wikiRecoveryRepository{db: db}
}

// The archive also contains KB-level asynq failures and retracts. Only a
// current document's ingest operation can be restored through this API.
func wikiRecoveryJSON(db *gorm.DB, field string) string {
	if db.Name() == "postgres" {
		return "(payload->>'" + field + "')"
	}
	return "json_extract(payload, '$." + field + "')"
}

func wikiDocumentQuery(db *gorm.DB, tenantID uint64, kbID string) *gorm.DB {
	attempt := "COALESCE(CAST(" + wikiRecoveryJSON(db, "attempt") + " AS INTEGER), 0)"
	return db.Table("knowledges AS k").
		Joins(`LEFT JOIN task_dead_letters AS d ON d.id = (
			SELECT MAX(d.id) FROM task_dead_letters AS d
			WHERE d.tenant_id = k.tenant_id AND d.scope_id = k.knowledge_base_id
			AND d.scope = ? AND d.task_type = ? AND d.related_id = k.id
			AND `+wikiRecoveryJSON(db, "op")+` = 'ingest'
			AND `+attempt+` >= COALESCE((SELECT MAX(a.attempt)
			FROM knowledge_processing_spans AS a WHERE a.knowledge_id = k.id), 0)
		)`, types.TaskScopeKnowledgeBase, types.TypeWikiIngest).
		Joins(`LEFT JOIN knowledge_processing_spans AS s ON s.id = (
			SELECT MAX(w.id) FROM knowledge_processing_spans AS w
			WHERE w.knowledge_id = k.id AND w.name = 'postprocess.wiki'
			AND w.attempt = COALESCE((SELECT MAX(a.attempt)
			FROM knowledge_processing_spans AS a WHERE a.knowledge_id = k.id), 0)
		)`).
		Where("k.tenant_id = ? AND k.knowledge_base_id = ? AND k.deleted_at IS NULL", tenantID, kbID)
}

const wikiDocumentPending = `EXISTS (SELECT 1 FROM task_pending_ops AS p
	WHERE p.tenant_id = k.tenant_id AND p.scope_id = k.knowledge_base_id
	AND p.scope = 'knowledge_base' AND p.task_type = 'wiki:ingest'
	AND p.dedup_key = k.id AND p.op = 'ingest')`

const wikiDocumentStatus = `CASE WHEN ` + wikiDocumentPending + ` THEN
    CASE WHEN s.status = 'running' THEN 'running' ELSE 'pending' END
	WHEN d.id IS NOT NULL THEN 'failed'
	WHEN s.status = 'done' THEN 'completed'
	WHEN s.status = 'running' THEN 'running'
	WHEN s.status = 'skipped' THEN 'skipped'
	ELSE 'not_started' END`

func (r *wikiRecoveryRepository) ListDocuments(
	ctx context.Context, tenantID uint64, kbID, status string, page, pageSize int,
) (*types.WikiDocumentList, error) {
	result := &types.WikiDocumentList{Documents: []*types.WikiDocument{}}
	query := func() *gorm.DB { return wikiDocumentQuery(r.db.WithContext(ctx), tenantID, kbID) }
	if err := query().Where("d.id IS NOT NULL AND NOT " + wikiDocumentPending).
		Count(&result.FailedTotal).Error; err != nil {
		return nil, err
	}
	filtered := func() *gorm.DB {
		q := query()
		if status != "" {
			q = q.Where("("+wikiDocumentStatus+") = ?", status)
		}
		return q
	}
	if err := filtered().Count(&result.Total).Error; err != nil {
		return nil, err
	}
	err := filtered().Select(`k.id AS knowledge_id, k.title, k.parse_status,
		` + wikiDocumentStatus + ` AS wiki_status,
		COALESCE(NULLIF(s.error_message, ''), d.last_error, '') AS last_error,
		COALESCE(d.fail_count, 0) AS fail_count`).
		Order("k.created_at DESC, k.id ASC").Offset((page - 1) * pageSize).Limit(pageSize).
		Scan(&result.Documents).Error
	return result, err
}

func (r *wikiRecoveryRepository) RetryDocuments(
	ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string,
) (int, error) {
	// Consistent lock order also covers overlapping batch requests on Postgres.
	ids := append([]string(nil), knowledgeIDs...)
	sort.Strings(ids)
	requeued := 0
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A write acquires the KB lock on both SQLite and Postgres and serializes
		// retries with deletion and other retry requests before reading state.
		locked := tx.Model(&types.KnowledgeBase{}).
			Where("tenant_id = ? AND id = ?", tenantID, kbID).
			UpdateColumn("updated_at", gorm.Expr("updated_at"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected == 0 {
			return errors.New("wiki knowledge base is unavailable")
		}
		active, err := tenantActiveWithinTx(tx, tenantID)
		if err != nil {
			return err
		}
		if !active {
			return errors.New("wiki workspace is unavailable")
		}
		for i, id := range ids {
			if i > 0 && id == ids[i-1] {
				continue
			}
			added, err := retryWikiDocument(tx, tenantID, kbID, id)
			if err != nil {
				return fmt.Errorf("retry Wiki for %s: %w", id, err)
			}
			if added {
				requeued++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return requeued, nil
}

func retryWikiDocument(tx *gorm.DB, tenantID uint64, kbID, id string) (bool, error) {
	// Lock the source row before checking its parse state and current attempt.
	locked := tx.Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, id).
		UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if locked.Error != nil {
		return false, locked.Error
	}
	if locked.RowsAffected == 0 {
		return false, ErrKnowledgeNotFound
	}
	var kn types.Knowledge
	if err := tx.Where("tenant_id = ? AND id = ?", tenantID, id).Take(&kn).Error; err != nil {
		return false, err
	}
	if kn.ParseStatus != types.ParseStatusCompleted && kn.ParseStatus != types.ParseStatusFinalizing {
		return false, fmt.Errorf("%w: source ingestion must finish before retrying Wiki", ErrWikiRecoveryNotReady)
	}
	var pending int64
	if err := tx.Model(&types.TaskPendingOp{}).Where(
		"tenant_id = ? AND task_type = ? AND scope = ? AND scope_id = ? AND dedup_key = ?",
		tenantID, types.TypeWikiIngest, types.TaskScopeKnowledgeBase, kbID, id,
	).Count(&pending).Error; err != nil {
		return false, err
	}
	if pending > 0 {
		return false, nil
	}
	var dl types.TaskDeadLetter
	err := wikiDocumentQuery(tx, tenantID, kbID).Where("k.id = ? AND d.id IS NOT NULL", id).
		Select("d.*").Take(&dl).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var op struct {
		Op          string `json:"op"`
		KnowledgeID string `json:"knowledge_id"`
		Attempt     int    `json:"attempt"`
	}
	if err := json.Unmarshal(dl.Payload, &op); err != nil {
		return false, err
	}
	if op.Op != "ingest" || op.KnowledgeID != id {
		return false, errors.New("invalid archived Wiki operation")
	}
	var chunks int64
	if err := tx.Model(&types.Chunk{}).Where(
		"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? AND chunk_type IN ? AND content <> ''",
		tenantID, kbID, id, []types.ChunkType{types.ChunkTypeText, types.ChunkTypeImageOCR},
	).Count(&chunks).Error; err != nil {
		return false, err
	}
	if chunks == 0 {
		return false, fmt.Errorf("%w: source text chunks are missing; rebuild the document first",
			ErrWikiRecoveryNotReady)
	}
	var unfinished int64
	if err := tx.Model(&types.Chunk{}).Where(
		"tenant_id = ? AND knowledge_id = ? AND index_status <> 'ready'", tenantID, id,
	).Count(&unfinished).Error; err != nil {
		return false, err
	}
	if unfinished > 0 {
		return false, fmt.Errorf("%w: source chunk indexes must finish before retrying Wiki", ErrWikiRecoveryNotReady)
	}
	// The stored payload retains its language and attempt. Existing pages are
	// reconciled by the ordinary ingest worker; source chunks are reused.
	if err := tx.Create(&types.TaskPendingOp{
		TenantID: tenantID, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: kbID, Op: "ingest", DedupKey: id, Payload: dl.Payload,
	}).Error; err != nil {
		return false, err
	}
	if err := tx.Model(&kn).Updates(map[string]interface{}{
		"parse_status":           types.ParseStatusFinalizing,
		"pending_subtasks_count": gorm.Expr("pending_subtasks_count + 1"),
	}).Error; err != nil {
		return false, err
	}
	// Consume all historical failures of this document up to this snapshot,
	// atomically with enqueue. Repeated requests cannot restore them twice.
	if err := tx.Where("tenant_id = ? AND task_type = ? AND scope = ? AND scope_id = ? "+
		"AND related_id = ? AND id <= ? AND "+wikiRecoveryJSON(tx, "op")+" = 'ingest'",
		tenantID, types.TypeWikiIngest, types.TaskScopeKnowledgeBase, kbID, id, dl.ID,
	).Delete(&types.TaskDeadLetter{}).Error; err != nil {
		return false, err
	}
	return true, nil
}
