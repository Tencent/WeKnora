package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// knowledgeFolderService implements interfaces.KnowledgeFolderService.
type knowledgeFolderService struct {
	repo    interfaces.KnowledgeFolderRepository
	kgRepo  interfaces.KnowledgeRepository
	db      *gorm.DB
}

// NewKnowledgeFolderService creates a new knowledge folder service.
func NewKnowledgeFolderService(
	repo interfaces.KnowledgeFolderRepository,
	kgRepo interfaces.KnowledgeRepository,
	db *gorm.DB,
) interfaces.KnowledgeFolderService {
	return &knowledgeFolderService{
		repo:   repo,
		kgRepo: kgRepo,
		db:     db,
	}
}

// CreateFolder creates a new folder under the specified parent.
func (s *knowledgeFolderService) CreateFolder(
	ctx context.Context,
	kbID string,
	req *types.CreateFolderRequest,
) (*types.KnowledgeFolder, error) {
	tenantID := types.MustTenantIDFromContext(ctx)

	// Validate name
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("folder name cannot be empty")
	}

	// Check name uniqueness under the same parent
	exists, err := s.repo.CheckNameExists(ctx, tenantID, kbID, req.ParentFolderID, name, "")
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, repository.ErrFolderNameExists
	}

	// Calculate path and depth
	var path string
	var depth int = 1
	if req.ParentFolderID != nil && *req.ParentFolderID != "" {
		parent, err := s.repo.GetByID(ctx, tenantID, *req.ParentFolderID)
		if err != nil {
			return nil, err
		}
		if parent.Depth >= types.MaxFolderDepth {
			return nil, repository.ErrMaxDepthExceeded
		}
		depth = parent.Depth + 1
		path = parent.Path
	} else {
		path = "/"
	}

	folder := &types.KnowledgeFolder{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		Name:            name,
		ParentFolderID:  req.ParentFolderID,
		Path:            path,
		Depth:           depth,
		Color:           req.Color,
		Description:     req.Description,
	}

	if err := s.repo.Create(ctx, folder); err != nil {
		return nil, err
	}

	// Update path to include the folder's own ID
	folder.Path = path + folder.ID + "/"
	if err := s.repo.Update(ctx, folder); err != nil {
		return nil, err
	}

	logger.Infof(ctx, "[Folder] Created folder %s (id=%s, depth=%d) in KB %s",
		folder.Name, folder.ID, folder.Depth, kbID)
	return folder, nil
}

// GetFolder retrieves a folder by its ID.
func (s *knowledgeFolderService) GetFolder(ctx context.Context, id string) (*types.KnowledgeFolder, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	folder, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	// Populate knowledge count
	count, err := s.repo.CountKnowledge(ctx, tenantID, folder.ID)
	if err != nil {
		logger.Warnf(ctx, "[Folder] Failed to count knowledge for folder %s: %v", id, err)
	} else {
		folder.KnowledgeCount = count
	}
	return folder, nil
}

// ListByParent lists all folders directly under the given parent.
func (s *knowledgeFolderService) ListByParent(
	ctx context.Context,
	kbID string,
	parentID *string,
) ([]*types.KnowledgeFolder, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	folders, err := s.repo.ListByParent(ctx, tenantID, kbID, parentID)
	if err != nil {
		return nil, err
	}
	// Bulk load counts for these folders
	counts, err := s.repo.CountKnowledgeByKB(ctx, tenantID, kbID)
	if err != nil {
		logger.Warnf(ctx, "[Folder] Failed to bulk count knowledge for KB %s: %v", kbID, err)
		return folders, nil
	}
	for _, f := range folders {
		f.KnowledgeCount = counts[f.ID]
	}
	return folders, nil
}

