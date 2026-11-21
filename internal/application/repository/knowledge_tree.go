package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ interfaces.KnowledgeTreeRepository = (*knowledgeRepository)(nil)

func (r *knowledgeRepository) CreateKnowledgeWithParent(ctx context.Context, k *types.Knowledge, tags ...string) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockKnowledgeTreeKB(tx, k.TenantID, k.KnowledgeBaseID); err != nil {
			return err
		}
		if err := validateKnowledgeParent(tx, k); err != nil {
			return err
		}
		if err := tx.Create(k).Error; err != nil {
			return err
		}
		if len(tags) > 0 {
			return (&knowledgeRepository{db: tx}).SetKnowledgeTags(ctx, k.ID, tags)
		}
		return nil
	})
	return nativeKnowledgeError(err)
}

func nativeKnowledgeError(err error) error {
	if err == nil {
		return nil
	}
	for _, message := range []string{
		"knowledge parent", "parent knowledge scope/state invalid",
		"cross-KB move of linked knowledge", "knowledge deletion is in progress",
	} {
		if strings.Contains(err.Error(), message) {
			return apperrors.NewConflictError(
				"knowledge relationship or lifecycle changed; retry after checking its state",
			)
		}
	}
	return err
}

func lockKnowledgeTreeKB(tx *gorm.DB, tenant uint64, kb string) error {
	var row struct{ ID string }
	if err := tx.Table("knowledge_bases").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").
		Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", kb, tenant).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.NewNotFoundError("knowledge base not found")
		}
		return err
	}
	return nil
}

func validateKnowledgeParent(tx *gorm.DB, child *types.Knowledge) error {
	parent := child.ParentKnowledgeID
	if parent == "" {
		return nil
	}
	if len(parent) > 36 || strings.TrimSpace(parent) != parent || strings.ContainsRune(parent, '\x00') {
		return apperrors.NewBadRequestError("invalid parent_knowledge_id")
	}
	seen := map[string]bool{}
	if child.ID != "" {
		seen[child.ID] = true
	}
	for depth := 1; parent != ""; depth++ {
		if depth > types.MaxKnowledgeDepth || seen[parent] {
			return apperrors.NewConflictError("knowledge parent cycle or depth limit exceeded")
		}
		seen[parent] = true
		var row types.Knowledge
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND tenant_id = ? AND knowledge_base_id = ? AND parse_status != ? AND NOT deletion_pending",
			parent, child.TenantID, child.KnowledgeBaseID, types.ParseStatusDeleting,
		).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperrors.NewNotFoundError("parent knowledge not found in this knowledge base")
			}
			return err
		}
		if err := access.RejectMovingKnowledge(&row); err != nil {
			return err
		}
		parent = row.ParentKnowledgeID
	}
	return nil
}

// Breadth-first, one query per level (not one query per child). Scope is checked
// on every row, including corrupt links; no expansion can cross a KB boundary.
func walkKnowledgeChildren(
	tx *gorm.DB, root types.KnowledgeTreeRoot, opts types.KnowledgeChildrenOptions, deleting bool,
) ([]types.KnowledgeChild, error) {
	var anchor types.Knowledge
	anchorQuery := tx.Model(&types.Knowledge{}).Where(
		"id = ? AND tenant_id = ? AND knowledge_base_id = ?", root.KnowledgeID, root.TenantID, root.KBID,
	)
	if !deleting {
		anchorQuery = anchorQuery.Where(
			"parse_status NOT IN ? AND NOT deletion_pending",
			[]string{types.ManualKnowledgeStatusDraft, types.ParseStatusDeleting},
		)
	}
	if err := anchorQuery.Take(&anchor).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NewNotFoundError("knowledge tree anchor changed or is unavailable")
		}
		return nil, err
	}
	if err := access.RejectMovingKnowledge(&anchor); err != nil {
		return nil, err
	}
	maxDepth := opts.Depth
	if maxDepth == -1 {
		maxDepth = types.MaxKnowledgeDepth
	}
	frontier := []string{root.KnowledgeID}
	seen := map[string]bool{root.KnowledgeID: true}
	var result []types.KnowledgeChild
	for depth := 1; len(frontier) > 0 && depth <= maxDepth+1; depth++ {
		var rows []types.KnowledgeChild
		q := tx.Table("knowledges").Select(
			"id, parent_knowledge_id, tenant_id, knowledge_base_id, title, type, parse_status, enable_status, metadata",
		).Where("parent_knowledge_id IN ? AND deleted_at IS NULL", frontier)
		if !deleting {
			q = q.Where(
				"parse_status NOT IN ? AND NOT deletion_pending",
				[]string{types.ManualKnowledgeStatusDraft, types.ParseStatusDeleting},
			)
		}
		if err := q.Order("created_at, id").Limit(opts.Limit + 1).Scan(&rows).Error; err != nil {
			return nil, err
		}
		if depth > maxDepth {
			if opts.Depth == -1 && len(rows) > 0 {
				return nil, apperrors.NewConflictError("knowledge tree depth limit exceeded")
			}
			break
		}
		frontier = nil
		for _, row := range rows {
			if row.TenantID != root.TenantID || row.KnowledgeBaseID != root.KBID {
				return nil, apperrors.NewConflictError("knowledge tree contains a cross-scope relation")
			}
			if seen[row.KnowledgeID] {
				return nil, apperrors.NewConflictError("knowledge tree contains a cycle")
			}
			if err := access.RejectMovingKnowledge(&types.Knowledge{Metadata: types.JSON(row.Metadata)}); err != nil {
				return nil, err
			}
			seen[row.KnowledgeID] = true
			row.AnchorKnowledgeID, row.Depth = root.KnowledgeID, depth
			result = append(result, row)
			frontier = append(frontier, row.KnowledgeID)
			if len(result) > opts.Limit {
				return nil, apperrors.NewBadRequestError("children_limit_exceeded")
			}
		}
	}
	return result, nil
}

