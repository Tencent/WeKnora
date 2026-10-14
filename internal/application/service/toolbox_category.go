package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const toolboxCategoryNameMaxRunes = 64

type toolboxCategoryService struct {
	categories interfaces.ToolboxCategoryRepository
	skills     repository.TenantSkillRepository
	mcp        interfaces.MCPServiceRepository
}

func validateToolboxTenantID(tenantID uint64) error {
	if tenantID == 0 {
		return apperrors.NewBadRequestError("workspace ID cannot be empty")
	}
	return nil
}

// NewToolboxCategoryService creates the service for workspace tags and assignments.
func NewToolboxCategoryService(
	categories interfaces.ToolboxCategoryRepository,
	skills repository.TenantSkillRepository,
	mcp interfaces.MCPServiceRepository,
) interfaces.ToolboxCategoryService {
	return &toolboxCategoryService{categories: categories, skills: skills, mcp: mcp}
}

func (s *toolboxCategoryService) List(
	ctx context.Context, tenantID uint64,
) ([]*types.ToolboxCategory, error) {
	if err := validateToolboxTenantID(tenantID); err != nil {
		return nil, err
	}
	return s.categories.List(ctx, tenantID)
}

func validateToolboxCategoryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", apperrors.NewBadRequestError("tag name cannot be empty")
	}
	if utf8.RuneCountInString(name) > toolboxCategoryNameMaxRunes {
		return "", apperrors.NewBadRequestError("tag name cannot exceed 64 characters")
	}
	visible := false
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return "", apperrors.NewBadRequestError("tag name cannot contain control characters or line breaks")
		}
		if unicode.IsGraphic(r) && !unicode.IsSpace(r) && !unicode.IsMark(r) {
			visible = true
		}
	}
	if !visible {
		return "", apperrors.NewBadRequestError("tag name must contain visible characters")
	}
	return name, nil
}

func (s *toolboxCategoryService) Create(
	ctx context.Context, tenantID uint64, name string,
) (*types.ToolboxCategory, error) {
	if err := validateToolboxTenantID(tenantID); err != nil {
		return nil, err
	}
	name, err := validateToolboxCategoryName(name)
	if err != nil {
		return nil, err
	}
	if existing, err := s.categories.GetByName(ctx, tenantID, name); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, apperrors.NewConflictError("tag name already exists")
	}
	now := time.Now()
	category := &types.ToolboxCategory{
		ID: uuid.NewString(), TenantID: tenantID, Name: name,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.categories.Create(ctx, category); err != nil {
		if errors.Is(err, repository.ErrToolboxCategoryNameExists) {
			return nil, apperrors.NewConflictError("tag name already exists")
		}
		return nil, err
	}
	return category, nil
}

func (s *toolboxCategoryService) Update(
	ctx context.Context, tenantID uint64, id, name string,
) (*types.ToolboxCategory, error) {
	if err := validateToolboxTenantID(tenantID); err != nil {
		return nil, err
	}
	name, err := validateToolboxCategoryName(name)
	if err != nil {
		return nil, err
	}
	category, err := s.categories.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if category == nil {
		return nil, apperrors.NewNotFoundError("tag not found")
	}
	if duplicate, err := s.categories.GetByName(ctx, tenantID, name); err != nil {
		return nil, err
	} else if duplicate != nil && duplicate.ID != id {
		return nil, apperrors.NewConflictError("tag name already exists")
	}
	category.Name = name
	category.UpdatedAt = time.Now()
	if err := s.categories.Update(ctx, category); err != nil {
		if errors.Is(err, repository.ErrToolboxCategoryNameExists) {
			return nil, apperrors.NewConflictError("tag name already exists")
		}
		return nil, err
	}
	return category, nil
}

func (s *toolboxCategoryService) Delete(
	ctx context.Context, tenantID uint64, id string,
) error {
	if err := validateToolboxTenantID(tenantID); err != nil {
		return err
	}
	category, err := s.categories.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if category == nil {
		return apperrors.NewNotFoundError("tag not found")
	}
	return s.categories.Delete(ctx, tenantID, id)
}

func (s *toolboxCategoryService) ListByResourceIDs(
	ctx context.Context, tenantID uint64, resourceType string, resourceIDs []string,
) (map[string][]types.ToolboxCategory, error) {
	if err := validateToolboxTenantID(tenantID); err != nil {
		return nil, err
	}
	return s.categories.ListByResourceIDs(ctx, tenantID, resourceType, resourceIDs)
}

func (s *toolboxCategoryService) validateCategoryIDs(
	ctx context.Context, tenantID uint64, categoryIDs []string,
) error {
	seen := make(map[string]struct{}, len(categoryIDs))
	for _, id := range categoryIDs {
		if strings.TrimSpace(id) == "" {
			return apperrors.NewBadRequestError("category_ids cannot contain an empty ID")
		}
		if _, exists := seen[id]; exists {
			return apperrors.NewBadRequestError("category_ids cannot contain duplicates")
		}
		seen[id] = struct{}{}
		category, err := s.categories.GetByID(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if category == nil {
			return apperrors.NewBadRequestError(fmt.Sprintf("tag %s does not belong to this workspace", id))
		}
	}
	return nil
}

func (s *toolboxCategoryService) replace(
	ctx context.Context,
	tenantID uint64,
	resourceType string,
	resourceID string,
	categoryIDs []string,
) ([]types.ToolboxCategory, error) {
	if err := validateToolboxTenantID(tenantID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(resourceID) == "" {
		return nil, apperrors.NewBadRequestError("resource ID cannot be empty")
	}
	switch resourceType {
	case types.ToolboxResourceSkill:
		resource, err := s.skills.GetCatalog(ctx, tenantID, resourceID)
		if err != nil {
			return nil, err
		}
		if resource == nil {
			return nil, apperrors.NewNotFoundError("skill catalog item not found")
		}
	case types.ToolboxResourceMCPService:
		resource, err := s.mcp.GetByID(ctx, tenantID, resourceID)
		if err != nil {
			return nil, err
		}
		if resource == nil {
			return nil, apperrors.NewNotFoundError("MCP service not found")
		}
	default:
		return nil, apperrors.NewBadRequestError("unsupported toolbox resource type")
	}
	if err := s.validateCategoryIDs(ctx, tenantID, categoryIDs); err != nil {
		return nil, err
	}
	if err := s.categories.ReplaceResourceCategories(
		ctx, tenantID, resourceType, resourceID, categoryIDs,
	); err != nil {
		return nil, err
	}
	byResource, err := s.categories.ListByResourceIDs(ctx, tenantID, resourceType, []string{resourceID})
	if err != nil {
		return nil, err
	}
	return byResource[resourceID], nil
}

func (s *toolboxCategoryService) ReplaceSkillCategories(
	ctx context.Context, tenantID uint64, skillID string, categoryIDs []string,
) ([]types.ToolboxCategory, error) {
	return s.replace(ctx, tenantID, types.ToolboxResourceSkill, skillID, categoryIDs)
}

func (s *toolboxCategoryService) ReplaceMCPServiceCategories(
	ctx context.Context, tenantID uint64, serviceID string, categoryIDs []string,
) ([]types.ToolboxCategory, error) {
	return s.replace(ctx, tenantID, types.ToolboxResourceMCPService, serviceID, categoryIDs)
}