// GetTree returns the full folder tree for a knowledge base.
func (s *knowledgeFolderService) GetTree(
	ctx context.Context,
	kbID string,
) ([]*types.KnowledgeFolder, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	allFolders, err := s.repo.GetAllInKB(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	// Bulk load knowledge counts for all folders
	counts, err := s.repo.CountKnowledgeByKB(ctx, tenantID, kbID)
	if err != nil {
		logger.Warnf(ctx, "[Folder] Failed to bulk count knowledge for KB %s: %v", kbID, err)
		counts = make(map[string]int64)
	}
	// Populate counts on each folder
	for _, f := range allFolders {
		f.KnowledgeCount = counts[f.ID]
	}
	// Build tree from flat list and accumulate child counts
	roots := buildFolderTree(allFolders)
	for _, root := range roots {
		populateChildCounts(root)
	}
	return roots, nil
}

// populateChildCounts recursively sums knowledge counts for a tree node.
func populateChildCounts(folder *types.KnowledgeFolder) int64 {
	total := folder.KnowledgeCount
	for _, child := range folder.Children {
		total += populateChildCounts(child)
	}
	folder.KnowledgeCount = total
	return total
}

// buildFolderTree converts a flat folder list into a tree structure.
func buildFolderTree(folders []*types.KnowledgeFolder) []*types.KnowledgeFolder {
	folderMap := make(map[string]*types.KnowledgeFolder, len(folders))
	for _, f := range folders {
		f.Children = make([]*types.KnowledgeFolder, 0)
		folderMap[f.ID] = f
	}

	var roots []*types.KnowledgeFolder
	for _, f := range folders {
		if f.ParentFolderID == nil || *f.ParentFolderID == "" {
			roots = append(roots, f)
		} else if parent, ok := folderMap[*f.ParentFolderID]; ok {
			parent.Children = append(parent.Children, f)
		} else {
			// Parent not found (might be deleted), treat as root
			roots = append(roots, f)
		}
	}
	return roots
}

// UpdateFolder updates folder properties.
func (s *knowledgeFolderService) UpdateFolder(
	ctx context.Context,
	id string,
	req *types.UpdateFolderRequest,
) (*types.KnowledgeFolder, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	folder, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	oldPath := folder.Path

	if req.Name != nil && *req.Name != "" {
		name := strings.TrimSpace(*req.Name)
		if name != folder.Name {
			// Check uniqueness
			exists, err := s.repo.CheckNameExists(ctx, tenantID, folder.KnowledgeBaseID,
				folder.ParentFolderID, name, folder.ID)
			if err != nil {
				return nil, err
			}
			if exists {
				return nil, repository.ErrFolderNameExists
			}
			folder.Name = name
		}
	}
	if req.Color != nil {
		folder.Color = *req.Color
	}
	if req.Description != nil {
		folder.Description = *req.Description
	}
	if req.SortOrder != nil {
		folder.SortOrder = *req.SortOrder
	}

	// If the name changed, we need to reconstruct the path (the last segment changes)
	// but since we use IDs for paths, renaming doesn't change paths. So just update.
	if err := s.repo.Update(ctx, folder); err != nil {
		return nil, err
	}

	_ = oldPath // no path change on rename since we use IDs in paths
	return folder, nil
}

// DeleteFolder deletes a folder. When force is true, cascade-deletes all contents.
func (s *knowledgeFolderService) DeleteFolder(ctx context.Context, id string, force bool) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	folder, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}

	// Check if folder has subfolders
	children, err := s.repo.ListByParent(ctx, tenantID, folder.KnowledgeBaseID, &id)
	if err != nil {
		return err
	}

	// Check if folder has knowledge entries
	knowledgeCount, err := s.repo.CountKnowledge(ctx, tenantID, id)
	if err != nil {
		return err
	}

	if !force && (len(children) > 0 || knowledgeCount > 0) {
		return repository.ErrFolderNotEmpty
	}

	// Use transaction for force deletion
	if force && (len(children) > 0 || knowledgeCount > 0) {
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Get all descendant folders
			descendants, err := s.repo.GetDescendants(ctx, id)
			if err != nil {
				return err
			}

			// Collect all folder IDs (including self)
			allFolderIDs := make([]string, 0, len(descendants)+1)
			for _, d := range descendants {
				allFolderIDs = append(allFolderIDs, d.ID)
			}
			allFolderIDs = append(allFolderIDs, id)

			// Move knowledge entries in all these folders to root
			if err := tx.Model(&types.Knowledge{}).
				Where("folder_id IN ?", allFolderIDs).
				Update("folder_id", nil).Error; err != nil {
				return err
			}

			// Delete descendant folders first (reverse depth order to avoid FK issues)
			for i := len(descendants) - 1; i >= 0; i-- {
				if err := tx.Where("id = ?", descendants[i].ID).Delete(&types.KnowledgeFolder{}).Error; err != nil {
					return err
				}
			}

			// Delete the folder itself
			if err := tx.Where("id = ?", id).Delete(&types.KnowledgeFolder{}).Error; err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
	} else {
		if err := s.repo.Delete(ctx, tenantID, id); err != nil {
			return err
		}
	}

	logger.Infof(ctx, "[Folder] Deleted folder %s (id=%s, force=%v)", folder.Name, folder.ID, force)
	return nil
}

