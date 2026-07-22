package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

var (
	ErrKnowledgeFolderInvalidArgument = errors.New("invalid knowledge folder argument")
	ErrKnowledgeFolderInvalidName     = errors.New("invalid knowledge folder name")
	ErrKnowledgeFolderNotFound        = errors.New("knowledge folder not found")
	ErrKnowledgeFolderConflict        = errors.New("knowledge folder conflict")
	ErrKnowledgeFolderNotEmpty        = errors.New("knowledge folder is not empty")
	ErrKnowledgeFolderCycle           = errors.New("knowledge folder cycle")
	ErrKnowledgeFolderDepthExceeded   = errors.New("knowledge folder depth exceeded")
	ErrKnowledgeFolderDataIntegrity   = errors.New("knowledge folder data integrity error")
	ErrKnowledgeFolderUnsupportedDB   = errors.New("unsupported knowledge folder database")
	ErrKnowledgeFolderInternal        = errors.New("knowledge folder internal error")
)

type knowledgeFolderService struct {
	repo interfaces.KnowledgeFolderRepository
}

var _ interfaces.KnowledgeFolderService = (*knowledgeFolderService)(nil)

// NewKnowledgeFolderService creates a knowledge folder service.
func NewKnowledgeFolderService(
	repo interfaces.KnowledgeFolderRepository,
) interfaces.KnowledgeFolderService {
	return &knowledgeFolderService{repo: repo}
}

func normalizeKnowledgeFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) {
		return "", ErrKnowledgeFolderInvalidName
	}
	if utf8.RuneCountInString(name) > types.KnowledgeFolderMaxNameRunes {
		return "", ErrKnowledgeFolderInvalidName
	}
	for _, char := range name {
		if char == '/' || char == '\\' || char == '\x00' || unicode.IsControl(char) {
			return "", ErrKnowledgeFolderInvalidName
		}
	}
	return name, nil
}

func normalizeKnowledgeFolderID(raw string, fieldName string, allowRoot bool) (string, error) {
	id := strings.TrimSpace(raw)
	if id == types.KnowledgeFolderRootID {
		if allowRoot {
			return types.KnowledgeFolderRootID, nil
		}
		return "", fmt.Errorf(
			"%w: %s must be a canonical UUID",
			ErrKnowledgeFolderInvalidArgument,
			fieldName,
		)
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", fmt.Errorf(
			"%w: %s must be a canonical UUID",
			ErrKnowledgeFolderInvalidArgument,
			fieldName,
		)
	}
	return id, nil
}

func (s *knowledgeFolderService) CreateFolder(
	ctx context.Context,
	kbID string,
	req *types.KnowledgeFolderCreateRequest,
) (*types.KnowledgeFolder, error) {
	tenantID, kbID, err := knowledgeFolderScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, ErrKnowledgeFolderInvalidArgument
	}

	name, err := normalizeKnowledgeFolderName(req.Name)
	if err != nil {
		return nil, err
	}
	parentID, err := normalizeKnowledgeFolderID(req.ParentID, "parent_id", true)
	if err != nil {
		return nil, err
	}
	sortOrder := req.SortOrder
	folderID := uuid.NewString()

	var created *types.KnowledgeFolder
	err = s.repo.RunTreeWriteTransaction(ctx, tenantID, kbID, func(
		txRepo interfaces.KnowledgeFolderTreeRepository,
	) error {
		parentPath := ""
		parentDepth := 0
		if parentID != types.KnowledgeFolderRootID {
			parent, getErr := txRepo.GetByID(ctx, tenantID, kbID, parentID)
			if getErr != nil {
				return mapKnowledgeFolderError(getErr)
			}
			parentChain, chainErr := loadValidatedKnowledgeFolderChain(
				ctx,
				txRepo,
				tenantID,
				kbID,
				parent,
			)
			if chainErr != nil {
				return chainErr
			}
			parent = parentChain[len(parentChain)-1]
			parentPath = parent.Path
			parentDepth = parent.Depth
		}

		depth := parentDepth + 1
		if depth > types.KnowledgeFolderMaxDepth {
			return ErrKnowledgeFolderDepthExceeded
		}
		if conflictErr := ensureKnowledgeFolderNameAvailable(
			ctx,
			txRepo,
			tenantID,
			kbID,
			parentID,
			name,
			"",
		); conflictErr != nil {
			return conflictErr
		}

		folder := &types.KnowledgeFolder{
			ID:              folderID,
			TenantID:        tenantID,
			KnowledgeBaseID: kbID,
			ParentID:        parentID,
			Name:            name,
			Path:            knowledgeFolderChildPath(parentPath, folderID),
			Depth:           depth,
			SortOrder:       sortOrder,
		}
		if createErr := txRepo.Create(ctx, folder); createErr != nil {
			if repository.IsKnowledgeFolderUniqueViolation(createErr) {
				return ErrKnowledgeFolderConflict
			}
			return createErr
		}
		folderCopy := *folder
		created = &folderCopy
		return nil
	})
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	if created == nil {
		return nil, ErrKnowledgeFolderInternal
	}
	return created, nil
}

