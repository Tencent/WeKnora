package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrSubKnowledgeConflict indicates that a source identity or group root already exists.
var ErrSubKnowledgeConflict = errors.New("sub_knowledge source or root already exists; update its knowledge_id")

// ErrSubKnowledgeIdentityChange rejects updates to immutable source identity fields.
var ErrSubKnowledgeIdentityChange = errors.New("sub_knowledge source identity cannot be changed")

// SubKnowledgeConflictError identifies the existing knowledge in an authorized scope.
type SubKnowledgeConflictError struct{ KnowledgeID string }

func (e *SubKnowledgeConflictError) Error() string { return ErrSubKnowledgeConflict.Error() }
func (e *SubKnowledgeConflictError) Unwrap() error { return ErrSubKnowledgeConflict }

var _ interfaces.SubKnowledgeRepository = (*knowledgeRepository)(nil)

func subKnowledgeOriginQuery(db *gorm.DB, f *types.SubKnowledgeMapping) *gorm.DB {
	return db.Where("tenant_id = ? AND knowledge_base_id = ? AND source_namespace = ? AND source_id = ?",
		f.TenantID, f.KnowledgeBaseID, f.SourceNamespace, f.SourceID)
}

// Called after rollback so a uniqueness failure can be mapped to a scoped 409.
// No lookup may reveal an identity from another tenant or KB.
func (r *knowledgeRepository) subKnowledgeConflict(
	ctx context.Context, cause error, f *types.SubKnowledgeMapping,
) error {
	if cause == nil {
		return nil
	}
	var existing types.SubKnowledgeMapping
	if e := subKnowledgeOriginQuery(r.db.WithContext(ctx), f).First(&existing).Error; e == nil &&
		existing.KnowledgeID != f.KnowledgeID {
		return &SubKnowledgeConflictError{KnowledgeID: existing.KnowledgeID}
	}
	if f.NodeType == "root" {
		e := r.db.WithContext(ctx).Where(
			"tenant_id = ? AND knowledge_base_id = ? AND source_namespace = ? AND root_id = ? AND node_type = ?",
			f.TenantID, f.KnowledgeBaseID, f.SourceNamespace, f.RootID, "root",
		).First(&existing).Error
		if e == nil && existing.KnowledgeID != f.KnowledgeID {
			return &SubKnowledgeConflictError{KnowledgeID: existing.KnowledgeID}
		}
	}
	return cause
}

// CreateSubKnowledge returns created=false for an identical retry. Both rows
// are in the same transaction; a concurrent duplicate cannot leave an orphan.
func (r *knowledgeRepository) CreateSubKnowledge(
	ctx context.Context, k *types.Knowledge, f *types.SubKnowledgeMapping, tagIDs ...string,
) (*types.Knowledge, bool, error) {
	if err := f.Validate(); err != nil {
		return nil, false, err
	}
	f.TenantID, f.KnowledgeBaseID = k.TenantID, k.KnowledgeBaseID
	// SQLite compares stored datetime strings; normalize source timestamps to
	// UTC so cursor cutoffs do not compare local wall time against UTC text.
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	} else {
		f.CreatedAt = f.CreatedAt.UTC()
	}
	f.UpdatedAt = f.CreatedAt
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if k.ParentKnowledgeID != "" {
			if err := lockKnowledgeTreeKB(tx, k.TenantID, k.KnowledgeBaseID); err != nil {
				return err
			}
			if err := validateKnowledgeParent(tx, k); err != nil {
				return err
			}
		}
		if err := tx.Create(k).Error; err != nil {
			return err
		}
		f.KnowledgeID = k.ID
		if e := tx.Create(f).Error; e != nil {
			return e
		}
		if len(tagIDs) > 0 {
			return (&knowledgeRepository{db: tx}).SetKnowledgeTags(ctx, k.ID, tagIDs)
		}
		return nil
	})
	if err == nil {
		k.SubKnowledge = &f.SubKnowledgeInput
		return k, true, nil
	}
	// Lookup is restricted to the same KB and tenant, including on conflicts.
	var existing types.SubKnowledgeMapping
	if lookupErr := subKnowledgeOriginQuery(r.db.WithContext(ctx), f).First(&existing).Error; lookupErr != nil {
		return nil, false, r.subKnowledgeConflict(ctx, err, f)
	}
	old, lookupErr := r.GetKnowledgeByID(ctx, k.TenantID, existing.KnowledgeID)
	if lookupErr != nil {
		return nil, false, err
	}
	if existing.SourceHash != f.SourceHash || old.ParentKnowledgeID != k.ParentKnowledgeID {
		return nil, false, &SubKnowledgeConflictError{KnowledgeID: old.ID}
	}
	// Generic metadata updates may rename Knowledge without going through the
	// manual ingestion path. Do not treat an obsolete fingerprint as proof that
	// the original content still matches an identical create retry.
	meta, metaErr := old.ManualMetadata()
	if old.Type != types.KnowledgeTypeManual || metaErr != nil || meta == nil ||
		existing.ContentHash(old.Title, meta.Content, meta.Status) != f.SourceHash {
		return nil, false, &SubKnowledgeConflictError{KnowledgeID: old.ID}
	}
	old.SubKnowledge = &existing.SubKnowledgeInput
	return old, false, nil
}

