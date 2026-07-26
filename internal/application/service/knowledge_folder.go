package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	maxKnowledgeFolderDepth     = 64
	maxKnowledgeFolderMoveBatch = 200
)

var (
	ErrKnowledgeFolderInvalid  = errors.New("invalid knowledge folder")
	ErrKnowledgeFolderConflict = errors.New("knowledge folder name conflict")
	ErrKnowledgeFolderCycle    = errors.New("knowledge folder move would create a cycle")
	ErrKnowledgeMoveInvalid    = errors.New("invalid knowledge move")
)

type knowledgeFolderService struct {
	repo interfaces.KnowledgeFolderRepository
}

func NewKnowledgeFolderService(repo interfaces.KnowledgeFolderRepository) interfaces.KnowledgeFolderService {
	return &knowledgeFolderService{repo: repo}
}

func (s *knowledgeFolderService) List(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) ([]*types.KnowledgeFolder, error) {
	if err := validateKnowledgeFolderScope(tenantID, kbID); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, tenantID, kbID)
}

func (s *knowledgeFolderService) Create(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parentID *string,
	name string,
) (*types.KnowledgeFolder, error) {
	if err := validateKnowledgeFolderScope(tenantID, kbID); err != nil {
		return nil, err
	}
	name, err := normalizeKnowledgeFolderName(name)
	if err != nil {
		return nil, err
	}
	parentID, err = normalizeOptionalUUID(parentID)
	if err != nil {
		return nil, err
	}

	var created *types.KnowledgeFolder
	err = s.repo.WithinTransaction(ctx, func(tx interfaces.KnowledgeFolderRepository) error {
		parentPath := ""
		depth := 1
		if parentID != nil {
			parent, err := tx.Get(ctx, tenantID, kbID, *parentID)
			if err != nil {
				return err
			}
			parentPath = parent.Path
			depth = parent.Depth + 1
		}
		if depth > maxKnowledgeFolderDepth {
			return fmt.Errorf("%w: maximum depth is %d", ErrKnowledgeFolderInvalid, maxKnowledgeFolderDepth)
		}

		id := uuid.NewString()
		path := "/" + id
		if parentPath != "" {
			path = parentPath + "/" + id
		}
		now := time.Now()
		created = &types.KnowledgeFolder{
			ID:              id,
			TenantID:        tenantID,
			KnowledgeBaseID: kbID,
			ParentID:        cloneStringPointer(parentID),
			Name:            name,
			Path:            path,
			Depth:           depth,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := tx.Create(ctx, created); err != nil {
			return mapKnowledgeFolderWriteError(err)
		}
		return nil
	})
	return created, err
}

func (s *knowledgeFolderService) Update(
	ctx context.Context,
	tenantID uint64,
	kbID, folderID string,
	name *string,
	parentID *string,
	moveParent bool,
) (*types.KnowledgeFolder, error) {
	if err := validateKnowledgeFolderScope(tenantID, kbID); err != nil {
		return nil, err
	}
	parsedFolderID, err := uuid.Parse(folderID)
	if err != nil || parsedFolderID == uuid.Nil {
		return nil, ErrKnowledgeFolderInvalid
	}
	folderID = parsedFolderID.String()
	var normalizedName *string
	if name != nil {
		value, err := normalizeKnowledgeFolderName(*name)
		if err != nil {
			return nil, err
		}
		normalizedName = &value
	}
	if moveParent {
		parentID, err = normalizeOptionalUUID(parentID)
		if err != nil {
			return nil, err
		}
	}

	var updated *types.KnowledgeFolder
	err = s.repo.WithinTransaction(ctx, func(tx interfaces.KnowledgeFolderRepository) error {
		folders, err := tx.List(ctx, tenantID, kbID)
		if err != nil {
			return err
		}
		byID := make(map[string]*types.KnowledgeFolder, len(folders))
		for _, folder := range folders {
			byID[folder.ID] = folder
		}
		folder := byID[folderID]
		if folder == nil {
			return repository.ErrKnowledgeFolderNotFound
		}

		targetName := folder.Name
		if normalizedName != nil {
			targetName = *normalizedName
		}
		targetParentID := folder.ParentID
		if moveParent {
			targetParentID = cloneStringPointer(parentID)
		}

		parentPath := ""
		parentDepth := 0
		if targetParentID != nil {
			if *targetParentID == folder.ID {
				return ErrKnowledgeFolderCycle
			}
			parent := byID[*targetParentID]
			if parent == nil {
				return repository.ErrKnowledgeFolderNotFound
			}
			if parent.Path == folder.Path || strings.HasPrefix(parent.Path, folder.Path+"/") {
				return ErrKnowledgeFolderCycle
			}
			parentPath = parent.Path
			parentDepth = parent.Depth
		}

		for _, sibling := range folders {
			if sibling.ID != folder.ID &&
				equalOptionalString(sibling.ParentID, targetParentID) &&
				sibling.Name == targetName {
				return ErrKnowledgeFolderConflict
			}
		}

		newDepth := parentDepth + 1
		if newDepth > maxKnowledgeFolderDepth {
			return ErrKnowledgeFolderInvalid
		}
		oldPath := folder.Path
		newPath := "/" + folder.ID
		if parentPath != "" {
			newPath = parentPath + "/" + folder.ID
		}
		depthDelta := newDepth - folder.Depth
		now := time.Now()

		affected := make([]*types.KnowledgeFolder, 0)
		for _, candidate := range folders {
			switch {
			case candidate.ID == folder.ID:
				candidate.ParentID = cloneStringPointer(targetParentID)
				candidate.Name = targetName
				candidate.Path = newPath
				candidate.Depth = newDepth
			case strings.HasPrefix(candidate.Path, oldPath+"/"):
				candidate.Path = newPath + strings.TrimPrefix(candidate.Path, oldPath)
				candidate.Depth += depthDelta
				if candidate.Depth > maxKnowledgeFolderDepth {
					return ErrKnowledgeFolderInvalid
				}
			default:
				continue
			}
			candidate.UpdatedAt = now
			affected = append(affected, candidate)
		}
		sort.Slice(affected, func(i, j int) bool { return affected[i].Depth < affected[j].Depth })
		for _, candidate := range affected {
			if err := tx.Save(ctx, candidate); err != nil {
				return mapKnowledgeFolderWriteError(err)
			}
			if candidate.ID == folder.ID {
				updated = candidate
			}
		}
		return nil
	})
	return updated, err
}

func (s *knowledgeFolderService) Delete(
	ctx context.Context,
	tenantID uint64,
	kbID, folderID string,
) error {
	if err := validateKnowledgeFolderScope(tenantID, kbID); err != nil {
		return err
	}
	parsedFolderID, err := uuid.Parse(folderID)
	if err != nil || parsedFolderID == uuid.Nil {
		return ErrKnowledgeFolderInvalid
	}
	folderID = parsedFolderID.String()
	return s.repo.WithinTransaction(ctx, func(tx interfaces.KnowledgeFolderRepository) error {
		return tx.DeleteEmpty(ctx, tenantID, kbID, folderID)
	})
}

func (s *knowledgeFolderService) MoveKnowledge(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	knowledgeIDs []string,
	folderID *string,
) (int, error) {
	if err := validateKnowledgeFolderScope(tenantID, kbID); err != nil {
		return 0, err
	}
	ids, err := normalizeUUIDs(knowledgeIDs)
	if err != nil || len(ids) == 0 || len(ids) > maxKnowledgeFolderMoveBatch {
		return 0, ErrKnowledgeMoveInvalid
	}
	folderID, err = normalizeOptionalUUID(folderID)
	if err != nil {
		return 0, ErrKnowledgeMoveInvalid
	}
	var count int64
	err = s.repo.WithinTransaction(ctx, func(tx interfaces.KnowledgeFolderRepository) error {
		if folderID != nil {
			if _, err := tx.Get(ctx, tenantID, kbID, *folderID); err != nil {
				return err
			}
		}
		count, err = tx.MoveKnowledge(ctx, tenantID, kbID, ids, folderID)
		if err != nil {
			return err
		}
		if count != int64(len(ids)) {
			return repository.ErrKnowledgeNotFound
		}
		return nil
	})
	return int(count), err
}

func (s *knowledgeFolderService) ResolveKnowledgeIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
) ([]string, error) {
	if err := validateKnowledgeFolderScope(tenantID, kbID); err != nil {
		return nil, err
	}
	ids, err := normalizeUUIDs(folderIDs)
	if err != nil || len(ids) == 0 {
		return nil, ErrKnowledgeFolderInvalid
	}
	return s.repo.ListKnowledgeIDs(ctx, tenantID, kbID, ids, true)
}

func validateKnowledgeFolderScope(tenantID uint64, kbID string) error {
	if tenantID == 0 || strings.TrimSpace(kbID) == "" {
		return ErrKnowledgeFolderInvalid
	}
	return nil
}

func normalizeKnowledgeFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, '\x00') ||
		utf8.RuneCountInString(name) > 255 {
		return "", ErrKnowledgeFolderInvalid
	}
	return name, nil
}

func normalizeOptionalUUID(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	parsed, err := uuid.Parse(trimmed)
	if err != nil || parsed == uuid.Nil {
		return nil, ErrKnowledgeFolderInvalid
	}
	canonical := parsed.String()
	return &canonical, nil
}

func normalizeUUIDs(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return nil, ErrKnowledgeFolderInvalid
		}
		value = parsed.String()
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func mapKnowledgeFolderWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(strings.ToLower(err.Error()), "unique constraint") ||
		strings.Contains(strings.ToLower(err.Error()), "duplicate key") ||
		strings.Contains(strings.ToLower(err.Error()), "duplicate entry") {
		return ErrKnowledgeFolderConflict
	}
	return err
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