// MoveFolder moves a folder to a new parent.
func (s *knowledgeFolderService) MoveFolder(
	ctx context.Context,
	id string,
	req *types.MoveFolderRequest,
) (*types.KnowledgeFolder, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	folder, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	// Prevent moving to self
	if req.TargetParentFolderID != nil && *req.TargetParentFolderID == id {
		return nil, repository.ErrCircularReference
	}

	// Calculate new path and depth
	var newParentPath string
	var newDepth int = 1
	if req.TargetParentFolderID != nil && *req.TargetParentFolderID != "" {
		targetParent, err := s.repo.GetByID(ctx, tenantID, *req.TargetParentFolderID)
		if err != nil {
			return nil, err
		}
		// Prevent circular reference: target must not be a descendant of source
		descendants, err := s.repo.GetDescendants(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, d := range descendants {
			if d.ID == *req.TargetParentFolderID {
				return nil, repository.ErrCircularReference
			}
		}
		if targetParent.Depth >= types.MaxFolderDepth {
			return nil, repository.ErrMaxDepthExceeded
		}
		newParentPath = targetParent.Path
		newDepth = targetParent.Depth + 1
	} else {
		newParentPath = "/"
		newDepth = 1
	}

	// Check name uniqueness in target parent
	exists, err := s.repo.CheckNameExists(ctx, tenantID, folder.KnowledgeBaseID,
		req.TargetParentFolderID, folder.Name, id)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, repository.ErrFolderNameExists
	}

	oldPath := folder.Path
	oldDepth := folder.Depth
	newPath := newParentPath + folder.ID + "/"
	depthDelta := newDepth - oldDepth

	// Execute in transaction: update self + batch update descendants
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.repo.Move(ctx, id, req.TargetParentFolderID, newPath, newDepth); err != nil {
			return err
		}
		if err := s.repo.BatchUpdateDescendantPaths(ctx, oldPath, newPath, depthDelta); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Reload the updated folder
	updated, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	logger.Infof(ctx, "[Folder] Moved folder %s (id=%s) from %s to %s",
		folder.Name, folder.ID, oldPath, newPath)
	return updated, nil
}

// GetBreadcrumb returns the path of folders from root to the given folder.
func (s *knowledgeFolderService) GetBreadcrumb(
	ctx context.Context,
	folderID string,
) ([]*types.KnowledgeFolder, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	folder, err := s.repo.GetByID(ctx, tenantID, folderID)
	if err != nil {
		return nil, err
	}

	// Parse the path to get ancestor folder IDs
	path := folder.Path
	if path == "/" || path == "/"+folderID+"/" {
		return []*types.KnowledgeFolder{folder}, nil
	}

	// Split path and fetch each ancestor (skip consecutive duplicate segments
	// that may exist from a previous path construction bug).
	// Path format: /ancestor1_id/ancestor2_id/current_id/
	segments := strings.Split(strings.Trim(path, "/"), "/")
	breadcrumb := make([]*types.KnowledgeFolder, 0, len(segments))
	var lastSegID string
	for _, seg := range segments {
		if seg == "" || seg == folderID || seg == lastSegID {
			continue
		}
		lastSegID = seg
		ancestor, err := s.repo.GetByID(ctx, tenantID, seg)
		if err != nil {
			logger.Warnf(ctx, "[Folder] Failed to fetch breadcrumb ancestor %s: %v", seg, err)
			continue
		}
		breadcrumb = append(breadcrumb, ancestor)
	}
	breadcrumb = append(breadcrumb, folder)
	return breadcrumb, nil
}
