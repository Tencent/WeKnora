package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Sentinel errors so the handler can map cleanly to HTTP status codes
// without leaking GORM internals.
var (
	ErrFavoriteInvalidType = errors.New("invalid favorite resource type")
	ErrFavoriteEmptyID     = errors.New("favorite resource id is required")
)

type userResourceFavoriteService struct {
	repo interfaces.UserResourceFavoriteRepository
}

// NewUserResourceFavoriteService wraps the repository with input
// validation (allowlist of resource types, non-empty resource id).
// We keep service thin on purpose — favoriting is a non-business action
// that doesn't need audit logging or cross-aggregate side effects.
func NewUserResourceFavoriteService(repo interfaces.UserResourceFavoriteRepository) interfaces.UserResourceFavoriteService {
	return &userResourceFavoriteService{repo: repo}
}

func (s *userResourceFavoriteService) List(
	ctx context.Context, userID string, tenantID uint64, resourceType string,
) ([]*types.UserResourceFavorite, error) {
	if !types.IsValidFavoriteResourceType(resourceType) {
		return nil, ErrFavoriteInvalidType
	}
	return s.repo.List(ctx, userID, tenantID, resourceType)
}

func (s *userResourceFavoriteService) Add(
	ctx context.Context, userID string, tenantID uint64, resourceType, resourceID string,
) error {
	if !types.IsValidFavoriteResourceType(resourceType) {
		return ErrFavoriteInvalidType
	}
	if strings.TrimSpace(resourceID) == "" {
		return ErrFavoriteEmptyID
	}
	_, err := s.repo.Add(ctx, userID, tenantID, resourceType, resourceID)
	return err
}

func (s *userResourceFavoriteService) Remove(
	ctx context.Context, userID string, tenantID uint64, resourceType, resourceID string,
) error {
	if !types.IsValidFavoriteResourceType(resourceType) {
		return ErrFavoriteInvalidType
	}
	if strings.TrimSpace(resourceID) == "" {
		return ErrFavoriteEmptyID
	}
	_, err := s.repo.Remove(ctx, userID, tenantID, resourceType, resourceID)
	return err
}
