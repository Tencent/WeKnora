package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrKnowledgeFolderNotFound = errors.New("knowledge folder not found")
	ErrKnowledgeFolderNotEmpty = errors.New("knowledge folder is not empty")
	ErrKnowledgeFolderCycle    = errors.New("knowledge folder cycle")
	ErrKnowledgeFolderTooDeep  = errors.New("knowledge folder exceeds maximum depth")
	ErrKnowledgeFolderConflict = errors.New("knowledge folder name already exists")
	ErrKnowledgeFolderScope    = errors.New("knowledge folder scope mismatch")
)

type knowledgeFolderRepository struct{ db *gorm.DB }

const maxKnowledgeFolderMoveBatchSize = 500

func NewKnowledgeFolderRepository(db *gorm.DB) interfaces.KnowledgeFolderRepository {
	return &knowledgeFolderRepository{db: db}
}

func folderScope(db *gorm.DB, tenantID uint64, kbID string) *gorm.DB {
	return db.Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
}

func (r *knowledgeFolderRepository) getRow(db *gorm.DB, tenantID uint64, kbID, id string) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	if err := folderScope(db.Model(&types.KnowledgeFolder{}), tenantID, kbID).Where("id = ?", id).Take(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeFolderRepository) ancestors(db *gorm.DB, tenantID uint64, kbID, id string) ([]*types.KnowledgeFolder, error) {
	var result []*types.KnowledgeFolder
	err := db.Model(&types.KnowledgeFolder{}).
		Joins("JOIN knowledge_folder_closure c ON c.ancestor_id = knowledge_folders.id").
		Where("c.descendant_id = ? AND c.depth > 0 AND knowledge_folders.tenant_id = ? AND knowledge_folders.knowledge_base_id = ?", id, tenantID, kbID).
		Order("c.depth DESC").Find(&result).Error
	return result, err
}

func (r *knowledgeFolderRepository) ancestorsByDescendant(
	db *gorm.DB, tenantID uint64, kbID string, descendantIDs []string,
) (map[string][]*types.KnowledgeFolder, error) {
	result := make(map[string][]*types.KnowledgeFolder, len(descendantIDs))
	if len(descendantIDs) == 0 {
		return result, nil
	}
	type ancestorRow struct {
		DescendantID    string
		ID              string
		TenantID        uint64
		KnowledgeBaseID string
		ParentID        string
		Name            string
		CreatedAt       time.Time
		UpdatedAt       time.Time
	}
	var rows []ancestorRow
	err := db.Table("knowledge_folder_closure c").
		Select("c.descendant_id, f.id, f.tenant_id, f.knowledge_base_id, f.parent_id, f.name, f.created_at, f.updated_at").
		Joins("JOIN knowledge_folders f ON f.id = c.ancestor_id").
		Where("c.descendant_id IN ? AND c.depth > 0 AND f.tenant_id = ? AND f.knowledge_base_id = ?", descendantIDs, tenantID, kbID).
		Order("c.descendant_id ASC, c.depth DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.DescendantID] = append(result[row.DescendantID], &types.KnowledgeFolder{
			ID: row.ID, TenantID: row.TenantID, KnowledgeBaseID: row.KnowledgeBaseID,
			ParentID: row.ParentID, Name: row.Name, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	return result, nil
}

func translateKnowledgeFolderWriteError(err error) error {
	if err == nil || errors.Is(err, ErrKnowledgeFolderConflict) {
		return err
	}
	message := strings.ToLower(err.Error())
	if (errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(message, "duplicate") || strings.Contains(message, "unique")) &&
		(strings.Contains(message, "knowledge_folders") || strings.Contains(message, "knowledge_folder_sibling")) {
		return fmt.Errorf("%w: %v", ErrKnowledgeFolderConflict, err)
	}
	return err
}

func (r *knowledgeFolderRepository) enrich(ctx context.Context, tenantID uint64, kbID string, folders []*types.KnowledgeFolder) ([]*types.KnowledgeFolderView, error) {
	views := make([]*types.KnowledgeFolderView, len(folders))
	if len(folders) == 0 {
		return views, nil
	}
	ids := make([]string, len(folders))
	byID := make(map[string]*types.KnowledgeFolderView, len(folders))
	for i, f := range folders {
		ids[i] = f.ID
		views[i] = &types.KnowledgeFolderView{KnowledgeFolder: *f}
		byID[f.ID] = views[i]
	}
	type countRow struct {
		ID    string
		Count int64
	}
	var direct, children, totals []countRow
	db := r.db.WithContext(ctx)
	if err := db.Model(&types.Knowledge{}).Select("folder_id AS id, COUNT(*) AS count").Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id IN ?", tenantID, kbID, ids).Group("folder_id").Scan(&direct).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&types.KnowledgeFolder{}).Select("parent_id AS id, COUNT(*) AS count").Where("tenant_id = ? AND knowledge_base_id = ? AND parent_id IN ?", tenantID, kbID, ids).Group("parent_id").Scan(&children).Error; err != nil {
		return nil, err
	}
	if err := db.Table("knowledge_folder_closure c").Select("c.ancestor_id AS id, COUNT(k.id) AS count").Joins("LEFT JOIN knowledges k ON k.folder_id = c.descendant_id AND k.tenant_id = ? AND k.knowledge_base_id = ? AND k.deleted_at IS NULL", tenantID, kbID).Where("c.ancestor_id IN ?", ids).Group("c.ancestor_id").Scan(&totals).Error; err != nil {
		return nil, err
	}
	for _, row := range direct {
		byID[row.ID].DirectKnowledgeCount = row.Count
	}
	for _, row := range children {
		byID[row.ID].ChildFolderCount = row.Count
	}
	for _, row := range totals {
		byID[row.ID].TotalKnowledgeCount = row.Count
	}
	for _, view := range views {
		view.HasChildren = view.ChildFolderCount > 0
	}
	return views, nil
}

