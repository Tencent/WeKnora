package impl

import (
	"context"
	"errors"
	"strings"

	vkbRepo "github.com/tencent/weknora/features/virtualkb/internal/repository/interfaces"
	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// VirtualKBService provides business logic around virtual knowledge bases.
type VirtualKBService struct {
	repo vkbRepo.VirtualKBRepository
}

// NewVirtualKBService creates a new instance.
func NewVirtualKBService(repository vkbRepo.VirtualKBRepository) *VirtualKBService {
	return &VirtualKBService{repo: repository}
}

// Create validates and persists a virtual knowledge base definition.
func (s *VirtualKBService) Create(ctx context.Context, vkb *types.VirtualKB) error {
	if err := validateVirtualKB(vkb); err != nil {
		return err
	}
	return s.repo.Create(ctx, vkb)
}

// Update modifies an existing virtual knowledge base definition.
func (s *VirtualKBService) Update(ctx context.Context, vkb *types.VirtualKB) error {
	if vkb.ID == 0 {
		return errors.New("virtual kb id is required")
	}
	if err := validateVirtualKB(vkb); err != nil {
		return err
	}
	return s.repo.Update(ctx, vkb)
}

// Delete removes a virtual knowledge base by identifier.
func (s *VirtualKBService) Delete(ctx context.Context, id int64) error {
	if id == 0 {
		return errors.New("virtual kb id is required")
	}
	return s.repo.Delete(ctx, id)
}

// GetByID fetches a virtual knowledge base by ID.
func (s *VirtualKBService) GetByID(ctx context.Context, id int64) (*types.VirtualKB, error) {
	if id == 0 {
		return nil, errors.New("virtual kb id is required")
	}
	return s.repo.GetByID(ctx, id)
}

// List returns all virtual knowledge bases.
func (s *VirtualKBService) List(ctx context.Context) ([]*types.VirtualKB, error) {
	return s.repo.List(ctx)
}

func validateVirtualKB(vkb *types.VirtualKB) error {
	if vkb == nil {
		return errors.New("virtual kb is required")
	}
	if strings.TrimSpace(vkb.Name) == "" {
		return errors.New("virtual kb name is required")
	}
	if len(vkb.Filters) == 0 {
		return errors.New("virtual kb requires at least one filter")
	}
	for _, filter := range vkb.Filters {
		if filter.TagCategoryID == 0 {
			return errors.New("filter tag_category_id is required")
		}
		if len(filter.TagIDs) == 0 {
			return errors.New("filter tag_ids are required")
		}
		if strings.TrimSpace(filter.Operator) == "" {
			return errors.New("filter operator is required")
		}
	}
	return nil
}
