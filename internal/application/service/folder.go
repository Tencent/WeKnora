package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

const maxFolderNameLength = 255

var (
	ErrInvalidFolderScope       = errors.New("invalid folder scope")
	ErrFolderNotFound           = errors.New("folder not found")
	ErrParentFolderNotFound     = errors.New("parent folder not found")
	ErrInvalidFolderName        = errors.New("invalid folder name")
	ErrFolderNameConflict       = errors.New("folder name conflict")
	ErrFolderMoveCycle          = errors.New("folder move would create a cycle")
	ErrFolderNotEmpty           = errors.New("folder is not empty")
	ErrFolderHierarchyCorrupted = errors.New("folder hierarchy is corrupted")
)

type folderService struct {
	repo interfaces.FolderRepository
}

func NewFolderService(repo interfaces.FolderRepository) interfaces.FolderService {
	return &folderService{repo: repo}
}

func (s *folderService) CreateFolder(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	parentID *string,
	name string,
) (*types.Folder, error) {
	if err := validateFolderScope(tenantID, knowledgeBaseID); err != nil {
		return nil, err
	}
	normalizedName, err := normalizeFolderName(name)
	if err != nil {
		return nil, err
	}
	if parentID != nil && strings.TrimSpace(*parentID) == "" {
		return nil, ErrParentFolderNotFound
	}

	var created *types.Folder
	err = s.repo.WithinTransaction(ctx, func(txRepo interfaces.FolderRepository) error {
		if parentID != nil {
			if _, err := txRepo.GetByIDForUpdate(
				ctx,
				tenantID,
				knowledgeBaseID,
				*parentID,
			); err != nil {
				return mapParentFolderError(err)
			}
		}

		folder := &types.Folder{
			TenantID:        tenantID,
			KnowledgeBaseID: knowledgeBaseID,
			ParentID:        copyFolderID(parentID),
			Name:            normalizedName,
		}
		if err := txRepo.Create(ctx, folder); err != nil {
			return mapFolderWriteError(err)
		}
		created = folder
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *folderService) GetFolder(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	folderID string,
) (*types.Folder, error) {
	if err := validateFolderScope(tenantID, knowledgeBaseID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(folderID) == "" {
		return nil, ErrFolderNotFound
	}
	folder, err := s.repo.GetByID(ctx, tenantID, knowledgeBaseID, folderID)
	if err != nil {
		return nil, mapFolderError(err)
	}
	return folder, nil
}

func (s *folderService) ListChildren(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	parentID *string,
) ([]*types.Folder, error) {
	if err := validateFolderScope(tenantID, knowledgeBaseID); err != nil {
		return nil, err
	}
	if parentID != nil && strings.TrimSpace(*parentID) == "" {
		return nil, ErrParentFolderNotFound
	}
	return s.repo.ListChildren(ctx, tenantID, knowledgeBaseID, parentID)
}

func (s *folderService) ListByKnowledgeBase(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
) ([]*types.Folder, error) {
	if err := validateFolderScope(tenantID, knowledgeBaseID); err != nil {
		return nil, err
	}
	return s.repo.ListByKnowledgeBase(ctx, tenantID, knowledgeBaseID)
}

func (s *folderService) RenameFolder(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	folderID string,
	name string,
) (*types.Folder, error) {
	if err := validateFolderScope(tenantID, knowledgeBaseID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(folderID) == "" {
		return nil, ErrFolderNotFound
	}

	var renamed *types.Folder
	err := s.repo.WithinTransaction(ctx, func(txRepo interfaces.FolderRepository) error {
		folder, err := txRepo.GetByIDForUpdate(ctx, tenantID, knowledgeBaseID, folderID)
		if err != nil {
			return mapFolderError(err)
		}
		normalizedName, err := normalizeFolderName(name)
		if err != nil {
			return err
		}
		if normalizedName == folder.Name {
			renamed = folder
			return nil
		}
		if err := txRepo.UpdateName(
			ctx,
			tenantID,
			knowledgeBaseID,
			folderID,
			normalizedName,
		); err != nil {
			return mapFolderWriteError(err)
		}
		renamed, err = txRepo.GetByID(ctx, tenantID, knowledgeBaseID, folderID)
		if err != nil {
			return mapFolderError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return renamed, nil
}

func (s *folderService) MoveFolder(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	folderID string,
	newParentID *string,
) (*types.Folder, error) {
	if err := validateFolderScope(tenantID, knowledgeBaseID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(folderID) == "" {
		return nil, ErrFolderNotFound
	}
	if newParentID != nil && strings.TrimSpace(*newParentID) == "" {
		return nil, ErrParentFolderNotFound
	}

	var moved *types.Folder
	err := s.repo.WithinTransaction(ctx, func(txRepo interfaces.FolderRepository) error {
		var source, target *types.Folder
		for _, lockID := range stableFolderMoveLockIDs(folderID, newParentID) {
			locked, err := txRepo.GetByIDForUpdate(ctx, tenantID, knowledgeBaseID, lockID)
			if err != nil {
				if lockID == folderID {
					return mapFolderError(err)
				}
				return mapParentFolderError(err)
			}
			if lockID == folderID {
				source = locked
			} else {
				target = locked
			}
		}

		if newParentID == nil {
			if source.ParentID == nil {
				moved = source
				return nil
			}
		} else {
			if *newParentID == source.ID {
				return ErrFolderMoveCycle
			}
			if sameFolderID(source.ParentID, newParentID) {
				moved = source
				return nil
			}
			if err := validateMoveAncestors(
				ctx,
				txRepo,
				tenantID,
				knowledgeBaseID,
				source.ID,
				target,
			); err != nil {
				return err
			}
		}

		if err := txRepo.UpdateParent(
			ctx,
			tenantID,
			knowledgeBaseID,
			source.ID,
			copyFolderID(newParentID),
		); err != nil {
			return mapFolderWriteError(err)
		}
		updated, err := txRepo.GetByID(ctx, tenantID, knowledgeBaseID, source.ID)
		if err != nil {
			return mapFolderError(err)
		}
		moved = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	return moved, nil
}

func stableFolderMoveLockIDs(sourceID string, targetID *string) []string {
	ids := []string{sourceID}
	if targetID != nil && *targetID != sourceID {
		ids = append(ids, *targetID)
	}
	sort.Strings(ids)
	return ids
}

func (s *folderService) DeleteFolder(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	folderID string,
) error {
	if err := validateFolderScope(tenantID, knowledgeBaseID); err != nil {
		return err
	}
	if strings.TrimSpace(folderID) == "" {
		return ErrFolderNotFound
	}

	return s.repo.WithinTransaction(ctx, func(txRepo interfaces.FolderRepository) error {
		if _, err := txRepo.GetByIDForUpdate(
			ctx,
			tenantID,
			knowledgeBaseID,
			folderID,
		); err != nil {
			return mapFolderError(err)
		}
		children, err := txRepo.CountChildren(ctx, tenantID, knowledgeBaseID, folderID)
		if err != nil {
			return err
		}
		if children > 0 {
			return ErrFolderNotEmpty
		}
		knowledge, err := txRepo.CountKnowledge(ctx, tenantID, knowledgeBaseID, folderID)
		if err != nil {
			return err
		}
		if knowledge > 0 {
			return ErrFolderNotEmpty
		}
		if err := txRepo.Delete(ctx, tenantID, knowledgeBaseID, folderID); err != nil {
			return mapFolderError(err)
		}
		return nil
	})
}

func validateMoveAncestors(
	ctx context.Context,
	repo interfaces.FolderRepository,
	tenantID uint64,
	knowledgeBaseID string,
	sourceID string,
	target *types.Folder,
) error {
	visited := make(map[string]struct{})
	current := target
	for {
		if current == nil || strings.TrimSpace(current.ID) == "" {
			return ErrFolderHierarchyCorrupted
		}
		if current.ID == sourceID {
			return ErrFolderMoveCycle
		}
		if _, ok := visited[current.ID]; ok {
			return ErrFolderHierarchyCorrupted
		}
		visited[current.ID] = struct{}{}
		if current.ParentID == nil {
			return nil
		}
		if strings.TrimSpace(*current.ParentID) == "" {
			return ErrFolderHierarchyCorrupted
		}
		parent, err := repo.GetByIDForUpdate(
			ctx,
			tenantID,
			knowledgeBaseID,
			*current.ParentID,
		)
		if err != nil {
			if errors.Is(err, repository.ErrFolderNotFound) {
				return ErrFolderHierarchyCorrupted
			}
			return err
		}
		current = parent
	}
}

func normalizeFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", fmt.Errorf("%w: name is required", ErrInvalidFolderName)
	case name == "." || name == "..":
		return "", fmt.Errorf("%w: reserved name", ErrInvalidFolderName)
	case strings.ContainsAny(name, `/\`):
		return "", fmt.Errorf("%w: path separators are not allowed", ErrInvalidFolderName)
	case strings.ContainsRune(name, '\x00'):
		return "", fmt.Errorf("%w: NUL is not allowed", ErrInvalidFolderName)
	case utf8.RuneCountInString(name) > maxFolderNameLength:
		return "", fmt.Errorf(
			"%w: name exceeds %d characters",
			ErrInvalidFolderName,
			maxFolderNameLength,
		)
	default:
		return name, nil
	}
}

func validateFolderScope(tenantID uint64, knowledgeBaseID string) error {
	if tenantID == 0 || strings.TrimSpace(knowledgeBaseID) == "" {
		return ErrInvalidFolderScope
	}
	return nil
}

func mapFolderError(err error) error {
	if errors.Is(err, repository.ErrFolderNotFound) {
		return ErrFolderNotFound
	}
	return err
}

func mapParentFolderError(err error) error {
	if errors.Is(err, repository.ErrFolderNotFound) {
		return ErrParentFolderNotFound
	}
	return err
}

func mapFolderWriteError(err error) error {
	switch {
	case errors.Is(err, repository.ErrFolderNotFound):
		return ErrFolderNotFound
	case isDuplicateFolderNameError(err):
		return fmt.Errorf("%w: %v", ErrFolderNameConflict, err)
	default:
		return err
	}
}

func isDuplicateFolderNameError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate") ||
		strings.Contains(message, "unique constraint")
}

func copyFolderID(id *string) *string {
	if id == nil {
		return nil
	}
	copied := *id
	return &copied
}

func sameFolderID(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