func (s *knowledgeFolderService) GetFolder(
	ctx context.Context,
	kbID string,
	folderID string,
) (*types.KnowledgeFolderWithStats, error) {
	tenantID, kbID, err := knowledgeFolderScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	folderID, err = normalizeKnowledgeFolderID(folderID, "folder_id", false)
	if err != nil {
		return nil, err
	}

	folder, err := s.repo.GetByID(ctx, tenantID, kbID, folderID)
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	chain, err := loadValidatedKnowledgeFolderChain(
		ctx,
		s.repo,
		tenantID,
		kbID,
		folder,
	)
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	folder = chain[len(chain)-1]
	folders, err := s.enrichKnowledgeFolders(ctx, tenantID, kbID, []*types.KnowledgeFolder{folder})
	if err != nil {
		return nil, err
	}
	return folders[0], nil
}

func (s *knowledgeFolderService) ListFolders(
	ctx context.Context,
	kbID string,
	parentID string,
	page *types.Pagination,
) (*types.PageResult, error) {
	tenantID, kbID, err := knowledgeFolderScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	parentID, err = normalizeKnowledgeFolderID(parentID, "parent_id", true)
	if err != nil {
		return nil, err
	}
	if page == nil {
		page = &types.Pagination{}
	}
	var parent *types.KnowledgeFolder
	if parentID != types.KnowledgeFolderRootID {
		parent, err = s.repo.GetByID(ctx, tenantID, kbID, parentID)
		if err != nil {
			return nil, mapKnowledgeFolderError(err)
		}
		parentChain, chainErr := loadValidatedKnowledgeFolderChain(
			ctx,
			s.repo,
			tenantID,
			kbID,
			parent,
		)
		if chainErr != nil {
			return nil, mapKnowledgeFolderError(chainErr)
		}
		parent = parentChain[len(parentChain)-1]
	}

	folders, total, err := s.repo.ListByParent(ctx, tenantID, kbID, parentID, page)
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	if err = validateKnowledgeFolderDirectChildren(
		tenantID,
		kbID,
		parent,
		folders,
	); err != nil {
		return nil, err
	}
	enriched, err := s.enrichKnowledgeFolders(ctx, tenantID, kbID, folders)
	if err != nil {
		return nil, err
	}
	return types.NewPageResult(total, page, enriched), nil
}

