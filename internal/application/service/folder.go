package service

import (
	"context"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type knowledgeFolderService struct {
	repo          interfaces.KnowledgeFolderRepository
	knowledgeRepo interfaces.KnowledgeRepository
}

func NewKnowledgeFolderService(repo interfaces.KnowledgeFolderRepository, knowledgeRepo interfaces.KnowledgeRepository) interfaces.KnowledgeFolderService {
	return &knowledgeFolderService{repo: repo, knowledgeRepo: knowledgeRepo}
}

func (s *knowledgeFolderService) CreateFolder(ctx context.Context, kbID string, parentID string, name string) (*types.KnowledgeFolder, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	if name == "" {
		return nil, werrors.NewBadRequestError("Folder name cannot be empty")
	}
	existing, err := s.repo.GetByName(ctx, tenantID, kbID, parentID, name)
	if err == nil && existing != nil {
		return nil, werrors.NewBadRequestError("A folder with this name already exists")
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	depth := 0
	parentPath := ""
	if parentID != "" {
		parent, e := s.repo.GetByID(ctx, tenantID, parentID)
		if e != nil {
			return nil, werrors.NewNotFoundError("Parent folder not found")
		}
		depth = parent.Depth + 1
		parentPath = parent.Path
	}
	folder := &types.KnowledgeFolder{ID: uuid.New().String(), TenantID: tenantID, KnowledgeBaseID: kbID, ParentID: parentID, Name: name, Depth: depth, SortOrder: 0}
	folder.Path = types.BuildFolderPath(parentPath, folder.ID)
	if err := s.repo.Create(ctx, folder); err != nil {
		return nil, err
	}
	return folder, nil
}

func (s *knowledgeFolderService) UpdateFolder(ctx context.Context, folderID string, name string) (*types.KnowledgeFolder, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	folder, err := s.repo.GetByID(ctx, tenantID, folderID)
	if err != nil {
		return nil, werrors.NewNotFoundError("Folder not found")
	}
	existing, _ := s.repo.GetByName(ctx, tenantID, folder.KnowledgeBaseID, folder.ParentID, name)
	if existing != nil && existing.ID != folderID {
		return nil, werrors.NewBadRequestError("A folder with this name already exists")
	}
	folder.Name = name
	if err := s.repo.Update(ctx, folder); err != nil {
		return nil, err
	}
	return folder, nil
}

func (s *knowledgeFolderService) DeleteFolder(ctx context.Context, folderID string) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	folder, err := s.repo.GetByID(ctx, tenantID, folderID)
	if err != nil {
		return werrors.NewNotFoundError("Folder not found")
	}
	cnt, _ := s.repo.CountChildFolders(ctx, tenantID, folder.KnowledgeBaseID, folderID)
	if cnt > 0 {
		return werrors.NewBadRequestError("Cannot delete folder with sub-folders")
	}
	kcnt, _ := s.repo.CountKnowledgeInFolder(ctx, tenantID, folder.KnowledgeBaseID, folderID)
	if kcnt > 0 {
		return werrors.NewBadRequestError("Cannot delete folder with documents")
	}
	return s.repo.Delete(ctx, tenantID, folderID)
}

func (s *knowledgeFolderService) ListFolders(ctx context.Context, kbID string) ([]*types.KnowledgeFolderWithStats, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	folders, err := s.repo.ListByKB(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	return s.enrich(ctx, tenantID, kbID, folders)
}

func (s *knowledgeFolderService) ListChildFolders(ctx context.Context, kbID string, parentID string) ([]*types.KnowledgeFolderWithStats, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	folders, err := s.repo.ListByParent(ctx, tenantID, kbID, parentID)
	if err != nil {
		return nil, err
	}
	return s.enrich(ctx, tenantID, kbID, folders)
}

func (s *knowledgeFolderService) GetFolderTree(ctx context.Context, kbID string) ([]*types.FolderTreeNode, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	folders, err := s.repo.ListByKB(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	enriched, err := s.enrich(ctx, tenantID, kbID, folders)
	if err != nil {
		return nil, err
	}
	return buildTree(enriched), nil
}

func (s *knowledgeFolderService) MoveKnowledgeToFolder(ctx context.Context, knowledgeID string, folderID string) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	knowledge, err := s.knowledgeRepo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return werrors.NewNotFoundError("Knowledge not found")
	}
	if folderID != "" {
		folder, err := s.repo.GetByID(ctx, tenantID, folderID)
		if err != nil {
			return werrors.NewNotFoundError("Folder not found")
		}
		if folder.KnowledgeBaseID != knowledge.KnowledgeBaseID {
			return werrors.NewBadRequestError("Folder not in same KB")
		}
	}
	return s.knowledgeRepo.UpdateKnowledgeColumn(ctx, knowledgeID, "folder_id", folderID)
}

func (s *knowledgeFolderService) GetFolderDescendantIDs(ctx context.Context, kbID string, folderID string) ([]string, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	return s.repo.GetDescendantIDs(ctx, tenantID, kbID, folderID)
}

func (s *knowledgeFolderService) enrich(ctx context.Context, tenantID uint64, kbID string, folders []*types.KnowledgeFolder) ([]*types.KnowledgeFolderWithStats, error) {
	result := make([]*types.KnowledgeFolderWithStats, 0, len(folders))
	for _, f := range folders {
		kc, _ := s.repo.CountKnowledgeInFolder(ctx, tenantID, kbID, f.ID)
		cc, _ := s.repo.CountChildFolders(ctx, tenantID, kbID, f.ID)
		result = append(result, &types.KnowledgeFolderWithStats{KnowledgeFolder: *f, KnowledgeCount: kc, ChildCount: cc})
	}
	return result, nil
}

func buildTree(folders []*types.KnowledgeFolderWithStats) []*types.FolderTreeNode {
	nodeMap := make(map[string]*types.FolderTreeNode)
	var roots []*types.FolderTreeNode
	for _, f := range folders {
		node := &types.FolderTreeNode{ID: f.ID, Name: f.Name, ParentID: f.ParentID, Path: f.Path, Depth: f.Depth, SortOrder: f.SortOrder, KnowledgeCount: f.KnowledgeCount, ChildCount: f.ChildCount, Children: make([]*types.FolderTreeNode, 0), CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt}
		nodeMap[f.ID] = node
	}
	for _, f := range folders {
		node := nodeMap[f.ID]
		if f.ParentID == "" {
			roots = append(roots, node)
		} else if p, ok := nodeMap[f.ParentID]; ok {
			p.Children = append(p.Children, node)
		} else {
			roots = append(roots, node)
		}
	}
	return roots
}