func (r *knowledgeFolderRepository) List(ctx context.Context, tenantID uint64, kbID, parentID, keyword string, page *types.Pagination) ([]*types.KnowledgeFolderView, int64, error) {
	q := folderScope(r.db.WithContext(ctx).Model(&types.KnowledgeFolder{}), tenantID, kbID)
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		q = q.Where("LOWER(name) LIKE ?", "%"+strings.ToLower(escapeLikeKeyword(keyword))+"%")
	} else {
		q = q.Where("parent_id = ?", parentID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var folders []*types.KnowledgeFolder
	if err := q.Order("name ASC, id ASC").Offset(page.Offset()).Limit(page.Limit()).Find(&folders).Error; err != nil {
		return nil, 0, err
	}
	views, err := r.enrich(ctx, tenantID, kbID, folders)
	if err != nil {
		return nil, 0, err
	}
	if keyword != "" {
		folderIDs := make([]string, len(folders))
		for i, folder := range folders {
			folderIDs[i] = folder.ID
		}
		paths, pathErr := r.ancestorsByDescendant(r.db.WithContext(ctx), tenantID, kbID, folderIDs)
		if pathErr != nil {
			return nil, 0, pathErr
		}
		for _, view := range views {
			view.Ancestors = paths[view.ID]
		}
	}
	return views, total, nil
}

func (r *knowledgeFolderRepository) Get(ctx context.Context, tenantID uint64, kbID, folderID string) (*types.KnowledgeFolderView, error) {
	folder, err := r.getRow(r.db.WithContext(ctx), tenantID, kbID, folderID)
	if err != nil {
		return nil, err
	}
	views, err := r.enrich(ctx, tenantID, kbID, []*types.KnowledgeFolder{folder})
	if err != nil {
		return nil, err
	}
	views[0].Ancestors, err = r.ancestors(r.db.WithContext(ctx), tenantID, kbID, folderID)
	return views[0], err
}

func (r *knowledgeFolderRepository) createTx(tx *gorm.DB, tenantID uint64, kbID, parentID, name string) (*types.KnowledgeFolder, error) {
	if parentID != "" {
		if _, err := r.getRow(tx, tenantID, kbID, parentID); err != nil {
			return nil, err
		}
	}
	var duplicate int64
	if err := folderScope(tx.Model(&types.KnowledgeFolder{}), tenantID, kbID).Where("parent_id = ? AND name = ?", parentID, name).Count(&duplicate).Error; err != nil {
		return nil, err
	}
	if duplicate > 0 {
		return nil, ErrKnowledgeFolderConflict
	}
	depth := 1
	if parentID != "" {
		var maxDepth int
		if err := tx.Model(&types.KnowledgeFolderClosure{}).Where("descendant_id = ?", parentID).Select("COALESCE(MAX(depth), 0)").Scan(&maxDepth).Error; err != nil {
			return nil, err
		}
		depth = maxDepth + 2
	}
	if depth > types.MaxKnowledgeFolderDepth {
		return nil, ErrKnowledgeFolderTooDeep
	}
	folder := &types.KnowledgeFolder{ID: uuid.NewString(), TenantID: tenantID, KnowledgeBaseID: kbID, ParentID: parentID, Name: name, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := tx.Create(folder).Error; err != nil {
		return nil, translateKnowledgeFolderWriteError(err)
	}
	links := []types.KnowledgeFolderClosure{{AncestorID: folder.ID, DescendantID: folder.ID, Depth: 0}}
	if parentID != "" {
		var ancestors []types.KnowledgeFolderClosure
		if err := tx.Where("descendant_id = ?", parentID).Find(&ancestors).Error; err != nil {
			return nil, err
		}
		for _, a := range ancestors {
			links = append(links, types.KnowledgeFolderClosure{AncestorID: a.AncestorID, DescendantID: folder.ID, Depth: a.Depth + 1})
		}
	}
	if err := tx.Create(&links).Error; err != nil {
		return nil, err
	}
	return folder, nil
}

func (r *knowledgeFolderRepository) Create(ctx context.Context, tenantID uint64, kbID, parentID, name string) (folder *types.KnowledgeFolder, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { folder, err = r.createTx(tx, tenantID, kbID, parentID, name); return err })
	return
}

func (r *knowledgeFolderRepository) Update(ctx context.Context, tenantID uint64, kbID, folderID string, name, parentID *string) (folder *types.KnowledgeFolder, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		folder, err = r.getRow(tx, tenantID, kbID, folderID)
		if err != nil {
			return err
		}
		newName, newParent := folder.Name, folder.ParentID
		if name != nil {
			newName = *name
		}
		if parentID != nil {
			newParent = *parentID
		}
		if newParent == folderID {
			return ErrKnowledgeFolderCycle
		}
		if newParent != "" {
			if _, err = r.getRow(tx, tenantID, kbID, newParent); err != nil {
				return err
			}
			var n int64
			if err = tx.Model(&types.KnowledgeFolderClosure{}).Where("ancestor_id = ? AND descendant_id = ?", folderID, newParent).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return ErrKnowledgeFolderCycle
			}
		}
		var duplicate int64
		if err = folderScope(tx.Model(&types.KnowledgeFolder{}), tenantID, kbID).Where("parent_id = ? AND name = ? AND id <> ?", newParent, newName, folderID).Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate > 0 {
			return ErrKnowledgeFolderConflict
		}
		if newParent != folder.ParentID {
			var subtreeDepth, parentDepth int
			if err = tx.Model(&types.KnowledgeFolderClosure{}).Where("ancestor_id = ?", folderID).Select("COALESCE(MAX(depth), 0)").Scan(&subtreeDepth).Error; err != nil {
				return err
			}
			if newParent != "" {
				if err = tx.Model(&types.KnowledgeFolderClosure{}).Where("descendant_id = ?", newParent).Select("COALESCE(MAX(depth), 0) + 1").Scan(&parentDepth).Error; err != nil {
					return err
				}
			}
			if parentDepth+1+subtreeDepth > types.MaxKnowledgeFolderDepth {
				return ErrKnowledgeFolderTooDeep
			}
			var descendants, ancestors []types.KnowledgeFolderClosure
			if err = tx.Where("ancestor_id = ?", folderID).Find(&descendants).Error; err != nil {
				return err
			}
			if err = tx.Where("descendant_id = ? AND ancestor_id <> ?", folderID, folderID).Find(&ancestors).Error; err != nil {
				return err
			}
			descIDs := make([]string, len(descendants))
			ancIDs := make([]string, len(ancestors))
			for i, v := range descendants {
				descIDs[i] = v.DescendantID
			}
			for i, v := range ancestors {
				ancIDs[i] = v.AncestorID
			}
			if len(ancIDs) > 0 {
				for _, descendantBatch := range knowledgeFolderMoveBatches(descIDs) {
					if err = tx.Where("ancestor_id IN ? AND descendant_id IN ?", ancIDs, descendantBatch).Delete(&types.KnowledgeFolderClosure{}).Error; err != nil {
						return err
					}
				}
			}
			if newParent != "" {
				var newAncestors []types.KnowledgeFolderClosure
				if err = tx.Where("descendant_id = ?", newParent).Find(&newAncestors).Error; err != nil {
					return err
				}
				for _, descendantBatch := range closureBatches(descendants) {
					links := make([]types.KnowledgeFolderClosure, 0, len(newAncestors)*len(descendantBatch))
					for _, a := range newAncestors {
						for _, d := range descendantBatch {
							links = append(links, types.KnowledgeFolderClosure{AncestorID: a.AncestorID, DescendantID: d.DescendantID, Depth: a.Depth + 1 + d.Depth})
						}
					}
					if len(links) > 0 {
						if err = tx.CreateInBatches(&links, maxKnowledgeFolderMoveBatchSize).Error; err != nil {
							return err
						}
					}
				}
			}
		}
		folder.Name, folder.ParentID, folder.UpdatedAt = newName, newParent, time.Now()
		return translateKnowledgeFolderWriteError(
			tx.Model(folder).Select("name", "parent_id", "updated_at").Updates(folder).Error,
		)
	})
	return
}