func (r *knowledgeRepository) GetSubKnowledgeMapping(
	ctx context.Context, tenant uint64, id string,
) (*types.SubKnowledgeMapping, error) {
	var f types.SubKnowledgeMapping
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND knowledge_id = ?", tenant, id).First(&f).Error
	return &f, err
}

// UpdateSubKnowledge preserves provenance when the caller omits it. A lock
// serializes mapping updates; it never permits switching a document's identity.
func (r *knowledgeRepository) UpdateSubKnowledge(
	ctx context.Context, k *types.Knowledge, input *types.SubKnowledgeInput, hash string,
) error {
	var saved types.SubKnowledgeMapping
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old types.SubKnowledgeMapping
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("knowledge_id = ? AND tenant_id = ?", k.ID, k.TenantID).First(&old).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && (old.KnowledgeBaseID != k.KnowledgeBaseID || input != nil && !old.SameIdentity(*input)) {
			return ErrSubKnowledgeIdentityChange
		}
		if input == nil && errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("sub_knowledge source not found")
		}
		if input != nil {
			if e := input.Validate(); e != nil {
				return e
			}
			old.SubKnowledgeInput = *input
		}
		old.KnowledgeID, old.TenantID, old.KnowledgeBaseID, old.SourceHash = k.ID, k.TenantID, k.KnowledgeBaseID, hash
		if old.CreatedAt.IsZero() {
			old.CreatedAt = time.Now().UTC()
		}
		old.UpdatedAt = time.Now().UTC()
		saved = old
		kr := &knowledgeRepository{db: tx}
		if e := kr.UpdateKnowledge(ctx, k); e != nil {
			return e
		}
		if e := tx.Save(&old).Error; e != nil {
			return e
		}
		k.SubKnowledge = &old.SubKnowledgeInput
		return nil
	})
	return r.subKnowledgeConflict(ctx, err, &saved)
}

// SetSubKnowledge attaches an existing Knowledge of any type without changing
// its content or scheduling ingestion. The authorized KB cannot change mid-call.
func (r *knowledgeRepository) SetSubKnowledge(
	ctx context.Context, tenant uint64, id, authorizedKB string, input *types.SubKnowledgeInput,
) error {
	if err := input.Validate(); err != nil {
		return err
	}
	f := types.SubKnowledgeMapping{
		KnowledgeID: id, TenantID: tenant, KnowledgeBaseID: authorizedKB, SubKnowledgeInput: *input,
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var k types.Knowledge
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND tenant_id = ? AND knowledge_base_id = ? AND parse_status != ?",
			id, tenant, authorizedKB, types.ParseStatusDeleting,
		).First(&k).Error; e != nil {
			return e
		}
		var old types.SubKnowledgeMapping
		e := tx.Where("knowledge_id = ? AND tenant_id = ?", id, tenant).First(&old).Error
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if e == nil {
			if old.KnowledgeBaseID != authorizedKB || !old.SameIdentity(*input) {
				return ErrSubKnowledgeIdentityChange
			}
			f.CreatedAt = old.CreatedAt
		}
		if f.CreatedAt.IsZero() {
			f.CreatedAt = time.Now().UTC()
		}
		f.UpdatedAt = time.Now().UTC()
		content, status := k.FileHash, k.ParseStatus
		if k.Type == types.KnowledgeTypeManual {
			meta, e := k.ManualMetadata()
			if e == nil && meta != nil {
				content, status = meta.Content, meta.Status
			}
		}
		f.SourceHash = input.ContentHash(k.Title, content, status)
		return tx.Save(&f).Error
	})
	return r.subKnowledgeConflict(ctx, err, &f)
}