func (s *knowledgeFolderService) UpdateFolder(
	ctx context.Context,
	kbID string,
	folderID string,
	req *types.KnowledgeFolderUpdateRequest,
) (*types.KnowledgeFolder, error) {
	tenantID, kbID, err := knowledgeFolderScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if req == nil || (req.ParentID == nil && req.Name == nil && req.SortOrder == nil) {
		return nil, ErrKnowledgeFolderInvalidArgument
	}
	folderID, err = normalizeKnowledgeFolderID(folderID, "folder_id", false)
	if err != nil {
		return nil, err
	}

	var requestedName *string
	if req.Name != nil {
		name, nameErr := normalizeKnowledgeFolderName(*req.Name)
		if nameErr != nil {
			return nil, nameErr
		}
		requestedName = &name
	}
	var requestedParentID *string
	if req.ParentID != nil {
		parentID, parentErr := normalizeKnowledgeFolderID(*req.ParentID, "parent_id", true)
		if parentErr != nil {
			return nil, parentErr
		}
		requestedParentID = &parentID
	}
	var requestedSortOrder *int
	if req.SortOrder != nil {
		sortOrder := *req.SortOrder
		requestedSortOrder = &sortOrder
	}

	var updated *types.KnowledgeFolder
	err = s.repo.RunTreeWriteTransaction(ctx, tenantID, kbID, func(
		txRepo interfaces.KnowledgeFolderTreeRepository,
	) error {
		current, getErr := txRepo.GetByID(ctx, tenantID, kbID, folderID)
		if getErr != nil {
			return mapKnowledgeFolderError(getErr)
		}
		currentChain, chainErr := loadValidatedKnowledgeFolderChain(
			ctx,
			txRepo,
			tenantID,
			kbID,
			current,
		)
		if chainErr != nil {
			return chainErr
		}
		current = currentChain[len(currentChain)-1]

		finalName := current.Name
		if requestedName != nil {
			finalName = *requestedName
		}
		finalParentID := current.ParentID
		if requestedParentID != nil {
			finalParentID = *requestedParentID
		}
		finalSortOrder := current.SortOrder
		if requestedSortOrder != nil {
			finalSortOrder = *requestedSortOrder
		}

		moving := finalParentID != current.ParentID
		newPath := current.Path
		depthDelta := 0
		var validatedSubtree []*types.KnowledgeFolder
		if moving {
			if finalParentID == current.ID {
				return ErrKnowledgeFolderCycle
			}

			parentPath := ""
			newDepth := 1
			if finalParentID != types.KnowledgeFolderRootID {
				parent, parentErr := txRepo.GetByID(ctx, tenantID, kbID, finalParentID)
				if parentErr != nil {
					return mapKnowledgeFolderError(parentErr)
				}
				parentChain, parentChainErr := loadValidatedKnowledgeFolderChain(
					ctx,
					txRepo,
					tenantID,
					kbID,
					parent,
				)
				if parentChainErr != nil {
					return parentChainErr
				}
				parent = parentChain[len(parentChain)-1]
				parentContainsCurrent := false
				for _, ancestor := range parentChain {
					if ancestor.ID == current.ID {
						parentContainsCurrent = true
						break
					}
				}
				if parentContainsCurrent {
					return ErrKnowledgeFolderCycle
				}
				parentPath = parent.Path
				newDepth = parent.Depth + 1
			}

			validatedSubtree, chainErr = loadValidatedKnowledgeFolderSubtree(
				ctx,
				txRepo,
				tenantID,
				kbID,
				current,
			)
			if chainErr != nil {
				return chainErr
			}
			subtreeMaxDepth := current.Depth
			for _, folder := range validatedSubtree {
				if folder.Depth > subtreeMaxDepth {
					subtreeMaxDepth = folder.Depth
				}
			}
			depthDelta = newDepth - current.Depth
			if newDepth < 1 ||
				subtreeMaxDepth+depthDelta > types.KnowledgeFolderMaxDepth {
				return ErrKnowledgeFolderDepthExceeded
			}
			newPath = knowledgeFolderChildPath(parentPath, current.ID)
		}

		if finalName != current.Name || moving {
			if conflictErr := ensureKnowledgeFolderNameAvailable(
				ctx,
				txRepo,
				tenantID,
				kbID,
				finalParentID,
				finalName,
				current.ID,
			); conflictErr != nil {
				return conflictErr
			}
		}

		nameChanged := finalName != current.Name
		sortChanged := finalSortOrder != current.SortOrder
		if !moving && !nameChanged && !sortChanged {
			currentCopy := *current
			updated = &currentCopy
			return nil
		}

		if moving {
			moveErr := txRepo.MoveSubtree(
				ctx,
				tenantID,
				kbID,
				interfaces.KnowledgeFolderMoveSubtreeParams{
					FolderID:            current.ID,
					ExpectedParentID:    current.ParentID,
					ExpectedPath:        current.Path,
					ExpectedDepth:       current.Depth,
					ExpectedFolderCount: int64(len(validatedSubtree)),
					NewParentID:         finalParentID,
					NewPath:             newPath,
					NewName:             finalName,
					NewSortOrder:        finalSortOrder,
					DepthDelta:          depthDelta,
				},
			)
			if moveErr != nil {
				if repository.IsKnowledgeFolderUniqueViolation(moveErr) {
					return ErrKnowledgeFolderConflict
				}
				return moveErr
			}
		} else {
			var nameUpdate *string
			if nameChanged {
				nameUpdate = &finalName
			}
			var sortUpdate *int
			if sortChanged {
				sortUpdate = &finalSortOrder
			}
			updateErr := txRepo.UpdateFolderAttributes(
				ctx,
				tenantID,
				kbID,
				current.ID,
				nameUpdate,
				sortUpdate,
			)
			if updateErr != nil {
				if repository.IsKnowledgeFolderUniqueViolation(updateErr) {
					return ErrKnowledgeFolderConflict
				}
				return updateErr
			}
		}

		fresh, freshErr := txRepo.GetByID(ctx, tenantID, kbID, current.ID)
		if freshErr != nil {
			return freshErr
		}
		freshChain, freshChainErr := loadValidatedKnowledgeFolderChain(
			ctx,
			txRepo,
			tenantID,
			kbID,
			fresh,
		)
		if freshChainErr != nil {
			return freshChainErr
		}
		freshCopy := *freshChain[len(freshChain)-1]
		updated = &freshCopy
		return nil
	})
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	if updated == nil {
		return nil, ErrKnowledgeFolderInternal
	}
	return updated, nil
}