func (r *knowledgeFolderRepository) Delete(ctx context.Context, tenantID uint64, kbID, folderID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := r.getRow(tx, tenantID, kbID, folderID); err != nil {
			return err
		}
		var files, children int64
		if err := tx.Model(&types.Knowledge{}).Where("tenant_id = ? AND knowledge_base_id = ? AND folder_id = ?", tenantID, kbID, folderID).Count(&files).Error; err != nil {
			return err
		}
		if err := folderScope(tx.Model(&types.KnowledgeFolder{}), tenantID, kbID).Where("parent_id = ?", folderID).Count(&children).Error; err != nil {
			return err
		}
		if files+children > 0 {
			return ErrKnowledgeFolderNotEmpty
		}
		if err := tx.Where("ancestor_id = ? OR descendant_id = ?", folderID, folderID).Delete(&types.KnowledgeFolderClosure{}).Error; err != nil {
			return err
		}
		return folderScope(tx, tenantID, kbID).Where("id = ?", folderID).Delete(&types.KnowledgeFolder{}).Error
	})
}

func (r *knowledgeFolderRepository) EnsurePaths(ctx context.Context, tenantID uint64, kbID, parentID string, paths []types.EnsureFolderPath) (results []types.EnsureFolderPathResult, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if parentID != "" {
			if _, e := r.getRow(tx, tenantID, kbID, parentID); e != nil {
				return e
			}
		}
		cache := map[string]string{}
		for _, path := range paths {
			current := parentID
			for _, segment := range path.Segments {
				key := current + "\x00" + segment
				if id := cache[key]; id != "" {
					current = id
					continue
				}
				var existing types.KnowledgeFolder
				e := folderScope(tx.Model(&types.KnowledgeFolder{}), tenantID, kbID).Where("parent_id = ? AND name = ?", current, segment).Take(&existing).Error
				if e == nil {
					current = existing.ID
				} else if errors.Is(e, gorm.ErrRecordNotFound) {
					created, e := r.createTx(tx, tenantID, kbID, current, segment)
					if e != nil {
						return e
					}
					current = created.ID
				} else {
					return e
				}
				cache[key] = current
			}
			results = append(results, types.EnsureFolderPathResult{ClientKey: path.ClientKey, FolderID: current})
		}
		return nil
	})
	return
}