func (r *knowledgeRepository) ListSubKnowledge(
	ctx context.Context, tenant uint64, kb, sourceNamespace, group string, cursor string, pageSize int,
) (*types.SubKnowledgeGroup, error) {
	if pageSize < 1 || pageSize > 50 {
		return nil, fmt.Errorf("invalid sub_knowledge pagination")
	}
	position, err := parseSubKnowledgeCursor(cursor, tenant, kb, sourceNamespace, group)
	if err != nil {
		return nil, err
	}
	result := &types.SubKnowledgeGroup{
		KnowledgeBaseID: kb, SourceNamespace: sourceNamespace, RootID: group, PageSize: pageSize,
		SnapshotAt: position.AsOf, Consistency: "BEST_EFFORT_IMPORT_SNAPSHOT",
		Items: []types.SubKnowledgeItem{}, Completeness: "IMPORTED_VISIBLE_KNOWLEDGE_ONLY",
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := func() *gorm.DB {
			return tx.Table("knowledge_sub_knowledge AS f").Joins(
				"JOIN knowledges AS k ON k.id = f.knowledge_id "+
					"AND k.tenant_id = f.tenant_id AND k.knowledge_base_id = f.knowledge_base_id",
			).Where(
				"f.tenant_id = ? AND f.knowledge_base_id = ? AND f.source_namespace = ? AND f.root_id = ?",
				tenant, kb, sourceNamespace, group,
			).Where("f.created_at <= ?", position.AsOf).Where(
				"f.deleted_at IS NULL AND k.deleted_at IS NULL AND k.parse_status NOT IN ?",
				[]string{types.ManualKnowledgeStatusDraft, types.ParseStatusDeleting},
			)
		}
		if e := q().Count(&result.Total).Error; e != nil {
			return e
		}
		var rootCount int64
		if e := q().Where("f.node_type = 'root'").Count(&rootCount).Error; e != nil {
			return e
		}
		result.HasRoot = rootCount > 0
		rowsQuery := q()
		if cursor != "" {
			rowsQuery = rowsQuery.Where(
				"(CASE WHEN f.node_type = 'root' THEN 0 ELSE 1 END, "+
					"f.sort_order, f.source_id, f.knowledge_id) > (?, ?, ?, ?)",
				position.Rank, position.Order, position.SourceID, position.KnowledgeID,
			)
		}
		if e := rowsQuery.Select("f.knowledge_id, f.source_namespace, f.root_id, f.node_type, f.source_id, " +
			"f.reference_id, f.parent_id, f.sort_order, k.title, k.type, k.metadata, " +
			"k.parse_status, k.enable_status, k.updated_at").
			Order("CASE WHEN f.node_type = 'root' THEN 0 ELSE 1 END, f.sort_order, f.source_id, f.knowledge_id").
			Limit(pageSize + 1).Scan(&result.Items).Error; e != nil {
			return e
		}
		result.HasMore = len(result.Items) > pageSize
		if result.HasMore {
			result.Items = result.Items[:pageSize]
		}
		if result.HasMore {
			last := result.Items[len(result.Items)-1]
			position.Rank = 1
			if last.NodeType == "root" {
				position.Rank = 0
			}
			position.Order = last.SortOrder
			position.SourceID = last.SourceID
			position.KnowledgeID = last.KnowledgeID
			result.NextCursor = position.encode()
		}
		ids := make([]string, 0, len(result.Items))
		positions := map[string]int{}
		for i := range result.Items {
			m := &result.Items[i]
			m.Images = []types.ImageInfo{}
			m.ContentSource = "original_resource_link"
			if m.Type == types.KnowledgeTypeManual {
				meta, e := (&types.Knowledge{Metadata: types.JSON(m.Metadata)}).ManualMetadata()
				if e != nil || meta == nil {
					m.ContentSource = "unavailable"
				} else {
					m.Content, m.ContentVersion = meta.Content, meta.Version
					m.ContentSource, m.ContentAvailable = "manual_markdown_snapshot", true
				}
			}
			ids = append(ids, m.KnowledgeID)
			positions[m.KnowledgeID] = i
		}
		if len(ids) > 0 {
			var images []struct {
				KnowledgeID string
				ImageInfo   string
			}
			if e := tx.Table("chunks").Select("knowledge_id, image_info").Where(
				"tenant_id = ? AND knowledge_id IN ? AND deleted_at IS NULL "+
					"AND is_enabled = ? AND image_info IS NOT NULL AND image_info != ''",
				tenant, ids, true,
			).Scan(&images).Error; e != nil {
				return e
			}
			seen := map[string]bool{}
			for _, row := range images {
				var infos []types.ImageInfo
				if e := json.Unmarshal([]byte(row.ImageInfo), &infos); e != nil {
					result.Items[positions[row.KnowledgeID]].ImagesStatus = "PARTIAL_METADATA_UNAVAILABLE"
					continue
				}
				for _, info := range infos {
					key := row.KnowledgeID + "\x00" + info.URL
					if info.URL != "" && !seen[key] {
						item := &result.Items[positions[row.KnowledgeID]]
						item.Images = append(item.Images, info)
						seen[key] = true
					}
				}
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}