func (s *knowledgeFolderService) DeleteFolder(
	ctx context.Context,
	kbID string,
	folderID string,
) error {
	tenantID, kbID, err := knowledgeFolderScope(ctx, kbID)
	if err != nil {
		return err
	}
	folderID, err = normalizeKnowledgeFolderID(folderID, "folder_id", false)
	if err != nil {
		return err
	}

	err = s.repo.RunTreeWriteTransaction(ctx, tenantID, kbID, func(
		txRepo interfaces.KnowledgeFolderTreeRepository,
	) error {
		current, getErr := txRepo.GetByID(ctx, tenantID, kbID, folderID)
		if getErr != nil {
			return getErr
		}
		if _, chainErr := loadValidatedKnowledgeFolderChain(
			ctx,
			txRepo,
			tenantID,
			kbID,
			current,
		); chainErr != nil {
			return chainErr
		}
		return txRepo.DeleteEmpty(ctx, tenantID, kbID, folderID)
	})
	return mapKnowledgeFolderError(err)
}

func (s *knowledgeFolderService) GetBreadcrumb(
	ctx context.Context,
	kbID string,
	folderID string,
) ([]*types.KnowledgeFolder, error) {
	tenantID, kbID, err := knowledgeFolderScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	folderID, err = normalizeKnowledgeFolderID(folderID, "folder_id", false)
	if err != nil {
		return nil, err
	}

	current, err := s.repo.GetByID(ctx, tenantID, kbID, folderID)
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	chain, err := loadValidatedKnowledgeFolderChain(
		ctx,
		s.repo,
		tenantID,
		kbID,
		current,
	)
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	return chain, nil
}

func (s *knowledgeFolderService) ListSubtreeFolderIDs(
	ctx context.Context,
	kbID string,
	folderID string,
) ([]string, error) {
	tenantID, kbID, err := knowledgeFolderScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	folderID, err = normalizeKnowledgeFolderID(folderID, "folder_id", true)
	if err != nil {
		return nil, err
	}

	var root *types.KnowledgeFolder
	if folderID != types.KnowledgeFolderRootID {
		folder, getErr := s.repo.GetByID(ctx, tenantID, kbID, folderID)
		if getErr != nil {
			return nil, mapKnowledgeFolderError(getErr)
		}
		folderChain, chainErr := loadValidatedKnowledgeFolderChain(
			ctx,
			s.repo,
			tenantID,
			kbID,
			folder,
		)
		if chainErr != nil {
			return nil, mapKnowledgeFolderError(chainErr)
		}
		root = folderChain[len(folderChain)-1]
	}

	folders, err := loadValidatedKnowledgeFolderSubtree(
		ctx,
		s.repo,
		tenantID,
		kbID,
		root,
	)
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	folderIDs := make([]string, len(folders))
	for index, folder := range folders {
		folderIDs[index] = folder.ID
	}
	return folderIDs, nil
}

