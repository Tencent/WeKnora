package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func knowledgeFolderError(err error) error {
	switch {
	case errors.Is(err, repository.ErrKnowledgeFolderChanged):
		return werrors.NewConflictError("目录或文件位置已变化，请刷新后重试")
	case errors.Is(err, repository.ErrKnowledgeFolderNotFound):
		return werrors.NewNotFoundError("文件夹不存在，请刷新后重试")
	case errors.Is(err, repository.ErrKnowledgeFolderConflict):
		return werrors.NewConflictError("同级目录已存在同名文件夹")
	case errors.Is(err, repository.ErrKnowledgeFolderNotEmpty):
		return werrors.NewConflictError("只能删除没有文件和子目录的空文件夹")
	case errors.Is(err, repository.ErrKnowledgeFolderInvalidPath):
		return werrors.NewBadRequestError("移动后文件夹路径无效或超出层级、长度限制，请缩短目标路径后重试")
	default:
		return err
	}
}

// Keep the existing slash/whitespace canonicalization, but never truncate a
// rename request. Descendant budgets are checked atomically by the repository.
func normalizeRenameFolderPath(raw string) (string, error) {
	safe, valid := secutils.ValidateInput(strings.TrimSpace(raw))
	if !valid {
		return "", werrors.NewBadRequestError("文件夹路径包含非法字符")
	}
	path := ""
	for _, segment := range strings.Split(strings.ReplaceAll(safe, "\\", "/"), "/") {
		segment = strings.TrimRight(strings.TrimSpace(segment), ". ")
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		var err error
		path, err = types.BuildKnowledgeFolderChildPath(path, segment)
		if err != nil {
			return "", werrors.NewBadRequestError(err.Error())
		}
	}
	return path, nil
}

func (s *knowledgeService) CreateKnowledgeFolder(
	ctx context.Context,
	kbID,
	parentPath,
	name string,
) (*types.KnowledgeFolderNode, error) {
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if kb == nil || kb.ID != kbID {
		return nil, werrors.NewNotFoundError("knowledge base not found")
	}
	ctx, err = requireKBWrite(ctx, kb)
	if err != nil {
		return nil, err
	}
	path, err := types.BuildKnowledgeFolderChildPath(parentPath, name)
	if err != nil {
		return nil, werrors.NewBadRequestError(err.Error())
	}
	if _, valid := secutils.ValidateInput(path); !valid {
		return nil, werrors.NewBadRequestError("文件夹名称包含非法字符")
	}
	if err := s.repo.CreateKnowledgeFolder(ctx, kb.TenantID, kbID, path); err != nil {
		return nil, knowledgeFolderError(err)
	}
	return &types.KnowledgeFolderNode{Path: path, Name: types.KnowledgeFolderName(path)}, nil
}

func (s *knowledgeService) DeleteKnowledgeFolder(ctx context.Context, kbID, path string) error {
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return err
	}
	if kb == nil || kb.ID != kbID {
		return werrors.NewNotFoundError("knowledge base not found")
	}
	ctx, err = requireKBWrite(ctx, kb)
	if err != nil {
		return err
	}
	if path == "" || strings.TrimSpace(path) != path || types.NormalizeKnowledgeFolderPath(path) != path {
		return werrors.NewBadRequestError("文件夹路径无效")
	}
	if _, valid := secutils.ValidateInput(path); !valid {
		return werrors.NewBadRequestError("文件夹路径包含非法字符")
	}
	return knowledgeFolderError(s.repo.DeleteKnowledgeFolder(ctx, kb.TenantID, kbID, path))
}