func (r *knowledgeFolderRepository) MoveKnowledge(ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string, folderID string) error {
	unique := make([]string, 0, len(knowledgeIDs))
	seen := map[string]struct{}{}
	for _, id := range knowledgeIDs {
		if id != "" {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				unique = append(unique, id)
			}
		}
	}
	if len(unique) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if folderID != "" {
			if _, err := r.getRow(tx, tenantID, kbID, folderID); err != nil {
				return err
			}
		}
		var found int64
		for _, batch := range knowledgeFolderMoveBatches(unique) {
			var batchCount int64
			if err := tx.Model(&types.Knowledge{}).
				Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, batch).
				Count(&batchCount).Error; err != nil {
				return err
			}
			found += batchCount
		}
		if found != int64(len(unique)) {
			return fmt.Errorf("%w: requested %d knowledge rows, found %d", ErrKnowledgeFolderScope, len(unique), found)
		}
		for _, batch := range knowledgeFolderMoveBatches(unique) {
			if err := tx.Model(&types.Knowledge{}).
				Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ?", tenantID, kbID, batch).
				Update("folder_id", folderID).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func knowledgeFolderMoveBatches(ids []string) [][]string {
	if len(ids) == 0 {
		return nil
	}
	batches := make([][]string, 0, (len(ids)+maxKnowledgeFolderMoveBatchSize-1)/maxKnowledgeFolderMoveBatchSize)
	for start := 0; start < len(ids); start += maxKnowledgeFolderMoveBatchSize {
		end := min(start+maxKnowledgeFolderMoveBatchSize, len(ids))
		batches = append(batches, ids[start:end])
	}
	return batches
}

func closureBatches(links []types.KnowledgeFolderClosure) [][]types.KnowledgeFolderClosure {
	if len(links) == 0 {
		return nil
	}
	batches := make([][]types.KnowledgeFolderClosure, 0, (len(links)+maxKnowledgeFolderMoveBatchSize-1)/maxKnowledgeFolderMoveBatchSize)
	for start := 0; start < len(links); start += maxKnowledgeFolderMoveBatchSize {
		end := min(start+maxKnowledgeFolderMoveBatchSize, len(links))
		batches = append(batches, links[start:end])
	}
	return batches
}

func (r *knowledgeFolderRepository) ListKnowledgeIDsRecursive(ctx context.Context, tenantID uint64, kbID string, folderIDs []string) ([]string, error) {
	if len(folderIDs) == 0 {
		return nil, nil
	}
	var ids []string
	err := r.db.WithContext(ctx).Model(&types.Knowledge{}).Distinct("knowledges.id").Joins("JOIN knowledge_folder_closure c ON c.descendant_id = knowledges.folder_id").Joins("JOIN knowledge_folders f ON f.id = c.ancestor_id AND f.tenant_id = knowledges.tenant_id AND f.knowledge_base_id = knowledges.knowledge_base_id").Where("knowledges.tenant_id = ? AND knowledges.knowledge_base_id = ? AND c.ancestor_id IN ?", tenantID, kbID, folderIDs).Pluck("knowledges.id", &ids).Error
	return ids, err
}