func (s *knowledgeFolderService) enrichKnowledgeFolders(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folders []*types.KnowledgeFolder,
) ([]*types.KnowledgeFolderWithStats, error) {
	if len(folders) == 0 {
		return []*types.KnowledgeFolderWithStats{}, nil
	}

	folderIDs := make([]string, 0, len(folders))
	seen := make(map[string]struct{}, len(folders))
	for _, folder := range folders {
		if _, validationErr := types.ValidateKnowledgeFolderStructure(folder); validationErr != nil ||
			folder.TenantID != tenantID ||
			folder.KnowledgeBaseID != kbID {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		if _, exists := seen[folder.ID]; exists {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		seen[folder.ID] = struct{}{}
		folderIDs = append(folderIDs, folder.ID)
	}
	counts, err := s.repo.CountKnowledgeByFolderIDs(ctx, tenantID, kbID, folderIDs)
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	hasChildren, err := s.repo.FindParentIDsWithChildren(ctx, tenantID, kbID, folderIDs)
	if err != nil {
		return nil, mapKnowledgeFolderError(err)
	}
	if len(counts) != len(folderIDs) || len(hasChildren) != len(folderIDs) {
		return nil, ErrKnowledgeFolderDataIntegrity
	}
	for folderID := range counts {
		if _, ok := seen[folderID]; !ok {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
	}
	for folderID := range hasChildren {
		if _, ok := seen[folderID]; !ok {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
	}

	result := make([]*types.KnowledgeFolderWithStats, 0, len(folders))
	for _, folder := range folders {
		knowledgeCount, countOK := counts[folder.ID]
		hasChild, childOK := hasChildren[folder.ID]
		if !countOK || !childOK || knowledgeCount < 0 {
			return nil, ErrKnowledgeFolderDataIntegrity
		}
		result = append(result, &types.KnowledgeFolderWithStats{
			KnowledgeFolder: *folder,
			KnowledgeCount:  knowledgeCount,
			HasChildren:     hasChild,
		})
	}
	return result, nil
}

func knowledgeFolderScope(ctx context.Context, kbID string) (uint64, string, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	kbID = strings.TrimSpace(kbID)
	if !ok || tenantID == 0 || kbID == "" {
		return 0, "", ErrKnowledgeFolderInvalidArgument
	}
	return tenantID, kbID, nil
}

func ensureKnowledgeFolderNameAvailable(
	ctx context.Context,
	repo interfaces.KnowledgeFolderReader,
	tenantID uint64,
	kbID string,
	parentID string,
	name string,
	currentFolderID string,
) error {
	existing, err := repo.GetByParentAndName(ctx, tenantID, kbID, parentID, name)
	if err == nil {
		if existing == nil || existing.ID != currentFolderID {
			return ErrKnowledgeFolderConflict
		}
		return nil
	}
	if errors.Is(err, repository.ErrKnowledgeFolderNotFound) {
		return nil
	}
	return err
}

func mapKnowledgeFolderError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrKnowledgeFolderInvalidArgument),
		errors.Is(err, ErrKnowledgeFolderInvalidName),
		errors.Is(err, ErrKnowledgeFolderNotFound),
		errors.Is(err, ErrKnowledgeFolderConflict),
		errors.Is(err, ErrKnowledgeFolderNotEmpty),
		errors.Is(err, ErrKnowledgeFolderCycle),
		errors.Is(err, ErrKnowledgeFolderDepthExceeded),
		errors.Is(err, ErrKnowledgeFolderDataIntegrity),
		errors.Is(err, ErrKnowledgeFolderUnsupportedDB),
		errors.Is(err, ErrKnowledgeFolderInternal):
		return err
	case errors.Is(err, repository.ErrKnowledgeFolderInvalid):
		return fmt.Errorf("%w: %w", ErrKnowledgeFolderInternal, err)
	case errors.Is(err, repository.ErrKnowledgeFolderNotFound),
		errors.Is(err, repository.ErrKnowledgeFolderKnowledgeBaseNotFound):
		return fmt.Errorf("%w: %w", ErrKnowledgeFolderNotFound, err)
	case errors.Is(err, repository.ErrKnowledgeFolderNotEmpty):
		return fmt.Errorf("%w: %w", ErrKnowledgeFolderNotEmpty, err)
	case errors.Is(err, repository.ErrKnowledgeFolderDataIntegrity):
		return fmt.Errorf("%w: %w", ErrKnowledgeFolderDataIntegrity, err)
	case errors.Is(err, repository.ErrKnowledgeFolderUnsupportedDialect):
		return fmt.Errorf("%w: %w", ErrKnowledgeFolderUnsupportedDB, err)
	case repository.IsKnowledgeFolderUniqueViolation(err):
		return fmt.Errorf("%w: %w", ErrKnowledgeFolderConflict, err)
	default:
		return fmt.Errorf("%w: %w", ErrKnowledgeFolderInternal, err)
	}
}
