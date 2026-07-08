package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const maxKnowledgeFolderDepth = 10

var (
	ErrKnowledgeFolderNameRequired = errors.New("folder name is required")
	ErrKnowledgeFolderInvalidName  = errors.New("folder name cannot contain path separators")
	ErrKnowledgeFolderExists       = repository.ErrKnowledgeFolderExists
	ErrKnowledgeFolderNotFound     = repository.ErrKnowledgeFolderNotFound
	ErrKnowledgeFolderNotEmpty     = repository.ErrKnowledgeFolderNotEmpty
	ErrKnowledgeFolderTooDeep      = errors.New("folder depth exceeds limit")
)

type knowledgeFolderService struct {
	folderRepo interfaces.KnowledgeFolderRepository
	kgRepo     interfaces.KnowledgeRepository
}

// NewKnowledgeFolderService creates a document folder service.
func NewKnowledgeFolderService(
	folderRepo interfaces.KnowledgeFolderRepository,
	kgRepo interfaces.KnowledgeRepository,
) interfaces.KnowledgeFolderService {
	return &knowledgeFolderService{
		folderRepo: folderRepo,
		kgRepo:     kgRepo,
	}
}

func (s *knowledgeFolderService) ListFolders(ctx context.Context, kbID string, parentID string) ([]*types.KnowledgeFolder, error) {
	tenantID, err := tenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	parentID = normalizeKnowledgeFolderID(parentID)
	if parentID != "" {
		if _, err := s.folderRepo.GetByID(ctx, tenantID, kbID, parentID); err != nil {
			return nil, err
		}
	}
	return s.folderRepo.ListByParent(ctx, tenantID, kbID, parentID)
}

func (s *knowledgeFolderService) CreateFolder(ctx context.Context, kbID string, parentID string, name string) (*types.KnowledgeFolder, error) {
	tenantID, err := tenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	parentID = normalizeKnowledgeFolderID(parentID)
	name, err = normalizeKnowledgeFolderName(name)
	if err != nil {
		return nil, err
	}

	parentPath := ""
	depth := 0
	if parentID != "" {
		parent, err := s.folderRepo.GetByID(ctx, tenantID, kbID, parentID)
		if err != nil {
			return nil, err
		}
		parentPath = parent.Path
		depth = parent.Depth + 1
	}
	if depth >= maxKnowledgeFolderDepth {
		return nil, ErrKnowledgeFolderTooDeep
	}
	if err := s.ensureFolderNameAvailable(ctx, tenantID, kbID, parentID, name, ""); err != nil {
		return nil, err
	}

	folder := &types.KnowledgeFolder{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		ParentID:        parentID,
		Name:            name,
		Path:            joinKnowledgeFolderPath(parentPath, name),
		Depth:           depth,
	}
	if err := s.folderRepo.Create(ctx, folder); err != nil {
		return nil, err
	}
	return folder, nil
}

func (s *knowledgeFolderService) RenameFolder(ctx context.Context, kbID string, folderID string, name string) (*types.KnowledgeFolder, error) {
	tenantID, err := tenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	folderID = normalizeKnowledgeFolderID(folderID)
	if folderID == "" {
		return nil, ErrKnowledgeFolderNotFound
	}
	name, err = normalizeKnowledgeFolderName(name)
	if err != nil {
		return nil, err
	}

	folder, err := s.folderRepo.GetByID(ctx, tenantID, kbID, folderID)
	if err != nil {
		return nil, err
	}
	parentPath := ""
	if folder.ParentID != "" {
		parent, err := s.folderRepo.GetByID(ctx, tenantID, kbID, folder.ParentID)
		if err != nil {
			return nil, err
		}
		parentPath = parent.Path
	}
	if err := s.ensureFolderNameAvailable(ctx, tenantID, kbID, folder.ParentID, name, folder.ID); err != nil {
		return nil, err
	}
	oldPath := folder.Path
	folder.Name = name
	folder.Path = joinKnowledgeFolderPath(parentPath, name)
	if err := s.folderRepo.UpdateWithDescendantPaths(ctx, folder, oldPath); err != nil {
		return nil, err
	}
	return folder, nil
}

func (s *knowledgeFolderService) DeleteEmptyFolder(ctx context.Context, kbID string, folderID string) error {
	tenantID, err := tenantIDFromContext(ctx)
	if err != nil {
		return err
	}
	folderID = normalizeKnowledgeFolderID(folderID)
	if folderID == "" {
		return ErrKnowledgeFolderNotFound
	}
	if _, err := s.folderRepo.GetByID(ctx, tenantID, kbID, folderID); err != nil {
		return err
	}
	return s.folderRepo.DeleteEmpty(ctx, tenantID, kbID, folderID)
}

func (s *knowledgeFolderService) MoveKnowledgeToFolder(ctx context.Context, knowledgeID string, folderID string) (*types.Knowledge, error) {
	tenantID, err := tenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	knowledge, err := s.kgRepo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, err
	}
	folderID = normalizeKnowledgeFolderID(folderID)
	if folderID != "" {
		if _, err := s.folderRepo.GetByID(ctx, tenantID, knowledge.KnowledgeBaseID, folderID); err != nil {
			return nil, err
		}
	}
	if err := s.kgRepo.UpdateKnowledgeColumn(ctx, knowledge.ID, "folder_id", folderID); err != nil {
		return nil, err
	}
	knowledge.FolderID = folderID
	return knowledge, nil
}

func (s *knowledgeFolderService) ensureFolderNameAvailable(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parentID string,
	name string,
	currentID string,
) error {
	existing, err := s.folderRepo.GetByParentAndName(ctx, tenantID, kbID, parentID, name)
	if errors.Is(err, ErrKnowledgeFolderNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing.ID != currentID {
		return ErrKnowledgeFolderExists
	}
	return nil
}

func tenantIDFromContext(ctx context.Context) (uint64, error) {
	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok || tenantID == 0 {
		return 0, werrors.NewUnauthorizedError("Tenant ID not found in context")
	}
	return tenantID, nil
}

func normalizeKnowledgeFolderID(folderID string) string {
	folderID = strings.TrimSpace(folderID)
	if folderID == types.KnowledgeFolderRootID {
		return ""
	}
	return folderID
}

func normalizeKnowledgeFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrKnowledgeFolderNameRequired
	}
	if strings.ContainsAny(name, `/\`) {
		return "", ErrKnowledgeFolderInvalidName
	}
	return name, nil
}

func joinKnowledgeFolderPath(parentPath string, name string) string {
	if parentPath == "" {
		return name
	}
	return parentPath + "/" + name
}