func (r *knowledgeRepository) KnowledgeChildren(
	ctx context.Context, roots []types.KnowledgeTreeRoot, opts types.KnowledgeChildrenOptions,
) ([]types.KnowledgeChild, error) {
	if opts.Depth == 0 {
		return nil, nil
	}
	if _, err := types.ParseKnowledgeChildrenOptions(fmt.Sprint(opts.Depth), fmt.Sprint(opts.Limit)); err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	var result []types.KnowledgeChild
	budget := 512 * 1024
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, root := range roots {
			rows, err := walkKnowledgeChildren(tx, root, opts, false)
			if err != nil {
				return err
			}
			for i := range rows {
				m := &rows[i]
				m.ContentURL = "/api/v1/knowledge/" + url.PathEscape(m.KnowledgeID) + "/preview"
				if m.Type == types.KnowledgeTypeManual {
					meta, e := (&types.Knowledge{Metadata: types.JSON(m.Metadata)}).ManualMetadata()
					if e == nil && meta != nil {
						m.Content, m.ContentAvailable = meta.Content, true
						if len(m.Content) > budget {
							text := m.Content[:budget]
							for !utf8.ValidString(text) {
								text = text[:len(text)-1]
							}
							m.Content = text
							m.ContentTruncated = true
						}
						budget -= len(m.Content)
					}
				}
				m.Metadata = ""
			}
			result = append(result, rows...)
			if len(result) > opts.Limit {
				return apperrors.NewBadRequestError("children_limit_exceeded")
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Admission uses mark=false. The worker repeats collection under the KB lock,
// then marks the complete set before cleanup to prevent new descendants.
func (r *knowledgeRepository) PrepareKnowledgeCascade(
	ctx context.Context, tenant uint64, kb string, seeds []string, mark bool,
) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockKnowledgeTreeKB(tx, tenant, kb); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, id := range seeds {
			if seen[id] {
				continue
			}
			var k types.Knowledge
			e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&k).Error
			if errors.Is(e, gorm.ErrRecordNotFound) && mark {
				continue
			}
			if e != nil {
				if errors.Is(e, gorm.ErrRecordNotFound) {
					return apperrors.NewNotFoundError("cascade knowledge not found in authorized KB")
				}
				return e
			}
			if k.TenantID != tenant || k.KnowledgeBaseID != kb {
				return apperrors.NewConflictError("cascade knowledge scope changed")
			}
			if e := access.RejectMovingKnowledge(&k); e != nil {
				return e
			}
			rows, e := walkKnowledgeChildren(
				tx, types.KnowledgeTreeRoot{TenantID: tenant, KBID: kb, KnowledgeID: id},
				types.KnowledgeChildrenOptions{Depth: -1, Limit: types.MaxKnowledgeChildren}, true,
			)
			if e != nil {
				return e
			}
			all := []string{id}
			for _, c := range rows {
				all = append(all, c.KnowledgeID)
			}
			for _, next := range all {
				if !seen[next] {
					seen[next] = true
					ids = append(ids, next)
				}
			}
			if len(ids) > types.MaxKnowledgeChildren {
				return apperrors.NewBadRequestError("cascade children_limit_exceeded")
			}
		}
		if len(ids) > 0 {
			var rows []types.Knowledge
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id IN ? AND tenant_id = ? AND knowledge_base_id = ?", ids, tenant, kb).
				Order("id").Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) != len(ids) {
				return apperrors.NewConflictError("cascade scope changed")
			}
			for _, k := range rows {
				if err := access.RejectMovingKnowledge(&k); err != nil {
					return err
				}
			}
			if mark {
				return tx.Model(&types.Knowledge{}).
					Where("id IN ? AND tenant_id = ? AND knowledge_base_id = ?", ids, tenant, kb).
					Updates(map[string]any{
						"parse_status": types.ParseStatusDeleting, "updated_at": time.Now().UTC(),
					}).Error
			}
		}
		return nil
	})
	return ids, err
}

func (r *knowledgeRepository) CheckKnowledgeTreeMove(
	ctx context.Context, tenant uint64, id, targetKB string,
) error {
	var count int64
	if err := r.db.WithContext(ctx).Model(&types.Knowledge{}).Where(
		"tenant_id = ? AND ((id = ? AND parent_knowledge_id IS NOT NULL AND parent_knowledge_id != '') "+
			"OR parent_knowledge_id = ?) AND knowledge_base_id != ?",
		tenant, id, id, targetKB,
	).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return apperrors.NewConflictError("cross-KB move of linked knowledge is not supported")
	}
	return nil
}
