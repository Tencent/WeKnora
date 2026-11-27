package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Called inside the page write transaction. Lock source rows in a stable order
// so concurrent slugs of the same documents cannot overwrite each other's
// progress. No model call runs under these locks.
func checkpointWikiPageWrite(ctx context.Context, tx *gorm.DB, page *types.WikiPage) error {
	cp, ok := types.WikiPageCheckpointFromContext(ctx)
	if !ok || cp.Slug != page.Slug || cp.KnowledgeBaseID != page.KnowledgeBaseID || cp.TenantID != page.TenantID {
		return nil
	}
	ids := slices.Clone(cp.PendingOpIDs)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ids = slices.Compact(ids)
	for _, id := range ids {
		var row types.TaskPendingOp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND task_type = ? AND scope = ? AND scope_id = ?",
				id, page.TenantID, "wiki:ingest", "knowledge_base", page.KnowledgeBaseID).Take(&row).Error; err != nil {
			return fmt.Errorf("lock wiki checkpoint %d: %w", id, err)
		}
		payload, err := appliedWikiSlugPayload(row.Payload, page.Slug)
		if err != nil {
			return fmt.Errorf("apply wiki checkpoint %d: %w", id, err)
		}
		if err := tx.Model(&types.TaskPendingOp{}).Where("id = ?", id).Update("payload", payload).Error; err != nil {
			return err
		}
	}
	return nil
}

// Preserve unknown payload fields for rolling upgrades and retain paired
// retraction/addition updates together until their page transaction commits.
func appliedWikiSlugPayload(payload []byte, slug string) ([]byte, error) {
	var op map[string]json.RawMessage
	if err := json.Unmarshal(payload, &op); err != nil {
		return nil, err
	}
	var cp map[string]json.RawMessage
	if err := json.Unmarshal(op["reduce_checkpoint"], &cp); err != nil {
		return nil, err
	}
	if cp == nil {
		return nil, fmt.Errorf("missing reduce checkpoint")
	}
	var updates []json.RawMessage
	if err := json.Unmarshal(cp["Updates"], &updates); err != nil {
		return nil, err
	}
	remaining := make([]json.RawMessage, 0, len(updates))
	for _, raw := range updates {
		var update struct{ Slug string }
		if err := json.Unmarshal(raw, &update); err != nil {
			return nil, err
		}
		if update.Slug != slug {
			remaining = append(remaining, raw)
		}
	}
	var applied []string
	if raw := cp["Applied"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &applied); err != nil {
			return nil, err
		}
	}
	if !slices.Contains(applied, slug) {
		applied = append(applied, slug)
	}
	cp["Updates"], _ = json.Marshal(remaining)
	cp["Applied"], _ = json.Marshal(applied)
	op["reduce_checkpoint"], _ = json.Marshal(cp)
	return json.Marshal(op)
}
