package service

import (
	"context"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

func (s *knowledgeTagService) requireTagWrite(
	ctx context.Context,
	tag *types.KnowledgeTag,
) (*types.KnowledgeBase, context.Context, error) {
	if tag == nil {
		return nil, ctx, apperrors.NewNotFoundError("标签不存在")
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, tag.KnowledgeBaseID)
	if err != nil {
		return nil, ctx, err
	}
	if kb == nil || kb.ID != tag.KnowledgeBaseID || kb.TenantID != tag.TenantID {
		return nil, ctx, apperrors.NewForbiddenError("标签不属于当前知识库")
	}
	ctx, err = requireKBWrite(ctx, kb)
	return kb, ctx, err
}

func (s *knowledgeTagService) validateTagDeleteExclusions(
	ctx context.Context,
	kb *types.KnowledgeBase,
	ids []string,
) error {
	if len(ids) == 0 {
		return nil
	}
	if kb.Type != types.KnowledgeBaseTypeFAQ {
		return apperrors.NewBadRequestError("仅 FAQ 条目删除支持排除条目")
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	chunks, err := s.chunkRepo.ListChunksByID(ctx, kb.TenantID, ids)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if chunk == nil || !wanted[chunk.ID] {
			continue
		}
		if chunk.TenantID != kb.TenantID || chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
			return apperrors.NewForbiddenError("排除条目不属于当前知识库")
		}
		delete(wanted, chunk.ID)
	}
	if len(wanted) != 0 {
		return apperrors.NewNotFoundError("排除条目不存在")
	}
	return nil
}
