package service

import (
	"context"
	"mime/multipart"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	_ interfaces.KnowledgeTreeService         = (*knowledgeService)(nil)
	_ interfaces.KnowledgeParentImportService = (*knowledgeService)(nil)
)

func (s *knowledgeService) validateImportParent(ctx context.Context, kb, id string) error {
	if id == "" {
		return nil
	}
	if len(id) > 36 || strings.TrimSpace(id) != id || strings.ContainsRune(id, '\x00') {
		return apperrors.NewBadRequestError("invalid parent_knowledge_id")
	}
	tenant, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return apperrors.NewUnauthorizedError("workspace context unavailable")
	}
	p, err := s.repo.GetKnowledgeByID(ctx, tenant, id)
	if err != nil || p == nil || p.KnowledgeBaseID != kb || p.ParseStatus == types.ParseStatusDeleting {
		return apperrors.NewNotFoundError("parent knowledge not found in this knowledge base")
	}
	return access.RejectMovingKnowledge(p)
}

func parentImportID(ids []string) string {
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

func (s *knowledgeService) CreateKnowledgeFromFileWithParent(
	ctx context.Context,
	kb string,
	file *multipart.FileHeader,
	metadata map[string]string,
	multi *bool,
	name string,
	tags []string,
	channel string,
	process *types.KnowledgeProcessOverrides,
	parent string,
) (*types.Knowledge, error) {
	if err := s.validateImportParent(ctx, kb, parent); err != nil {
		return nil, err
	}
	return s.createKnowledgeFromFile(ctx, kb, file, metadata, multi, name, tags, channel, process, nil, parent)
}

func (s *knowledgeService) CreateKnowledgeFromURLWithParent(
	ctx context.Context,
	kb, rawURL, name, fileType string,
	multi *bool,
	title string,
	tags []string,
	channel string,
	process *types.KnowledgeProcessOverrides,
	parent string,
) (*types.Knowledge, error) {
	if err := s.validateImportParent(ctx, kb, parent); err != nil {
		return nil, err
	}
	return s.createKnowledgeFromURL(ctx, kb, rawURL, name, fileType, multi, title, tags, channel, process, nil, parent)
}

func (s *knowledgeService) KnowledgeChildren(
	ctx context.Context,
	roots []types.KnowledgeTreeRoot,
	opts types.KnowledgeChildrenOptions,
) ([]types.KnowledgeChild, error) {
	r, ok := s.repo.(interfaces.KnowledgeTreeRepository)
	if !ok {
		return nil, apperrors.NewServiceUnavailableError("knowledge tree repository unavailable")
	}
	return r.KnowledgeChildren(ctx, roots, opts)
}

func (s *knowledgeService) PrepareKnowledgeCascade(
	ctx context.Context,
	tenant uint64,
	kb string,
	seeds []string,
	mark bool,
) ([]string, error) {
	r, ok := s.repo.(interfaces.KnowledgeTreeRepository)
	if !ok {
		return nil, apperrors.NewServiceUnavailableError("knowledge tree repository unavailable")
	}
	return r.PrepareKnowledgeCascade(ctx, tenant, kb, seeds, mark)
}

func (s *knowledgeService) CheckKnowledgeTreeMove(ctx context.Context, tenant uint64, id, target string) error {
	r, ok := s.repo.(interfaces.KnowledgeTreeRepository)
	if !ok {
		return nil
	}
	return r.CheckKnowledgeTreeMove(ctx, tenant, id, target)
}
