package service

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type knowledgeFolderService struct {
	repo interfaces.KnowledgeFolderRepository
	kb   interfaces.KnowledgeBaseService
}

func NewKnowledgeFolderService(repo interfaces.KnowledgeFolderRepository, kb interfaces.KnowledgeBaseService) interfaces.KnowledgeFolderService {
	return &knowledgeFolderService{repo: repo, kb: kb}
}

func validateKnowledgeFolderName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 {
		return "", werrors.NewBadRequestError("folder name must contain 1 to 100 characters")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", werrors.NewBadRequestError("folder name contains invalid characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", werrors.NewBadRequestError("folder name contains control characters")
		}
	}
	return name, nil
}

func (s *knowledgeFolderService) scope(ctx context.Context, kbID string) (uint64, error) {
	kb, err := s.kb.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return 0, err
	}
	if kb.Type != types.KnowledgeBaseTypeDocument {
		return 0, werrors.NewBadRequestError("folders are only available for document knowledge bases")
	}
	return kb.TenantID, nil
}
func mapFolderError(err error) error {
	switch {
	case errors.Is(err, repository.ErrKnowledgeFolderNotFound):
		return werrors.NewNotFoundError("folder not found")
	case errors.Is(err, repository.ErrKnowledgeFolderNotEmpty):
		return werrors.NewConflictError("folder must be empty before deletion")
	case errors.Is(err, repository.ErrKnowledgeFolderCycle):
		return werrors.NewConflictError("a folder cannot be moved into itself or its descendant")
	case errors.Is(err, repository.ErrKnowledgeFolderTooDeep):
		return werrors.NewBadRequestError("folder hierarchy cannot exceed 20 levels")
	case errors.Is(err, repository.ErrKnowledgeFolderConflict):
		return werrors.NewConflictError("a folder with this name already exists")
	case errors.Is(err, repository.ErrKnowledgeFolderScope):
		return werrors.NewBadRequestError("folder or knowledge does not belong to this knowledge base")
	default:
		return err
	}
}
func (s *knowledgeFolderService) List(ctx context.Context, kbID, parentID, keyword string, page *types.Pagination) (*types.PageResult, error) {
	tenant, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if parentID != "" {
		if _, err = s.repo.Get(ctx, tenant, kbID, parentID); err != nil {
			return nil, mapFolderError(err)
		}
	}
	rows, total, err := s.repo.List(ctx, tenant, kbID, parentID, keyword, page)
	if err != nil {
		return nil, mapFolderError(err)
	}
	return types.NewPageResult(total, page, rows), nil
}
func (s *knowledgeFolderService) Get(ctx context.Context, kbID, id string) (*types.KnowledgeFolderView, error) {
	tenant, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	v, err := s.repo.Get(ctx, tenant, kbID, id)
	return v, mapFolderError(err)
}
func (s *knowledgeFolderService) Create(ctx context.Context, kbID, parentID, name string) (*types.KnowledgeFolder, error) {
	tenant, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	name, err = validateKnowledgeFolderName(name)
	if err != nil {
		return nil, err
	}
	v, err := s.repo.Create(ctx, tenant, kbID, parentID, name)
	return v, mapFolderError(err)
}
func (s *knowledgeFolderService) Update(ctx context.Context, kbID, id string, name, parentID *string) (*types.KnowledgeFolder, error) {
	tenant, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if name != nil {
		n, e := validateKnowledgeFolderName(*name)
		if e != nil {
			return nil, e
		}
		name = &n
	}
	v, err := s.repo.Update(ctx, tenant, kbID, id, name, parentID)
	return v, mapFolderError(err)
}
func (s *knowledgeFolderService) Delete(ctx context.Context, kbID, id string) error {
	tenant, err := s.scope(ctx, kbID)
	if err != nil {
		return err
	}
	return mapFolderError(s.repo.Delete(ctx, tenant, kbID, id))
}
func (s *knowledgeFolderService) EnsurePaths(ctx context.Context, kbID, parentID string, paths []types.EnsureFolderPath) ([]types.EnsureFolderPathResult, error) {
	tenant, err := s.scope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return []types.EnsureFolderPathResult{}, nil
	}
	for i := range paths {
		if paths[i].ClientKey == "" {
			return nil, werrors.NewBadRequestError("client_key is required")
		}
		if len(paths[i].Segments) == 0 {
			return nil, werrors.NewBadRequestError("segments cannot be empty")
		}
		for j := range paths[i].Segments {
			paths[i].Segments[j], err = validateKnowledgeFolderName(paths[i].Segments[j])
			if err != nil {
				return nil, err
			}
		}
	}
	v, err := s.repo.EnsurePaths(ctx, tenant, kbID, parentID, paths)
	return v, mapFolderError(err)
}
func (s *knowledgeFolderService) MoveKnowledge(ctx context.Context, kbID string, ids []string, folderID string) error {
	tenant, err := s.scope(ctx, kbID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return werrors.NewBadRequestError("knowledge_ids cannot be empty")
	}
	return mapFolderError(s.repo.MoveKnowledge(ctx, tenant, kbID, ids, folderID))
}
